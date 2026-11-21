package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zhouyuejie/opencode-trace/internal/attribution"
	"github.com/zhouyuejie/opencode-trace/internal/deepworks"
	"github.com/zhouyuejie/opencode-trace/internal/opencode"
	"github.com/zhouyuejie/opencode-trace/internal/table"
	"github.com/zhouyuejie/opencode-trace/internal/trace"
)

const dbFlagHelp = "opencode 会话库路径（默认自动发现：$OPENCODE_TRACE_DB → DeepWorks → ~/.local/share/opencode）"

// ---------------- workspaces ----------------

func runWorkspaces(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("workspaces", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", "", dbFlagHelp)
	pid := flags.Int("pid", 0, "用某个 opencode 进程在用的库（先用 processes 查 PID）")
	asJSON := flags.Bool("json", false, "输出 JSON（机器可读，含完整 id）")
	showEmpty := flags.Bool("all", false, "连没有会话/目录不存在的工作区也列出来")
	source := flags.String("source", "auto", "工作区来源：auto（优先 DeepWorks 状态文件）/ deepworks / db（只看 opencode 库）")
	flags.Usage = func() {
		fmt.Fprint(stderr, "opencode-trace workspaces —— 列出工作区（DeepWorks 的「我的工作区」）\n\n用法：\n  opencode-trace workspaces [flags]\n\n参数：\n")
		flags.PrintDefaults()
		fmt.Fprint(stderr, "\n说明：\n  工作区清单来自 DeepWorks 的 workspace-state.json（opencode 库里没有这份映射）；\n  会话数按 session.directory 与工作区路径对上来。\n")
	}
	if err := flags.Parse(args); err != nil {
		if errorsIsHelp(err) {
			return exitOK
		}
		return exitBadInput
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "opencode-trace: 不认识的位置参数 %q\n", flags.Arg(0))
		return exitBadInput
	}

	// 库是可选的：没有库也能列出工作区（只是没有会话数）
	database, dbCode := openDatabaseOptional(*dbPath, *pid, stderr)
	if database != nil {
		defer database.Close()
	}
	if *source == "db" && database == nil {
		return dbCode
	}

	directoryStats := map[string]opencode.DirectoryStats{}
	if database != nil {
		stats, err := database.SessionStatsByDirectory()
		if err != nil {
			fmt.Fprintf(stderr, "opencode-trace: 提示：按目录统计会话失败（%v），会话数会显示 -\n", err)
		} else {
			directoryStats = stats
		}
	}

	type workspaceRow struct {
		ID        string
		Name      string
		Kind      string
		Directory string
		Port      int
		Sessions  int64
		Roots     int64
		LastMs    int64
		Source    string
		Missing   bool
		HasCount  bool
	}
	rows := make([]workspaceRow, 0, 8)
	notes := make([]string, 0, 3)

	if *source != "db" {
		state, err := deepworks.ReadState()
		switch {
		case err != nil:
			if *source == "deepworks" {
				fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
				return exitBadInput
			}
			notes = append(notes, "没读到 DeepWorks 工作区状态（"+err.Error()+"），退回 opencode 库")
		default:
			notes = append(notes, "工作区来自 "+filepath.Join(state.ConfigDir, "workspace-state.json"))
			notes = append(notes, state.Warnings...)
			for _, workspace := range state.Workspaces {
				item := workspaceRow{
					ID: workspace.ID, Name: workspace.Label(), Kind: workspace.WorkspaceType,
					Directory: workspace.Path, Port: workspace.APIPort,
					Source: "deepworks", Missing: workspace.DirectoryMissing,
				}
				if item.Kind == "" {
					item.Kind = "local"
				}
				// 库可用时，即便这个目录一个会话都没有，也算"已知 0 个"（默认会被 -all 过滤掉）
				if database != nil {
					item.HasCount = true
				}
				if stats, ok := directoryStats[workspace.Path]; ok {
					item.Sessions, item.Roots, item.LastMs, item.HasCount = stats.Sessions, stats.Roots, stats.LastMs, true
				}
				rows = append(rows, item)
			}
		}
	}

	if len(rows) == 0 {
		// 离线回退：只能从 opencode 库推（project 行 + 会话目录）
		if database == nil {
			fmt.Fprintln(stderr, "opencode-trace: 既读不到 DeepWorks 工作区，也没有可用的 opencode 库")
			return exitBadInput
		}
		projects, err := database.ListProjects()
		if err != nil {
			fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
			return exitBadInput
		}
		for _, project := range projects {
			rows = append(rows, workspaceRow{
				ID: project.ID, Name: project.Label(), Kind: "project", Directory: project.Worktree,
				Sessions: project.SessionCount, Roots: project.RootCount,
				LastMs: deref(project.LastActivityMs), Source: "opencode-project", HasCount: true,
			})
		}
		workspaces, _ := database.ListWorkspaces()
		for _, workspace := range workspaces {
			directory := "-"
			if workspace.Directory != nil {
				directory = *workspace.Directory
			}
			rows = append(rows, workspaceRow{
				ID: workspace.ID, Name: workspace.Name, Kind: "workspace", Directory: directory,
				LastMs: workspace.TimeUsed, Source: "opencode-workspace", HasCount: true,
			})
		}
		known := map[string]bool{}
		for _, item := range rows {
			known[item.Directory] = true
		}
		directories := make([]string, 0, len(directoryStats))
		for directory := range directoryStats {
			if !known[directory] {
				directories = append(directories, directory)
			}
		}
		sort.Strings(directories)
		for _, directory := range directories {
			stats := directoryStats[directory]
			rows = append(rows, workspaceRow{
				ID: "-", Name: baseNameOf(directory), Kind: "session-dir", Directory: directory,
				Sessions: stats.Sessions, Roots: stats.Roots, LastMs: stats.LastMs,
				Source: "session-dir", HasCount: true,
			})
		}
	}

	if *asJSON {
		items := make([]map[string]any, 0, len(rows))
		for _, item := range rows {
			entry := map[string]any{
				"id": item.ID, "name": item.Name, "kind": item.Kind, "directory": item.Directory,
				"sessions": item.Sessions, "roots": item.Roots, "source": item.Source,
				"directory_missing": item.Missing, "api_port": item.Port,
			}
			if item.LastMs > 0 {
				entry["last_activity_ms"] = item.LastMs
				entry["last_activity_iso"] = time.UnixMilli(item.LastMs).UTC().Format(time.RFC3339)
			}
			items = append(items, entry)
		}
		payload := map[string]any{"workspaces": items, "notes": notes}
		if database != nil {
			payload["db"] = database.Path
		}
		return writeJSON(stdout, stderr, payload)
	}

	if len(rows) == 0 {
		fmt.Fprintln(stderr, "opencode-trace: 没有可展示的工作区")
		return exitOK
	}
	if !*showEmpty {
		filtered := make([]workspaceRow, 0, len(rows))
		for _, item := range rows {
			if item.HasCount && item.Sessions == 0 {
				continue
			}
			filtered = append(filtered, item)
		}
		rows = filtered
	}

	tableRows := make([][]string, 0, len(rows))
	for _, item := range rows {
		sessions, roots := "-", "-"
		if item.HasCount {
			sessions = strconv.FormatInt(item.Sessions, 10)
			roots = strconv.FormatInt(item.Roots, 10)
		}
		port := "-"
		if item.Port > 0 {
			port = strconv.Itoa(item.Port)
			if !portAlive(item.Port) {
				port += "（未运行）"
			}
		}
		directory := item.Directory
		if item.Missing {
			directory += "（目录不存在）"
		}
		tableRows = append(tableRows, []string{
			item.ID, item.Name, item.Kind, directory, port, sessions, roots, formatTimePtr(timePtr(item.LastMs)), item.Source,
		})
	}
	fmt.Fprint(stdout, table.Render([]table.Column{
		{Header: "ID"},
		{Header: "NAME"},
		{Header: "KIND"},
		{Header: "DIRECTORY"},
		{Header: "PORT", Align: table.Right},
		{Header: "SESSIONS", Align: table.Right},
		{Header: "ROOTS", Align: table.Right},
		{Header: "LAST ACTIVE"},
		{Header: "SOURCE"},
	}, tableRows))

	if database != nil {
		fmt.Fprintf(stderr, "\n库：%s\n", database.Path)
	}
	for _, note := range notes {
		fmt.Fprintf(stderr, "%s\n", note)
	}
	fmt.Fprint(stderr, "下一步：opencode-trace sessions -workspace <ID 或名字>\n")
	return exitOK
}

// openDatabaseOptional：库打不开不算致命（workspaces 可以只靠 DeepWorks 状态文件）。
func openDatabaseOptional(dbPath string, pid int, stderr io.Writer) (*opencode.DB, int) {
	path, err := resolveDatabase(dbPath, pid)
	if err != nil {
		fmt.Fprintf(stderr, "opencode-trace: 提示：没有可用的 opencode 库（%v），工作区将不带会话数\n", err)
		return nil, exitBadInput
	}
	database, err := opencode.Open(path)
	if err != nil {
		fmt.Fprintf(stderr, "opencode-trace: 提示：打不开库 %s（%v），工作区将不带会话数\n", path, err)
		return nil, exitBadInput
	}
	return database, exitOK
}

// portAlive 判断本地端口是否在监听（看工作区是否在运行）。
func portAlive(port int) bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 120*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// ---------------- sessions ----------------

func runSessions(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("sessions", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", "", dbFlagHelp)
	pid := flags.Int("pid", 0, "用某个 opencode 进程在用的库（先用 processes 查 PID）")
	workspace := flags.String("workspace", "", "只看某个工作区（project id 前缀、worktree 或名字）")
	grep := flags.String("grep", "", "标题/目录/id 里包含该子串")
	limit := flags.Int("limit", 30, "最多列多少条（0 表示不限）")
	includeChildren := flags.Bool("all", false, "连子 agent 会话一起列（默认只列顶层会话）")
	sortBy := flags.String("sort", "updated", "排序：updated / created / tokens / messages")
	asJSON := flags.Bool("json", false, "输出 JSON（机器可读）")
	flags.Usage = func() {
		fmt.Fprint(stderr, "opencode-trace sessions —— 列出会话\n\n用法：\n  opencode-trace sessions [flags]\n\n参数：\n")
		flags.PrintDefaults()
		fmt.Fprint(stderr, "\n示例：\n  opencode-trace sessions -limit 20\n  opencode-trace sessions -workspace fastagi-monorepo -all\n")
	}
	if err := flags.Parse(args); err != nil {
		if errorsIsHelp(err) {
			return exitOK
		}
		return exitBadInput
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "opencode-trace: 不认识的位置参数 %q\n", flags.Arg(0))
		return exitBadInput
	}

	database, code := openDatabase(*dbPath, *pid, stderr)
	if database == nil {
		return code
	}
	defer database.Close()

	projects, err := database.ListProjects()
	if err != nil {
		fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
		return exitBadInput
	}
	projectByID := make(map[string]opencode.Project, len(projects))
	for _, project := range projects {
		projectByID[project.ID] = project
	}
	// 会话目录 → DeepWorks 工作区名（UI 里看到的名字），对不上再退回 opencode project 名
	workspaceByDirectory := map[string]string{}
	if state, stateErr := deepworks.ReadState(); stateErr == nil {
		for _, item := range state.Workspaces {
			workspaceByDirectory[strings.ToLower(strings.TrimRight(item.Path, "/"))] = item.Label()
		}
	}

	// -workspace 可以直接给 DeepWorks 的 ws_ id
	workspaceArg := *workspace
	if strings.HasPrefix(workspaceArg, "ws_") {
		if state, stateErr := deepworks.ReadState(); stateErr == nil {
			for _, item := range state.Workspaces {
				if item.ID == workspaceArg {
					workspaceArg = item.Path
					break
				}
			}
		}
	}
	sessions, err := database.ListSessions(opencode.SessionFilter{
		Workspace:       workspaceArg,
		Grep:            *grep,
		IncludeChildren: *includeChildren,
		Limit:           *limit,
		Sort:            *sortBy,
	})
	if err != nil {
		fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
		return exitBadInput
	}

	if *asJSON {
		items := make([]map[string]any, 0, len(sessions))
		for _, session := range sessions {
			item := sessionMap(session)
			if name, ok := workspaceByDirectory[strings.ToLower(strings.TrimRight(session.Directory, "/"))]; ok {
				item["workspace_name"] = name
				item["workspace_directory"] = session.Directory
			} else if project, ok := projectByID[session.ProjectID]; ok {
				item["workspace_name"] = project.Label()
				item["workspace_worktree"] = project.Worktree
			}
			items = append(items, item)
		}
		return writeJSON(stdout, stderr, map[string]any{"db": database.Path, "sessions": items})
	}

	if len(sessions) == 0 {
		fmt.Fprintln(stderr, "opencode-trace: 没有匹配的会话（试试放开 -workspace / -grep，或加 -all）")
		return exitOK
	}

	rows := make([][]string, 0, len(sessions))
	for _, session := range sessions {
		workspaceLabel := session.ProjectID
		if name, ok := workspaceByDirectory[strings.ToLower(strings.TrimRight(session.Directory, "/"))]; ok {
			workspaceLabel = name
		} else if project, ok := projectByID[session.ProjectID]; ok {
			workspaceLabel = project.Label()
		}
		title := session.Title
		if title == "" {
			title = "（无标题）"
		}
		if session.IsChild() {
			title = "└ " + title
		}
		rows = append(rows, []string{
			session.ID,
			truncateDisplay(workspaceLabel, 16),
			truncateDisplay(title, 44),
			strconv.FormatInt(session.MessageCount, 10),
			formatCount(session.TokensInput),
			formatCount(session.TokensOutput),
			formatTimeLocal(session.TimeUpdated),
		})
	}
	fmt.Fprint(stdout, table.Render([]table.Column{
		{Header: "SESSION"},
		{Header: "WORKSPACE"},
		{Header: "TITLE"},
		{Header: "MSG", Align: table.Right},
		{Header: "TOK IN", Align: table.Right},
		{Header: "TOK OUT", Align: table.Right},
		{Header: "UPDATED"},
	}, rows))

	fmt.Fprintf(stderr, "\n库：%s\n", database.Path)
	fmt.Fprintf(stderr, "显示 %d 个会话", len(sessions))
	if !*includeChildren {
		fmt.Fprint(stderr, "（仅顶层；加 -all 含子 agent 会话）")
	}
	fmt.Fprint(stderr, "\n")
	fmt.Fprint(stderr, "下一步：opencode-trace export -session <会话 id> -out session-export.json -html trace.html\n")
	return exitOK
}

// ---------------- export ----------------

func runExport(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", "", dbFlagHelp)
	pid := flags.Int("pid", 0, "用某个 opencode 进程在用的库（先用 processes 查 PID）")
	sessionID := flags.String("session", "", "要导出的会话 id（支持前缀）")
	outPath := flags.String("out", "-", "输出：session-export.json 路径，- 表示 stdout")
	htmlPath := flags.String("html", "", "可选：同时渲染成 HTML 到这个路径")
	dataPath := flags.String("data", "", "可选：同时写出归一化数据 JSON（配合 -html）")
	generatedAt := flags.String("generated-at", "", "固定 meta.generatedAt（RFC3339），用于可复现构建")
	quiet := flags.Bool("quiet", false, "不打印摘要")
	chainGap := flags.Int("chain-gap", attribution.DefaultChainGap, "补偿链允许夹带的无关步数上限（默认 6）")
	flags.Usage = func() {
		fmt.Fprint(stderr, "opencode-trace export —— 把会话从 SQLite 库导出成 session-export.json\n\n用法：\n  opencode-trace export -session <id> [-out 文件] [-html 文件]\n\n参数：\n")
		flags.PrintDefaults()
		fmt.Fprint(stderr, "\n示例：\n  opencode-trace export -session ses_e1dc9f03 -out session-export.json\n")
		fmt.Fprint(stderr, "  opencode-trace export -session ses_e1dc9f03 | opencode-trace render -out trace.html\n")
	}
	if err := flags.Parse(args); err != nil {
		if errorsIsHelp(err) {
			return exitOK
		}
		return exitBadInput
	}
	if strings.TrimSpace(*sessionID) == "" {
		fmt.Fprintln(stderr, "opencode-trace: 用 -session 指定要导出的会话（先跑 opencode-trace sessions 看列表）")
		return exitBadInput
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "opencode-trace: 不认识的位置参数 %q\n", flags.Arg(0))
		return exitBadInput
	}
	if *outPath == "-" && (*htmlPath == "-" || *dataPath == "-") {
		fmt.Fprintln(stderr, "opencode-trace: -out 已经是 stdout，-html/-data 不能也用 -")
		return exitBadInput
	}

	stamp := time.Now()
	if *generatedAt != "" {
		parsed, err := time.Parse(time.RFC3339, *generatedAt)
		if err != nil {
			fmt.Fprintf(stderr, "opencode-trace: -generated-at 需要 RFC3339 时间：%v\n", err)
			return exitBadInput
		}
		stamp = parsed
	}

	database, code := openDatabase(*dbPath, *pid, stderr)
	if database == nil {
		return code
	}
	defer database.Close()

	document, err := database.ExportDocument(*sessionID, stamp)
	if err != nil {
		fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
		return exitBadInput
	}
	encoded, err := opencode.EncodeDocument(document)
	if err != nil {
		fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
		return exitRuntime
	}
	if err := writeOutput(*outPath, encoded, stdout); err != nil {
		fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
		return exitRuntime
	}

	htmlSize := -1
	if *htmlPath != "" {
		data, err := buildData(encoded, describe(*outPath), stamp, *chainGap)
		if err != nil {
			fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
			return exitBadInput
		}
		html, err := trace.RenderHTML(data)
		if err != nil {
			fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
			return exitRuntime
		}
		if err := writeOutput(*htmlPath, html, stdout); err != nil {
			fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
			return exitRuntime
		}
		if *htmlPath != "-" {
			htmlSize = len(html)
		}
		if *dataPath != "" {
			dataJSON, err := trace.EncodeDataJSON(data)
			if err != nil {
				fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
				return exitRuntime
			}
			if err := writeOutput(*dataPath, dataJSON, stdout); err != nil {
				fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
				return exitRuntime
			}
		}
	} else if *dataPath != "" {
		fmt.Fprintln(stderr, "opencode-trace: -data 需要配合 -html 使用（它写的是页面用的归一化数据）")
		return exitBadInput
	}

	if !*quiet {
		stats := document.GlobalStats
		title := document.Session.Title
		if title == "" {
			title = "（无标题）"
		}
		fmt.Fprintf(stderr, "会话：%s  «%s»\n", document.Session.ID, title)
		fmt.Fprintf(stderr, "消息 %d 条 · part %d 个 · 轮次 %d",
			stats.MessageCount, countParts(document), stats.TotalTurns)
		if len(stats.RoleCounts) > 0 {
			roles := make([]string, 0, len(stats.RoleCounts))
			for role, count := range stats.RoleCounts {
				roles = append(roles, fmt.Sprintf("%s %d", role, count))
			}
			sortStrings(roles)
			fmt.Fprintf(stderr, "（%s）", strings.Join(roles, " / "))
		}
		fmt.Fprint(stderr, "\n")
		if stats.FirstActivityMs != nil && stats.LastActivityMs != nil {
			fmt.Fprintf(stderr, "区间 %s → %s · 跨时 %s\n",
				formatTimeLocal(*stats.FirstActivityMs), formatTimeLocal(*stats.LastActivityMs),
				formatDuration(*stats.LastActivityMs-*stats.FirstActivityMs))
		}
		fmt.Fprint(stderr, "\n输出：")
		if *outPath == "-" {
			fmt.Fprint(stderr, "JSON 已写到 stdout\n")
		} else {
			fmt.Fprintf(stderr, "%s（%s）\n", *outPath, formatBytes(len(encoded)))
		}
		if *htmlPath != "" {
			if *htmlPath == "-" {
				fmt.Fprint(stderr, "      HTML 已写到 stdout\n")
			} else {
				fmt.Fprintf(stderr, "      %s（%s）\n", *htmlPath, formatBytes(htmlSize))
			}
		}
		if *dataPath != "" {
			fmt.Fprintf(stderr, "      %s\n", *dataPath)
		}
	}
	return exitOK
}

// buildData 把导出的 JSON 走一遍 render 的归一化流程，并附上本地归因。
func buildData(encoded []byte, exportPath string, stamp time.Time, chainGap int) (*trace.Data, error) {
	var document trace.Export
	if err := trace.DecodeExport(encoded, &document); err != nil {
		return nil, fmt.Errorf("导出的 JSON 自己解析不回来：%w", err)
	}
	data, err := trace.Normalize(&document, trace.Options{ExportPath: exportPath, GeneratedAt: stamp})
	if err != nil {
		return nil, err
	}
	data.Episodes = attribution.Analyze(data)
	data.Chains = attribution.BuildChains(data, data.Episodes, chainGap)
	return data, nil
}

// ---------------- 公共小工具 ----------------

func openDatabase(dbPath string, pid int, stderr io.Writer) (*opencode.DB, int) {
	path, err := resolveDatabase(dbPath, pid)
	if err != nil {
		fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
		return nil, exitBadInput
	}
	database, err := opencode.Open(path)
	if err != nil {
		fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
		return nil, exitBadInput
	}
	return database, exitOK
}

// resolveDatabase：-pid 优先（从进程环境推库），其次 -db，最后自动发现。
func resolveDatabase(dbPath string, pid int) (string, error) {
	if pid > 0 {
		return opencode.DBPathForPID(pid)
	}
	return opencode.Discover(dbPath)
}

func writeJSON(stdout, stderr io.Writer, payload any) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(payload); err != nil {
		fmt.Fprintf(stderr, "opencode-trace: 写 JSON：%v\n", err)
		return exitRuntime
	}
	return exitOK
}

func projectMap(project opencode.Project) map[string]any {
	out := map[string]any{
		"id":            project.ID,
		"kind":          "project",
		"name":          project.Label(),
		"worktree":      project.Worktree,
		"session_count": project.SessionCount,
		"root_count":    project.RootCount,
		"time_created":  project.TimeCreated,
		"time_updated":  project.TimeUpdated,
	}
	if project.VCS != nil {
		out["vcs"] = *project.VCS
	}
	if project.LastActivityMs != nil {
		out["last_activity_ms"] = *project.LastActivityMs
		out["last_activity_iso"] = time.UnixMilli(*project.LastActivityMs).UTC().Format(time.RFC3339)
	}
	return out
}

func workspaceMap(workspace opencode.Workspace) map[string]any {
	out := map[string]any{
		"id":         workspace.ID,
		"kind":       "workspace",
		"name":       workspace.Name,
		"type":       workspace.Type,
		"project_id": workspace.ProjectID,
		"time_used":  workspace.TimeUsed,
	}
	if workspace.Branch != nil {
		out["branch"] = *workspace.Branch
	}
	if workspace.Directory != nil {
		out["directory"] = *workspace.Directory
	}
	return out
}

func sessionMap(session opencode.SessionRow) map[string]any {
	out := map[string]any{
		"id":                session.ID,
		"project_id":        session.ProjectID,
		"slug":              session.Slug,
		"directory":         session.Directory,
		"title":             session.Title,
		"version":           session.Version,
		"cost":              session.Cost,
		"tokens_input":      session.TokensInput,
		"tokens_output":     session.TokensOutput,
		"tokens_reasoning":  session.TokensReason,
		"tokens_cache_read": session.TokensCacheRead,
		"message_count":     session.MessageCount,
		"part_count":        session.PartCount,
		"time_created_ms":   session.TimeCreated,
		"time_updated_ms":   session.TimeUpdated,
		"time_created_iso":  time.UnixMilli(session.TimeCreated).UTC().Format(time.RFC3339),
		"time_updated_iso":  time.UnixMilli(session.TimeUpdated).UTC().Format(time.RFC3339),
		"is_child":          session.IsChild(),
	}
	if session.ParentID != nil {
		out["parent_id"] = *session.ParentID
	}
	if session.Agent != nil {
		out["agent"] = *session.Agent
	}
	if session.Model != nil {
		out["model"] = json.RawMessage(*session.Model)
	}
	return out
}

func countParts(document *opencode.Document) int {
	total := 0
	for _, message := range document.Messages {
		total += len(message.Parts)
	}
	return total
}

func formatTimeLocal(ms int64) string {
	return time.UnixMilli(ms).Local().Format("2006-01-02 15:04")
}

func formatTimePtr(ms *int64) string {
	if ms == nil {
		return "-"
	}
	return formatTimeLocal(*ms)
}

func formatCount(value int64) string {
	text := strconv.FormatInt(value, 10)
	if value < 1000 {
		return text
	}
	if value < 1000000 {
		return fmt.Sprintf("%.1fk", float64(value)/1000)
	}
	return fmt.Sprintf("%.1fm", float64(value)/1000000)
}

func formatBytes(size int) string {
	switch {
	case size < 0:
		return "-"
	case size < 1024:
		return strconv.Itoa(size) + " B"
	case size < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
	}
}

func formatDuration(ms int64) string {
	if ms < 0 {
		return "-"
	}
	if ms < 1000 {
		return strconv.FormatInt(ms, 10) + " ms"
	}
	if ms < 60000 {
		return fmt.Sprintf("%.1f s", float64(ms)/1000)
	}
	if ms < 3600000 {
		return fmt.Sprintf("%d m %d s", ms/60000, (ms%60000)/1000)
	}
	hours := float64(ms) / 3600000
	if hours < 48 {
		return fmt.Sprintf("%.1f h", hours)
	}
	return fmt.Sprintf("%.1f d", hours/24)
}

func truncateDisplay(text string, width int) string {
	if table.DisplayWidth(text) <= width {
		return text
	}
	runes := []rune(text)
	for len(runes) > 0 && table.DisplayWidth(string(runes)+"…") > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func errorsIsHelp(err error) bool {
	return err == flag.ErrHelp
}

// deref 取指针值（nil 当 0）。
func deref(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

// timePtr 把毫秒时间戳变成指针（0 当作"没有"）。
func timePtr(ms int64) *int64 {
	if ms <= 0 {
		return nil
	}
	return &ms
}

// baseNameOf 取路径最后一段（给"只按目录发现"时当名字）。
func baseNameOf(path string) string {
	trimmed := strings.TrimRight(path, "/\\")
	name := filepath.Base(trimmed)
	if name == "." || name == "/" || name == "" {
		return path
	}
	return name
}
