package trace

import (
	"bytes"
	"strings"
	"testing"
)

func minimalData(t *testing.T, toolOutput string) *Data {
	t.Helper()
	toolEnd := int64(1500)
	toolStart := int64(1000)
	tool := Tool{
		CallID:     "call_1",
		PartID:     "prt_t1",
		Name:       "bash",
		Status:     "completed",
		StartMs:    &toolStart,
		EndMs:      &toolEnd,
		DurationMs: int64Ptr(500),
		ArgsText:   "{\n  \"command\": \"ls\"\n}",
		OutputText: toolOutput,
		TurnIndex:  1,
		StepIndex:  1,
	}
	stepStart := int64(900)
	stepEnd := int64(1500)
	modelStart := int64(950)
	modelEnd := int64(980)
	return &Data{
		Meta: Meta{
			SessionID:   strPtr("ses_test"),
			Title:       strPtr("测试会话"),
			FirstMs:     int64Ptr(100),
			LastMs:      int64Ptr(1500),
			GeneratedAt: "2026-11-20T10:22:04.130Z",
			ExportPath:  "/tmp/export.json",
		},
		Stats: Stats{Turns: 1, UserTurns: 1, Steps: 1, ToolCalls: 1},
		Warnings: []Warning{
			{Level: "info", Code: "no-cost", Title: "cost 全程为 0", Detail: "说明"},
		},
		Turns: []Turn{{
			Index:     1,
			Kind:      "user",
			MessageID: "msg_u1",
			StartMs:   int64Ptr(100),
			EndMs:     int64Ptr(1500),
			Input:     TurnInput{Text: "跑一下", Attachments: []string{}},
			Steps: []Step{{
				Index:        1,
				TurnIndex:    1,
				MessageID:    "msg_a1",
				Role:         "assistant",
				StartMs:      &stepStart,
				EndMs:        &stepEnd,
				StepMs:       int64Ptr(600),
				ModelStartMs: &modelStart,
				ModelEndMs:   &modelEnd,
				ModelMs:      int64Ptr(30),
				Status:       "completed",
				Reasoning:    []Span{},
				Texts:        []Span{},
				Tools:        []Tool{tool},
				Timeline:     []TimelineItem{},
			}},
		}},
	}
}

func strPtr(value string) *string { return &value }

func TestRenderHTMLReplacesAllPlaceholders(t *testing.T) {
	html, err := RenderHTML(minimalData(t, "ok"))
	if err != nil {
		t.Fatalf("渲染失败：%v", err)
	}
	text := string(html)
	for _, placeholder := range []string{cssPlaceholder, dataPlaceholder, jsPlaceholder} {
		if strings.Contains(text, placeholder) {
			t.Errorf("占位符 %s 没有被替换", placeholder)
		}
	}
	if !strings.HasPrefix(text, "<!doctype html>") {
		t.Error("HTML 开头不对")
	}
	if !strings.Contains(text, "测试会话") {
		t.Error("页面里没有会话标题")
	}
	// CSS 与 JS 必须真的内联进来（保证是"自包含单文件"）
	if !strings.Contains(text, "--assistant-decode") {
		t.Error("内联 CSS 缺失")
	}
	if !strings.Contains(text, "window.__trace") {
		t.Error("内联 JS 缺失")
	}
}

func TestRenderHTMLEscapesScriptTagInData(t *testing.T) {
	// 工具输出里带 </script>，若不转义会提前闭合 <script type="application/json"> 块
	hostile := "before</script><script>alert(1)</script>after"
	html, err := RenderHTML(minimalData(t, hostile))
	if err != nil {
		t.Fatalf("渲染失败：%v", err)
	}
	text := string(html)
	backslash := string(rune(92))
	if !strings.Contains(text, backslash+"u003c/script") {
		t.Error("数据里的 < 没有被转义成 " + backslash + "u003c")
	}
	if strings.Contains(text, hostile) {
		t.Error("原始 </script> 仍留在页面里")
	}
	// 页面骨架自身只有两个 script 标签：数据块 + app.js
	if got := strings.Count(text, "<script"); got != 2 {
		t.Errorf("<script 出现 %d 次，期望 2 次（数据块 + app.js）", got)
	}
	if got := strings.Count(text, "</script>"); got != 2 {
		t.Errorf("</script> 出现 %d 次，期望 2 次", got)
	}
}

func TestEncodeDataJSONIsStable(t *testing.T) {
	data := minimalData(t, "ok")
	first, err := EncodeDataJSON(data)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	second, err := EncodeDataJSON(data)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("同一份数据两次序列化结果不同")
	}
	if !bytes.HasPrefix(first, []byte("{\n  ")) {
		t.Errorf("输出不是 2 空格缩进：%q", string(first[:min(20, len(first))]))
	}
	if bytes.HasSuffix(first, []byte("\n")) {
		t.Error("输出不应带结尾换行（要和 Node 版逐字节一致）")
	}
	// 中文不转义，保持可读
	if !strings.Contains(string(first), "测试会话") {
		t.Error("中文被转义了")
	}
}

func TestSummaryMentionsCoreNumbers(t *testing.T) {
	summary := Summary(minimalData(t, "ok"), "/tmp/export.json", "/tmp/out.html", "/tmp/out.json", 123456)
	for _, needle := range []string{"轮次 1", "步骤 1", "工具调用 1", "数据提示 1 条", "/tmp/out.html", "/tmp/out.json"} {
		if !strings.Contains(summary, needle) {
			t.Errorf("摘要缺少 %q：\n%s", needle, summary)
		}
	}
}
