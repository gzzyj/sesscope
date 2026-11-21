package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gzzyj/sesscope/internal/testfixture"
)

func fixtureDB(t *testing.T) string {
	t.Helper()
	path, err := testfixture.Build(t.TempDir())
	if err != nil {
		t.Fatalf("建 fixture 库：%v", err)
	}
	return path
}

func runArgs(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	// 隔离 DeepWorks 状态文件：默认走"读不到 → 退回各来源原生工作区"分支，
	// 否则测试会读这台机器上真实的工作区，结果不可重复。
	if os.Getenv("DEEPWORKS_CONFIG_HOME") == "" {
		t.Setenv("DEEPWORKS_CONFIG_HOME", t.TempDir())
	}
	var out, errOut bytes.Buffer
	code = run(args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

// decodeEnvelope 解析带 schema 的 JSON 外壳。
func decodeEnvelope(t *testing.T, stdout string, target any) string {
	t.Helper()
	var envelope struct {
		Schema string          `json:"schema"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("输出不是合法 JSON：%v��%s", err, stdout)
	}
	if envelope.Schema == "" {
		t.Fatalf("--json 必须带 schema 字段：%s", stdout)
	}
	if err := json.Unmarshal(envelope.Result, target); err != nil {
		t.Fatalf("解析 result：%v��%s", err, string(envelope.Result))
	}
	return envelope.Schema
}

func TestWorkspacesCommand(t *testing.T) {
	dbPath := fixtureDB(t)
	code, stdout, stderr := runArgs(t, "workspaces", "--db", dbPath)
	if code != exitOK {
		t.Fatalf("退出码 = %d，stderr：%s", code, stderr)
	}
	for _, needle := range []string{"SOURCE", "NAME", "KIND", "DIRECTORY", "SESSIONS", "ORIGIN", "demo", "global"} {
		if !strings.Contains(stdout, needle) {
			t.Errorf("输出里缺少 %q：%s", needle, stdout)
		}
	}
}

func TestWorkspacesFromDeepWorksState(t *testing.T) {
	dbPath := fixtureDB(t)
	configDir := t.TempDir()
	t.Setenv("DEEPWORKS_CONFIG_HOME", configDir)
	state := `{"selectedId":"ws_a","workspaces":[{"id":"ws_a","name":"演示工作区","path":"/work/root"},{"id":"ws_b","name":"空工作区","path":"/nowhere"}]}`
	if err := os.WriteFile(filepath.Join(configDir, "workspace-state.json"), []byte(state), 0o644); err != nil {
		t.Fatalf("写状态文件：%v", err)
	}
	code, stdout, stderr := runArgs(t, "workspaces", "--db", dbPath)
	if code != exitOK {
		t.Fatalf("退出码 = %d，stderr：%s", code, stderr)
	}
	if !strings.Contains(stdout, "ws_a") || !strings.Contains(stdout, "演示工作区") {
		t.Errorf("应当列出应用态工作区：%s", stdout)
	}
	if !strings.Contains(stderr, "工作区来自") {
		t.Errorf("应当说明工作区来源：%s", stderr)
	}
}

func TestWorkspacesJSON(t *testing.T) {
	dbPath := fixtureDB(t)
	configDir := t.TempDir()
	t.Setenv("DEEPWORKS_CONFIG_HOME", configDir)
	state := `{"selectedId":"ws_a","workspaces":[{"id":"ws_a","name":"演示工作区","path":"/work/root"}]}`
	if err := os.WriteFile(filepath.Join(configDir, "workspace-state.json"), []byte(state), 0o644); err != nil {
		t.Fatalf("写状态文件：%v", err)
	}
	code, stdout, stderr := runArgs(t, "workspaces", "--db", dbPath, "--json")
	if code != exitOK {
		t.Fatalf("退出码 = %d，stderr：%s", code, stderr)
	}
	var result struct {
		Workspaces []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			SessionCount int    `json:"session_count"`
			Origin       string `json:"origin"`
		} `json:"workspaces"`
		Notes []string `json:"notes"`
	}
	schema := decodeEnvelope(t, stdout, &result)
	if schema != "sesscope.workspaces.v1" {
		t.Errorf("schema = %q", schema)
	}
	if len(result.Workspaces) == 0 {
		t.Fatal("没有工作区")
	}
	found := false
	for _, item := range result.Workspaces {
		if item.ID == "ws_a" {
			found = true
			if item.Name != "演示工作区" {
				t.Errorf("name = %q", item.Name)
			}
			if item.SessionCount != 1 {
				t.Errorf("session_count = %d，期望 1", item.SessionCount)
			}
			if item.Origin != "app-state" {
				t.Errorf("origin = %q", item.Origin)
			}
		}
	}
	if !found {
		t.Errorf("没有 ws_a：%+v", result.Workspaces)
	}
}

func TestSessionsCommand(t *testing.T) {
	dbPath := fixtureDB(t)
	code, stdout, stderr := runArgs(t, "sessions", "--db", dbPath)
	if code != exitOK {
		t.Fatalf("退出码 = %d，stderr：%s", code, stderr)
	}
	if !strings.Contains(stdout, "SOURCE") || !strings.Contains(stdout, "opencode") {
		t.Errorf("会话表应带来源列：%s", stdout)
	}
	if !strings.Contains(stdout, testfixture.RootSessionID) {
		t.Errorf("缺少根会话：%s", stdout)
	}
	if strings.Contains(stdout, testfixture.ChildSessionID) {
		t.Errorf("默认不应列出子会话：%s", stdout)
	}
	if !strings.Contains(stderr, "仅顶层") {
		t.Errorf("stderr 应提示过滤口径：%s", stderr)
	}

	code, stdout, _ = runArgs(t, "sessions", "--db", dbPath, "--all")
	if code != exitOK {
		t.Fatalf("退出码 = %d", code)
	}
	if !strings.Contains(stdout, testfixture.ChildSessionID) {
		t.Errorf("--all 应包含子会话：%s", stdout)
	}
	if !strings.Contains(stdout, "└") {
		t.Errorf("子会话应当有缩进标记：%s", stdout)
	}
}

func TestSessionsFilterAndJSON(t *testing.T) {
	dbPath := fixtureDB(t)
	code, stdout, _ := runArgs(t, "sessions", "--db", dbPath, "--workspace", "demo", "--json")
	if code != exitOK {
		t.Fatalf("退出码 = %d", code)
	}
	var result struct {
		Items []struct {
			Ref struct {
				Source string `json:"source"`
				ID     string `json:"id"`
			} `json:"ref"`
			Messages int `json:"messages"`
		} `json:"Items"`
	}
	// 结构体字段名大小写不敏感，Go 的 encoding/json 能对上 items。
	schema := decodeEnvelope(t, stdout, &result)
	if schema != "sesscope.sessions.v1" {
		t.Errorf("schema = %q", schema)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %d，期望 1（workspace=demo）", len(result.Items))
	}
	if result.Items[0].Ref.ID != testfixture.DemoSessionID {
		t.Errorf("id = %q", result.Items[0].Ref.ID)
	}
	if result.Items[0].Ref.Source != "opencode" {
		t.Errorf("source = %q", result.Items[0].Ref.Source)
	}
	if result.Items[0].Messages != 0 {
		t.Errorf("messages = %d", result.Items[0].Messages)
	}
}

func TestExportLegacyFormat(t *testing.T) {
	dbPath := fixtureDB(t)
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "session-export.json")
	code, _, stderr := runArgs(t, "export", "--db", dbPath, "--source", "opencode", "--session", "ses_root", "--format", "legacy", "--out", jsonPath)
	if code != exitOK {
		t.Fatalf("退出码 = %d，stderr：%s", code, stderr)
	}
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("读导出文件：%v", err)
	}
	var document struct {
		ExportInfo struct {
			SessionID string `json:"session_id"`
		} `json:"export_info"`
		Session struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"session"`
		GlobalStats struct {
			MessageCount int `json:"message_count"`
		} `json:"global_stats"`
		Messages []struct {
			MessageID string `json:"message_id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("导出文件不是合法 JSON：%v", err)
	}
	if document.Session.ID != testfixture.RootSessionID || document.Session.Title != "根会话：导出一条流水线" {
		t.Errorf("session = %+v", document.Session)
	}
	if document.GlobalStats.MessageCount != 2 || len(document.Messages) != 2 {
		t.Errorf("消息数 = %d / %d，期望 2", document.GlobalStats.MessageCount, len(document.Messages))
	}
}

func TestExportCanonicalThenRender(t *testing.T) {
	dbPath := fixtureDB(t)
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "session.json")
	htmlPath := filepath.Join(dir, "trace.html")
	code, _, stderr := runArgs(t, "export", "--db", dbPath, "--session", "opencode:ses_root", "--out", jsonPath, "--html", htmlPath)
	if code != exitOK {
		t.Fatalf("退出码 = %d，stderr：%s", code, stderr)
	}
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("读导出文件：%v", err)
	}
	var document struct {
		Schema  string `json:"schema"`
		Session struct {
			Ref  struct{ ID string } `json:"ref"`
			Meta struct {
				Title string `json:"title"`
			} `json:"meta"`
			Messages []struct {
				ID string `json:"id"`
			} `json:"messages"`
		} `json:"session"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("规范导出不是合法 JSON：%v", err)
	}
	if document.Schema != "sesscope/session.v1" {
		t.Errorf("schema = %q", document.Schema)
	}
	if document.Session.Ref.ID != testfixture.RootSessionID || document.Session.Meta.Title != "根会话：导出一条流水线" {
		t.Errorf("session = %+v", document.Session.Ref)
	}
	if len(document.Session.Messages) != 2 {
		t.Errorf("消息数 = %d，期望 2", len(document.Session.Messages))
	}
	html, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatalf("读 HTML：%v", err)
	}
	if !bytes.HasPrefix(html, []byte("<!doctype html>")) {
		t.Error("HTML 开头不对")
	}
	if !bytes.Contains(html, []byte("根会话：导出一条流水线")) {
		t.Error("HTML 里没有会话标题")
	}
	if !strings.Contains(stderr, "轮次 1") {
		t.Errorf("stderr 摘要 = %s", stderr)
	}
}

func TestExportMissingSession(t *testing.T) {
	dbPath := fixtureDB(t)
	code, stdout, stderr := runArgs(t, "export", "--db", dbPath, "--source", "opencode", "--session", "ses_nope")
	if code != exitBadInput {
		t.Errorf("退出码 = %d，期望 %d", code, exitBadInput)
	}
	if stdout != "" {
		t.Errorf("失败时不该写 stdout")
	}
	if !strings.Contains(stderr, "会话不存在") {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestExportRejectsBareSessionIDWithoutSource(t *testing.T) {
	dbPath := fixtureDB(t)
	code, stdout, stderr := runArgs(t, "export", "--db", dbPath, "--session", "ses_root")
	if code != exitBadInput {
		t.Errorf("退出码 = %d，期望 %d", code, exitBadInput)
	}
	if stdout != "" {
		t.Errorf("失败时不该写 stdout")
	}
	if !strings.Contains(stderr, "只在所属来源内唯一") || !strings.Contains(stderr, "--source") {
		t.Errorf("stderr 应说明必须带来源：%s", stderr)
	}
}

func TestExportAcceptsQualifiedSelector(t *testing.T) {
	dbPath := fixtureDB(t)
	// 只给 id 前缀、不给 --source，靠 source:id 带出唯一来源。
	code, stdout, stderr := runArgs(t, "export", "--db", dbPath, "--session", "opencode:ses_ro")
	if code != exitOK {
		t.Fatalf("退出码 = %d，stderr：%s", code, stderr)
	}
	var document struct {
		Schema  string `json:"schema"`
		Session struct {
			Ref struct {
				Source string `json:"source"`
				ID     string `json:"id"`
			} `json:"ref"`
		} `json:"session"`
	}
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("规范导出不是合法 JSON：%v", err)
	}
	if document.Schema != "sesscope/session.v1" {
		t.Errorf("schema = %q", document.Schema)
	}
	if document.Session.Ref.Source != "opencode" || document.Session.Ref.ID != testfixture.RootSessionID {
		t.Errorf("ref = %+v", document.Session.Ref)
	}
}

func TestExportRejectsSourceOutsideSelection(t *testing.T) {
	dbPath := fixtureDB(t)
	code, _, stderr := runArgs(t, "export", "--db", dbPath, "--source", "dsh", "--session", "opencode:ses_root")
	if code != exitBadInput {
		t.Errorf("退出码 = %d，期望 %d", code, exitBadInput)
	}
	if !strings.Contains(stderr, "不在本次 --source 范围内") {
		t.Errorf("stderr 应指出来源冲突：%s", stderr)
	}
}

func TestSessionsPrintsQualifiedSelector(t *testing.T) {
	dbPath := fixtureDB(t)
	code, stdout, stderr := runArgs(t, "sessions", "--db", dbPath, "--limit", "0")
	if code != exitOK {
		t.Fatalf("退出码 = %d，stderr：%s", code, stderr)
	}
	if !strings.Contains(stdout, "opencode:"+testfixture.DemoSessionID) {
		t.Errorf("SESSION 列应是 source:id，实际：%s", stdout)
	}
}

func TestExportRequiresSession(t *testing.T) {
	dbPath := fixtureDB(t)
	code, _, stderr := runArgs(t, "export", "--db", dbPath)
	if code != exitBadInput {
		t.Errorf("退出码 = %d", code)
	}
	if !strings.Contains(stderr, "--session") {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, stdout, stderr := runArgs(t, "frobnicate")
	if code != exitBadInput {
		t.Errorf("退出码 = %d", code)
	}
	if stdout != "" {
		t.Errorf("不该往 stdout 写")
	}
	if !strings.Contains(stderr, "不认识的子命令") || !strings.Contains(stderr, "workspaces") {
		t.Errorf("stderr 应给出用法：%s", stderr)
	}
}

func TestHelpCommand(t *testing.T) {
	code, stdout, _ := runArgs(t, "help")
	if code != exitOK {
		t.Errorf("退出码 = %d", code)
	}
	for _, needle := range []string{"sources", "workspaces", "sessions", "export", "render"} {
		if !strings.Contains(stdout, needle) {
			t.Errorf("帮助里缺少 %q", needle)
		}
	}
}

func TestBadStorePath(t *testing.T) {
	code, _, stderr := runArgs(t, "sessions", "--db", filepath.Join(t.TempDir(), "nope.db"))
	if code != exitBadInput {
		t.Errorf("退出码 = %d", code)
	}
	if !strings.Contains(stderr, "没有可用的会话存储") {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestSourcesAndDoctor(t *testing.T) {
	dbPath := fixtureDB(t)
	code, stdout, stderr := runArgs(t, "sources", "--db", dbPath)
	if code != exitOK {
		t.Fatalf("sources 退出码 = %d，stderr：%s", code, stderr)
	}
	for _, needle := range []string{"SOURCE", "STATUS", "FORMAT", "opencode", "ok"} {
		if !strings.Contains(stdout, needle) {
			t.Errorf("sources 输出缺少 %q：%s", needle, stdout)
		}
	}
	code, stdout, stderr = runArgs(t, "sources", "--db", dbPath, "--explain")
	if code != exitOK {
		t.Fatalf("sources --explain 退出码 = %d，stderr：%s", code, stderr)
	}
	if !strings.Contains(stderr, "规则：") || !strings.Contains(stderr, "证据：") {
		t.Errorf("--explain 应打印规则与证据：%s", stderr)
	}
	code, stdout, stderr = runArgs(t, "doctor", "--db", dbPath)
	if code != exitOK {
		t.Fatalf("doctor 退出码 = %d，stderr：%s", code, stderr)
	}
	if !strings.Contains(stdout, "store-ok") {
		t.Errorf("doctor 应当给出可读结论：%s", stdout)
	}
}

func TestLegacySingleDashFlagsStillWork(t *testing.T) {
	dbPath := fixtureDB(t)
	code, stdout, stderr := runArgs(t, "sessions", "-db", dbPath, "-limit", "1", "-json")
	if code != exitOK {
		t.Fatalf("退出码 = %d，stderr：%s", code, stderr)
	}
	if !strings.Contains(stderr, "已弃用") {
		t.Errorf("老写法应当提示已弃用：%s", stderr)
	}
	var result struct {
		Items []json.RawMessage `json:"items"`
	}
	decodeEnvelope(t, stdout, &result)
	if len(result.Items) != 1 {
		t.Errorf("items = %d，期望 1", len(result.Items))
	}
}
