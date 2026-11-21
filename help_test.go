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
	for _, needle := range []string{"processes", "workspaces", "sessions", "export", "analyze", "render"} {
		if !strings.Contains(stdout, needle) {
			t.Errorf("顶层帮助里缺少子命令 %q", needle)
		}
	}
	// 顶层帮助不能是 render 的参数表（这正是之前的 bug）
	if strings.Contains(stdout, "-fixture-tool-error") {
		t.Error("顶层 -h 打出了 render 的参数表，而不是子命令列表")
	}
	if !strings.Contains(stdout, "从零开始") {
		t.Error("顶层帮助应当给出从零开始的使用顺序")
	}
}

func TestHelpForSubcommand(t *testing.T) {
	code, _, stderr := runArgs(t, "help", "sessions")
	if code != exitOK {
		t.Fatalf("退出码 = %d", code)
	}
	if !strings.Contains(stderr, "-workspace") || !strings.Contains(stderr, "-pid") {
		t.Errorf("sessions 帮助应当包含 -workspace 和 -pid，实际：\n%s", stderr)
	}
}

func TestSessionsHelpMentionsPid(t *testing.T) {
	code, _, stderr := runArgs(t, "sessions", "-h")
	if code != exitOK {
		t.Fatalf("退出码 = %d", code)
	}
	if !strings.Contains(stderr, "-pid") {
		t.Errorf("sessions -h 应当说明 -pid：\n%s", stderr)
	}
}
