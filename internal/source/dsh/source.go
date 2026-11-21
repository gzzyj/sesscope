package dsh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gzzyj/sesscope/internal/session"
	"github.com/gzzyj/sesscope/internal/source"
	"github.com/gzzyj/sesscope/internal/source/jsonl"
)

// Adapter 是 dsh 的 Source 实现。
//
// 存储形态：~/.dsh/sessions/<cwd-slug>/session-<uuid>/session.v{3,4}.jsonl.zstd
// （append-only JSONL + zstd 多帧；正在写的会话尾部可能被截断）。
type Adapter struct{}

// New 返回 dsh 适配器。
func New() *Adapter { return &Adapter{} }

// ID 实现 source.Source。
func (a *Adapter) ID() session.SourceID { return session.DSH }

// Capabilities 实现 source.Source。
func (a *Adapter) Capabilities() source.Capabilities {
	return source.Capabilities{
		ProcessDiscovery:  false,
		WorkspaceCatalog:  false,
		ServerSideFilter:  false,
		PrefixLookup:      false,
		SessionTitles:     true,
		SubagentTree:      true,
		TurnHints:         true,
		Tokens:            true,
		Cost:              false,
		ReasoningText:     true,
		ToolArgs:          true,
		ToolOutput:        true,
		ToolStatus:        true,
		CompactionRecords: true,
		LiveActivity:      false,
		ListCost:          source.ListHeaderScan,
		OpenCost:          source.OpenStream,
	}
}

// Locate 按 L0 契约 → L1 环境 → L2 默认路径定位会话根目录。
func (a *Adapter) Locate(ctx context.Context, l source.Locator) ([]source.Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l.Store != "" {
		return []source.Store{probe(l.Store, "l0:--store")}, nil
	}
	home := l.Home
	rule := "l1:--home"
	if home == "" {
		home = strings.TrimSpace(os.Getenv("DSH_HOME"))
		rule = "l1:env DSH_HOME"
	}
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("取 HOME：%w", err)
		}
		home = filepath.Join(userHome, ".dsh")
		rule = "l2:dsh-default"
	}
	return []source.Store{probe(filepath.Join(home, "sessions"), rule)}, nil
}

func probe(path, rule string) source.Store {
	store := source.Store{Source: session.DSH, Path: path, Kind: "jsonl-dir", Format: formatName, ReadOnly: true, Rule: rule}
	info, err := os.Stat(path)
	if err != nil {
		store.Detail = "目录不存在（已尝试）"
		return store
	}
	store.Exists = true
	store.ModTime = info.ModTime().UnixMilli()
	if !info.IsDir() {
		store.Kind = "jsonl-file"
		store.SizeBytes = info.Size()
		store.Readable = true
		store.Version = versionOfFile(path)
		store.Evidence = filepath.Base(path)
		return store
	}
	files, err := sessionFiles(path, "jsonl-dir")
	if err != nil {
		store.Detail = "扫描会话目录失败：" + err.Error()
		return store
	}
	if len(files) == 0 {
		store.Detail = "目录里没有 session.v*.jsonl* 会话文件"
		return store
	}
	store.Readable = true
	versions := map[string]bool{}
	for _, file := range files {
		if file.Version != "" {
			versions["v"+file.Version] = true
		}
	}
	keys := make([]string, 0, len(versions))
	for key := range versions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	store.Version = strings.Join(keys, "+")
	store.Evidence = fmt.Sprintf("%d 个会话文件，版本 %s", len(files), store.Version)
	return store
}

type sessionFile struct {
	Path    string
	ID      string
	Version string
}

// sessionFiles 收集会话文件：
//   - jsonl-dir：<root>/<slug>/session-<id>/session.v*.jsonl*
//   - session-dir：<root>/session.v*.jsonl*
//   - jsonl-file：就是它自己
//
// 同一会话有多个版本时只取最高版本（v4 > v3）。
func sessionFiles(path, kind string) ([]sessionFile, error) {
	switch kind {
	case "jsonl-file":
		return []sessionFile{{Path: path, ID: sessionIDFromPath(path), Version: versionOfFile(path)}}, nil
	case "session-dir":
		best, ok := bestVersion(path)
		if !ok {
			return nil, nil
		}
		return []sessionFile{best}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]sessionFile, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		slugDir := filepath.Join(path, entry.Name())
		if strings.HasPrefix(entry.Name(), "session-") {
			if best, ok := bestVersion(slugDir); ok {
				out = append(out, best)
				continue
			}
		}
		children, err := os.ReadDir(slugDir)
		if err != nil {
			continue
		}
		for _, child := range children {
			if !child.IsDir() {
				continue
			}
			if best, ok := bestVersion(filepath.Join(slugDir, child.Name())); ok {
				out = append(out, best)
			}
		}
	}
	return out, nil
}

// bestVersion 在会话目录里挑最高版本的 session.v<N>.jsonl*。
func bestVersion(dir string) (sessionFile, bool) {
	matches, err := filepath.Glob(filepath.Join(dir, "session.v*.jsonl*"))
	if err != nil || len(matches) == 0 {
		return sessionFile{}, false
	}
	best := sessionFile{Path: matches[0], Version: versionOfFile(matches[0])}
	for _, candidate := range matches[1:] {
		version := versionOfFile(candidate)
		if version > best.Version {
			best = sessionFile{Path: candidate, Version: version}
		}
	}
	best.ID = sessionIDFromPath(dir)
	if best.ID == "" {
		best.ID = sessionIDFromPath(best.Path)
	}
	return best, true
}

// versionOfFile 从文件名 session.v4.jsonl.zstd 里取 "4"。
func versionOfFile(path string) string {
	base := filepath.Base(path)
	const prefix = "session.v"
	if !strings.HasPrefix(base, prefix) {
		return ""
	}
	rest := base[len(prefix):]
	if index := strings.IndexByte(rest, '.'); index > 0 {
		return rest[:index]
	}
	return ""
}

func sessionIDFromPath(path string) string {
	base := filepath.Base(path)
	if strings.HasPrefix(base, "session.v") {
		return decodeSessionDir(filepath.Base(filepath.Dir(path)))
	}
	return decodeSessionDir(base)
}

// encodeSessionDir 与 dsh 的目录命名保持一致：不安全字符写成 ~XXXX（大写十六进制码点）。
func encodeSessionDir(id string) string {
	var builder strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			builder.WriteRune(r)
		default:
			fmt.Fprintf(&builder, "~%04X", r)
		}
	}
	return builder.String()
}

// decodeSessionDir 是 encodeSessionDir 的逆运算，用于从目录名还原真实会话 id。
func decodeSessionDir(name string) string {
	if !strings.Contains(name, "~") {
		return name
	}
	var builder strings.Builder
	for index := 0; index < len(name); {
		if name[index] == '~' && index+4 < len(name) {
			if code, err := strconv.ParseUint(name[index+1:index+5], 16, 32); err == nil {
				builder.WriteRune(rune(code))
				index += 5
				continue
			}
		}
		builder.WriteByte(name[index])
		index++
	}
	return builder.String()
}

// Sessions 只读每个会话文件的第一行（头部），因此列 300 个会话也很快。
func (a *Adapter) Sessions(ctx context.Context, store source.Store, q source.Query) ([]session.Summary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !store.Exists || !store.Readable {
		return nil, &source.UnsupportedError{Source: session.DSH, Store: store.Path, Reason: store.Detail}
	}
	files, err := sessionFiles(store.Path, store.Kind)
	if err != nil {
		return nil, err
	}
	out := make([]session.Summary, 0, len(files))
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, a.summaryFor(ctx, file, store))
	}
	return out, nil
}

func (a *Adapter) summaryFor(ctx context.Context, file sessionFile, store source.Store) session.Summary {
	summary := session.Summary{
		Ref:         session.Ref{Source: session.DSH, Store: store.Path, ID: file.ID, Object: file.Path},
		UpdatedKind: "file-mtime",
		Messages:    -1,
		Flags:       []string{},
	}
	head, err := readHeader(ctx, file.Path)
	if err != nil {
		summary.Title = file.ID
		summary.TitleSource = "none"
		summary.Diagnostics = append(summary.Diagnostics, session.Diagnostic{
			Level: "warn", Code: "unreadable-session", Title: "读不到会话头部", Detail: err.Error(),
		})
		return summary
	}
	if head.ID != "" {
		summary.ID = head.ID
	}
	summary.WorkspaceKey = head.Cwd
	summary.Directory = head.Cwd
	summary.Agent = head.AgentPreset
	summary.ParentID = head.ParentSession
	summary.IsChild = head.ParentSession != "" || head.DelegationDepth > 0
	if head.CreatedAt > 0 {
		summary.CreatedMs = session.I64(head.CreatedAt)
	}
	if info, err := os.Stat(file.Path); err == nil {
		summary.UpdatedMs = session.I64(info.ModTime().UnixMilli())
	}
	if head.IsSeeded {
		summary.Flags = append(summary.Flags, "seeded")
	}
	if title, tokens, ok := readProjcache(dshHome(store.Path), summary.ID); ok {
		summary.Title = title
		summary.TitleSource = "native"
		if tokens != nil {
			summary.Tokens = *tokens
		}
	}
	if summary.Title == "" {
		summary.TitleSource = "none"
	}
	summary.Diagnostics = append(summary.Diagnostics, session.Diagnostic{
		Level:  "info",
		Code:   "mtime-updated",
		Title:  "UPDATED 取自文件修改时间",
		Detail: "dsh 不在头部记录最后活动时间；要按最后活动排序需要全扫（代价高）。",
	})
	return summary
}

func readHeader(ctx context.Context, path string) (header, error) {
	var head header
	n := 0
	err := jsonl.Each(ctx, path, func(number int, line []byte) error {
		n = number
		if number == 1 {
			return json.Unmarshal(line, &head)
		}
		return errStop
	})
	if errors.Is(err, errStop) {
		err = nil
	}
	if err != nil {
		return header{}, err
	}
	if n == 0 {
		return header{}, fmt.Errorf("文件是空的")
	}
	return head, nil
}

var errStop = errors.New("stop")

// dshHome 从 sessions 根目录推出 ~/.dsh（projcache 在它下面）。
func dshHome(storePath string) string {
	cleaned := filepath.Clean(storePath)
	if filepath.Base(cleaned) == "sessions" {
		return filepath.Dir(cleaned)
	}
	return cleaned
}

// readProjcache 读 ~/.dsh/storages/session_projcache/sessions/<id>.json 的标题与累计用量。
// 快照可能不存在（best-effort），失败就当没有——但绝不编造标题。
func readProjcache(home, id string) (string, *session.Tokens, bool) {
	if home == "" || id == "" {
		return "", nil, false
	}
	dir := filepath.Join(home, "storages", "session_projcache", "sessions")
	candidates := []string{filepath.Join(dir, id+".json")}
	if strings.HasPrefix(id, "session-") {
		candidates = append(candidates, filepath.Join(dir, strings.TrimPrefix(id, "session-")+".json"))
	}
	for _, path := range candidates {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var snapshot map[string]any
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			continue
		}
		rows := childMap(childMap(snapshot, "record"), "rows")
		title := stringValue(childMap(rows, "title"), "val")
		var tokens *session.Tokens
		if totals := childMap(childMap(childMap(rows, "tokenUsage"), "val"), "totals"); len(totals) > 0 {
			tokens = &session.Tokens{
				Input:      int64Value(totals, "uncachedInputTokens"),
				Output:     int64Value(totals, "outputTokens"),
				CacheRead:  int64Value(totals, "cacheReadTokens"),
				CacheWrite: int64Value(totals, "cacheWriteTokens"),
			}
		}
		return title, tokens, true
	}
	return "", nil, false
}

func childMap(object map[string]any, key string) map[string]any {
	if object == nil {
		return nil
	}
	value, _ := object[key].(map[string]any)
	return value
}

func stringValue(object map[string]any, key string) string {
	if object == nil {
		return ""
	}
	value, _ := object[key].(string)
	return value
}

func int64Value(object map[string]any, key string) *int64 {
	if object == nil {
		return nil
	}
	value, ok := object[key].(float64)
	if !ok {
		return nil
	}
	number := int64(value)
	return &number
}

// Open 取一个会话：按 id 找文件后流式映射。
func (a *Adapter) Open(ctx context.Context, store source.Store, ref session.Ref, o source.OpenOptions) (*session.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !store.Exists || !store.Readable {
		return nil, &source.UnsupportedError{Source: session.DSH, Store: store.Path, Reason: store.Detail}
	}
	path, err := locateSessionFile(store, ref)
	if err != nil {
		return nil, err
	}
	return mapSession(ctx, path, store, o)
}

func locateSessionFile(store source.Store, ref session.Ref) (string, error) {
	switch store.Kind {
	case "jsonl-file":
		return store.Path, nil
	case "session-dir":
		if best, ok := bestVersion(store.Path); ok {
			return best.Path, nil
		}
		return "", fmt.Errorf("目录里没有会话文件：%s", store.Path)
	}
	// 目录名会把不安全字符转义成 ~XXXX（例如 IM 会话 id 里的 ':' → ~003A），
	// 而 header 里的 id 是原文；两种写法都要试，否则这类会话永远打不开。
	names := []string{ref.ID}
	if encoded := encodeSessionDir(ref.ID); encoded != ref.ID {
		names = append(names, encoded)
	}
	matches := make([]string, 0, 1)
	for _, name := range names {
		found, err := filepath.Glob(filepath.Join(store.Path, "*", name, "session.v*.jsonl*"))
		if err != nil {
			return "", err
		}
		matches = append(matches, found...)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("%w：%s（在 %s 下没有找到会话目录）", source.ErrNotFound, ref.ID, store.Path)
	}
	best := matches[0]
	for _, candidate := range matches[1:] {
		if versionOfFile(candidate) > versionOfFile(best) {
			best = candidate
		}
	}
	return best, nil
}

// Workspaces 按会话目录分组：dsh 没有原生工作区概念，这里给出的是派生视图。
func (a *Adapter) Workspaces(ctx context.Context, store source.Store) ([]session.Workspace, error) {
	summaries, err := a.Sessions(ctx, store, source.Query{IncludeChildren: true})
	if err != nil {
		return nil, err
	}
	type bucket struct {
		sessions int
		roots    int
		last     *int64
	}
	buckets := map[string]*bucket{}
	for _, summary := range summaries {
		if summary.Directory == "" {
			continue
		}
		current := buckets[summary.Directory]
		if current == nil {
			current = &bucket{}
			buckets[summary.Directory] = current
		}
		current.sessions++
		if !summary.IsChild {
			current.roots++
		}
		if summary.UpdatedMs != nil && (current.last == nil || *summary.UpdatedMs > *current.last) {
			current.last = summary.UpdatedMs
		}
	}
	out := make([]session.Workspace, 0, len(buckets))
	for directory, item := range buckets {
		out = append(out, session.Workspace{
			Source:       session.DSH,
			Key:          directory,
			ID:           directory,
			Name:         filepath.Base(directory),
			Directory:    directory,
			Kind:         "directory",
			Origin:       "derived",
			SessionCount: item.sessions,
			RootCount:    item.roots,
			LastActiveMs: item.last,
		})
	}
	return out, nil
}

// Doctor 实现 source.Diagnoser。
func (a *Adapter) Doctor(ctx context.Context, store source.Store) []source.Check {
	checks := make([]source.Check, 0, 5)
	if !store.Exists {
		return append(checks, source.Check{Level: "error", Code: "store-missing", Title: "会话目录不存在", Detail: store.Path})
	}
	if !store.Readable {
		return append(checks, source.Check{Level: "warn", Code: "store-unreadable", Title: "目录不可用", Detail: store.Detail})
	}
	checks = append(checks, source.Check{Level: "ok", Code: "store-ok", Title: "会话目录可读", Detail: store.Evidence})
	checks = append(checks, source.Check{Level: "info", Code: "format-version", Title: "格式版本", Detail: store.Version})
	if strings.Contains(store.Version, "v3") && !strings.Contains(store.Version, "v4") {
		checks = append(checks, source.Check{Level: "info", Code: "v3-only", Title: "只有 v3 会话", Detail: "v3 没有 compaction 事件，压缩耗时会是 0"})
	}
	checks = append(checks, source.Check{Level: "info", Code: "mtime-updated", Title: "列表的 UPDATED 取自文件修改时间", Detail: "zstd 不能随机读，按最后活动排序需要全扫"})
	if store.ModTime > 0 {
		checks = append(checks, source.Check{Level: "info", Code: "store-mtime", Title: "目录最后修改", Detail: time.UnixMilli(store.ModTime).UTC().Format(time.RFC3339)})
	}
	return checks
}
