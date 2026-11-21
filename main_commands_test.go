package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhouyuejie/opencode-trace/internal/testfixture"
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
	// 隔离 DeepWorks 状态文件：默认走"读不到 → 回退 opencode 库"分支，
	// 否则测试会读这台机器上真实的工作区，结果不可重复。
	t.Setenv("DEEPWORKS_CONFIG_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	code = run(args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestWorkspacesCommand(t *testing.T) {
	dbPath := fixtureDB(t)
	code, stdout, stderr := runArgs(t, "workspaces", "-db", dbPath)
	if code != exitOK {
		t.Fatalf("退出码 = %d，stderr：%s", code, stderr)
	}
	// 没有 DeepWorks 状态文件时，回退到 opencode 库（project + 会话目录）
	for _, needle := range []string{"NAME", "KIND", "project", testfixture.GlobalProjectID, "demo", "SESSIONS", "SOURCE"} {
		if !strings.Contains(stdout, needle) {
			t.Errorf("输出里缺少 %q：\n%s", needle, stdout)
		}
	}
	if !strings.Contains(stderr, "没读到 DeepWorks 工作区状态") {
		t.Errorf("应当说明回退原因：%s", stderr)
	}
}

func TestWorkspacesFromDeepWorksState(t *testing.T) {
	dbPath := fixtureDB(t)
	configDir := t.TempDir()
	t.Setenv("DEEPWORKS_CONFIG_HOME", configDir)
	state := "{\"selectedId\":\"ws_a\",\"workspaces\":[" +
		"{\"id\":\"ws_a\",\"name\":\"演示工作区\",\"path\":\"/work/root\"}," +
		"{\"id\":\"ws_b\",\"name\":\"空工作区\",\"path\":\"/nowhere\"}]}"
	if err := os.WriteFile(filepath.Join(configDir, "workspace-state.json"), []byte(state), 0o644); err != nil {
		t.Fatalf("写状态文件：%v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "deepworks-port-state.json"),
		[]byte("{\"workspacePorts\":{\"/work/root\":12345}}"), 0o644); err != nil {
		t.Fatalf("写端口文件：%v", err)
	}

	var out, errOut bytes.Buffer
	code := run([]string{"workspaces", "-db", dbPath}, strings.NewReader(""), &out, &errOut)
	if code != exitOK {
		t.Fatalf("退出码 = %d，stderr：%s", code, errOut.String())
	}
	// fixture 里根会话的目录是 /work/root，应当对上 ws_a 并带出会话数
	if !strings.Contains(out.String(), "演示工作区") {
		t.Errorf("应当列出 DeepWorks 工作区名：\n%s", out.String())
	}
	if !strings.Contains(out.String(), "deepworks") {
		t.Errorf("来源列应当是 deepworks：\n%s", out.String())
	}
	if strings.Contains(out.String(), "空工作区") {
		t.Errorf("0 会话的工作区默认不该显示（除非 -all）：\n%s", out.String())
	}
}

func TestWorkspacesJSON(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("DEEPWORKS_CONFIG_HOME", configDir)
	state := "{\"workspaces\":[{\"id\":\"ws_a\",\"name\":\"演示工作区\",\"path\":\"/work/root\"}]}"
	if err := os.WriteFile(filepath.Join(configDir, "workspace-state.json"), []byte(state), 0o644); err != nil {
		t.Fatalf("写状态文件：%v", err)
	}
	dbPath := fixtureDB(t)

	var out, errOut bytes.Buffer
	code := run([]string{"workspaces", "-db", dbPath, "-json"}, strings.NewReader(""), &out, &errOut)
	if code != exitOK {
		t.Fatalf("退出码 = %d，stderr：%s", code, errOut.String())
	}
	var payload struct {
		Workspaces []map[string]any `json:"workspaces"`
		Notes      []string         `json:"notes"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("输出不是合法 JSON：%v\n%s", err, out.String())
	}
	if len(payload.Workspaces) != 1 {
		t.Fatalf("workspaces = %d，期望 1", len(payload.Workspaces))
	}
	if payload.Workspaces[0]["id"] != "ws_a" || payload.Workspaces[0]["name"] != "演示工作区" {
		t.Errorf("工作区字段 = %v", payload.Workspaces[0])
	}
	if payload.Workspaces[0]["sessions"].(float64) != 1 {
		t.Errorf("应当按目录对上会话数，实际 %v", payload.Workspaces[0]["sessions"])
	}
}

func TestSessionsCommand(t *testing.T) {
	dbPath := fixtureDB(t)
	code, stdout, stderr := runArgs(t, "sessions", "-db", dbPath)
	if code != exitOK {
		t.Fatalf("退出码 = %d，stderr：%s", code, stderr)
	}
	if !strings.Contains(stdout, testfixture.RootSessionID) {
		t.Errorf("缺少根会话：\n%s", stdout)
	}
	if strings.Contains(stdout, testfixture.ChildSessionID) {
		t.Errorf("默认不应列出子会话：\n%s", stdout)
	}
	if !strings.Contains(stderr, "仅顶层") {
		t.Errorf("stderr 应提示过滤口径：%s", stderr)
	}

	code, stdout, _ = runArgs(t, "sessions", "-db", dbPath, "-all")
	if code != exitOK {
		t.Fatalf("退出码 = %d", code)
	}
	if !strings.Contains(stdout, testfixture.ChildSessionID) {
		t.Errorf("-all 应包含子会话：\n%s", stdout)
	}
	if !strings.Contains(stdout, "└") {
		t.Errorf("子会话应当有缩进标记：\n%s", stdout)
	}
}

func TestSessionsFilterAndJSON(t *testing.T) {
	dbPath := fixtureDB(t)
	code, stdout, _ := runArgs(t, "sessions", "-db", dbPath, "-workspace", "demo", "-json")
	if code != exitOK {
		t.Fatalf("退出码 = %d", code)
	}
	var payload struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("输出不是合法 JSON：%v\n%s", err, stdout)
	}
	if len(payload.Sessions) != 1 {
		t.Fatalf("sessions = %d，期望 1", len(payload.Sessions))
	}
	item := payload.Sessions[0]
	if item["id"] != testfixture.DemoSessionID {
		t.Errorf("id = %v", item["id"])
	}
	if item["workspace_name"] != "demo" {
		t.Errorf("workspace_name = %v", item["workspace_name"])
	}
	if item["message_count"].(float64) != 0 {
		t.Errorf("message_count = %v", item["message_count"])
	}
}

func TestExportCommandWritesJSONAndHTML(t *testing.T) {
	dbPath := fixtureDB(t)
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "session-export.json")
	htmlPath := filepath.Join(dir, "trace.html")
	code, _, stderr := runArgs(t, "export", "-db", dbPath,
		"-session", "ses_root", "-out", jsonPath, "-html", htmlPath)
	if code != exitOK {
		t.Fatalf("退出码 = %d，stderr：%s", code, stderr)
	}
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("读导出文件：%v", err)
	}
	var document struct {
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
	if !strings.Contains(stderr, "消息 2 条") {
		t.Errorf("stderr 摘要 = %s", stderr)
	}
}

func TestExportToStdoutThenRender(t *testing.T) {
	dbPath := fixtureDB(t)
	code, jsonOut, stderr := runArgs(t, "export", "-db", dbPath, "-session", testfixture.RootSessionID)
	if code != exitOK {
		t.Fatalf("导出失败：%d %s", code, stderr)
	}
	// 再把导出的 JSON 喂给 render（模拟管道）
	var out, errOut bytes.Buffer
	code = run([]string{"render", "-out", "-"}, strings.NewReader(jsonOut), &out, &errOut)
	if code != exitOK {
		t.Fatalf("render 失败：%d %s", code, errOut.String())
	}
	if !strings.HasPrefix(out.String(), "<!doctype html>") {
		t.Errorf("render 输出不是 HTML")
	}
}

func TestExportMissingSession(t *testing.T) {
	dbPath := fixtureDB(t)
	code, stdout, stderr := runArgs(t, "export", "-db", dbPath, "-session", "ses_nope")
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

func TestExportRequiresSession(t *testing.T) {
	dbPath := fixtureDB(t)
	code, _, stderr := runArgs(t, "export", "-db", dbPath)
	if code != exitBadInput {
		t.Errorf("退出码 = %d", code)
	}
	if !strings.Contains(stderr, "-session") {
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
	for _, needle := range []string{"workspaces", "sessions", "export", "render"} {
		if !strings.Contains(stdout, needle) {
			t.Errorf("帮助里缺少 %q", needle)
		}
	}
}

func TestBadDatabasePath(t *testing.T) {
	code, _, stderr := runArgs(t, "sessions", "-db", filepath.Join(t.TempDir(), "nope.db"))
	if code != exitBadInput {
		t.Errorf("退出码 = %d", code)
	}
	if !strings.Contains(stderr, "库不存在") {
		t.Errorf("stderr = %s", stderr)
	}
}
