package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gzzyj/sesscope/internal/session"
	"github.com/gzzyj/sesscope/internal/source"
)

func writeSession(t *testing.T, records []map[string]any) source.Store {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "-work-demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录：%v", err)
	}
	var buffer bytes.Buffer
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("序列化：%v", err)
		}
		buffer.Write(line)
		buffer.WriteByte(10)
	}
	if err := os.WriteFile(filepath.Join(dir, "sess-1.jsonl"), buffer.Bytes(), 0o644); err != nil {
		t.Fatalf("写文件：%v", err)
	}
	stores, err := New().Locate(context.Background(), source.Locator{Store: root})
	if err != nil {
		t.Fatalf("定位：%v", err)
	}
	return stores[0]
}

func fixture() []map[string]any {
	stamp := "2026-10-28T13:58:53.957Z"
	return []map[string]any{
		{"type": "user", "uuid": "u1", "timestamp": stamp, "cwd": "/work/demo", "sessionId": "sess-1", "version": "2.1.312",
			"origin": map[string]any{"kind": "human"}, "promptSource": "typed", "turnPosition": map[string]any{"turnIndex": 1},
			"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "跑一下"}}}},
		{"type": "assistant", "uuid": "a1", "timestamp": stamp, "sessionId": "sess-1",
			"message": map[string]any{
				"role": "assistant", "model": "claude-x", "stop_reason": "tool_use",
				"content": []any{
					map[string]any{"type": "text", "text": "好的"},
					map[string]any{"type": "tool_use", "id": "call_1", "name": "Bash", "input": map[string]any{"command": "ls"}},
				},
				"usage": map[string]any{"input_tokens": 10, "output_tokens": 2},
			}},
		{"type": "user", "uuid": "u2", "timestamp": stamp, "sessionId": "sess-1",
			"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call_1", "content": "a.txt", "is_error": false}}}},
		{"type": "system", "subtype": "turn_duration", "durationMs": 500, "timestamp": stamp},
	}
}

func TestClaudeMapping(t *testing.T) {
	store := writeSession(t, fixture())
	if !store.Readable {
		t.Fatalf("store = %+v", store)
	}
	summaries, err := New().Sessions(context.Background(), store, source.Query{})
	if err != nil {
		t.Fatalf("列会话：%v", err)
	}
	if len(summaries) != 1 || summaries[0].ID != "sess-1" {
		t.Fatalf("summaries = %+v", summaries)
	}
	if summaries[0].Title != "跑一下" || summaries[0].Directory != "/work/demo" {
		t.Fatalf("摘要不对：%+v", summaries[0])
	}
	opened, err := New().Open(context.Background(), store, summaries[0].Ref, source.OpenOptions{})
	if err != nil {
		t.Fatalf("取会话：%v", err)
	}
	if len(opened.Messages) != 2 {
		t.Fatalf("消息数 = %d，期望 2（tool_result 记录不应变成轮次：%+v）", len(opened.Messages), opened.Messages)
	}
	assistant := opened.Messages[1]
	if assistant.Role != session.RoleAssistant {
		t.Fatalf("第二条应是 assistant：%v", assistant.Role)
	}
	if assistant.Tokens == nil || assistant.Tokens.Input == nil || *assistant.Tokens.Input != 10 {
		t.Fatalf("tokens = %+v", assistant.Tokens)
	}
	var tools int
	for _, part := range assistant.Parts {
		if part.Kind != session.PartToolCall {
			continue
		}
		tools++
		if part.Tool.Kind != session.ToolShell || part.Tool.Command != "ls" {
			t.Fatalf("工具语义 = %s / %q", part.Tool.Kind, part.Tool.Command)
		}
		if part.Tool.Status == nil || *part.Tool.Status != "completed" {
			t.Fatalf("状态 = %v", part.Tool.Status)
		}
	}
	if tools != 1 {
		t.Fatalf("工具数 = %d", tools)
	}
}

// TestClaudeLocalCommandsAreNotTurns：/exit、<local-command-*>、isMeta 记录不是人类输入，
// 不能各开一轮（否则 4 秒的会话会出现 3 个空轮次）。
func TestClaudeLocalCommandsAreNotTurns(t *testing.T) {
	stamp := "2026-10-28T13:58:53.957Z"
	records := []map[string]any{
		{"type": "user", "uuid": "u1", "timestamp": stamp, "origin": map[string]any{"kind": "human"}, "promptSource": "typed",
			"message": map[string]any{"role": "user", "content": "hi"}},
		{"type": "assistant", "uuid": "a1", "timestamp": stamp, "message": map[string]any{
			"role": "assistant", "model": "claude-x", "content": []any{map[string]any{"type": "text", "text": "Hi!"}},
		}},
		{"type": "system", "subtype": "turn_duration", "durationMs": 2000, "timestamp": stamp},
		{"type": "user", "uuid": "u2", "timestamp": stamp, "isMeta": true,
			"message": map[string]any{"role": "user", "content": "<local-command-caveat>cmd</local-command-caveat>"}},
		{"type": "user", "uuid": "u3", "timestamp": stamp,
			"message": map[string]any{"role": "user", "content": "<command-name>/exit</command-name>"}},
		{"type": "user", "uuid": "u4", "timestamp": stamp,
			"message": map[string]any{"role": "user", "content": "<local-command-stdout>Bye!</local-command-stdout>"}},
	}
	store := writeSession(t, records)
	summaries, err := New().Sessions(context.Background(), store, source.Query{})
	if err != nil {
		t.Fatalf("列会话：%v", err)
	}
	opened, err := New().Open(context.Background(), store, summaries[0].Ref, source.OpenOptions{})
	if err != nil {
		t.Fatalf("取会话：%v", err)
	}
	users, systems := 0, 0
	var assistant *session.Message
	for index := range opened.Messages {
		switch opened.Messages[index].Role {
		case session.RoleUser:
			users++
		case session.RoleSystem:
			systems++
		case session.RoleAssistant:
			assistant = &opened.Messages[index]
		}
	}
	if users != 1 {
		t.Errorf("user 消息 = %d，期望 1（本地命令不该算人类输入）", users)
	}
	if systems != 3 {
		t.Errorf("system 消息 = %d，期望 3（保留本地命令记录）", systems)
	}
	if assistant == nil || assistant.CompletedMs == nil {
		t.Fatalf("assistant 消息或结束时间缺失：%+v", assistant)
	}
	if assistant.CreatedMs == nil || *assistant.CompletedMs != *assistant.CreatedMs+2000 {
		t.Errorf("结束时间 = %v，期望创建时间 + 2000", assistant.CompletedMs)
	}
	if len(assistant.Parts) == 0 || assistant.Parts[0].EndMs == nil || *assistant.Parts[0].EndMs != *assistant.CompletedMs {
		t.Errorf("文本部件的结束时间没跟着 turn_duration 校准：%+v", assistant.Parts)
	}
	found := false
	for _, diagnostic := range opened.Diagnostics {
		if diagnostic.Code == "local-command" {
			found = true
		}
	}
	if !found {
		t.Errorf("缺少 local-command 诊断：%+v", opened.Diagnostics)
	}
}
