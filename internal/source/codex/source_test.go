package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/gzzyj/sesscope/internal/session"
	"github.com/gzzyj/sesscope/internal/source"
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

// TestRolloutModelSpans：codex 的 item_completed 带真实起止，必须落到 ModelSpans 上。
// 思考常常是加密的（没有 part），起止仍然要保留，否则页面上模型泳道是空的。
func TestRolloutModelSpans(t *testing.T) {
	base := int64(1790000000000)
	stamp := "2026-09-22T10:00:00.000Z"
	records := []map[string]any{
		{"timestamp": stamp, "type": "session_meta", "payload": map[string]any{
			"session_id": "01a0c89f-3c06-73c3-aae8-e6de46100531", "cwd": "/work/demo", "timestamp": stamp,
		}},
		{"timestamp": stamp, "type": "turn_context", "payload": map[string]any{"turn_id": "turn-1"}},
		{"timestamp": stamp, "type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "turn-1"}},
		{"timestamp": stamp, "type": "event_msg", "payload": map[string]any{
			"type": "item_completed", "turn_id": "turn-1", "started_at_ms": base, "completed_at_ms": base,
			"item": map[string]any{"type": "UserMessage", "id": "u1", "content": []any{map[string]any{"type": "text", "text": "跑一下"}}},
		}},
		{"timestamp": stamp, "type": "response_item", "payload": map[string]any{
			"type": "function_call", "id": "fc1", "name": "exec_command", "arguments": `{"cmd":"ls"}`, "call_id": "call_1",
		}},
		{"timestamp": stamp, "type": "response_item", "payload": map[string]any{
			"type": "function_call_output", "id": "fco1", "call_id": "call_1", "output": "a.txt",
		}},
		// 顺序与真实 rollout 一致：先 item_completed（带起止），再 response_item。
		{"timestamp": stamp, "type": "event_msg", "payload": map[string]any{
			"type": "item_completed", "turn_id": "turn-1", "started_at_ms": base + 100, "completed_at_ms": base + 400,
			"item": map[string]any{"type": "Reasoning", "id": "r1"},
		}},
		{"timestamp": stamp, "type": "response_item", "payload": map[string]any{
			"type": "reasoning", "id": "r1", "summary": []any{}, "encrypted_content": "gAAAA",
		}},
		{"timestamp": stamp, "type": "event_msg", "payload": map[string]any{
			"type": "item_completed", "turn_id": "turn-1", "started_at_ms": base + 500, "completed_at_ms": base + 900,
			"item": map[string]any{"type": "AgentMessage", "id": "a2", "content": []any{map[string]any{"type": "output_text", "text": "好了"}}},
		}},
		{"timestamp": stamp, "type": "response_item", "payload": map[string]any{
			"type": "message", "id": "a2", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": "好了"}},
		}},
	}
	store := writeRollout(t, records)
	summaries, err := New().Sessions(context.Background(), store, source.Query{})
	if err != nil {
		t.Fatalf("列会话：%v", err)
	}
	opened, err := New().Open(context.Background(), store, summaries[0].Ref, source.OpenOptions{})
	if err != nil {
		t.Fatalf("取会话：%v", err)
	}
	var toolStep, textStep *session.Message
	for index := range opened.Messages {
		message := &opened.Messages[index]
		if message.Role != session.RoleAssistant {
			continue
		}
		for _, part := range message.Parts {
			if part.Kind == session.PartToolCall {
				toolStep = message
			}
			if part.Kind == session.PartText {
				textStep = message
			}
		}
	}
	if toolStep == nil || textStep == nil {
		t.Fatalf("步骤划分不对：tool=%v text=%v", toolStep != nil, textStep != nil)
	}
	if len(toolStep.ModelSpans) != 1 || toolStep.ModelSpans[0] != (session.ModelSpan{StartMs: base + 100, EndMs: base + 400}) {
		t.Errorf("工具步骤的模型区间 = %+v", toolStep.ModelSpans)
	}
	if len(textStep.ModelSpans) != 1 || textStep.ModelSpans[0] != (session.ModelSpan{StartMs: base + 500, EndMs: base + 900}) {
		t.Errorf("文本步骤的模型区间 = %+v", textStep.ModelSpans)
	}
	if textStep.CreatedMs == nil || *textStep.CreatedMs != base+500 {
		t.Errorf("文本步骤起点 = %v，期望 %d", textStep.CreatedMs, base+500)
	}
	if textStep.CompletedMs == nil || *textStep.CompletedMs != base+900 {
		t.Errorf("文本步骤终点 = %v，期望 %d", textStep.CompletedMs, base+900)
	}
	if part := textStep.Parts[0]; part.StartMs == nil || *part.StartMs != base+500 || part.EndMs == nil || *part.EndMs != base+900 {
		t.Errorf("文本 part 区间 = %v..%v", part.StartMs, part.EndMs)
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

// TestRolloutReasoningFromContent：经代理接入的模型（如 DeepSeek）把思考写在
// response_item.content[type=reasoning_text]，summary 为空；必须照样落成思考部件。
func TestRolloutReasoningFromContent(t *testing.T) {
	stamp := "2026-09-22T10:00:00.000Z"
	records := []map[string]any{
		{"timestamp": stamp, "type": "session_meta", "payload": map[string]any{
			"session_id": "01a0c89f-3c06-73c3-aae8-e6de46100531", "cwd": "/work/demo", "timestamp": stamp,
		}},
		{"timestamp": stamp, "type": "turn_context", "payload": map[string]any{"turn_id": "turn-1"}},
		{"timestamp": stamp, "type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "turn-1"}},
		{"timestamp": stamp, "type": "event_msg", "payload": map[string]any{
			"type": "item_completed", "turn_id": "turn-1", "started_at_ms": 1000, "completed_at_ms": 1000,
			"item": map[string]any{"type": "UserMessage", "id": "u1", "content": []any{map[string]any{"type": "text", "text": "跑一下"}}},
		}},
		{"timestamp": stamp, "type": "response_item", "payload": map[string]any{"type": "message", "id": "a1", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": "好的"}}}},
		{"timestamp": stamp, "type": "response_item", "payload": map[string]any{
			"type": "reasoning", "id": "r1", "summary": []any{},
			"content": []any{map[string]any{"type": "reasoning_text", "text": "先想一下\n再回答"}},
		}},
	}
	store := writeRollout(t, records)
	summaries, err := New().Sessions(context.Background(), store, source.Query{})
	if err != nil {
		t.Fatalf("列会话：%v", err)
	}
	opened, err := New().Open(context.Background(), store, summaries[0].Ref, source.OpenOptions{})
	if err != nil {
		t.Fatalf("取会话：%v", err)
	}
	var reasoning *session.Part
	for index := range opened.Messages {
		for partIndex := range opened.Messages[index].Parts {
			part := &opened.Messages[index].Parts[partIndex]
			if part.Kind == session.PartReasoning {
				reasoning = part
			}
		}
	}
	if reasoning == nil || reasoning.Text == nil {
		t.Fatalf("没有解析出思考部件：%+v", opened.Messages)
	}
	if !strings.Contains(*reasoning.Text, "先想一下") {
		t.Errorf("思考文本 = %q", *reasoning.Text)
	}
	for _, diagnostic := range opened.Diagnostics {
		if diagnostic.Code == "encrypted-reasoning" {
			t.Errorf("有正文时不该报 encrypted-reasoning：%+v", diagnostic)
		}
	}
}
