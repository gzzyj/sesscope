package main

import (
	"flag"
	"io"
)

// helpFor 打印某个子命令的帮助（等价于 <子命令> -h）。
func helpFor(command string, stdout, stderr io.Writer) int {
	switch command {
	case "processes", "ps", "procs":
		return runProcesses([]string{"-h"}, stdout, stderr)
	case "workspaces", "workspace", "ws":
		return runWorkspaces([]string{"-h"}, stdout, stderr)
	case "sessions", "session", "ls":
		return runSessions([]string{"-h"}, stdout, stderr)
	case "export", "dump":
		return runExport([]string{"-h"}, stdout, stderr)
	case "analyze":
		return runAnalyze([]string{"-h"}, nil, stdout, stderr)
	case "render":
		return runRender([]string{"-h"}, nil, stdout, stderr)
	default:
		printUsage(stdout)
		return exitOK
	}
}

var _ = flag.ErrHelp
