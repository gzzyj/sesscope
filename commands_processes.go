package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/zhouyuejie/opencode-trace/internal/source/opencode"
	"github.com/zhouyuejie/opencode-trace/internal/table"
)

func runProcesses(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("processes", flag.ContinueOnError)
	flags.SetOutput(stderr)
	asJSON := flags.Bool("json", false, "输出 JSON")
	flags.Usage = func() {
		fmt.Fprint(stderr, "opencode-trace processes —— 列出本机 opencode 进程与它们各自在用的会话库\n\n用法：\n  opencode-trace processes\n\n参数：\n")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errorsIsHelp(err) {
			return exitOK
		}
		return exitBadInput
	}

	processes, err := opencode.DiscoverProcesses()
	if err != nil {
		fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
		return exitRuntime
	}

	type row struct {
		PID        int
		Name       string
		Port       string
		Started    string
		DBPath     string
		DBExists   bool
		Workspaces int64
		Sessions   int64
		Note       string
	}
	rows := make([]row, 0, len(processes)+2)
	usedDBs := map[string]bool{}
	for _, process := range processes {
		item := row{PID: process.PID, Name: process.Name, Port: process.Port, DBPath: process.DBPath, DBExists: process.DBExists}
		if !process.Started.IsZero() {
			item.Started = formatElapsed(time.Since(process.Started))
		}
		if process.DBExists {
			usedDBs[process.DBPath] = true
			database, err := opencode.Open(process.DBPath)
			if err != nil {
				item.Note = err.Error()
			} else {
				workspaces, sessions, countErr := database.Counts()
				database.Close()
				if countErr != nil {
					item.Note = countErr.Error()
				} else {
					item.Workspaces = int64(workspaces)
					item.Sessions = int64(sessions)
				}
			}
		} else if process.DBPath == "" {
			item.Note = "推不出库位置（没有 XDG_DATA_HOME/HOME）"
		} else {
			item.Note = "库文件不存在"
		}
		if item.Note != "" && item.Note != "没有进程指向它" {
			// 打开失败的原因已经写在 Note 里，不要再显示 0 工作区/0 会话造成误解
		}
		rows = append(rows, item)
	}

	// 没有进程在用的库也列出来，否则用户永远不知道它的存在
	for _, candidate := range opencode.CandidatePaths() {
		if usedDBs[candidate] {
			continue
		}
		info, statErr := statFile(candidate)
		if statErr != nil || info.IsDir() {
			continue
		}
		item := row{PID: 0, Name: "（无进程）", DBPath: candidate, DBExists: true}
		database, openErr := opencode.Open(candidate)
		switch {
		case openErr != nil:
			item.Note = "读不了：" + openErr.Error()
		default:
			workspaces, sessions, countErr := database.Counts()
			database.Close()
			if countErr != nil {
				item.Note = "读不了：" + countErr.Error()
			} else {
				item.Workspaces, item.Sessions = int64(workspaces), int64(sessions)
				item.Note = "没有进程指向它"
			}
		}
		rows = append(rows, item)
	}

	if *asJSON {
		items := make([]map[string]any, 0, len(rows))
		for _, item := range rows {
			entry := map[string]any{
				"pid": item.PID, "name": item.Name, "port": item.Port, "db_path": item.DBPath,
				"db_exists": item.DBExists, "workspaces": item.Workspaces, "sessions": item.Sessions, "note": item.Note,
			}
			items = append(items, entry)
		}
		return writeJSON(stdout, stderr, map[string]any{"processes": items})
	}

	if len(rows) == 0 {
		fmt.Fprintln(stderr, "opencode-trace: 没找到 opencode 进程，也没有已知的会话库")
		return exitOK
	}

	tableRows := make([][]string, 0, len(rows))
	for _, item := range rows {
		pid := "-"
		if item.PID > 0 {
			pid = strconv.Itoa(item.PID)
		}
		dbPath := item.DBPath
		if dbPath == "" {
			dbPath = "-"
		}
		if item.Note != "" {
			dbPath = dbPath + "（" + item.Note + "）"
		}
		port := item.Port
		if port == "" {
			port = "-"
		}
		tableRows = append(tableRows, []string{
			pid,
			item.Name,
			port,
			item.Started,
			strconv.FormatInt(item.Workspaces, 10),
			strconv.FormatInt(item.Sessions, 10),
			shortenHome(dbPath),
		})
	}
	fmt.Fprint(stdout, table.Render([]table.Column{
		{Header: "PID"},
		{Header: "进程"},
		{Header: "端口"},
		{Header: "已运行"},
		{Header: "工作区", Align: table.Right},
		{Header: "会话", Align: table.Right},
		{Header: "会话库"},
	}, tableRows))

	fmt.Fprint(stderr, "\n下一步：用 -pid 或 -db 指定要看的库\n")
	for _, item := range rows {
		if item.PID > 0 && item.Sessions > 0 {
			fmt.Fprintf(stderr, "  opencode-trace workspaces -pid %d\n", item.PID)
			fmt.Fprintf(stderr, "  opencode-trace sessions   -pid %d -limit 20\n", item.PID)
			break
		}
	}
	return exitOK
}

// shortenHome 把 $HOME 换成 ~，表格更短。
func shortenHome(path string) string {
	home, err := homeDir()
	if err != nil || home == "" {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

func formatElapsed(duration time.Duration) string {
	if duration < 0 {
		return "-"
	}
	days := int(duration.Hours()) / 24
	hours := int(duration.Hours()) % 24
	minutes := int(duration.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%d 天 %d 小时", days, hours)
	case hours > 0:
		return fmt.Sprintf("%d 小时 %d 分", hours, minutes)
	default:
		return fmt.Sprintf("%d 分", minutes)
	}
}
