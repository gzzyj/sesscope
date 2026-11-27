// Package deepworks 读 DeepWorks 桌面端在本机维护的工作区状态文件。
//
// 工作区清单（id / 名称 / 路径）在 workspace-state.json，每个工作区的 API 端口在
// deepworks-port-state.json，凭据在 deepworks-server-tokens.json。
// opencode 的 SQLite 库里没有这份映射（session.workspace_id 一直是 NULL），
// 所以"我的工作区"必须从这里读，不能只看 opencode 的 project 表。
package deepworks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gzzyj/sesscope/internal/platform"
)

// Workspace 是 DeepWorks 的一个工作区。
type Workspace struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	DisplayName      string `json:"displayName"`
	Path             string `json:"path"`
	Preset           string `json:"preset"`
	WorkspaceType    string `json:"workspaceType"`
	DirectoryMissing bool   `json:"directoryMissing"`
	// 下面两个来自其它状态文件（不是 workspace-state.json 里的字段）
	APIPort  int  `json:"-"`
	HasToken bool `json:"-"`
}

// Label 返回展示名。
func (w Workspace) Label() string {
	for _, candidate := range []string{w.DisplayName, w.Name} {
		if candidate != "" {
			return candidate
		}
	}
	return w.ID
}

// State 是合并后的工作区状态。
type State struct {
	// ConfigDir 是状态文件所在目录。
	ConfigDir  string      `json:"config_dir"`
	SelectedID string      `json:"selected_id"`
	Workspaces []Workspace `json:"workspaces"`
	// Warnings 记录"哪个文件读失败"，不静默吞掉。
	Warnings []string `json:"warnings"`
}

// ConfigDir 返回 DeepWorks 配置目录（可用 DEEPWORKS_CONFIG_HOME 覆盖）。
func ConfigDir() (string, error) { return ConfigDirFor(platform.Detect()) }

// ConfigDirFor 按 OS 适配器返回 DeepWorks 配置目录：
//   - Linux/macOS：$XDG_CONFIG_HOME/com.deepexi.deepworks（默认 ~/.config/...）
//   - Windows：%APPDATA%\\com.deepexi.deepworks
func ConfigDirFor(p platform.Adapter) (string, error) {
	if custom := strings.TrimSpace(os.Getenv("DEEPWORKS_CONFIG_HOME")); custom != "" {
		return custom, nil
	}
	if p == nil {
		p = platform.Detect()
	}
	base := p.AppDataDir()
	if base == "" {
		return "", fmt.Errorf("取应用配置目录失败：家目录与 APPDATA/XDG_CONFIG_HOME 都为空")
	}
	return filepath.Join(base, "com.deepexi.deepworks"), nil
}

// ReadState 读取并合并工作区状态（当前 OS）。
func ReadState() (*State, error) { return ReadStateFor(platform.Detect()) }

// ReadStateFor 读取并合并工作区状态；任一附属文件读不到只记 warning，不影响主清单。
func ReadStateFor(p platform.Adapter) (*State, error) {
	dir, err := ConfigDirFor(p)
	if err != nil {
		return nil, err
	}
	state := &State{ConfigDir: dir, Workspaces: []Workspace{}, Warnings: []string{}}

	raw, err := os.ReadFile(filepath.Join(dir, "workspace-state.json"))
	if err != nil {
		return nil, fmt.Errorf("读 workspace-state.json：%w", err)
	}
	var payload struct {
		SelectedID string      `json:"selectedId"`
		Workspaces []Workspace `json:"workspaces"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("解析 workspace-state.json：%w", err)
	}
	state.SelectedID = payload.SelectedID
	state.Workspaces = payload.Workspaces

	// 端口：目录 → 端口
	ports, portErr := readPorts(filepath.Join(dir, "deepworks-port-state.json"))
	if portErr != nil {
		state.Warnings = append(state.Warnings, portErr.Error())
	}
	// 凭据：目录 → 是否存在 token
	tokens, tokenErr := readTokenDirectories(filepath.Join(dir, "deepworks-server-tokens.json"))
	if tokenErr != nil {
		state.Warnings = append(state.Warnings, tokenErr.Error())
	}
	for index := range state.Workspaces {
		workspace := &state.Workspaces[index]
		if port, ok := ports[normalizeDir(workspace.Path)]; ok {
			workspace.APIPort = port
		}
		if tokens[normalizeDir(workspace.Path)] {
			workspace.HasToken = true
		}
	}
	return state, nil
}

// readPorts 读 deepworks-port-state.json 里的 目录 → 端口。
func readPorts(path string) (map[string]int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]int{}, fmt.Errorf("读 deepworks-port-state.json：%w", err)
	}
	var payload struct {
		WorkspacePorts map[string]int `json:"workspacePorts"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return map[string]int{}, fmt.Errorf("解析 deepworks-port-state.json：%w", err)
	}
	ports := make(map[string]int, len(payload.WorkspacePorts))
	for dir, port := range payload.WorkspacePorts {
		ports[normalizeDir(dir)] = port
	}
	return ports, nil
}

// readTokenDirectories 只关心"哪些目录有凭据"，不读 token 内容。
func readTokenDirectories(path string) (map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]bool{}, nil // 没有凭据文件是正常情况，不报错
	}
	var payload struct {
		Workspaces map[string]struct {
			ClientToken string `json:"clientToken"`
			HostToken   string `json:"hostToken"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return map[string]bool{}, fmt.Errorf("解析 deepworks-server-tokens.json：%w", err)
	}
	out := make(map[string]bool, len(payload.Workspaces))
	for dir, tokens := range payload.Workspaces {
		out[normalizeDir(dir)] = tokens.ClientToken != "" || tokens.HostToken != ""
	}
	return out, nil
}

// normalizeDir 统一目录比较口径（大小写与尾斜杠，实测 port-state 里是小写）。
func normalizeDir(path string) string {
	return strings.ToLower(strings.TrimRight(filepath.ToSlash(path), "/"))
}
