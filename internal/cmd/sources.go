package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/gzzyj/sesscope/internal/app"
	"github.com/gzzyj/sesscope/internal/cli"
	"github.com/gzzyj/sesscope/internal/table"
)

func newSourcesCommand(env *app.Env) *cobra.Command {
	var sourceOpts sourceOptions
	var outputOpts outputOptions
	var explain bool
	command := &cobra.Command{
		Use:     "sources",
		Aliases: []string{"ps", "procs", "processes"},
		Short:   "列出可用的来源与存储（状态/版本/会话数/运行中进程）",
		Long: `列出每个来源在本机找到的存储，以及它能不能读：
  STATUS  ok / unsupported / unreadable / missing
  FORMAT  opencode/sqlite:v1、dsh/jsonl:v4 之类
  LIVE    只作提示：进程存在不代表存储可读，反之亦然

用 --explain 打印"为什么选了这个存储"（命中的发现规则与证据）。`,
		GroupID: "discover",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := env.Status(cmd.Context(), sourceOpts.sources, sourceOpts.locator())
			if err != nil {
				return wrap(err)
			}
			if outputOpts.asJSON {
				return cli.WriteJSON(cmd.OutOrStdout(), "sesscope.sources.v1", report)
			}
			return printStatuses(cmd, report, explain)
		},
	}
	sourceOpts.add(command)
	outputOpts.add(command)
	command.Flags().BoolVar(&explain, "explain", false, "打印每个存储命中的发现规则与证据")
	return command
}

func printStatuses(cmd *cobra.Command, report *app.StatusReport, explain bool) error {
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if len(report.Rows) == 0 {
		fmt.Fprintln(stderr, "没有找到任何来源。用 --store 指定存储，或 --source 限定来源。")
		return nil
	}
	rows := make([][]string, 0, len(report.Rows))
	for _, row := range report.Rows {
		status := "ok"
		switch {
		case !row.Store.Exists:
			status = "missing"
		case !row.Store.Readable:
			if row.Store.Version != "" {
				status = "unsupported"
			} else {
				status = "unreadable"
			}
		}
		format := row.Store.Format
		if row.Store.Version != "" {
			format += ":" + row.Store.Version
		}
		sessions := "-"
		if row.CountKnown {
			sessions = strconv.Itoa(row.SessionCount)
		}
		live := "-"
		for _, process := range row.Processes {
			if process.Port != "" {
				live = "pid " + strconv.Itoa(process.PID) + " :" + process.Port
			} else {
				live = "pid " + strconv.Itoa(process.PID)
			}
			break
		}
		note := row.Store.Detail
		if row.Note != "" {
			note = row.Note
		}
		rows = append(rows, []string{
			string(row.Source), status, format, cli.ShortenPath(row.Store.Path), sessions, live, cli.TruncateDisplay(note, 48),
		})
	}
	cli.RenderTable(stdout, []table.Column{
		{Header: "SOURCE"},
		{Header: "STATUS"},
		{Header: "FORMAT"},
		{Header: "STORE"},
		{Header: "SESSIONS", Align: table.Right},
		{Header: "LIVE"},
		{Header: "NOTE"},
	}, rows)
	if explain {
		fmt.Fprintln(stderr)
		for _, row := range report.Rows {
			fmt.Fprintf(stderr, "%s  %s"+NL, row.Source, row.Store.Path)
			fmt.Fprintf(stderr, "  规则：%s"+NL, row.Store.Rule)
			if row.Store.Evidence != "" {
				fmt.Fprintf(stderr, "  证据：%s"+NL, row.Store.Evidence)
			}
			for _, process := range row.Processes {
				fmt.Fprintf(stderr, "  进程：pid %d %s %s"+NL, process.PID, process.Name, process.Port)
			}
		}
	}
	printProblems(stderr, report.Problems)
	fmt.Fprintf(stderr, NL+"下一步：sesscope sessions --limit 20"+NL)
	return nil
}
