package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/gzzyj/sesscope/internal/app"
	"github.com/gzzyj/sesscope/internal/cli"
	"github.com/gzzyj/sesscope/internal/session"
	"github.com/gzzyj/sesscope/internal/source"
	"github.com/gzzyj/sesscope/internal/table"
)

func newSessionsCommand(env *app.Env) *cobra.Command {
	var sourceOpts sourceOptions
	var outputOpts outputOptions
	var filterOpts filterOptions
	var timeOpts timeOptions
	command := &cobra.Command{
		Use:     "sessions",
		Aliases: []string{"session", "ls"},
		Short:   "列出会话（跨来源；--source 限定）",
		Long: `跨来源列出会话。四源共用同一套过滤/排序语义（--workspace/--grep/--sort/--limit）。

会话 id 只在所属来源内唯一，所以 SESSION 列打成 <source>:<id>，可以直接粘给 export / analyze。
前缀只在源内匹配；同一来源内命中多个会话时列出候选并退出码 2，不猜。
文件型来源（dsh/codex/claude）的 UPDATED 可能取自文件修改时间，表格里打 * 号说明。`,
		GroupID: "discover",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			since, until, err := timeOpts.bounds()
			if err != nil {
				return cli.BadInput(err)
			}
			query := source.Query{
				Workspace:       filterOpts.workspace,
				Grep:            filterOpts.grep,
				SinceMs:         since,
				UntilMs:         until,
				Limit:           filterOpts.limit,
				IncludeChildren: filterOpts.all,
				Sort:            filterOpts.sortBy,
			}
			list, err := env.Sessions(cmd.Context(), sourceOpts.sources, sourceOpts.locator(), query)
			if err != nil {
				return wrap(err)
			}
			if outputOpts.asJSON {
				return cli.WriteJSON(cmd.OutOrStdout(), "sesscope.sessions.v1", list)
			}
			return printSessions(cmd, list, filterOpts)
		},
	}
	sourceOpts.add(command)
	outputOpts.add(command)
	filterOpts.add(command)
	timeOpts.add(command)
	return command
}

func printSessions(cmd *cobra.Command, list *app.SessionList, filterOpts filterOptions) error {
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if len(list.Items) == 0 {
		fmt.Fprintln(stderr, "没有匹配的会话（试试放开 --workspace / --grep，或加 --all）")
		printProblems(stderr, list.Problems)
		return nil
	}
	mtimeMarked := false
	rows := make([][]string, 0, len(list.Items))
	for _, item := range list.Items {
		title := item.Title
		if title == "" {
			title = "（无标题）"
		}
		if item.IsChild {
			title = "└ " + title
		}
		workspace := list.WorkspaceLabel(item)
		if workspace == "" {
			workspace = "-"
		}
		updated := cli.FormatTimePtr(item.UpdatedMs)
		if item.UpdatedKind == "file-mtime" {
			updated += "*"
			mtimeMarked = true
		}
		rows = append(rows, []string{
			string(item.Source),
			sessionSelector(item.Source, item.ID),
			cli.TruncateDisplay(workspace, 16),
			cli.TruncateDisplay(title, 40),
			strconv.Itoa(item.Messages),
			cli.FormatCountPtr(item.Tokens.Input),
			cli.FormatCountPtr(item.Tokens.Output),
			updated,
		})
	}
	cli.RenderTable(stdout, []table.Column{
		{Header: "SOURCE"},
		{Header: "SESSION"},
		{Header: "WORKSPACE"},
		{Header: "TITLE"},
		{Header: "MSG", Align: table.Right},
		{Header: "TOK IN", Align: table.Right},
		{Header: "TOK OUT", Align: table.Right},
		{Header: "UPDATED"},
	}, rows)

	fmt.Fprintln(stderr)
	for _, item := range list.Located {
		fmt.Fprintf(stderr, "库：%s（%s）"+NL, item.Store.Path, item.Store.Format)
	}
	fmt.Fprintf(stderr, "显示 %d 个会话", len(list.Items))
	if !filterOpts.all {
		fmt.Fprint(stderr, "（仅顶层；加 --all 含子 agent 会话）")
	}
	fmt.Fprint(stderr, NL)
	if mtimeMarked {
		fmt.Fprint(stderr, "* UPDATED 取自文件修改时间（该来源不在文件里记录最后活动时间）"+NL)
	}
	printProblems(stderr, list.Problems)
	fmt.Fprint(stderr, "下一步：sesscope export --session <source>:<id> --html trace.html"+NL)
	return nil
}

// sessionSelector 把来源与源内 id 拼成唯一选择器：会话 id 单独拿出来并不唯一。
func sessionSelector(sourceID session.SourceID, id string) string {
	return string(sourceID) + ":" + id
}
