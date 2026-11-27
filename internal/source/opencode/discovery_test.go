package opencode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gzzyj/sesscope/internal/platform"
	"github.com/gzzyj/sesscope/internal/source"
	"github.com/gzzyj/sesscope/internal/testfixture"
)

// windowsTestPlatform 用临时目录模拟 Windows 的 APPDATA/LOCALAPPDATA，
// 这样路径发现在 Linux 上也能被单测覆盖（不依赖真的 Windows 机器）。
func windowsTestPlatform(t *testing.T) (platform.Adapter, string, string, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "Users", "fastagi")
	appData := filepath.Join(root, "AppData", "Roaming")
	localAppData := filepath.Join(root, "AppData", "Local")
	for _, dir := range []string{home, appData, localAppData} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("建目录 %s：%v", dir, err)
		}
	}
	env := map[string]string{"APPDATA": appData, "LOCALAPPDATA": localAppData}
	adapter := platform.For("windows", platform.Options{
		Home:   home,
		Getenv: func(key string) string { return env[key] },
	})
	return adapter, home, appData, localAppData
}

func TestWindowsDiscoveryCandidates(t *testing.T) {
	adapter, home, appData, localAppData := windowsTestPlatform(t)
	candidates := New(adapter).discoveryCandidates()
	want := []pathCandidate{
		{filepath.Join(appData, "com.deepexi.deepworks", "deepworks-engine-data", "xdg", "data", "opencode", "opencode.db"), "l2:deepworks-sidecar"},
		{filepath.Join(appData, "opencode", "opencode.db"), "l2:opencode-roaming"},
		{filepath.Join(localAppData, "opencode", "opencode.db"), "l2:opencode-localappdata"},
		{filepath.Join(home, ".local", "share", "opencode", "opencode.db"), "l2:opencode-xdg"},
		{filepath.Join(home, ".opencode", "opencode.db"), "l2:opencode-legacy"},
	}
	for _, item := range want {
		found := false
		for _, got := range candidates {
			if got.Path == item.Path && got.Rule == item.Rule {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("缺少候选 %s（%s），实际：%+v", item.Path, item.Rule, candidates)
		}
	}
}

func TestWindowsLocateFindsLocalAppData(t *testing.T) {
	adapter, _, _, localAppData := windowsTestPlatform(t)
	dir := filepath.Join(localAppData, "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录：%v", err)
	}
	path, err := testfixture.Build(dir)
	if err != nil {
		t.Fatalf("建 fixture：%v", err)
	}
	stores, err := New(adapter).Locate(context.Background(), source.Locator{})
	if err != nil {
		t.Fatalf("Locate：%v", err)
	}
	for _, store := range stores {
		if store.Path != path {
			continue
		}
		if store.Rule != "l2:opencode-localappdata" {
			t.Errorf("规则 = %q，期望 l2:opencode-localappdata", store.Rule)
		}
		if !store.Readable || store.Version != "v1" {
			t.Errorf("store 应可读且为 v1：%+v", store)
		}
		return
	}
	t.Fatalf("没有定位到 %s：%+v", path, stores)
}

func TestWindowsProcessStoreInference(t *testing.T) {
	adapter, _, _, localAppData := windowsTestPlatform(t)
	dir := filepath.Join(localAppData, "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录：%v", err)
	}
	path, err := testfixture.Build(dir)
	if err != nil {
		t.Fatalf("建 fixture：%v", err)
	}
	process := platform.Process{
		PID: 4242, Name: "opencode", Args: []string{"opencode.exe", "--port", "4099"},
		EnvironError: "Windows 读不到进程环境",
	}
	fake := &fakePlatform{Adapter: adapter, processes: []platform.Process{process}}
	processes, err := New(fake).Processes(context.Background())
	if err != nil {
		t.Fatalf("Processes：%v", err)
	}
	if len(processes) != 1 {
		t.Fatalf("进程数 = %d，期望 1：%+v", len(processes), processes)
	}
	got := processes[0]
	if got.Port != "4099" {
		t.Errorf("端口 = %q，期望 4099", got.Port)
	}
	if got.Store != path || !got.StoreExists {
		t.Errorf("库 = %q（存在=%t），期望 %q", got.Store, got.StoreExists, path)
	}
	if got.Note == "" {
		t.Error("读不到进程环境时必须在 Note 里说明，不能静默")
	}
}

func TestWindowsPIDUsesSingleReadableCandidate(t *testing.T) {
	adapter, _, _, localAppData := windowsTestPlatform(t)
	dir := filepath.Join(localAppData, "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录：%v", err)
	}
	path, err := testfixture.Build(dir)
	if err != nil {
		t.Fatalf("建 fixture：%v", err)
	}
	process := platform.Process{PID: 4243, Name: "opencode", EnvironError: "Windows 读不到进程环境"}
	fake := &fakePlatform{Adapter: adapter, processes: []platform.Process{process}}
	stores, err := New(fake).Locate(context.Background(), source.Locator{PID: 4243})
	if err != nil {
		t.Fatalf("Locate --pid：%v", err)
	}
	if len(stores) != 1 || stores[0].Path != path {
		t.Fatalf("--pid 定位 = %+v，期望 %s", stores, path)
	}
	if stores[0].Rule == "" {
		t.Error("--pid 的推断规则必须写清楚")
	}
}

func TestWindowsProcessPrefersDeepWorksSidecar(t *testing.T) {
	adapter, _, appData, localAppData := windowsTestPlatform(t)
	deepDir := filepath.Join(appData, "com.deepexi.deepworks", "deepworks-engine-data", "xdg", "data", "opencode")
	if err := os.MkdirAll(deepDir, 0o755); err != nil {
		t.Fatalf("建 DeepWorks 目录：%v", err)
	}
	deepPath, err := testfixture.Build(deepDir)
	if err != nil {
		t.Fatalf("建 DeepWorks fixture：%v", err)
	}
	localDir := filepath.Join(localAppData, "opencode")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatalf("建 local 目录：%v", err)
	}
	localPath, err := testfixture.Build(localDir)
	if err != nil {
		t.Fatalf("建 local fixture：%v", err)
	}

	deepProcess := platform.Process{
		PID: 6001, Name: "opencode-x86_64-pc-windows-msvc",
		Command:      `C:\Users\fastagi\AppData\Local\Programs\DeepWorks\resources\sidecars\opencode-x86_64-pc-windows-msvc.exe serve --port 49951`,
		Args:         []string{`C:\Users\fastagi\AppData\Local\Programs\DeepWorks\resources\sidecars\opencode-x86_64-pc-windows-msvc.exe`, "serve", "--port", "49951"},
		EnvironError: "Windows 读不到进程环境",
	}
	fake := &fakePlatform{Adapter: adapter, processes: []platform.Process{deepProcess}}
	processes, err := New(fake).Processes(context.Background())
	if err != nil {
		t.Fatalf("Processes：%v", err)
	}
	if len(processes) != 1 || processes[0].Store != deepPath {
		t.Fatalf("应选 DeepWorks sidecar 库 %s，实际：%+v", deepPath, processes)
	}
	stores, err := New(fake).Locate(context.Background(), source.Locator{PID: 6001})
	if err != nil {
		t.Fatalf("Locate --pid：%v", err)
	}
	if len(stores) != 1 || stores[0].Path != deepPath {
		t.Fatalf("--pid 应选 DeepWorks 库 %s，实际：%+v", deepPath, stores)
	}
	if !strings.Contains(stores[0].Rule, "命令行") {
		t.Errorf("--pid 的规则应说明按命令行推断：%q", stores[0].Rule)
	}

	// 独立 opencode 进程没有 DeepWorks 字样：应优先本地安装的库，而不是 sidecar。
	localProcess := platform.Process{
		PID: 6002, Name: "opencode",
		Command:      `C:\Users\fastagi\opencode.exe --port 4099`,
		Args:         []string{`C:\Users\fastagi\opencode.exe`, "--port", "4099"},
		EnvironError: "Windows 读不到进程环境",
	}
	fake = &fakePlatform{Adapter: adapter, processes: []platform.Process{localProcess}}
	processes, err = New(fake).Processes(context.Background())
	if err != nil {
		t.Fatalf("Processes：%v", err)
	}
	if len(processes) != 1 || processes[0].Store != localPath {
		t.Fatalf("独立进程应选本地库 %s，实际：%+v", localPath, processes)
	}
}

type fakePlatform struct {
	platform.Adapter
	processes []platform.Process
}

func (f *fakePlatform) Processes(ctx context.Context, prefix string) ([]platform.Process, error) {
	return f.processes, nil
}

func (f *fakePlatform) Process(ctx context.Context, pid int) (platform.Process, error) {
	for _, process := range f.processes {
		if process.PID == pid {
			return process, nil
		}
	}
	return platform.Process{}, fmt.Errorf("进程 %d 不存在", pid)
}
