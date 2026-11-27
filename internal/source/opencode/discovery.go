package opencode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gzzyj/sesscope/internal/platform"
)

// pathCandidate 是自动发现时的一个候选库及其命中规则（给 sources --explain 看）。
type pathCandidate struct {
	Path string
	Rule string
}

// discoveryCandidates 按优先级列出各平台可能的 opencode 库：
//
//	Windows:
//	  %APPDATA%\com.deepexi.deepworks\deepworks-engine-data\xdg\data\opencode\opencode.db  (DeepWorks sidecar)
//	  %APPDATA%\opencode\opencode.db                                                     (部分独立安装)
//	  %LOCALAPPDATA%\opencode\opencode.db                                                (env-paths 版本)
//	  %USERPROFILE%\.local\share\opencode\opencode.db                                    (opencode 的 xdg-basedir)
//	  %USERPROFILE%\.opencode\opencode.db                                                (旧版)
//	Linux/macOS:
//	  ~/.config/com.deepexi.deepworks/deepworks-engine-data/xdg/data/opencode/opencode.db
//	  ~/.local/share/opencode/opencode.db
//	  ~/.opencode/opencode.db
//
// 环境变量 OPENCODE_TRACE_DB / OPENCODE_DB 优先级最高。
func (a *Adapter) discoveryCandidates() []pathCandidate {
	out := make([]pathCandidate, 0, 8)
	seen := make(map[string]bool, 8)
	add := func(path, rule string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		key := filepath.Clean(path)
		if a.platform.Kind() == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, pathCandidate{Path: path, Rule: rule})
	}

	if value := strings.TrimSpace(a.platform.Env("OPENCODE_TRACE_DB")); value != "" {
		add(value, "l1:env OPENCODE_TRACE_DB")
	}
	if value := strings.TrimSpace(a.platform.Env("OPENCODE_DB")); value != "" {
		add(value, "l1:env OPENCODE_DB")
	}
	if dir := strings.TrimSpace(a.platform.AppDataDir()); dir != "" {
		add(filepath.Join(dir, "com.deepexi.deepworks", "deepworks-engine-data", "xdg", "data", "opencode", "opencode.db"),
			"l2:deepworks-sidecar")
		if a.platform.Kind() == "windows" {
			add(filepath.Join(dir, "opencode", "opencode.db"), "l2:opencode-roaming")
		}
	}
	if dir := strings.TrimSpace(a.platform.LocalAppDataDir()); dir != "" {
		rule := "l2:opencode-default"
		if a.platform.Kind() == "windows" {
			rule = "l2:opencode-localappdata"
		}
		add(filepath.Join(dir, "opencode", "opencode.db"), rule)
	}
	if dir := strings.TrimSpace(a.platform.XDGDataDir()); dir != "" {
		add(filepath.Join(dir, "opencode", "opencode.db"), "l2:opencode-xdg")
	}
	if home, err := a.platform.HomeDir(); err == nil && home != "" {
		add(filepath.Join(home, ".opencode", "opencode.db"), "l2:opencode-legacy")
	}
	return out
}

func (a *Adapter) existingCandidates() []pathCandidate {
	out := make([]pathCandidate, 0, 4)
	for _, candidate := range a.discoveryCandidates() {
		info, err := os.Stat(candidate.Path)
		if err == nil && !info.IsDir() {
			out = append(out, candidate)
		}
	}
	return out
}

// readableCandidates 只保留能按 v1 schema 读的库；Windows 上用来在
// "读不到进程环境" 时挑一个最可信的默认库。
func (a *Adapter) readableCandidates() []pathCandidate {
	out := make([]pathCandidate, 0, 4)
	for _, candidate := range a.existingCandidates() {
		version, _, err := detectVersion(candidate.Path)
		if err == nil && version == "v1" {
			out = append(out, candidate)
		}
	}
	return out
}

// storeForPID 给 --pid 推库：能读进程环境就按环境推；读不到（Windows）
// 只在默认库唯一/唯一可读时推断，否则报错让用户用 --store 指定，绝不瞎猜。
func (a *Adapter) storeForPID(ctx context.Context, pid int) (string, string, error) {
	process, err := a.platform.Process(ctx, pid)
	if err != nil {
		return "", "", fmt.Errorf("读进程 %d：%w", pid, err)
	}
	if !isOpencodeProcessName(process.Name) {
		return "", "", fmt.Errorf("进程 %d 不是 opencode 进程（%s）", pid, process.Name)
	}
	if dataHome, ok := dataHomeOf(process); ok && dataHome != "" {
		return filepath.Join(dataHome, "opencode", "opencode.db"), "l3:pid " + itoa(pid), nil
	}
	if preferred, ok := a.preferredCandidate(process); ok {
		return preferred.Path, fmt.Sprintf("l3:pid %d（按命令行里的程序路径推断）", pid), nil
	}
	readable := a.readableCandidates()
	if len(readable) == 1 {
		return readable[0].Path, fmt.Sprintf("l3:pid %d（读不到进程环境，按唯一可读默认库）", pid), nil
	}
	existing := a.existingCandidates()
	if len(existing) == 1 {
		return existing[0].Path, fmt.Sprintf("l3:pid %d（读不到进程环境，按唯一默认库）", pid), nil
	}
	reason := process.EnvironError
	if reason == "" {
		reason = "读不到进程环境变量"
	}
	if len(existing) == 0 {
		return "", "", fmt.Errorf("进程 %d 在跑，但%s；也没有找到默认库，用 --store 指定", pid, reason)
	}
	paths := make([]string, 0, len(existing))
	for _, candidate := range existing {
		paths = append(paths, candidate.Path)
	}
	sort.Strings(paths)
	return "", "", fmt.Errorf("进程 %d 在跑，但%s；候选库有 %d 个：\n  %s\n用 --store 指定",
		pid, reason, len(paths), strings.Join(paths, "\n  "))
}

// storeForProcess 给 sources 的 LIVE 列表关联库（只作提示）。
func (a *Adapter) storeForProcess(process platform.Process) (string, bool, string) {
	if dataHome, ok := dataHomeOf(process); ok && dataHome != "" {
		path := filepath.Join(dataHome, "opencode", "opencode.db")
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, true, ""
		}
		return path, false, "进程环境指向的库不存在"
	}
	if preferred, ok := a.preferredCandidate(process); ok {
		return preferred.Path, true, "按命令行里的程序路径推断"
	}
	readable := a.readableCandidates()
	if len(readable) > 0 {
		note := ""
		if len(readable) > 1 {
			note = fmt.Sprintf("有 %d 个可读默认库，按第一个展示", len(readable))
		}
		return readable[0].Path, true, note
	}
	existing := a.existingCandidates()
	if len(existing) > 0 {
		note := ""
		if len(existing) > 1 {
			note = fmt.Sprintf("有 %d 个默认库，按第一个展示", len(existing))
		}
		return existing[0].Path, true, note
	}
	return "", false, "没有找到默认库"
}

// preferredCandidate 用命令行里的可执行文件路径推断库：
// DeepWorks 的 sidecar 在 ...\Programs\DeepWorks\resources\sidecars\ 下，
// 独立 opencode 有自己的安装目录。Windows 读不到环境变量时，这是唯一的
// 强证据；只有文件真实存在才采用，否则退回"默认库唯一"的规则。
func (a *Adapter) preferredCandidate(process platform.Process) (pathCandidate, bool) {
	executable := process.Command
	if len(process.Args) > 0 {
		executable = process.Args[0]
	}
	executable = strings.ToLower(strings.TrimSpace(executable))
	if executable == "" {
		return pathCandidate{}, false
	}
	wantDeepWorks := strings.Contains(executable, "deepworks")
	for _, candidate := range a.discoveryCandidates() {
		isDeepWorks := candidate.Rule == "l2:deepworks-sidecar"
		if wantDeepWorks != isDeepWorks {
			continue
		}
		if info, err := os.Stat(candidate.Path); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return pathCandidate{}, false
}

func dataHomeOf(process platform.Process) (string, bool) {
	if process.Environ == nil {
		return "", false
	}
	if value := strings.TrimSpace(process.Environ["XDG_DATA_HOME"]); value != "" {
		return value, true
	}
	for _, key := range []string{"HOME", "USERPROFILE"} {
		if value := strings.TrimSpace(process.Environ[key]); value != "" {
			return filepath.Join(value, ".local", "share"), true
		}
	}
	return "", false
}

func isOpencodeProcessName(name string) bool {
	lowered := strings.ToLower(name)
	return strings.HasPrefix(lowered, "opencode") && !strings.Contains(lowered, "trace")
}

func portOf(args []string) string {
	for index, arg := range args {
		if arg == "--port" && index+1 < len(args) {
			return args[index+1]
		}
		if strings.HasPrefix(arg, "--port=") {
			return strings.TrimPrefix(arg, "--port=")
		}
	}
	return ""
}
