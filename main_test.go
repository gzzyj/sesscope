package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gzzyj/atrace/internal/cli"
)

// 退出码常量：与 cli 包保持一致（测试历史上直接叫 exitOK/exitBadInput）。
const (
	exitOK       = cli.ExitOK
	exitBadInput = cli.ExitBadInput
)

// sampleExport 造一份最小但完整的 opencode 导出（一轮：用户输入 + 一次模型调用 + 一次工具）。
func sampleExport(t *testing.T) []byte {
	t.Helper()
	created := int64(1000)
	reasonStart := int64(1100)
	reasonEnd := int64(1400)
	toolStart := int64(1500)
	toolEnd := int64(1700)
	completed := int64(1800)

	payload := map[string]any{
		"export_info": map[string]any{"exported_at": "2026-11-20T16:05:25.250945+08:00", "source": "opencode.db (read-only)"},
		"session": map[string]any{
			"id": "ses_test", "title": "单元测试会话", "slug": "test", "directory": "/tmp",
			"agent": "deepworks", "version": "1.18.18", "cost": 0,
			"model": map[string]any{"id": "GLM-5.3", "providerID": "dth", "variant": "high"},
		},
		"global_stats": map[string]any{"total_tool_duration_ms": 200},
		"turn_timings": []any{map[string]any{"message_id": "msg_a1", "turn_duration_ms": 800}},
		"messages": []any{
			map[string]any{
				"message_id": "msg_u1", "role": "user", "time_created_ms": created,
				"parts": []any{map[string]any{
					"id": "prt_u1", "type": "text", "time_created_ms": created,
					"data": map[string]any{"type": "text", "text": "跑一下"},
				}},
			},
			map[string]any{
				"message_id": "msg_a1", "role": "assistant", "time_created_ms": reasonStart,
				"model_id": "GLM-5.3", "model_provider": "dth",
				"data": map[string]any{
					"role": "assistant", "finish": "tool-calls",
					"time":   map[string]any{"created": reasonStart, "completed": completed},
					"tokens": map[string]any{"total": 100, "input": 80, "output": 10, "reasoning": 10, "cache": map[string]any{"read": 0, "write": 0}},
				},
				"parts": []any{
					map[string]any{"id": "prt_s1", "type": "step-start", "time_created_ms": reasonStart, "data": map[string]any{"type": "step-start"}},
					map[string]any{
						"id": "prt_r1", "type": "reasoning", "time_created_ms": reasonStart,
						"data": map[string]any{"type": "reasoning", "text": "想一想", "time": map[string]any{"start": reasonStart, "end": reasonEnd}},
					},
					map[string]any{
						"id": "prt_t1", "type": "tool", "time_created_ms": toolStart,
						"data": map[string]any{
							"type": "tool", "tool": "bash", "callID": "call_1",
							"state": map[string]any{
								"status": "completed",
								"input":  map[string]any{"command": "ls"},
								"output": "done",
								"time":   map[string]any{"start": toolStart, "end": toolEnd},
							},
						},
					},
				},
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("构造样例导出：%v", err)
	}
	return raw
}

func runCLI(t *testing.T, stdin []byte, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, bytes.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestRunStdinToStdout(t *testing.T) {
	code, stdout, stderr := runCLI(t, sampleExport(t), "-generated-at", "2026-11-20T10:22:04.130Z")
	if code != 0 {
		t.Fatalf("退出码 = %d，stderr：%s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "<!doctype html>") {
		t.Errorf("stdout 不是 HTML：%q", stdout[:min(60, len(stdout))])
	}
	if !strings.Contains(stdout, "单元测试会话") {
		t.Error("HTML 里没有会话标题")
	}
	// 摘要必须走 stderr，不能污染 stdout 里的 HTML
	if !strings.Contains(stderr, "轮次 1") {
		t.Errorf("stderr 摘要缺失：%s", stderr)
	}
}

func TestRunWritesFilesAndHonorsGeneratedAt(t *testing.T) {
	dir := t.TempDir()
	htmlPath := filepath.Join(dir, "nested", "trace.html")
	dataPath := filepath.Join(dir, "nested", "trace-data.json")
	code, _, stderr := runCLI(t, sampleExport(t),
		"-out", htmlPath, "-data", dataPath,
		"-generated-at", "2026-11-20T10:22:04.130Z", "-quiet")
	if code != 0 {
		t.Fatalf("退出码 = %d，stderr：%s", code, stderr)
	}
	html, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatalf("读 HTML：%v", err)
	}
	if !bytes.HasPrefix(html, []byte("<!doctype html>")) {
		t.Error("写出的不是 HTML")
	}
	raw, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatalf("读数据：%v", err)
	}
	var decoded struct {
		Meta struct {
			GeneratedAt string `json:"generatedAt"`
			Title       string `json:"title"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("数据不是合法 JSON：%v", err)
	}
	if decoded.Meta.GeneratedAt != "2026-11-20T10:22:04.130Z" {
		t.Errorf("generatedAt = %q", decoded.Meta.GeneratedAt)
	}
	if decoded.Meta.Title != "单元测试会话" {
		t.Errorf("title = %q", decoded.Meta.Title)
	}
	// 目录自动创建 + 不留临时文件
	entries, err := os.ReadDir(filepath.Dir(htmlPath))
	if err != nil {
		t.Fatalf("读目录：%v", err)
	}
	if len(entries) != 2 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("目录里应只有两个产物，实际：%v", names)
	}
}

func TestRunRejectsBadInput(t *testing.T) {
	cases := []struct {
		name  string
		stdin string
		want  string
	}{
		{"不是 JSON", "not json", "JSON"},
		{"顶层不是对象", "[1,2,3]", "JSON 对象"},
		{"空输入", "", "空"},
		{"没有 messages", `{"session":{}}`, "messages"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(t, []byte(testCase.stdin))
			if code != exitBadInput {
				t.Errorf("退出码 = %d，期望 %d", code, exitBadInput)
			}
			if stdout != "" {
				t.Errorf("失败时不应往 stdout 写东西，得到 %q", stdout)
			}
			if !strings.Contains(stderr, testCase.want) {
				t.Errorf("stderr 应包含 %q，实际：%s", testCase.want, stderr)
			}
		})
	}
}

func TestRunRejectsDoubleStdout(t *testing.T) {
	code, _, stderr := runCLI(t, sampleExport(t), "-out", "-", "-data", "-")
	if code != exitBadInput {
		t.Errorf("退出码 = %d，期望 %d", code, exitBadInput)
	}
	if !strings.Contains(stderr, "stdout") {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestRunRejectsUnknownArgument(t *testing.T) {
	// 不带子命令时，第一个非 flag 参数会被当成子命令；render 自己的多余参数要单独报错
	code, _, stderr := runCLI(t, sampleExport(t), "render", "extra-positional")
	if code != exitBadInput {
		t.Errorf("退出码 = %d，期望 %d", code, exitBadInput)
	}
	if !strings.Contains(stderr, "extra-positional") {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestRunMissingInputFile(t *testing.T) {
	code, _, stderr := runCLI(t, nil, "-in", filepath.Join(t.TempDir(), "nope.json"))
	if code != exitBadInput {
		t.Errorf("退出码 = %d，期望 %d", code, exitBadInput)
	}
	if !strings.Contains(stderr, "读输入") {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestRunFixtureInjection(t *testing.T) {
	code, stdout, stderr := runCLI(t, sampleExport(t), "-fixture-tool-error", "-generated-at", "2026-11-20T10:22:04.130Z")
	if code != 0 {
		t.Fatalf("退出码 = %d，stderr：%s", code, stderr)
	}
	if !strings.Contains(stdout, "（模拟数据）单元测试会话") {
		t.Error("fixture 模式下标题应带「（模拟数据）」前缀")
	}
	if !strings.Contains(stdout, "这是模拟数据") {
		t.Error("fixture 模式下应注入说明用的数据提示")
	}
}

func TestRunVersion(t *testing.T) {
	code, stdout, stderr := runCLI(t, nil, "-version")
	if code != 0 {
		t.Fatalf("退出码 = %d，stderr：%s", code, stderr)
	}
	if !strings.Contains(stdout, "atrace") {
		t.Errorf("stdout = %s", stdout)
	}
}

func TestRunGeneratedAtValidation(t *testing.T) {
	code, _, stderr := runCLI(t, sampleExport(t), "-generated-at", "昨天")
	if code != exitBadInput {
		t.Errorf("退出码 = %d，期望 %d", code, exitBadInput)
	}
	if !strings.Contains(stderr, "RFC3339") {
		t.Errorf("stderr = %s", stderr)
	}
}
