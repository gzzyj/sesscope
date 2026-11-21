package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/gzzyj/atrace/internal/session"
	"github.com/gzzyj/atrace/internal/source"
)

func writeRollout(t *testing.T, records []map[string]any) source.Store {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "2026", "09", "22")
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
	path := filepath.Join(dir, "rollout-2026-09-22T10-00-00-01a0c89f-3c06-73c3-aae8-e6de46100531.jsonl.zst")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("建文件：%v", err)
	}
	writer, err := zstd.NewWriter(file)
	if err != nil {
		t.Fatalf("zstd：%v", err)
	}
	writer.Write(buffer.Bytes())
	writer.Close()
	file.Close()

	stores, err := New().Locate(context.Background(), source.Locator{Store: root})
	if err != nil {
		t.Fatalf("定位：%v", err)
	}
	return stores[0]
}

func fixture() []map[string]any {
	stamp := "2026-09-22T10:00:00.000Z"
	return []map[string]any{
		{"timestamp": stamp, "type": "session_meta", "payload": map[string]any{
			"session_id": "01a0c89f-3c06-73c3-aae8-e6de46100531", "cwd": "/work/demo",
			"cli_version": "0.163.0", "model_provider": "OpenAI", "originator": "codex-tui", "timestamp": stamp,
		}},
		{"timestamp": stamp, "type": "turn_context", "payload": map[string]any{"turn_id": "turn-1", "cwd": "/work/demo"}},
		{"timestamp": stamp, "type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "turn-1"}},
		{"timestamp": stamp, "type": "event_msg", "payload": map[string]any{
			"type": "item_completed", "turn_id": "turn-1", "started_at_ms": 1000, "completed_at_ms": 1001,
			"item": map[string]any{"type": "UserMessage", "id": "u1", "content": []any{map[string]any{"type": "text", "text": "跑一下"}}},
		}},
		{"timestamp": stamp, "type": "response_item", "payload": map[string]any{
			"type": "message", "id": "a1", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": "好的"}},
		}},
		{"timestamp": stamp, "type": "response_item", "payload": map[string]any{"type": "reasoning", "id": "r1", "summary": []any{}, "encrypted_content": "gAAAA"}},
		{"timestamp": stamp, "type": "response_item", "payload": map[string]any{
			"type": "function_call", "id": "fc1", "name": "exec_command", "arguments": `{"cmd":"ls"}`, "call_id": "call_1",
		}},
		{"timestamp": stamp, "type": "response_item", "payload": map[string]any{"type": "function_call_output", "id": "fco1", "call_id": "call_1", "output": "a.txt"}},
		{"timestamp": stamp, "type": "token_usage_record", "payload": map[string]any{
			"turn_id": "turn-1", "usage": map[string]any{"input_tokens": 10, "output_tokens": 2, "cached_input_tokens": 4},
		}},
		{"timestamp": stamp, "type": "event_msg", "payload": map[string]any{"type": "task_complete", "turn_id": "turn-1", "completed_at_ms": 2000}},
	}
}

func TestRolloutMapping(t *testing.T) {
	store := writeRollout(t, fixture())
	if !store.Readable {
		t.Fatalf("store = %+v", store)
	}
	summaries, err := New().Sessions(context.Background(), store, source.Query{})
	if err != nil {
		t.Fatalf("列会话：%v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("会话数 = %d", len(summaries))
	}
	if summaries[0].Title != "跑一下" || summaries[0].TitleSource != "derived" {
		t.Fatalf("标题派生不对：%+v", summaries[0])
	}
	opened, err := New().Open(context.Background(), store, summaries[0].Ref, source.OpenOptions{})
	if err != nil {
		t.Fatalf("取会话：%v", err)
	}
	if len(opened.Messages) != 2 {
		t.Fatalf("消息数 = %d，期望 2（user+assistant）", len(opened.Messages))
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
	codes := map[string]bool{}
	for _, diagnostic := range opened.Diagnostics {
		codes[diagnostic.Code] = true
	}
	for _, want := range []string{"encrypted-reasoning", "derived-tool-status"} {
		if !codes[want] {
			t.Errorf("缺少诊断 %q：%+v", want, opened.Diagnostics)
		}
	}
}
