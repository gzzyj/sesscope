package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// linuxAdapter 用 /proc 读进程：不需要额外命令，也不依赖外部工具。
type linuxAdapter struct {
	base
}

func (l *linuxAdapter) Processes(ctx context.Context, prefix string) ([]Process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("读 /proc：%w", err)
	}
	self := os.Getpid()
	out := make([]Process, 0, 8)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == self {
			continue
		}
		process := l.read(pid)
		if process.PID == 0 || !matchProcessName(process.Name, prefix) {
			continue
		}
		out = append(out, process)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out, nil
}

func (l *linuxAdapter) Process(ctx context.Context, pid int) (Process, error) {
	if err := ctx.Err(); err != nil {
		return Process{}, err
	}
	process := l.read(pid)
	if process.PID == 0 {
		return Process{}, fmt.Errorf("进程 %d 不存在或不可读", pid)
	}
	return process, nil
}

func (l *linuxAdapter) read(pid int) Process {
	dir := filepath.Join("/proc", strconv.Itoa(pid))
	raw, err := os.ReadFile(filepath.Join(dir, "cmdline"))
	if err != nil || len(raw) == 0 {
		return Process{}
	}
	parts := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	if len(parts) == 0 || parts[0] == "" {
		return Process{}
	}
	process := Process{
		PID:     pid,
		Name:    stripExe(basename(parts[0])),
		Args:    parts,
		Command: strings.Join(parts, " "),
	}
	if info, err := os.Stat(dir); err == nil {
		process.StartedMs = info.ModTime().UnixMilli()
	}
	process.PPID = readPPID(dir)
	environ, err := readEnviron(dir)
	if err != nil {
		process.EnvironError = "读 /proc 环境变量：" + err.Error()
	} else {
		process.Environ = environ
	}
	return process
}

func readPPID(dir string) int {
	raw, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return 0
	}
	// stat: pid (comm) state ppid ...；comm 里可能有空格和括号，取最后一个右括号之后再切。
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

func readEnviron(dir string) (map[string]string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "environ"))
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, 32)
	for _, entry := range strings.Split(string(raw), "\x00") {
		if index := strings.IndexByte(entry, '='); index > 0 {
			out[entry[:index]] = entry[index+1:]
		}
	}
	return out, nil
}
