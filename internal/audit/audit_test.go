package audit

import (
	"testing"

	"github.com/gzzyj/sesscope/internal/session"
	"github.com/gzzyj/sesscope/internal/source"
)

func int64p(value int64) *int64 { return &value }
func strp(value string) *string { return &value }

func capsAll() source.Capabilities {
	return source.Capabilities{Tokens: true, ToolStatus: true, ToolArgs: true, ToolOutput: true}
}

func validSession() *session.Session {
	created, updated, completed := int64(1000), int64(2000), int64(1900)
	return &session.Session{
		Ref: session.Ref{Source: session.DSH, Store: "/tmp/store", ID: "s1"},
		Meta: session.Meta{
			ID:           strp("s1"),
			Title:        strp("一个会话"),
			Directory:    strp("/work"),
			Agent:        strp("dsh"),
			CreatedMs:    &created,
			UpdatedMs:    &updated,
			Provenance:   session.Provenance{Source: session.DSH, Format: "dsh/jsonl"},
			WorkspaceKey: "/work",
		},
		Messages: []session.Message{
			{
				ID: "m1", Role: session.RoleUser, CreatedMs: &created,
				Parts: []session.Part{{ID: "p1", Kind: session.PartText, Type: "text", CreatedMs: &created, Text: strp("hi")}},
			},
			{
				ID: "m2", Role: session.RoleAssistant, CreatedMs: &created, CompletedMs: &completed,
				Tokens: &session.Tokens{Input: int64p(10), Output: int64p(2)},
				Parts: []session.Part{{
					ID: "p2", Kind: session.PartToolCall, Type: "tool-call", CreatedMs: &created,
					Tool: &session.ToolCall{
						CallID: strp("c1"), Name: strp("bash"), Kind: session.ToolShell,
						Status: strp("completed"), Args: []byte(`{"command":"ls"}`), Output: []byte(`"ok"`),
						Command: "ls", StartMs: &created, EndMs: &completed,
					},
				}},
			},
		},
	}
}

func violations(issues []Issue) map[string]int {
	out := map[string]int{}
	for _, issue := range issues {
		if issue.Level == LevelViolation {
			out[issue.Path]++
		}
	}
	return out
}

func TestSessionPassesOnCompleteModel(t *testing.T) {
	if issues := Session(validSession(), capsAll()); len(issues) != 0 {
		t.Fatalf("完整会话不该有空缺，实际：%+v", issues)
	}
}

func TestSessionFindsMissingIdentity(t *testing.T) {
	value := validSession()
	value.Meta.ID = nil
	got := violations(Session(value, capsAll()))
	if got["meta.id"] != 1 {
		t.Errorf("meta.id = %d，期望 1：%+v", got["meta.id"], got)
	}
}

func TestSessionExplainsEmptySession(t *testing.T) {
	value := validSession()
	value.Messages = nil
	value.Meta.Title = nil
	value.Meta.UpdatedMs = nil
	if got := violations(Session(value, capsAll())); len(got) == 0 {
		t.Fatal("没有诊断的空会话应当报违例")
	}
	value.Diagnostics = append(value.Diagnostics, session.Diagnostic{Level: "info", Code: "empty-session"})
	issues := Session(value, capsAll())
	if got := violations(issues); len(got) != 0 {
		t.Fatalf("有 empty-session 诊断时不该违例：%+v", got)
	}
}

func TestSessionAllowsOpenLastStep(t *testing.T) {
	value := validSession()
	value.Messages[1].CompletedMs = nil
	issues := Session(value, capsAll())
	if got := violations(issues); len(got) != 0 {
		t.Fatalf("最后一条 assistant 可能还在写，不该违例：%+v", got)
	}
	found := false
	for _, issue := range issues {
		if issue.Path == "message.completed_ms" && issue.Level == LevelNote {
			found = true
		}
	}
	if !found {
		t.Error("最后一条 assistant 缺结束时间应记一条 note")
	}
}

func TestSessionRequiresCompletionBeforeLaterMessage(t *testing.T) {
	value := validSession()
	value.Messages[1].CompletedMs = nil
	extra := value.Messages[1]
	extra.ID = "m3"
	value.Messages = append(value.Messages, extra)
	if got := violations(Session(value, capsAll())); got["message.completed_ms"] != 1 {
		t.Errorf("非最后一条 assistant 缺结束时间应违例：%+v", got)
	}
}

func TestSessionAllowsSyntheticEmptyText(t *testing.T) {
	value := validSession()
	synthetic := true
	value.Messages[0].Parts = append(value.Messages[0].Parts, session.Part{
		ID: "p9", Kind: session.PartText, Type: "text", CreatedMs: int64p(1000), Text: strp(""), Synthetic: &synthetic,
	})
	if got := violations(Session(value, capsAll())); got["part.text"] != 0 {
		t.Errorf("合成占位的空文本不该违例：%+v", got)
	}
}

func TestSessionAllowsFailedCallWithoutParts(t *testing.T) {
	value := validSession()
	value.Messages[1].Parts = nil
	value.Messages[1].Error = &session.Error{Name: strp("APIError")}
	if got := violations(Session(value, capsAll())); got["message.parts"] != 0 {
		t.Errorf("报错的模型调用可以没有内容：%+v", got)
	}
}

func TestSessionAllowsErroredToolWithoutTarget(t *testing.T) {
	value := validSession()
	tool := value.Messages[1].Parts[0].Tool
	tool.Kind = session.ToolWrite
	tool.Status = strp("error")
	tool.Target = ""
	tool.Command = ""
	if got := violations(Session(value, capsAll())); got["tool.target"] != 0 {
		t.Errorf("报错的工具可以没有参数目标：%+v", got)
	}
}

func TestSessionRequiresShellCommand(t *testing.T) {
	value := validSession()
	value.Messages[1].Parts[0].Tool.Command = ""
	if got := violations(Session(value, capsAll())); got["tool.command"] != 1 {
		t.Errorf("shell 工具缺命令行应违例：%+v", got)
	}
}

func TestAggregatorCountsAndSorts(t *testing.T) {
	aggregator := NewAggregator()
	broken := validSession()
	broken.Meta.ID = nil
	aggregator.Add(validSession(), "s1", capsAll())
	aggregator.Add(broken, "s2", capsAll())
	if aggregator.Violations() != 1 {
		t.Fatalf("violations = %d，期望 1", aggregator.Violations())
	}
	issues := aggregator.Issues()
	found := false
	for _, issue := range issues {
		if issue.Level != LevelViolation {
			continue
		}
		if issue.Field != "meta.id" || issue.Count != 1 {
			t.Fatalf("违例 = %+v", issue)
		}
		if len(issue.Samples) == 0 || issue.Samples[0] == "" {
			t.Errorf("缺少样本：%+v", issue)
		}
		found = true
	}
	if !found {
		t.Fatalf("没有找到 meta.id 违例：%+v", issues)
	}
}
