// Package platform 把"这台机器"的差异收进一个适配器。
//
// 上层（source / app / cmd）只依赖 Adapter，不写 runtime.GOOS 分支：
//   - 家目录、配置目录、数据目录（Linux/macOS 的 XDG 与 Windows 的 APPDATA/LOCALAPPDATA）
//   - 列进程、读进程环境（Linux 走 /proc，Windows 走 PowerShell/Get-CimInstance）
//   - 执行命令（Windows 上没有 /proc，进程发现只能靠命令）
//   - 用系统默认程序打开文件
//
// 启动时 Detect() 用 runtime.GOOS 选具体实现；新增一个 OS 只需加一个文件，
// 发现、取数、渲染、归因这些流程一行都不用改。
package platform

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Process 是一个与 OS 无关的进程快照。
//
// Environ 为 nil 表示这个实现读不到目标进程的环境（Windows 没有免特权接口）；
// 调用方必须把它当成"未知"处理（用命令行/默认路径推断），不能当成空环境。
type Process struct {
	PID          int
	PPID         int
	Name         string
	Args         []string
	Command      string
	StartedMs    int64
	Environ      map[string]string
	EnvironError string
	Note         string
}

// Env 读取进程环境变量；第二个返回值表示这个进程的环境是否可读。
func (p Process) Env(key string) (string, bool) {
	if p.Environ == nil {
		return "", false
	}
	value, ok := p.Environ[key]
	return value, ok
}

// Options 是创建适配器时的依赖，测试可以注入假的家目录/环境变量/命令执行器。
type Options struct {
	Home   string
	Getenv func(string) string
	Runner func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Adapter 是"具体机器"的唯一入口。
type Adapter interface {
	// Kind 返回适配器类型：linux / windows / darwin / unix。
	Kind() string
	// HomeDir 返回当前用户家目录。
	HomeDir() (string, error)
	// AppDataDir 返回配置类目录：Linux/macOS 是 XDG_CONFIG_HOME/~/.config，
	// Windows 是 %APPDATA%（Roaming）。
	AppDataDir() string
	// LocalAppDataDir 返回本机数据目录：Linux/macOS 是 XDG_DATA_HOME/~/.local/share，
	// Windows 是 %LOCALAPPDATA%。
	LocalAppDataDir() string
	// XDGDataDir 返回严格按 XDG 规则的数据目录（~/.local/share）。
	// opencode 在 Windows 上仍按 xdg-basedir 落到这里，所以单列出来。
	XDGDataDir() string
	// Env 读取环境变量（测试可注入）。
	Env(key string) string
	// Processes 列出名字以 prefix 开头的进程（prefix 为空表示全部）。
	Processes(ctx context.Context, prefix string) ([]Process, error)
	// Process 读取单个进程；进程不存在时返回错误。
	Process(ctx context.Context, pid int) (Process, error)
	// Run 执行命令并返回 stdout（stderr 会进 error，不静默丢）。
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
	// OpenBrowser 用系统默认程序打开文件/URL。
	OpenBrowser(ctx context.Context, target string) error
	// PrepareConsole 做启动时的控制台准备（Windows 把输出代码页设为 UTF-8）。
	PrepareConsole()
}

// Detect 按当前运行的 OS 选适配器。这是"命令启动时自己探测 OS"的唯一入口。
func Detect() Adapter {
	home, _ := os.UserHomeDir()
	return For(runtime.GOOS, Options{Home: home, Getenv: os.Getenv, Runner: runCommand})
}

// For 返回指定 goos 的适配器（测试与交叉验证用；Detect 只是传 runtime.GOOS）。
func For(goos string, opts Options) Adapter {
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.Runner == nil {
		opts.Runner = runCommand
	}
	if opts.Home == "" {
		if home, err := os.UserHomeDir(); err == nil {
			opts.Home = home
		}
	}
	base := base{kind: normalizeKind(goos), opts: opts}
	switch base.kind {
	case "windows":
		return &windowsAdapter{base: base}
	case "darwin", "unix":
		return &posixAdapter{base: base}
	default:
		return &linuxAdapter{base: base}
	}
}

func normalizeKind(goos string) string {
	lowered := strings.ToLower(strings.TrimSpace(goos))
	switch {
	case strings.HasPrefix(lowered, "windows"), lowered == "win32", lowered == "win64":
		return "windows"
	case lowered == "darwin", lowered == "macos", lowered == "mac":
		return "darwin"
	case lowered == "linux":
		return "linux"
	case lowered == "":
		return "unix"
	default:
		return "unix"
	}
}

type base struct {
	kind string
	opts Options
}

func (b base) Kind() string { return b.kind }

func (b base) HomeDir() (string, error) {
	if b.opts.Home != "" {
		return b.opts.Home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("取用户家目录：%w", err)
	}
	return home, nil
}

func (b base) home() string {
	home, _ := b.HomeDir()
	return home
}

func (b base) Env(key string) string {
	if b.opts.Getenv == nil {
		return ""
	}
	return b.opts.Getenv(key)
}

func (b base) AppDataDir() string {
	if b.kind == "windows" {
		if value := strings.TrimSpace(b.Env("APPDATA")); value != "" {
			return value
		}
		if home := b.home(); home != "" {
			return filepath.Join(home, "AppData", "Roaming")
		}
		return ""
	}
	if value := strings.TrimSpace(b.Env("XDG_CONFIG_HOME")); value != "" {
		return value
	}
	if home := b.home(); home != "" {
		return filepath.Join(home, ".config")
	}
	return ""
}

func (b base) LocalAppDataDir() string {
	if b.kind == "windows" {
		if value := strings.TrimSpace(b.Env("LOCALAPPDATA")); value != "" {
			return value
		}
		if home := b.home(); home != "" {
			return filepath.Join(home, "AppData", "Local")
		}
		return ""
	}
	return b.XDGDataDir()
}

func (b base) XDGDataDir() string {
	if value := strings.TrimSpace(b.Env("XDG_DATA_HOME")); value != "" {
		return value
	}
	if home := b.home(); home != "" {
		return filepath.Join(home, ".local", "share")
	}
	return ""
}

func (b base) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return b.opts.Runner(ctx, name, args...)
}

// OpenBrowser 用系统默认程序打开文件：Windows 走 rundll32，macOS 走 open，
// 其它 Unix 走 xdg-open。命令拉起即返回，不阻塞调用方。
func (b base) OpenBrowser(ctx context.Context, target string) error {
	var (
		name string
		args []string
	)
	switch b.kind {
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", target}
	case "darwin":
		name, args = "open", []string{target}
	default:
		name, args = "xdg-open", []string{target}
	}
	command := exec.Command(name, args...)
	command.Stdout, command.Stderr = nil, nil
	if err := command.Start(); err != nil {
		return fmt.Errorf("打开 %s：%w", target, err)
	}
	go func() { _ = command.Wait() }()
	return nil
}

func (b base) PrepareConsole() {}

// runCommand 执行命令，stdout 原样返回；失败时把 stderr 拼进错误。
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return stdout.Bytes(), fmt.Errorf("执行 %s：%w（%s）", name, err, message)
		}
		return stdout.Bytes(), fmt.Errorf("执行 %s：%w", name, err)
	}
	return stdout.Bytes(), nil
}

// matchProcessName 统一进程名匹配口径：去掉目录与 .exe，忽略大小写。
func matchProcessName(name, prefix string) bool {
	if prefix == "" {
		return true
	}
	return strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix))
}

func stripExe(name string) string {
	if strings.HasSuffix(strings.ToLower(name), ".exe") {
		return name[:len(name)-4]
	}
	return name
}

func basename(name string) string {
	cleaned := strings.ReplaceAll(name, "\\", "/")
	if index := strings.LastIndex(cleaned, "/"); index >= 0 {
		return cleaned[index+1:]
	}
	return cleaned
}

// SplitCommandLine 按 Windows 命令行规则做最小解析（引号、反斜杠转义），
// 只用于展示与提取 --port，不做 shell 语义解释。
func SplitCommandLine(line string) []string {
	args := make([]string, 0, 8)
	var current strings.Builder
	inQuotes := false
	flush := func() {
		if current.Len() > 0 {
			args = append(args, current.String())
			current.Reset()
		}
	}
	for index := 0; index < len(line); index++ {
		ch := line[index]
		switch {
		case ch == '"':
			inQuotes = !inQuotes
		case ch == '\\' && index+1 < len(line) && line[index+1] == '"':
			current.WriteByte('"')
			index++
		case (ch == ' ' || ch == '\t') && !inQuotes:
			flush()
		default:
			current.WriteByte(ch)
		}
	}
	flush()
	return args
}
