package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gzzyj/atrace/internal/session"
	"github.com/gzzyj/atrace/internal/source"
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
