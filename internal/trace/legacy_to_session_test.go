package trace

import (
	"os"
	"testing"

	"github.com/gzzyj/atrace/internal/session"
)

func loadGoldenExport(t *testing.T) *Export {
	t.Helper()
	raw, err := os.ReadFile(goldenExportPath)
	if err != nil {
		t.Skipf("没有黄金样例（先跑 make golden）：%v", err)
	}
	var doc Export
	if err := DecodeExport(raw, &doc); err != nil {
		t.Fatalf("解析黄金样例：%v", err)
	}
	return &doc
}

// TestExportToSessionPreservesShape 守住"旧导出 → 领域模型"这一步不丢东西：
// 消息数、部件数、部件语义类别、工具调用标识、压缩标记、声明统计都必须对得上。
// 内容级等价由 TestGoldenParity 逐字节守住，这里补的是结构级断言。
func TestExportToSessionPreservesShape(t *testing.T) {
	doc := loadGoldenExport(t)
	got := ExportToSession(doc)
	if got == nil {
		t.Fatal("ExportToSession 返回 nil")
	}
	if len(got.Messages) != len(doc.Messages) {
		t.Fatalf("消息数 %d，期望 %d", len(got.Messages), len(doc.Messages))
	}
	if got.Meta.ID == nil || doc.Session == nil || doc.Session.ID == nil {
		t.Fatal("会话 id 丢失")
	}
	if *got.Meta.ID != *doc.Session.ID {
		t.Fatalf("会话 id = %q，期望 %q", *got.Meta.ID, *doc.Session.ID)
	}

	toolParts, compactionParts, textParts, reasoningParts := 0, 0, 0, 0
	for index, message := range doc.Messages {
		converted := got.Messages[index]
		if converted.ID != message.MessageID {
			t.Fatalf("第 %d 条消息 id = %q，期望 %q", index, converted.ID, message.MessageID)
		}
		if len(converted.Parts) != len(message.Parts) {
			t.Fatalf("消息 %s 部件数 %d，期望 %d", message.MessageID, len(converted.Parts), len(message.Parts))
		}
		for partIndex, part := range converted.Parts {
			switch part.Kind {
			case session.PartToolCall:
				toolParts++
				if part.Tool == nil {
					t.Fatalf("消息 %s 第 %d 个 tool 部件没有 ToolCall", message.MessageID, partIndex)
				}
			case session.PartCompaction:
				compactionParts++
			case session.PartText:
				textParts++
			case session.PartReasoning:
				reasoningParts++
			}
			if part.Type == "" {
				t.Fatalf("消息 %s 第 %d 个部件丢了原生类型原文", message.MessageID, partIndex)
			}
		}
	}
	if toolParts == 0 || textParts == 0 {
		t.Fatalf("结构异常：tool=%d text=%d，黄金样例不该缺这两类", toolParts, textParts)
	}
	if compactionParts == 0 {
		t.Log("提示：黄金样例里没有 compaction 部件")
	}
	t.Logf("部件统计：text=%d reasoning=%d tool=%d compaction=%d", textParts, reasoningParts, toolParts, compactionParts)

	if doc.GlobalStats != nil && doc.GlobalStats.TotalToolDurationMs != nil {
		if got.Declared.TotalToolDurationMs == nil || *got.Declared.TotalToolDurationMs != *doc.GlobalStats.TotalToolDurationMs {
			t.Fatal("声明统计 total_tool_duration_ms 没映射过来")
		}
	}
	if len(got.Declared.TurnTimings) != len(doc.TurnTimings) {
		t.Fatalf("声明轮次 %d 条，期望 %d 条", len(got.Declared.TurnTimings), len(doc.TurnTimings))
	}
	if got.Meta.Provenance.Source != session.Opencode {
		t.Fatalf("provenance.source = %q，期望 opencode", got.Meta.Provenance.Source)
	}
}

// TestPartKindMapping 守住原生类型 → 语义类别的映射表。
func TestPartKindMapping(t *testing.T) {
	cases := map[string]session.PartKind{
		"text":        session.PartText,
		"reasoning":   session.PartReasoning,
		"tool":        session.PartToolCall,
		"compaction":  session.PartCompaction,
		"file":        session.PartFile,
		"step-start":  session.PartStepStart,
		"step-finish": session.PartStepFinish,
		"snapshot":    session.PartOther,
	}
	for raw, want := range cases {
		if got := exportPartKind(raw); got != want {
			t.Errorf("exportPartKind(%q) = %q，期望 %q", raw, got, want)
		}
	}
}

// TestNormalizeNilSession 守住入口校验。
func TestNormalizeNilSession(t *testing.T) {
	if _, err := Normalize(nil, testOptions()); err == nil {
		t.Fatal("空会话应当报错")
	}
	if _, err := Normalize(&session.Session{}, testOptions()); err == nil {
		t.Fatal("没有 messages 的会话应当报错")
	}
}
