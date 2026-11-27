package platform

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// posixAdapter 用于 macOS 与其它 Unix：进程列表靠 ps。
// 它不读进程环境（macOS 没有 /proc），EnvironError 会写明，--pid 只能按默认库推断。
type posixAdapter struct {
	base
}

const posixEnvironNote = "该平台未实现读取其它进程环境变量；--pid 只能按默认库候选推断"

func (p *posixAdapter) Processes(ctx context.Context, prefix string) ([]Process, error) {
	raw, err := p.Run(ctx, "ps", "-axo", "pid=,ppid=,comm=,args=")
	if err != nil {
		return nil, fmt.Errorf("执行 ps：%w", err)
	}
	self := os.Getpid()
	out := make([]Process, 0, 16)
	for _, line := range strings.Split(string(raw), string(rune(10))) {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 || pid == self {
			continue
		}
		ppid, _ := strconv.Atoi(fields[1])
		name := stripExe(basename(fields[2]))
		if !matchProcessName(name, prefix) {
			continue
		}
		args := fields[2:]
		out = append(out, Process{
			PID: pid, PPID: ppid, Name: name, Args: args,
			Command: strings.Join(args, " "), EnvironError: posixEnvironNote,
		})
	}
	return out, nil
}

func (p *posixAdapter) Process(ctx context.Context, pid int) (Process, error) {
	raw, err := p.Run(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "pid=,ppid=,comm=,args=")
	if err != nil {
		return Process{}, fmt.Errorf("读进程 %d：%w", pid, err)
	}
	for _, line := range strings.Split(string(raw), string(rune(10))) {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		gotPID, err := strconv.Atoi(fields[0])
		if err != nil || gotPID != pid {
			continue
		}
		ppid, _ := strconv.Atoi(fields[1])
		name := stripExe(basename(fields[2]))
		args := fields[2:]
		return Process{
			PID: pid, PPID: ppid, Name: name, Args: args,
			Command: strings.Join(args, " "), EnvironError: posixEnvironNote,
		}, nil
	}
	return Process{}, fmt.Errorf("进程 %d 不存在（或已退出）", pid)
}
