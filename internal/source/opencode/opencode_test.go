package opencode

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gzzyj/sesscope/internal/testfixture"
)

func openFixture(t *testing.T) *DB {
	t.Helper()
	path, err := testfixture.Build(t.TempDir())
	if err != nil {
		t.Fatalf("建 fixture 库：%v", err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatalf("打开 fixture 库：%v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenRejectsNonOpencodeDB(t *testing.T) {
	dir := t.TempDir()
	empty := dir + "/empty.db"
	if err := writeFile(empty, ""); err != nil {
		t.Fatalf("建空文件：%v", err)
	}
	if _, err := Open(empty); err == nil {
		t.Fatal("空库应当报错，而不是当成 opencode 库")
	}
}

func writeFile(path, content string) error {
	return osWriteFile(path, []byte(content), 0o644)
}

func TestListProjects(t *testing.T) {
	db := openFixture(t)
	projects, err := db.ListProjects()
	if err != nil {
		t.Fatalf("列工作区：%v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("工作区数 = %d，期望 2", len(projects))
	}
	// 按最后活跃时间倒序：demo(7000) 早于 global(6000)? demo 7000 > global 6000
	if projects[0].ID != testfixture.DemoProjectID {
		t.Errorf("第一个工作区 = %s，期望 demo（最近活跃）", projects[0].ID)
	}
	byID := map[string]Project{}
	for _, project := range projects {
		byID[project.ID] = project
	}
	global := byID[testfixture.GlobalProjectID]
	if global.SessionCount != 3 || global.RootCount != 2 {
		t.Errorf("global 会话 %d / 顶层 %d，期望 3 / 2", global.SessionCount, global.RootCount)
	}
	if global.Label() != "global" {
		t.Errorf("worktree 为 / 时名称 = %q", global.Label())
	}
	demo := byID[testfixture.DemoProjectID]
	if demo.Label() != "demo" {
		t.Errorf("demo 的名称 = %q，期望取 worktree 目录名", demo.Label())
	}
	if demo.VCS == nil || *demo.VCS != "git" {
		t.Errorf("demo 的 vcs = %v", demo.VCS)
	}
}

func TestListSessionsFilters(t *testing.T) {
	db := openFixture(t)

	roots, err := db.ListSessions(SessionFilter{})
	if err != nil {
		t.Fatalf("列会话：%v", err)
	}
	if len(roots) != 3 {
		t.Errorf("默认（只列顶层）会话数 = %d，期望 3", len(roots))
	}
	all, err := db.ListSessions(SessionFilter{IncludeChildren: true})
	if err != nil {
		t.Fatalf("列会话：%v", err)
	}
	if len(all) != 4 {
		t.Errorf("含子会话时 = %d，期望 4", len(all))
	}
	if all[0].ID != testfixture.DemoSessionID {
		t.Errorf("按更新时间倒序第一个 = %s，期望 demo 会话", all[0].ID)
	}

	byName, err := db.ListSessions(SessionFilter{Workspace: "demo"})
	if err != nil {
		t.Fatalf("按工作区过滤：%v", err)
	}
	if len(byName) != 1 || byName[0].ID != testfixture.DemoSessionID {
		t.Errorf("按 demo 过滤 = %v", ids(byName))
	}
	byPrefix, err := db.ListSessions(SessionFilter{Workspace: testfixture.GlobalProjectID[:4]})
	if err != nil {
		t.Fatalf("按 id 前缀过滤：%v", err)
	}
	if len(byPrefix) != 2 {
		t.Errorf("按前缀过滤 = %v", ids(byPrefix))
	}
	if _, err := db.ListSessions(SessionFilter{Workspace: "不存在的项目"}); err == nil {
		t.Error("过滤到不存在的工作区时应当报错")
	}

	byGrep, err := db.ListSessions(SessionFilter{Grep: "全零"})
	if err != nil {
		t.Fatalf("搜索：%v", err)
	}
	if len(byGrep) != 1 || byGrep[0].ID != testfixture.SecondSessionID {
		t.Errorf("按标题搜索 = %v", ids(byGrep))
	}

	sorted, err := db.ListSessions(SessionFilter{Sort: "created"})
	if err != nil {
		t.Fatalf("按创建时间排序：%v", err)
	}
	// fixture 的创建时间：demo 4000 > root 1000 > second 500
	if sorted[0].ID != testfixture.DemoSessionID {
		t.Errorf("按创建时间倒序第一个 = %s，期望 demo 会话", sorted[0].ID)
	}
	if sorted[len(sorted)-1].ID != testfixture.SecondSessionID {
		t.Errorf("按创建时间倒序最后一个 = %s，期望 second 会话", sorted[len(sorted)-1].ID)
	}

	limited, err := db.ListSessions(SessionFilter{IncludeChildren: true, Limit: 2})
	if err != nil {
		t.Fatalf("限条数：%v", err)
	}
	if len(limited) != 2 {
		t.Errorf("limit=2 返回 %d 条", len(limited))
	}

	if _, err := db.ListSessions(SessionFilter{Sort: "乱写"}); err == nil {
		t.Error("未知排序应当报错")
	}
}

func TestSessionPrefixResolution(t *testing.T) {
	db := openFixture(t)
	got, err := db.Session("ses_root")
	if err != nil {
		t.Fatalf("前缀解析：%v", err)
	}
	if got.ID != testfixture.RootSessionID {
		t.Errorf("前缀解析到 %s", got.ID)
	}
	if _, err := db.Session("ses_"); !errors.Is(err, ErrSessionNotFound) && !strings.Contains(err.Error(), "不唯一") {
		t.Errorf("歧义前缀应报错，实际：%v", err)
	}
	if _, err := db.Session("ses_nope"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("不存在的会话应返回 ErrSessionNotFound，实际：%v", err)
	}
	if _, err := db.Session(""); err == nil {
		t.Error("空 id 应报错")
	}
}

func TestLoadOrdering(t *testing.T) {
	db := openFixture(t)
	detail, err := db.Load(testfixture.RootSessionID)
	if err != nil {
		t.Fatalf("加载会话：%v", err)
	}
	if len(detail.Messages) != 2 || detail.Messages[0].ID != "msg_u1" {
		t.Errorf("消息顺序 = %v", messageIDs(detail.Messages))
	}
	if len(detail.Parts) != 5 || detail.Parts[0].ID != "prt_s1" || detail.Parts[4].ID != "prt_f1" {
		t.Errorf("part 顺序 = %v", partIDs(detail.Parts))
	}
}

func TestExportDocument(t *testing.T) {
	db := openFixture(t)
	doc, err := db.ExportDocument(testfixture.RootSessionID, time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("导出：%v", err)
	}
	if doc.Session.ID != testfixture.RootSessionID || doc.Session.Title != "根会话：导出一条流水线" {
		t.Errorf("会话信息 = %+v", doc.Session)
	}
	if doc.Session.Model == nil {
		t.Error("model 应当解析成 JSON 值")
	}
	if doc.GlobalStats.MessageCount != 2 {
		t.Errorf("消息数 = %d", doc.GlobalStats.MessageCount)
	}
	if doc.GlobalStats.RoleCounts["user"] != 1 || doc.GlobalStats.RoleCounts["assistant"] != 1 {
		t.Errorf("角色统计 = %v", doc.GlobalStats.RoleCounts)
	}
	if doc.GlobalStats.PartTypeCounts["tool"] != 1 || doc.GlobalStats.PartTypeCounts["reasoning"] != 1 {
		t.Errorf("part 统计 = %v", doc.GlobalStats.PartTypeCounts)
	}
	// reasoning 1200→2000 = 800；tool 2600→5000 = 2400
	if doc.GlobalStats.TotalReasoningDurationMs != 800 {
		t.Errorf("reasoning 合计 = %d，期望 800", doc.GlobalStats.TotalReasoningDurationMs)
	}
	if doc.GlobalStats.TotalToolDurationMs != 2400 {
		t.Errorf("tool 合计 = %d，期望 2400", doc.GlobalStats.TotalToolDurationMs)
	}
	if doc.GlobalStats.FirstActivityMs == nil || *doc.GlobalStats.FirstActivityMs != 1000 {
		t.Errorf("first_activity = %v", doc.GlobalStats.FirstActivityMs)
	}
	if doc.GlobalStats.LastActivityMs == nil || *doc.GlobalStats.LastActivityMs != 6000 {
		t.Errorf("last_activity = %v", doc.GlobalStats.LastActivityMs)
	}

	if len(doc.TurnTimings) != 1 {
		t.Fatalf("轮次画像 = %d 条，期望 1", len(doc.TurnTimings))
	}
	timing := doc.TurnTimings[0]
	if timing.Steps != 1 || timing.ReasoningParts != 1 || timing.TextParts != 1 || timing.ToolCalls != 1 {
		t.Errorf("轮次计数 = %+v", timing)
	}
	if timing.TurnDurationMs == nil || *timing.TurnDurationMs != 4900 {
		t.Errorf("轮次时长 = %v，期望 4900", timing.TurnDurationMs)
	}

	assistant := doc.Messages[1]
	if assistant.Role != "assistant" || assistant.ModelID == nil || *assistant.ModelID != "GLM-5.3" {
		t.Errorf("assistant 消息 = %+v", assistant)
	}
	if len(assistant.Parts) != 5 {
		t.Errorf("assistant part 数 = %d", len(assistant.Parts))
	}
	// 键序必须保持库里的顺序（parentID 在前），render 依赖这一点
	raw, err := json.Marshal(assistant.Data)
	if err != nil {
		t.Fatalf("序列化 data：%v", err)
	}
	if !strings.HasPrefix(string(raw), "{\"parentID\":") {
		t.Errorf("data 键序被改动：%s", string(raw)[:40])
	}
	if assistant.Parts[1].DurationMs != 800 {
		t.Errorf("reasoning part duration = %d，期望 800", assistant.Parts[1].DurationMs)
	}
}

func TestExportRejectsMissingSession(t *testing.T) {
	db := openFixture(t)
	if _, err := db.ExportDocument("ses_missing", time.Now()); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("不存在的会话应返回 ErrSessionNotFound，实际：%v", err)
	}
}

func TestDiscover(t *testing.T) {
	if _, err := Discover("definitely/not/here.db"); err == nil {
		t.Error("不存在的 -db 应报错")
	}
	dir := t.TempDir()
	path, err := testfixture.Build(dir)
	if err != nil {
		t.Fatalf("建 fixture：%v", err)
	}
	got, err := Discover(path)
	if err != nil {
		t.Fatalf("Discover：%v", err)
	}
	if got != path {
		t.Errorf("Discover = %s，期望 %s", got, path)
	}
}

func ids(sessions []SessionRow) []string {
	out := make([]string, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, session.ID)
	}
	return out
}

func messageIDs(messages []MessageRow) []string {
	out := make([]string, 0, len(messages))
	for _, message := range messages {
		out = append(out, message.ID)
	}
	return out
}

func partIDs(parts []PartRow) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, part.ID)
	}
	return out
}
