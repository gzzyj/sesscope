package opencode

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Process 是一个正在运行的 opencode 相关进程，以及它能用到的会话库。
type Process struct {
	PID      int
	PPID     int
	Name     string
	Args     []string
	Port     string
	Started  time.Time
	DataHome string
	DBPath   string
	DBExists bool
}

// Description 返回进程的一句话描述（给表格用）。
func (p Process) Description() string {
	if p.Port != "" {
		return p.Name + " · 端口 " + p.Port
	}
	return p.Name
}

// DiscoverProcesses 扫 /proc 找 opencode 进程（排除本工具自己和浏览器）。
func DiscoverProcesses() ([]Process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("读 /proc：%w", err)
	}
	processes := make([]Process, 0, 4)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		process, ok := readProcess(pid)
		if !ok {
			continue
		}
		processes = append(processes, process)
	}
	sort.SliceStable(processes, func(i, j int) bool { return processes[i].PID < processes[j].PID })
	return processes, nil
}

func readProcess(pid int) (Process, bool) {
	rawCmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil || len(rawCmdline) == 0 {
		return Process{}, false
	}
	parts := strings.Split(strings.TrimRight(string(rawCmdline), "\x00"), "\x00")
	if len(parts) == 0 {
		return Process{}, false
	}
	name := filepath.Base(parts[0])
	if !strings.HasPrefix(name, "opencode") || strings.Contains(name, "trace") {
		return Process{}, false
	}
	process := Process{PID: pid, Name: name, Args: parts}
	for index, arg := range parts {
		if arg == "--port" && index+1 < len(parts) {
			process.Port = parts[index+1]
		}
		if strings.HasPrefix(arg, "--port=") {
			process.Port = strings.TrimPrefix(arg, "--port=")
		}
	}
	if info, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); err == nil {
		process.Started = info.ModTime()
	}
	process.PPID = readPPID(pid)
	process.DataHome = readEnvironValue(pid, "XDG_DATA_HOME")
	if process.DataHome == "" {
		if home := readEnvironValue(pid, "HOME"); home != "" {
			process.DataHome = filepath.Join(home, ".local", "share")
		}
	}
	if process.DataHome != "" {
		candidate := filepath.Join(process.DataHome, "opencode", "opencode.db")
		process.DBPath = candidate
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			process.DBExists = true
		}
	}
	return process, true
}

func readPPID(pid int) int {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	// stat: pid (comm) state ppid ...；comm 里可能有空格和括号，取最后一个右括号之后再切
	text := string(raw)
	close := strings.LastIndex(text, ")")
	if close < 0 || close+2 >= len(text) {
		return 0
	}
	fields := strings.Fields(text[close+2:])
	if len(fields) < 2 {
		return 0
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0
	}
	return ppid
}

func readEnvironValue(pid int, key string) string {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
	if err != nil {
		return ""
	}
	prefix := key + "="
	for _, entry := range strings.Split(string(raw), "\x00") {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

// DBPathForPID 解析某个进程实际在用的库路径（不存在也返回路径，调用方据此报错）。
func DBPathForPID(pid int) (string, error) {
	process, ok := readProcess(pid)
	if !ok {
		return "", fmt.Errorf("进程 %d 不是 opencode 进程（或已退出）", pid)
	}
	if process.DBPath == "" {
		return "", fmt.Errorf("进程 %d 没有 XDG_DATA_HOME/HOME，推不出库位置；请用 -db 指定", pid)
	}
	return process.DBPath, nil
}
