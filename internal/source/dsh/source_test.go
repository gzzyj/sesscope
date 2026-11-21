package dsh

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/gzzyj/sesscope/internal/session"
	"github.com/gzzyj/sesscope/internal/source"
)

// writeStore 用 map 造 fixture（不内嵌 JSON 字符串，避免转义问题），压成 zstd 后按真实目录布局落盘。
func writeStore(t *testing.T, records []map[string]any) source.Store {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "demo-slug", "session-test-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录：%v", err)
	}
	var buffer bytes.Buffer
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("序列化 fixture：%v", err)
		}
		buffer.Write(line)
		buffer.WriteByte(10)
	}
	path := filepath.Join(dir, "session.v4.jsonl.zstd")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("建文件：%v", err)
	}
	writer, err := zstd.NewWriter(file)
	if err != nil {
		t.Fatalf("zstd：%v", err)
	}
	if _, err := writer.Write(buffer.Bytes()); err != nil {
		t.Fatalf("写 zstd：%v", err)
	}
	writer.Close()
	file.Close()

	stores, err := New().Locate(context.Background(), source.Locator{Store: root})
	if err != nil {
		t.Fatalf("定位：%v", err)
	}
	return stores[0]
}

func fixtureRecords() []map[string]any {
	return []map[string]any{
		{"type": "session", "version": 4, "id": "session-test-1", "createdAt": 1000, "cwd": "/work/demo", "agentPreset": "ptc"},
		{"type": "turn/start", "seq": 1, "time": 1100, "data": map[string]any{"turn": 1}},
		{"type": "step/start", "seq": 2, "time": 1200, "data": map[string]any{"turn": 1, "step": 1}},
		{"type": "user/message", "seq": 3, "time": 1250, "data": map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "跑一下测试"}},
			"source":  map[string]any{"kind": "user"},
			"role":    "user",
			"id":      "u1",
		}},
		{"type": "assistant/message", "seq": 4, "time": 2000, "data": map[string]any{
			"turn": 1, "step": 1,
			"message": map[string]any{
				"id": "a1", "role": "assistant",
				"content": []any{
					map[string]any{"type": "reasoning", "text": "想一想"},
					map[string]any{"type": "text", "text": "执行"},
					map[string]any{"type": "tool-call", "id": "call_1", "name": "bash", "arguments": `{"command":"ls"}`},
				},
			},
			"usage": map[string]any{"inputTokens": 10, "outputTokens": 2, "totalTokens": 12},
			"stream": []any{
				map[string]any{"type": "chunk", "time": 1400, "chunk": map[string]any{"type": "block-start", "index": 0}},
				map[string]any{"type": "chunk", "time": 1600, "chunk": map[string]any{"type": "block-end", "index": 0}},
				map[string]any{"type": "chunk", "time": 1700, "chunk": map[string]any{"type": "block-start", "index": 2}},
				map[string]any{"type": "chunk", "time": 1900, "chunk": map[string]any{"type": "block-end", "index": 2}},
			},
		}},
		{"type": "tool/call", "seq": 5, "time": 2001, "data": map[string]any{
			"turn": 1, "step": 1, "callId": "call_1", "name": "bash", "arguments": `{"command":"ls"}`,
		}},
		{"type": "tool/result", "seq": 6, "time": 2100, "data": map[string]any{
			"turn": 1, "step": 1,
			"message": map[string]any{
				"role": "tool", "toolCallId": "call_1", "isError": false,
				"content": []any{map[string]any{"type": "text", "text": "a.txt"}},
			},
		}},
		{"type": "step/end", "seq": 7, "time": 2200, "data": map[string]any{"turn": 1, "step": 1}},
		{"type": "turn/end", "seq": 8, "time": 2300, "data": map[string]any{"turn": 1, "reason": map[string]any{"kind": "completed"}}},
	}
}

func TestSessionsAndOpen(t *testing.T) {
	store := writeStore(t, fixtureRecords())
	if !store.Readable || store.Version != "v4" {
		t.Fatalf("store = %+v", store)
	}
	summaries, err := New().Sessions(context.Background(), store, source.Query{})
	if err != nil {
		t.Fatalf("列会话：%v", err)
	}
	if len(summaries) != 1 || summaries[0].ID != "session-test-1" {
		t.Fatalf("summaries = %+v", summaries)
	}
	if summaries[0].UpdatedKind != "file-mtime" || summaries[0].Messages != -1 {
		t.Fatalf("只读头部时应标注 mtime、消息数未知：%+v", summaries[0])
	}
	if summaries[0].Directory != "/work/demo" {
		t.Fatalf("directory = %q", summaries[0].Directory)
	}

	opened, err := New().Open(context.Background(), store, summaries[0].Ref, source.OpenOptions{})
	if err != nil {
		t.Fatalf("取会话：%v", err)
	}
	// 头行字段在顶层：id / cwd / agentPreset 必须带出来（曾经因为按 data 解析而丢失）。
	if opened.Ref.ID != "session-test-1" {
		t.Fatalf("Ref.ID = %q，期望 session-test-1", opened.Ref.ID)
	}
	if opened.Meta.Directory == nil || *opened.Meta.Directory != "/work/demo" {
		t.Fatalf("Directory = %v，期望 /work/demo", opened.Meta.Directory)
	}
	if opened.Meta.Agent == nil || *opened.Meta.Agent != "ptc" {
		t.Fatalf("Agent = %v，期望 ptc", opened.Meta.Agent)
	}
	if len(opened.Messages) != 2 {
		t.Fatalf("消息数 = %d，期望 2", len(opened.Messages))
	}
	if opened.Messages[0].Role != session.RoleUser || opened.Messages[1].Role != session.RoleAssistant {
		t.Fatalf("角色顺序不对：%v / %v", opened.Messages[0].Role, opened.Messages[1].Role)
	}
	assistant := opened.Messages[1]
	if assistant.TurnHint.Turn != 1 || assistant.TurnHint.Step != 1 {
		t.Fatalf("turn hint = %+v", assistant.TurnHint)
	}
	if assistant.CompletedMs == nil || *assistant.CompletedMs != 2200 {
		t.Fatalf("步骤完成时间 = %v，期望 2200", assistant.CompletedMs)
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
		if part.Tool == nil || part.Tool.Status == nil || *part.Tool.Status != "completed" {
			t.Fatalf("工具状态不对：%+v", part.Tool)
		}
		if part.Tool.Kind != session.ToolShell || part.Tool.Command != "ls" {
			t.Fatalf("工具语义 = %s / %q", part.Tool.Kind, part.Tool.Command)
		}
		if string(part.Tool.Output) != `"a.txt"` {
			t.Fatalf("工具输出 = %s", string(part.Tool.Output))
		}
		if part.Tool.EndMs == nil || *part.Tool.EndMs != 2100 {
			t.Fatalf("工具结束时间 = %v", part.Tool.EndMs)
		}
	}
	if tools != 1 {
		t.Fatalf("工具数 = %d，期望 1", tools)
	}
	if opened.Meta.Provenance.Source != session.DSH {
		t.Fatalf("provenance = %+v", opened.Meta.Provenance)
	}
}
