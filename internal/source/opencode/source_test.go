package opencode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gzzyj/sesscope/internal/session"
	"github.com/gzzyj/sesscope/internal/source"
	"github.com/gzzyj/sesscope/internal/testfixture"
	"github.com/gzzyj/sesscope/internal/trace"
)

func fixtureStore(t *testing.T) source.Store {
	t.Helper()
	path, err := testfixture.Build(t.TempDir())
	if err != nil {
		t.Fatalf("建 fixture 库：%v", err)
	}
	stores, err := New().Locate(context.Background(), source.Locator{Store: path})
	if err != nil {
		t.Fatalf("定位：%v", err)
	}
	if len(stores) != 1 {
		t.Fatalf("显式 --store 只应返回一个存储，实际 %d", len(stores))
	}
	return stores[0]
}

func TestLocateExplicitStore(t *testing.T) {
	store := fixtureStore(t)
	if !store.Exists || !store.Readable {
		t.Fatalf("fixture 库应当可读：%+v", store)
	}
	if store.Version != "v1" {
		t.Fatalf("版本 = %q，期望 v1", store.Version)
	}
	if store.Rule != "l0:--store" {
		t.Fatalf("rule = %q，期望 l0:--store", store.Rule)
	}
	if !strings.Contains(store.Evidence, "session") {
		t.Fatalf("evidence 应当带上表名：%q", store.Evidence)
	}
}

func TestLocateReportsMissingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.db")
	stores, err := New().Locate(context.Background(), source.Locator{Store: path})
	if err != nil {
		t.Fatalf("定位不该报错：%v", err)
	}
	if len(stores) != 1 || stores[0].Exists {
		t.Fatalf("不存在的库应当如实标成 Exists=false：%+v", stores)
	}
	if !strings.Contains(stores[0].Detail, "不存在") {
		t.Fatalf("detail 应当说明不存在：%q", stores[0].Detail)
	}
}

func TestLocateRejectsNonOpencodeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "random.bin")
	if err := os.WriteFile(path, []byte("not a sqlite database at all"), 0o644); err != nil {
		t.Fatalf("写文件：%v", err)
	}
	stores, err := New().Locate(context.Background(), source.Locator{Store: path})
	if err != nil {
		t.Fatalf("定位不该报错：%v", err)
	}
	if stores[0].Readable {
		t.Fatal("非 sqlite 文件不该被标成可读")
	}
	if stores[0].Detail == "" {
		t.Fatal("不可读必须给出原因")
	}
}

func TestSessionsListsAllIncludingChildren(t *testing.T) {
	store := fixtureStore(t)
	summaries, err := New().Sessions(context.Background(), store, source.Query{})
	if err != nil {
		t.Fatalf("列会话：%v", err)
	}
	if len(summaries) != 4 {
		t.Fatalf("fixture 有 4 个会话，实际 %d", len(summaries))
	}
	var child *session.Summary
	for index := range summaries {
		if summaries[index].ID == testfixture.ChildSessionID {
			child = &summaries[index]
		}
	}
	if child == nil {
		t.Fatal("子会话没被列出来")
	}
	if !child.IsChild || child.ParentID != testfixture.RootSessionID {
		t.Fatalf("子会话标记不对：%+v", child)
	}
	if child.TitleSource != "native" {
		t.Fatalf("标题来源 = %q，期望 native", child.TitleSource)
	}
}

func TestApplyQuerySemantics(t *testing.T) {
	store := fixtureStore(t)
	summaries, err := New().Sessions(context.Background(), store, source.Query{})
	if err != nil {
		t.Fatalf("列会话：%v", err)
	}
	// 默认不带子会话
	visible := source.ApplyQuery(summaries, source.Query{})
	for _, item := range visible {
		if item.IsChild {
			t.Fatalf("默认不该列出子会话：%s", item.ID)
		}
	}
	if len(visible) != 3 {
		t.Fatalf("默认可见 3 个根会话，实际 %d", len(visible))
	}
	// 前缀
	prefixed := source.ApplyQuery(summaries, source.Query{Prefix: "ses_root", IncludeChildren: true})
	if len(prefixed) != 1 || prefixed[0].ID != testfixture.RootSessionID {
		t.Fatalf("前缀过滤结果不对：%+v", prefixed)
	}
	// 排序：updated 倒序
	sorted := source.ApplyQuery(summaries, source.Query{IncludeChildren: true, Sort: "updated"})
	if sorted[0].ID != testfixture.DemoSessionID {
		t.Fatalf("updated 倒序第一条应当是 demo（7000），实际 %s", sorted[0].ID)
	}
}

func TestUnsupportedStoreSurfacesReason(t *testing.T) {
	store := source.Store{Source: session.Opencode, Path: "/x/y.db", Format: "opencode/sqlite", Version: "v2", Exists: true, Readable: false, Detail: "新版 schema"}
	_, err := New().Sessions(context.Background(), store, source.Query{})
	if err == nil {
		t.Fatal("不可读的存储必须报错，不能返回空列表")
	}
	if !strings.Contains(err.Error(), "新版 schema") {
		t.Fatalf("错误里必须带原因，实际：%v", err)
	}
}

// TestOpenMatchesLegacyExport 是两条取数路径的等价性守卫：
// 直连库（Adapter.Open）与旧导出（ExportDocument → ExportToSession）在同一会话上
// 必须产出逐字节相同的页面数据。
func TestOpenMatchesLegacyExport(t *testing.T) {
	store := fixtureStore(t)
	ctx := context.Background()

	opened, err := New().Open(ctx, store, session.Ref{Source: session.Opencode, ID: testfixture.RootSessionID}, source.OpenOptions{})
	if err != nil {
		t.Fatalf("直连库取数：%v", err)
	}

	database, err := Open(store.Path)
	if err != nil {
		t.Fatalf("打开库：%v", err)
	}
	defer database.Close()
	document, err := database.ExportDocument(testfixture.RootSessionID, time.UnixMilli(0).UTC())
	if err != nil {
		t.Fatalf("旧导出：%v", err)
	}
	encoded, err := EncodeDocument(document)
	if err != nil {
		t.Fatalf("编码旧导出：%v", err)
	}
	var legacy trace.Export
	if err := trace.DecodeExport(encoded, &legacy); err != nil {
		t.Fatalf("解析旧导出：%v", err)
	}
	// 旧导出带"导出动作"元信息，直连库不带；页面数据里只体现为 meta.exportedAt/source，这里对齐掉。
	legacy.ExportInfo = nil

	options := trace.Options{
		ExportPath:  "fixture",
		GeneratedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	wantData, err := trace.NormalizeExport(&legacy, options)
	if err != nil {
		t.Fatalf("归一化旧导出：%v", err)
	}
	gotData, err := trace.Normalize(opened, options)
	if err != nil {
		t.Fatalf("归一化直连库：%v", err)
	}
	want, err := trace.EncodeDataJSON(wantData)
	if err != nil {
		t.Fatalf("编码旧导出数据：%v", err)
	}
	got, err := trace.EncodeDataJSON(gotData)
	if err != nil {
		t.Fatalf("编码直连库数据：%v", err)
	}
	if string(want) != string(got) {
		wantText, gotText := string(want), string(got)
		limit := len(wantText)
		if len(gotText) < limit {
			limit = len(gotText)
		}
		offset := 0
		for offset < limit && wantText[offset] == gotText[offset] {
			offset++
		}
		from := offset - 80
		if from < 0 {
			from = 0
		}
		to := offset + 120
		if to > limit {
			to = limit
		}
		t.Fatalf("两条取数路径产出的页面数据不一致（首个差异 @%d）：\n期望 ...%s...\n实际 ...%s...", offset, wantText[from:to], gotText[from:to])
	}
}

func TestOpenUnknownSessionMapsToNotFound(t *testing.T) {
	store := fixtureStore(t)
	_, err := New().Open(context.Background(), store, session.Ref{Source: session.Opencode, ID: "ses_missing"}, source.OpenOptions{})
	if err == nil {
		t.Fatal("不存在的会话必须报错")
	}
	if !strings.Contains(err.Error(), source.ErrNotFound.Error()) {
		t.Fatalf("错误应当包装 ErrNotFound，实际：%v", err)
	}
}
