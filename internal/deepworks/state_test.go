package deepworks

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("写 %s：%v", name, err)
	}
}

func TestReadStateMergesPortsAndTokens(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "workspace-state.json", `{
  "selectedId": "ws_a",
  "workspaces": [
    {"id":"ws_a","name":"默认工作区","displayName":"默认工作区","path":"/home/u/DeepWorks","workspaceType":"local"},
    {"id":"ws_b","name":"deepexi","path":"/opt/deepexi","workspaceType":"local"}
  ]
}`)
	// 注意：port-state 里的路径是小写的，用来验证目录归一化
	writeFile(t, dir, "deepworks-port-state.json", `{
  "version": 3,
  "workspacePorts": {"/home/u/deepworks": 34039, "/opt/deepexi": 46881}
}`)
	writeFile(t, dir, "deepworks-server-tokens.json", `{
  "version": 1,
  "workspaces": {"/opt/deepexi": {"clientToken":"c","hostToken":"h"}}
}`)

	t.Setenv("DEEPWORKS_CONFIG_HOME", dir)
	state, err := ReadState()
	if err != nil {
		t.Fatalf("读状态：%v", err)
	}
	if len(state.Workspaces) != 2 {
		t.Fatalf("工作区数 = %d，期望 2", len(state.Workspaces))
	}
	if state.SelectedID != "ws_a" {
		t.Errorf("selectedId = %q", state.SelectedID)
	}
	first, second := state.Workspaces[0], state.Workspaces[1]
	if first.Label() != "默认工作区" {
		t.Errorf("第一个工作区名 = %q", first.Label())
	}
	if first.APIPort != 34039 {
		t.Errorf("大小写不同的目录也要匹配上端口，得到 %d", first.APIPort)
	}
	if second.APIPort != 46881 || !second.HasToken {
		t.Errorf("deepexi 端口/凭据 = %d / %v", second.APIPort, second.HasToken)
	}
	if len(state.Warnings) != 0 {
		t.Errorf("不该有 warning：%v", state.Warnings)
	}
}

func TestReadStateMissingMainFile(t *testing.T) {
	t.Setenv("DEEPWORKS_CONFIG_HOME", t.TempDir())
	if _, err := ReadState(); err == nil {
		t.Fatal("没有 workspace-state.json 时应报错")
	}
}

func TestReadStateToleratesMissingAuxFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "workspace-state.json", `{"workspaces":[{"id":"ws_a","name":"x","path":"/x"}]}`)
	t.Setenv("DEEPWORKS_CONFIG_HOME", dir)
	state, err := ReadState()
	if err != nil {
		t.Fatalf("缺附属文件不该失败：%v", err)
	}
	if len(state.Workspaces) != 1 || state.Workspaces[0].APIPort != 0 {
		t.Errorf("状态 = %+v", state)
	}
	if len(state.Warnings) == 0 {
		t.Error("缺附属文件时应留下 warning，而不是静默")
	}
}
