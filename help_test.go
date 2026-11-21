package main

import (
	"strings"
	"testing"
)

func TestTopLevelHelpListsCommands(t *testing.T) {
	code, stdout, _ := runArgs(t, "-h")
	if code != exitOK {
		t.Fatalf("退出码 = %d", code)
	}
	for _, needle := range []string{"sources", "workspaces", "sessions", "export", "analyze", "render", "doctor"} {
		if !strings.Contains(stdout, needle) {
			t.Errorf("顶层帮助里缺少子命令 %q", needle)
		}
	}
	// 顶层帮助不能是 render 的参数表（这正是之前的 bug）
	if strings.Contains(stdout, "fixture-tool-error") {
		t.Error("顶层 -h 打出了 render 的参数表，而不是子命令列表")
	}
	if !strings.Contains(stdout, "从零开始") {
		t.Error("顶层帮助应当给出从零开始的使用顺序")
	}
}

func TestHelpForSubcommand(t *testing.T) {
	code, stdout, _ := runArgs(t, "help", "sessions")
	if code != exitOK {
		t.Fatalf("退出码 = %d", code)
	}
	// 契约变更：子命令帮助走 stdout（cobra 惯例），参数错误时的用法才走 stderr。
	if !strings.Contains(stdout, "workspace") || !strings.Contains(stdout, "pid") {
		t.Errorf("sessions 帮助应当包含 workspace 和 pid，实际：%s", stdout)
	}
}

func TestSessionsHelpMentionsPid(t *testing.T) {
	code, stdout, _ := runArgs(t, "sessions", "-h")
	if code != exitOK {
		t.Fatalf("退出码 = %d", code)
	}
	if !strings.Contains(stdout, "--pid") {
		t.Errorf("sessions -h 应当说明 --pid：%s", stdout)
	}
}
