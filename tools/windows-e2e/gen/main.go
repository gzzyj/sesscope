// Command gen 生成 Windows E2E 用的最小会话夹具。
//
// 它不模拟产品内部实现，只按各来源真实的落盘格式写文件：
//   - dsh：~/.dsh/sessions/<slug>/session-<id>/session.v4.jsonl.zstd
//   - codex：~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl.zst
//   - claude：~/.claude/projects/<slug>/<uuid>.jsonl
//   - opencode：SQLite v1（session/message/part），用 internal/testfixture 建库后
//     把 worktree/directory 改成 Windows 路径
//
// 所有会话的 cwd 都是 C:\Users\fastagi\work\demo，用来验证 Windows 路径在
// 工作区归并、页面显示、导出里都不被当成 POSIX 路径处理。
package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"

	"github.com/gzzyj/sesscope/internal/testfixture"
)

const (
	winCwd     = `C:\Users\fastagi\work\demo`
	winRootCwd = `C:\Users\fastagi\work\root`
	dshID      = "session-wine2e-0001"
	claudeID   = "11111111-2222-4333-8444-555555555555"
	codexID    = "01a0c89f-3c06-73c3-aae8-e6de46100531"
)

func main() {
	out := flag.String("out", "out/windows-e2e", "输出目录")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run(root string) error {
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	home := filepath.Join(root, "fixtures", "home")

	if err := writeZstdJSONL(
		filepath.Join(home, ".dsh", "sessions", "--C-Users-fastagi-work-demo--", dshID, "session.v4.jsonl.zstd"),
		dshRecords(),
	); err != nil {
		return fmt.Errorf("dsh fixture：%w", err)
	}
	if err := writeJSON(
		filepath.Join(home, ".dsh", "storages", "session_projcache", "sessions", dshID+".json"),
		map[string]any{"record": map[string]any{"rows": map[string]any{
			"title": map[string]any{"val": "Windows 演示：跑一遍全流程"},
			"tokenUsage": map[string]any{"val": map[string]any{"totals": map[string]any{
				"uncachedInputTokens": 1234, "outputTokens": 567, "cacheReadTokens": 89,
			}}},
		}}},
	); err != nil {
		return fmt.Errorf("dsh projcache：%w", err)
	}

	if err := writeZstdJSONL(
		filepath.Join(home, ".codex", "sessions", "2026", "11", "27",
			"rollout-2026-11-27T10-00-00-"+codexID+".jsonl.zst"),
		codexRecords(),
	); err != nil {
		return fmt.Errorf("codex fixture：%w", err)
	}

	if err := writeJSONL(
		filepath.Join(home, ".claude", "projects", "--C-Users-fastagi-work-demo", claudeID+".jsonl"),
		claudeRecords(),
	); err != nil {
		return fmt.Errorf("claude fixture：%w", err)
	}

	opencodeDir := filepath.Join(home, "AppData", "Local", "opencode")
	if err := os.MkdirAll(opencodeDir, 0o755); err != nil {
		return err
	}
	opencodePath, err := testfixture.Build(opencodeDir)
	if err != nil {
		return fmt.Errorf("opencode fixture：%w", err)
	}
	if err := retargetOpencode(opencodePath); err != nil {
		return fmt.Errorf("opencode 改 Windows 路径：%w", err)
	}

	if err := writeDeepWorksState(filepath.Join(root, "fixtures", "deepworks")); err != nil {
		return fmt.Errorf("deepworks 状态：%w", err)
	}

	fmt.Println("生成完成：", root)
	fmt.Println("  opencode fixture：", opencodePath)
	return nil
}

func dshRecords() []map[string]any {
	base := int64(1795000000000)
	return []map[string]any{
		{"type": "session", "version": 4, "id": dshID, "createdAt": base, "cwd": winCwd, "agentPreset": "ptc"},
		{"type": "turn/start", "seq": 1, "time": base + 100, "data": map[string]any{"turn": 1}},
		{"type": "step/start", "seq": 2, "time": base + 200, "data": map[string]any{"turn": 1, "step": 1}},
		{"type": "user/message", "seq": 3, "time": base + 250, "data": map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "在 Windows 上跑一遍 sesscope 全流程"}},
			"source":  map[string]any{"kind": "user"}, "role": "user", "id": "u1",
		}},
		{"type": "assistant/message", "seq": 4, "time": base + 2000, "data": map[string]any{
			"turn": 1, "step": 1,
			"message": map[string]any{
				"id": "a1", "role": "assistant",
				"content": []any{
					map[string]any{"type": "reasoning", "text": "先列目录，再导出页面"},
					map[string]any{"type": "text", "text": "执行 dir"},
					map[string]any{"type": "tool-call", "id": "call_1", "name": "bash", "arguments": `{"command":"dir C:\\Users\\fastagi\\work\\demo"}`},
				},
			},
			"usage": map[string]any{"inputTokens": 100, "outputTokens": 20, "totalTokens": 120},
			"stream": []any{
				map[string]any{"type": "chunk", "time": base + 400, "chunk": map[string]any{"type": "block-start", "index": 0}},
				map[string]any{"type": "chunk", "time": base + 600, "chunk": map[string]any{"type": "block-end", "index": 0}},
				map[string]any{"type": "chunk", "time": base + 700, "chunk": map[string]any{"type": "block-start", "index": 2}},
				map[string]any{"type": "chunk", "time": base + 1900, "chunk": map[string]any{"type": "block-end", "index": 2}},
			},
		}},
		{"type": "tool/call", "seq": 5, "time": base + 2001, "data": map[string]any{
			"turn": 1, "step": 1, "callId": "call_1", "name": "bash",
			"arguments": `{"command":"dir C:\\Users\\fastagi\\work\\demo"}`,
		}},
		{"type": "tool/result", "seq": 6, "time": base + 2600, "data": map[string]any{
			"turn": 1, "step": 1,
			"message": map[string]any{
				"role": "tool", "toolCallId": "call_1", "isError": false,
				"content": []any{map[string]any{"type": "text", "text": "a.txt\nb.txt"}},
			},
		}},
		{"type": "step/end", "seq": 7, "time": base + 2700, "data": map[string]any{"turn": 1, "step": 1}},
		{"type": "turn/end", "seq": 8, "time": base + 2800, "data": map[string]any{"turn": 1, "reason": map[string]any{"kind": "completed"}}},
	}
}

func codexRecords() []map[string]any {
	stamp := "2026-11-27T10:00:00.000Z"
	base := int64(1795000000000)
	return []map[string]any{
		{"timestamp": stamp, "type": "session_meta", "payload": map[string]any{
			"session_id": codexID, "cwd": winCwd, "cli_version": "0.163.0",
			"model_provider": "OpenAI", "originator": "codex-tui", "timestamp": stamp,
		}},
		{"timestamp": stamp, "type": "turn_context", "payload": map[string]any{"turn_id": "turn-1", "cwd": winCwd}},
		{"timestamp": stamp, "type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "turn-1"}},
		{"timestamp": stamp, "type": "event_msg", "payload": map[string]any{
			"type": "item_completed", "turn_id": "turn-1", "started_at_ms": base + 100, "completed_at_ms": base + 200,
			"item": map[string]any{"type": "UserMessage", "id": "u1",
				"content": []any{map[string]any{"type": "text", "text": "Windows 上的 codex 会话"}}},
		}},
		{"timestamp": stamp, "type": "response_item", "payload": map[string]any{
			"type": "message", "id": "a1", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": "收到"}},
		}},
		{"timestamp": stamp, "type": "response_item", "payload": map[string]any{"type": "reasoning", "id": "r1", "summary": []any{}, "encrypted_content": "gAAAA"}},
		{"timestamp": stamp, "type": "response_item", "payload": map[string]any{
			"type": "function_call", "id": "fc1", "name": "exec_command", "call_id": "call_1",
			"arguments": `{"cmd":"dir C:\\Users\\fastagi\\work\\demo"}`,
		}},
		{"timestamp": stamp, "type": "response_item", "payload": map[string]any{
			"type": "function_call_output", "id": "fco1", "call_id": "call_1", "output": "a.txt",
		}},
		{"timestamp": stamp, "type": "token_usage_record", "payload": map[string]any{
			"turn_id": "turn-1", "usage": map[string]any{"input_tokens": 10, "output_tokens": 2, "cached_input_tokens": 4},
		}},
		{"timestamp": stamp, "type": "event_msg", "payload": map[string]any{"type": "task_complete", "turn_id": "turn-1", "completed_at_ms": base + 3000}},
	}
}

func claudeRecords() []map[string]any {
	stamp := "2026-11-27T10:00:00.000Z"
	return []map[string]any{
		{"type": "user", "uuid": "u1", "timestamp": stamp, "cwd": winCwd, "sessionId": claudeID,
			"version": "2.1.312", "origin": map[string]any{"kind": "human"}, "promptSource": "typed",
			"turnPosition": map[string]any{"turnIndex": 1},
			"message":      map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "Windows 上的 claude 会话"}}}},
		{"type": "assistant", "uuid": "a1", "timestamp": stamp, "sessionId": claudeID,
			"message": map[string]any{
				"role": "assistant", "model": "claude-x", "stop_reason": "tool_use",
				"content": []any{
					map[string]any{"type": "text", "text": "好的"},
					map[string]any{"type": "tool_use", "id": "call_1", "name": "Bash", "input": map[string]any{"command": "dir"}},
				},
				"usage": map[string]any{"input_tokens": 10, "output_tokens": 2},
			}},
		{"type": "user", "uuid": "u2", "timestamp": stamp, "sessionId": claudeID,
			"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call_1", "content": "a.txt", "is_error": false}}}},
		{"type": "system", "subtype": "turn_duration", "durationMs": 500, "timestamp": stamp},
	}
}

func retargetOpencode(path string) error {
	conn, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		return err
	}
	defer conn.Close()
	statements := []struct {
		query string
		args  []any
	}{
		{`update project set worktree = ?, name = ? where id = ?`, []any{winRootCwd, "win-root", testfixture.GlobalProjectID}},
		{`update project set worktree = ?, name = ? where id = ?`, []any{winCwd, "win-demo", testfixture.DemoProjectID}},
		{`update session set directory = ?, path = ? where project_id = ?`, []any{winRootCwd, winRootCwd, testfixture.GlobalProjectID}},
		{`update session set directory = ?, path = ? where project_id = ?`, []any{winCwd, winCwd, testfixture.DemoProjectID}},
	}
	for _, statement := range statements {
		if _, err := conn.Exec(statement.query, statement.args...); err != nil {
			return err
		}
	}
	// 把 fixture 的 1970 年时间整体平移到 2026-11-26，保留相对间隔，
	// 这样 Windows 上的 sessions/workspaces 排序能看到它排在前排。
	delta := int64(1795700000000) - 500
	for _, table := range []string{"project", "session", "message", "part"} {
		query := "update " + table + " set time_created = time_created + ?, time_updated = time_updated + ?"
		if _, err := conn.Exec(query, delta, delta); err != nil {
			return err
		}
	}
	return nil
}

func writeDeepWorksState(dir string) error {
	normalized := strings.ToLower(strings.ReplaceAll(winCwd, "\\", "/"))
	state := map[string]any{
		"selectedId": "ws-win-demo",
		"workspaces": []any{map[string]any{
			"id": "ws-win-demo", "name": "win-demo", "displayName": "Windows 演示工作区",
			"path": winCwd, "preset": "default", "workspaceType": "local", "directoryMissing": false,
		}},
	}
	ports := map[string]any{"workspacePorts": map[string]any{normalized: 45678}}
	tokens := map[string]any{"workspaces": map[string]any{normalized: map[string]any{"clientToken": "fixture-token"}}}
	for name, payload := range map[string]any{
		"workspace-state.json":         state,
		"deepworks-port-state.json":    ports,
		"deepworks-server-tokens.json": tokens,
	} {
		if err := writeJSON(filepath.Join(dir, name), payload); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, 10)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o644)
}

func writeJSONL(path string, records []map[string]any) error {
	var buffer bytes.Buffer
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			return err
		}
		buffer.Write(line)
		buffer.WriteByte(10)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buffer.Bytes(), 0o644)
}

func writeZstdJSONL(path string, records []map[string]any) error {
	var buffer bytes.Buffer
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			return err
		}
		buffer.Write(line)
		buffer.WriteByte(10)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	writer, err := zstd.NewWriter(file)
	if err != nil {
		file.Close()
		return err
	}
	if _, err := writer.Write(buffer.Bytes()); err != nil {
		writer.Close()
		file.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
