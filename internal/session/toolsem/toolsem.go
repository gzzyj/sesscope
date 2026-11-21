// Package toolsem 把不同 Agent 产品的工具名/参数归一成语义类别。
//
// 为什么需要：归因规则（路径探查、授权补偿、编辑锚点失配……）判断的是"读了哪个文件、
// 跑了什么命令"，而不是"工具叫 bash 还是 exec_command"。没有这一层，规则只能绑死
// opencode 的工具名，换源立刻失效。
package toolsem

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/gzzyj/atrace/internal/session"
)

// Unwrap 把"被编码成 JSON 字符串的参数"还原成对象/文本字节。
// dsh、codex 都把 arguments 存成 JSON 字符串（里面再套一层 JSON），不拆这一层
// 会让页面显示一坨转义内容，也让工具语义识别不到 command/path。
func Unwrap(args []byte) []byte {
	trimmed := bytes.TrimSpace(args)
	if len(trimmed) < 2 || trimmed[0] != 34 { // 34 = 双引号
		return args
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err != nil {
		return args
	}
	inner := strings.TrimSpace(text)
	if strings.HasPrefix(inner, "{") || strings.HasPrefix(inner, "[") {
		return []byte(inner)
	}
	return []byte(text)
}

// Classify 返回工具语义类别、归一化目标（文件/路径）与命令文本。
// args 保持原始字节：解析失败不影响分类，只是拿不到 target/command。
func Classify(source session.SourceID, name string, args []byte) (session.ToolKind, string, string) {
	_ = source // 目前四源共用一张表；将来若某产品有同名不同义的工具，在这里按 source 分叉。
	lowered := strings.ToLower(strings.TrimSpace(name))
	kind := kindOf(lowered)
	target, command := extract(args, kind)
	return kind, target, command
}

func kindOf(name string) session.ToolKind {
	switch name {
	case "bash", "sh", "shell", "exec", "exec_command", "run_code", "terminal", "run_terminal_cmd":
		return session.ToolShell
	case "read", "read_file", "view_file", "cat", "open_file":
		return session.ToolRead
	case "write", "write_file", "create_file", "new_file":
		return session.ToolWrite
	case "edit", "edit_file", "str_replace", "apply_patch", "patch", "multiedit":
		return session.ToolEdit
	case "grep", "rg", "search", "search_files", "find_files", "grep_search":
		return session.ToolSearch
	case "glob", "list", "ls", "list_dir", "list_directory", "list_files":
		return session.ToolList
	case "webfetch", "web_fetch", "fetch", "browser", "web_search":
		return session.ToolWeb
	case "question", "ask", "ask_user", "askuserquestion", "exit_plan_mode", "permission", "request_approval":
		return session.ToolAsk
	case "task", "agent", "subagent", "run_agent", "dispatch_agent":
		return session.ToolTask
	default:
		return session.ToolOther
	}
}

// extract 从参数里取"对象路径"和"命令行"。
// 不同产品的键名不一致（command/cmd/code/filePath/file_path/path），这里统一。
func extract(args []byte, kind session.ToolKind) (target string, command string) {
	object := decode(args)
	if object == nil {
		text := strings.TrimSpace(string(args))
		if kind == session.ToolShell && text != "" && !strings.HasPrefix(text, "{") {
			return firstPath(text), text
		}
		return "", ""
	}
	switch kind {
	case session.ToolShell:
		command = firstString(object, "command", "cmd", "input", "code", "script")
		target = firstString(object, "workdir", "cwd", "dir")
		if target == "" {
			target = firstPath(command)
		}
		return target, command
	case session.ToolRead, session.ToolWrite, session.ToolEdit, session.ToolList:
		return firstString(object, "filePath", "file_path", "path", "file", "filename", "directory", "dir"), ""
	case session.ToolSearch:
		target = firstString(object, "path", "directory", "dir")
		if target == "" {
			target = firstString(object, "pattern", "query", "glob")
		}
		return target, firstString(object, "command")
	case session.ToolWeb:
		return firstString(object, "url", "query"), ""
	case session.ToolTask:
		return firstString(object, "description", "prompt", "task"), ""
	default:
		return firstString(object, "filePath", "file_path", "path", "file", "url"), ""
	}
}

func decode(args []byte) map[string]any {
	trimmed := strings.TrimSpace(string(args))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(trimmed), &object); err != nil {
		return nil
	}
	return object
}

func firstString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := object[key]
		if !ok {
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

// firstPath 从命令行里挑第一个像路径的 token（归因做"同一目标"匹配用）。
// 引号/逗号/分号/括号用码点判断，避免在源码里出现转义字符。
func firstPath(command string) string {
	fields := strings.Fields(command)
	for _, field := range fields {
		trimmed := strings.TrimFunc(field, func(r rune) bool {
			switch r {
			case 34, 39, 96, 44, 59, 40, 41: // " ' 反引号 , ; ( )
				return true
			}
			return false
		})
		if strings.Contains(trimmed, "/") || strings.Contains(trimmed, string(rune(92))) {
			return trimmed
		}
	}
	return ""
}
