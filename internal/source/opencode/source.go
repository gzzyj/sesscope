package opencode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gzzyj/sesscope/internal/platform"
	"github.com/gzzyj/sesscope/internal/session"
	"github.com/gzzyj/sesscope/internal/source"
)

// Adapter 是 opencode 的 Source 实现。
//
// 目前支持旧版 SQLite schema（session/message/part）。新版
// （session_v2/session_message/session_part）会被 Locate 如实标成"不可读 + 原因"，
// 而不是返回 0 个会话。
type Adapter struct{ platform platform.Adapter }

// New 返回 opencode 适配器。
func New(adapters ...platform.Adapter) *Adapter {
	var p platform.Adapter
	if len(adapters) > 0 {
		p = adapters[0]
	}
	if p == nil {
		p = platform.Detect()
	}
	return &Adapter{platform: p}
}

// ID 实现 source.Source。
func (a *Adapter) ID() session.SourceID { return session.Opencode }

// Capabilities 实现 source.Source。
func (a *Adapter) Capabilities() source.Capabilities {
	return source.Capabilities{
		ProcessDiscovery:  true,
		WorkspaceCatalog:  true,
		ServerSideFilter:  true,
		PrefixLookup:      true,
		SessionTitles:     true,
		SubagentTree:      true,
		TurnHints:         false,
		Tokens:            true,
		Cost:              true,
		ReasoningText:     true,
		ToolArgs:          true,
		ToolOutput:        true,
		ToolStatus:        true,
		CompactionRecords: true,
		LiveActivity:      false,
		ListCost:          source.ListConstant,
		OpenCost:          source.OpenFullLoad,
	}
}

// Locate 按 L0 契约 → L1 环境 → L2 默认路径 的优先级给出候选存储。
// 每条候选都带 Rule/Evidence，供 sources --explain 回答"凭什么选它"。
func (a *Adapter) Locate(ctx context.Context, l source.Locator) ([]source.Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l.Store != "" {
		return []source.Store{probe(l.Store, "l0:--store")}, nil
	}
	if l.PID > 0 {
		path, rule, err := a.storeForPID(ctx, l.PID)
		if err != nil {
			return nil, err
		}
		return []source.Store{probe(path, rule)}, nil
	}
	stores := make([]source.Store, 0, 8)
	seen := make(map[string]bool, 8)
	add := func(path, rule string) {
		if path == "" {
			return
		}
		key := filepath.Clean(path)
		if a.platform.Kind() == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			return
		}
		seen[key] = true
		stores = append(stores, probe(path, rule))
	}
	if l.Home != "" {
		add(filepath.Join(l.Home, "opencode", "opencode.db"), "l1:--home")
	}
	for _, candidate := range a.discoveryCandidates() {
		add(candidate.Path, candidate.Rule)
	}
	return stores, nil
}

// probe 描述一个候选存储的健康度。不存在/打不开/版本不支持都如实写进 Detail。
func probe(path, rule string) source.Store {
	store := source.Store{
		Source:   session.Opencode,
		Path:     path,
		Kind:     "sqlite",
		Format:   "opencode/sqlite",
		ReadOnly: true,
		Rule:     rule,
	}
	info, err := os.Stat(path)
	if err != nil {
		store.Detail = "文件不存在（已尝试）"
		return store
	}
	store.Exists = true
	store.SizeBytes = info.Size()
	store.ModTime = info.ModTime().UnixMilli()
	version, tables, err := detectVersion(path)
	if err != nil {
		store.Detail = "打开失败：" + err.Error()
		store.Evidence = "无法读取 sqlite_master"
		return store
	}
	store.Version = version
	store.Evidence = "tables: " + strings.Join(tables, ",")
	switch version {
	case "v1":
		store.Readable = true
		store.Detail = "旧版 schema（session/message/part）"
	case "v2":
		store.Detail = "新版 schema（session_v2/session_message/session_part），尚未实现读取"
	default:
		store.Detail = "不是 opencode 会话库（缺少 session/message/part 表）"
	}
	return store
}

// detectVersion 只看表集，不依赖任何具体行，因此对新版库也安全。
func detectVersion(path string) (string, []string, error) {
	conn, err := OpenReadOnly(path)
	if err != nil {
		return "", nil, err
	}
	defer conn.Close()
	rows, err := conn.Query("select name from sqlite_master where type='table' order by name")
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	tables := make([]string, 0, 16)
	have := make(map[string]bool, 16)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return "", nil, err
		}
		tables = append(tables, name)
		have[name] = true
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	sort.Strings(tables)
	switch {
	case have["session"] && have["message"] && have["part"]:
		return "v1", tables, nil
	case have["session_v2"] && have["session_message"] && have["session_part"]:
		return "v2", tables, nil
	default:
		return "", tables, nil
	}
}

// Sessions 实现 source.Source：返回该库的全部会话摘要（含子会话），过滤交给调用方统一做。
func (a *Adapter) Sessions(ctx context.Context, store source.Store, q source.Query) ([]session.Summary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !store.Readable {
		return nil, &source.UnsupportedError{Source: session.Opencode, Store: store.Path, Reason: store.Detail}
	}
	database, err := Open(store.Path)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	rows, err := database.ListSessions(SessionFilter{IncludeChildren: true})
	if err != nil {
		return nil, err
	}
	out := make([]session.Summary, 0, len(rows))
	for _, row := range rows {
		out = append(out, summaryFromRow(row, store))
	}
	return out, nil
}

func summaryFromRow(row SessionRow, store source.Store) session.Summary {
	summary := session.Summary{
		Ref: session.Ref{
			Source: session.Opencode,
			Store:  store.Path,
			ID:     row.ID,
			Object: "session:" + row.ID,
		},
		WorkspaceKey: row.ProjectID,
		Title:        row.Title,
		TitleSource:  "native",
		Directory:    row.Directory,
		Model:        parseModelRaw(row.Model),
		CreatedMs:    &row.TimeCreated,
		UpdatedMs:    &row.TimeUpdated,
		UpdatedKind:  "native",
		Messages:     int(row.MessageCount),
		Tokens: session.Tokens{
			Input:      &row.TokensInput,
			Output:     &row.TokensOutput,
			Reasoning:  &row.TokensReason,
			CacheRead:  &row.TokensCacheRead,
			CacheWrite: &row.TokensCacheWrite,
		},
		Cost:    &row.Cost,
		IsChild: row.IsChild(),
		Flags:   []string{},
	}
	if row.Agent != nil {
		summary.Agent = *row.Agent
	}
	if row.ParentID != nil {
		summary.ParentID = *row.ParentID
	}
	if summary.Title == "" {
		summary.TitleSource = "none"
	}
	return summary
}

// Open 实现 source.Source：取一个会话的完整领域模型。
func (a *Adapter) Open(ctx context.Context, store source.Store, ref session.Ref, o source.OpenOptions) (*session.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !store.Readable {
		return nil, &source.UnsupportedError{Source: session.Opencode, Store: store.Path, Reason: store.Detail}
	}
	database, err := Open(store.Path)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	detail, err := database.Load(ref.ID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return nil, fmt.Errorf("%w：%s", source.ErrNotFound, ref.ID)
		}
		return nil, err
	}
	if o.MaxTextBytes <= 0 {
		o.MaxTextBytes = 256 << 10
	}
	return mapDetail(detail, store, o), nil
}

// LegacyDocument 实现 source.LegacyExporter：输出旧口径的 session-export.json。
func (a *Adapter) LegacyDocument(ctx context.Context, store source.Store, id string, generatedAt time.Time) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !store.Readable {
		return nil, &source.UnsupportedError{Source: session.Opencode, Store: store.Path, Reason: store.Detail}
	}
	database, err := Open(store.Path)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	document, err := database.ExportDocument(id, generatedAt)
	if err != nil {
		return nil, err
	}
	return EncodeDocument(document)
}

// Workspaces 实现 source.WorkspaceLister：把 project 当作原生工作区。
// workspace 表在本机一直为空，因此这里只读 project，并如实标注 Origin=native。
func (a *Adapter) Workspaces(ctx context.Context, store source.Store) ([]session.Workspace, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !store.Readable {
		return nil, &source.UnsupportedError{Source: session.Opencode, Store: store.Path, Reason: store.Detail}
	}
	database, err := Open(store.Path)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	projects, err := database.ListProjects()
	if err != nil {
		return nil, err
	}
	out := make([]session.Workspace, 0, len(projects))
	for _, project := range projects {
		out = append(out, session.Workspace{
			Source:       session.Opencode,
			Key:          project.ID,
			ID:           project.ID,
			Name:         project.Label(),
			Directory:    project.Worktree,
			Kind:         "project",
			Origin:       "native",
			SessionCount: int(project.SessionCount),
			RootCount:    int(project.RootCount),
			LastActiveMs: project.LastActivityMs,
		})
	}
	return out, nil
}

// Processes 实现 source.ProcessLister（只作提示，不进主链路）。
func (a *Adapter) Processes(ctx context.Context) ([]source.Process, error) {
	processes, err := a.platform.Processes(ctx, "opencode")
	if err != nil {
		return nil, err
	}
	out := make([]source.Process, 0, len(processes))
	for _, process := range processes {
		if !isOpencodeProcessName(process.Name) {
			continue
		}
		store, storeExists, storeNote := a.storeForProcess(process)
		notes := make([]string, 0, 3)
		if process.Note != "" {
			notes = append(notes, process.Note)
		}
		if process.EnvironError != "" {
			notes = append(notes, process.EnvironError)
		}
		if storeNote != "" {
			notes = append(notes, storeNote)
		}
		out = append(out, source.Process{
			PID:         process.PID,
			PPID:        process.PPID,
			Name:        process.Name,
			Args:        process.Args,
			Port:        portOf(process.Args),
			StartedMs:   process.StartedMs,
			Store:       store,
			StoreExists: storeExists,
			Note:        strings.Join(notes, "；"),
		})
	}
	return out, nil
}

// Doctor 实现 source.Diagnoser：逐项检查这个库能不能用、有没有坑。
func (a *Adapter) Doctor(ctx context.Context, store source.Store) []source.Check {
	checks := make([]source.Check, 0, 6)
	if !store.Exists {
		checks = append(checks, source.Check{Level: "error", Code: "store-missing", Title: "库不存在", Detail: store.Path})
		return checks
	}
	if !store.Readable {
		checks = append(checks, source.Check{Level: "warn", Code: "store-unreadable", Title: "库存在但读不了", Detail: store.Detail})
		return checks
	}
	checks = append(checks, source.Check{Level: "ok", Code: "store-ok", Title: "库可读", Detail: store.Evidence})
	if store.ModTime > 0 {
		stamp := time.UnixMilli(store.ModTime).UTC().Format(time.RFC3339)
		checks = append(checks, source.Check{Level: "info", Code: "store-mtime", Title: "最后修改", Detail: stamp})
	}
	if strings.Contains(store.Evidence, "project") {
		checks = append(checks, source.Check{Level: "info", Code: "workspace-source", Title: "工作区来自 project 表", Detail: "DeepWorks 的 workspace 表为空时，工作区按 project/worktree 兜底"})
	} else {
		checks = append(checks, source.Check{Level: "warn", Code: "no-project-table", Title: "没有 project 表", Detail: "这是更旧的 opencode 库：能列会话，但列不了工作区"})
	}
	if store.ModTime > 0 && time.Since(time.UnixMilli(store.ModTime)) < time.Hour {
		checks = append(checks, source.Check{Level: "info", Code: "recent-write", Title: "最近一小时内写过", Detail: "可能在跑：只读打开可读 WAL，但新会话要等它落盘"})
	}
	return checks
}
