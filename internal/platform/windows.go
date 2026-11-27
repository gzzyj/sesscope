package platform

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// windowsAdapter 用 PowerShell/Get-CimInstance 读进程。
//
// Windows 没有 /proc，也没有免特权读取其它进程环境变量的接口；
// 因此 Environ 恒为 nil，EnvironError 会写明原因，调用方按"未知"处理。
type windowsAdapter struct {
	base
}

const windowsEnvironNote = "Windows 不提供免特权读取其它进程环境变量的接口（需要调试权限）；只能按命令行与默认库推断"

type windowsProcessJSON struct {
	PID     json.Number `json:"pid"`
	PPID    json.Number `json:"ppid"`
	Name    string      `json:"name"`
	Command string      `json:"command"`
	Started json.Number `json:"started"`
}

func (w *windowsAdapter) Processes(ctx context.Context, prefix string) ([]Process, error) {
	out, powerErr := w.Run(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", windowsProcessScript(prefix, 0))
	if powerErr == nil {
		processes, parseErr := parseWindowsProcessJSON(out)
		if parseErr == nil {
			return filterSelf(processes, prefix), nil
		}
		powerErr = parseErr
	}
	processes, taskErr := w.tasklistProcesses(ctx, prefix)
	if taskErr != nil {
		return nil, fmt.Errorf("列进程失败：PowerShell：%v；tasklist：%v", powerErr, taskErr)
	}
	note := "PowerShell 读取进程失败（" + powerErr.Error() + "），退回 tasklist：拿不到命令行/端口/启动时间"
	for index := range processes {
		processes[index].Note = note
	}
	return processes, nil
}

func (w *windowsAdapter) Process(ctx context.Context, pid int) (Process, error) {
	if pid <= 0 {
		return Process{}, fmt.Errorf("进程 id 必须大于 0：%d", pid)
	}
	out, powerErr := w.Run(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", windowsProcessScript("", pid))
	if powerErr == nil {
		processes, parseErr := parseWindowsProcessJSON(out)
		if parseErr == nil {
			for _, process := range processes {
				if process.PID == pid {
					return process, nil
				}
			}
			return Process{}, fmt.Errorf("进程 %d 不存在（或已退出）", pid)
		}
		powerErr = parseErr
	}
	processes, taskErr := w.tasklistProcesses(ctx, "")
	if taskErr != nil {
		return Process{}, fmt.Errorf("读进程 %d 失败：PowerShell：%v；tasklist：%v", pid, powerErr, taskErr)
	}
	for _, process := range processes {
		if process.PID != pid {
			continue
		}
		process.Note = "PowerShell 读取进程失败（" + powerErr.Error() + "），退回 tasklist：拿不到命令行/启动时间"
		return process, nil
	}
	return Process{}, fmt.Errorf("进程 %d 不存在（或已退出）", pid)
}

func (w *windowsAdapter) PrepareConsole() { setConsoleUTF8() }

// windowsProcessScript 生成一段自包含的 PowerShell：
// 输出 UTF-8 JSON（数组或单对象），时间统一为 Unix 毫秒。
func windowsProcessScript(prefix string, pid int) string {
	filter := ""
	switch {
	case pid > 0:
		filter = fmt.Sprintf(` -Filter "ProcessId = %d"`, pid)
	case strings.TrimSpace(prefix) != "":
		filter = fmt.Sprintf(` -Filter "Name LIKE '%s%%'"`, escapeWQL(prefix))
	}
	script := "[Console]::OutputEncoding=[System.Text.Encoding]::UTF8; $ErrorActionPreference='Stop'; " +
		"Get-CimInstance Win32_Process" + filter +
		" | ForEach-Object { [pscustomobject]@{pid=[int]$_.ProcessId;ppid=[int]$_.ParentProcessId;name=[string]$_.Name;command=[string]$_.CommandLine;started=[int64]([System.DateTimeOffset]::new([datetime]$_.CreationDate).ToUnixTimeMilliseconds())} }" +
		" | ConvertTo-Json -Compress"
	return script
}

func escapeWQL(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func parseWindowsProcessJSON(raw []byte) ([]Process, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return []Process{}, nil
	}
	items := make([]windowsProcessJSON, 0, 4)
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, fmt.Errorf("解析 PowerShell 进程 JSON：%w", err)
		}
	} else {
		var single windowsProcessJSON
		if err := json.Unmarshal(trimmed, &single); err != nil {
			return nil, fmt.Errorf("解析 PowerShell 进程 JSON：%w", err)
		}
		items = append(items, single)
	}
	out := make([]Process, 0, len(items))
	for _, item := range items {
		name := stripExe(basename(item.Name))
		if name == "" {
			continue
		}
		process := Process{
			PID:          numberToInt(item.PID),
			PPID:         numberToInt(item.PPID),
			Name:         name,
			Command:      strings.TrimSpace(item.Command),
			StartedMs:    numberToInt64(item.Started),
			EnvironError: windowsEnvironNote,
		}
		if process.Command != "" {
			process.Args = SplitCommandLine(process.Command)
		}
		out = append(out, process)
	}
	return out, nil
}

func (w *windowsAdapter) tasklistProcesses(ctx context.Context, prefix string) ([]Process, error) {
	raw, err := w.Run(ctx, "tasklist.exe", "/FO", "CSV", "/NH")
	if err != nil {
		return nil, err
	}
	reader := csv.NewReader(bytes.NewReader(raw))
	reader.FieldsPerRecord = -1
	out := make([]Process, 0, 16)
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("解析 tasklist CSV：%w", err)
		}
		if len(record) < 2 {
			continue
		}
		name := stripExe(basename(record[0]))
		if !matchProcessName(name, prefix) {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(record[1]))
		if err != nil || pid <= 0 || pid == os.Getpid() {
			continue
		}
		out = append(out, Process{PID: pid, Name: name, EnvironError: windowsEnvironNote})
	}
	return out, nil
}

func filterSelf(processes []Process, prefix string) []Process {
	self := os.Getpid()
	out := make([]Process, 0, len(processes))
	for _, process := range processes {
		if process.PID == self || process.PID <= 0 {
			continue
		}
		if !matchProcessName(process.Name, prefix) {
			continue
		}
		out = append(out, process)
	}
	return out
}

func numberToInt(value json.Number) int {
	parsed, err := strconv.Atoi(string(value))
	if err != nil {
		return 0
	}
	return parsed
}

func numberToInt64(value json.Number) int64 {
	parsed, err := strconv.ParseInt(string(value), 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}
