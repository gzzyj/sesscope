package platform

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestDetectKind(t *testing.T) {
	got := Detect().Kind()
	switch runtime.GOOS {
	case "linux":
		if got != "linux" {
			t.Errorf("Detect().Kind() = %q，期望 linux", got)
		}
	case "windows":
		if got != "windows" {
			t.Errorf("Detect().Kind() = %q，期望 windows", got)
		}
	case "darwin":
		if got != "darwin" {
			t.Errorf("Detect().Kind() = %q，期望 darwin", got)
		}
	}
}

func TestWindowsDirs(t *testing.T) {
	home := filepath.Join("C:", "Users", "fastagi")
	appData := filepath.Join("C:", "Users", "fastagi", "AppData", "Roaming")
	localAppData := filepath.Join("C:", "Users", "fastagi", "AppData", "Local")
	env := map[string]string{"APPDATA": appData, "LOCALAPPDATA": localAppData}
	adapter := For("windows", Options{Home: home, Getenv: func(key string) string { return env[key] }})
	if got := adapter.AppDataDir(); got != appData {
		t.Errorf("AppDataDir = %q，期望 %q", got, appData)
	}
	if got := adapter.LocalAppDataDir(); got != localAppData {
		t.Errorf("LocalAppDataDir = %q，期望 %q", got, localAppData)
	}
	if got := adapter.XDGDataDir(); got != filepath.Join(home, ".local", "share") {
		t.Errorf("XDGDataDir = %q，期望 ~/.local/share", got)
	}

	// 环境变量缺失时按家目录兜底，而不是返回空串/相对路径。
	fallback := For("windows", Options{Home: home, Getenv: func(string) string { return "" }})
	if got := fallback.AppDataDir(); got != filepath.Join(home, "AppData", "Roaming") {
		t.Errorf("APPDATA 缺失时 AppDataDir = %q", got)
	}
	if got := fallback.LocalAppDataDir(); got != filepath.Join(home, "AppData", "Local") {
		t.Errorf("LOCALAPPDATA 缺失时 LocalAppDataDir = %q", got)
	}
}

func TestXDGDirs(t *testing.T) {
	home := filepath.Join("/home", "demo")
	env := map[string]string{"XDG_CONFIG_HOME": "/cfg", "XDG_DATA_HOME": "/data"}
	adapter := For("linux", Options{Home: home, Getenv: func(key string) string { return env[key] }})
	if got := adapter.AppDataDir(); got != "/cfg" {
		t.Errorf("AppDataDir = %q，期望 /cfg", got)
	}
	if got := adapter.LocalAppDataDir(); got != "/data" {
		t.Errorf("LocalAppDataDir = %q，期望 /data", got)
	}
	if got := adapter.XDGDataDir(); got != "/data" {
		t.Errorf("XDGDataDir = %q，期望 /data", got)
	}
}

func TestSplitCommandLine(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{`"C:Program Filesopencodeopencode.exe" --port 4096`, []string{`C:Program Filesopencodeopencode.exe`, "--port", "4096"}},
		{`opencode.exe --port="4096"`, []string{"opencode.exe", "--port=4096"}},
		{`opencode.exe --title "hello world"`, []string{"opencode.exe", "--title", "hello world"}},
		{`opencode.exe --empty ""`, []string{"opencode.exe", "--empty"}},
	}
	for _, item := range cases {
		if got := SplitCommandLine(item.line); !reflect.DeepEqual(got, item.want) {
			t.Errorf("SplitCommandLine(%q) = %#v，期望 %#v", item.line, got, item.want)
		}
	}
}

func TestParseWindowsProcessJSON(t *testing.T) {
	array := []byte(`[{"pid":42,"ppid":1,"name":"opencode.exe","command":"opencode.exe --port 4096","started":1700000000000},{"pid":43,"ppid":42,"name":"tasklist.exe","command":null,"started":null}]`)
	processes, err := parseWindowsProcessJSON(array)
	if err != nil {
		t.Fatalf("解析数组：%v", err)
	}
	if len(processes) != 2 {
		t.Fatalf("进程数 = %d，期望 2", len(processes))
	}
	if processes[0].PID != 42 || processes[0].PPID != 1 || processes[0].Name != "opencode" {
		t.Errorf("第一个进程解析不对：%+v", processes[0])
	}
	if processes[0].StartedMs != 1700000000000 {
		t.Errorf("StartedMs = %d", processes[0].StartedMs)
	}
	if len(processes[0].Args) != 3 || processes[0].Args[2] != "4096" {
		t.Errorf("Args = %#v", processes[0].Args)
	}
	if processes[0].Environ != nil || processes[0].EnvironError == "" {
		t.Error("Windows 进程环境必须显式标成不可读")
	}

	single := []byte(`{"pid":7,"ppid":1,"name":"OpenCode.EXE","command":"","started":0}`)
	processes, err = parseWindowsProcessJSON(single)
	if err != nil {
		t.Fatalf("解析单对象：%v", err)
	}
	if len(processes) != 1 || processes[0].Name != "OpenCode" {
		t.Errorf("单对象解析不对：%+v", processes)
	}

	processes, err = parseWindowsProcessJSON([]byte("  "))
	if err != nil || len(processes) != 0 {
		t.Errorf("空输出应得到 0 个进程，实际：%+v %v", processes, err)
	}
}

func TestWindowsProcessScript(t *testing.T) {
	script := windowsProcessScript("opencode", 0)
	if !strings.Contains(script, "Name LIKE 'opencode%'") {
		t.Errorf("脚本缺少名称过滤：%s", script)
	}
	if !strings.Contains(script, "ConvertTo-Json") || !strings.Contains(script, "UTF8") {
		t.Errorf("脚本缺少 JSON/UTF-8 设置：%s", script)
	}
	byPID := windowsProcessScript("", 42)
	if !strings.Contains(byPID, "ProcessId = 42") {
		t.Errorf("脚本缺少 PID 过滤：%s", byPID)
	}
}

func TestLinuxProcessSelf(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("只在 Linux 上验证 /proc 实现")
	}
	adapter := Detect()
	process, err := adapter.Process(context.Background(), os.Getpid())
	if err != nil {
		t.Fatalf("读自身进程：%v", err)
	}
	if process.Name == "" || process.PID != os.Getpid() {
		t.Fatalf("自身进程信息不完整：%+v", process)
	}
	if process.Environ == nil {
		t.Fatalf("Linux 应能读到自身环境变量：%s", process.EnvironError)
	}
	if wanted := os.Getenv("HOME"); wanted != "" {
		if got, ok := process.Env("HOME"); !ok || got != wanted {
			t.Errorf("HOME = %q（ok=%t），期望 %q", got, ok, wanted)
		}
	}
	processes, err := adapter.Processes(context.Background(), "")
	if err != nil {
		t.Fatalf("列进程：%v", err)
	}
	for _, item := range processes {
		if item.PID == os.Getpid() {
			t.Fatal("不应把自己列进进程表")
		}
		if item.PID <= 0 || item.Name == "" {
			t.Errorf("进程信息不完整：%+v", item)
		}
	}
}
