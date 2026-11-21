package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/gzzyj/atrace/internal/app"
	"github.com/gzzyj/atrace/internal/cli"
	"github.com/gzzyj/atrace/internal/table"
)

func newWorkspacesCommand(env *app.Env) *cobra.Command {
	var sourceOpts sourceOptions
	var outputOpts outputOptions
	var includeEmpty bool
	var workspaceSource string
	command := &cobra.Command{
		Use:     "workspaces",
		Aliases: []string{"workspace", "ws"},
		Short:   "列出工作区（跨来源 + DeepWorks 应用态叠加）",
		Long: `列出工作区。opencode 的 project 表按 worktree 懒创建，DeepWorks 的"我的工作区"
在 workspace-state.json 里，两者对不上会漏列，因此默认叠加应用态：
  --workspace-source auto       （默认）应用态 + 各来源原生工作区 + 会话目录兜底
  --workspace-source app-state  只看应用态
  --workspace-source native     只看各来源自己的（opencode = project）`,
		GroupID: "discover",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			includeAppState := true
			switch workspaceSource {
			case "", "auto", "app-state":
				includeAppState = true
			case "native":
				includeAppState = false
			default:
				return cli.BadInput(fmt.Errorf("不认识的 --workspace-source %q（可选 auto / app-state / native）", workspaceSource))
			}
			report, err := env.Workspaces(cmd.Context(), sourceOpts.sources, sourceOpts.locator(), includeAppState, includeEmpty)
			if err != nil {
				return wrap(err)
			}
			if outputOpts.asJSON {
				return cli.WriteJSON(cmd.OutOrStdout(), "atrace.workspaces.v1", map[string]any{
					"workspaces": report.Items(), "notes": report.Notes, "problems": report.Problems,
				})
			}
			stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
			if len(report.Rows) == 0 {
				fmt.Fprintln(stderr, "没有可展示的工作区（试试 --all 看空工作区）")
				return nil
			}
			rows := make([][]string, 0, len(report.Rows))
			for _, row := range report.Rows {
				workspace := row.Workspace
				sessions, roots := "-", "-"
				if row.HasCount {
					sessions = strconv.Itoa(workspace.SessionCount)
					roots = strconv.Itoa(workspace.RootCount)
				}
				port := "-"
				if row.Port > 0 {
					port = strconv.Itoa(row.Port)
					if !cli.PortAlive(row.Port) {
						port += "（未运行）"
					}
				}
				directory := workspace.Directory
				if directory == "" {
					directory = "-"
				}
				if row.Missing {
					directory += "（目录不存在）"
				}
				rows = append(rows, []string{
					string(workspace.Source),
					workspace.ID,
					workspace.Name,
					workspace.Kind,
					cli.TruncateDisplay(directory, 42),
					port,
					sessions,
					roots,
					cli.FormatTimePtr(workspace.LastActiveMs),
					workspace.Origin,
				})
			}
			cli.RenderTable(stdout, []table.Column{
				{Header: "SOURCE"},
				{Header: "ID"},
				{Header: "NAME"},
				{Header: "KIND"},
				{Header: "DIRECTORY"},
				{Header: "PORT", Align: table.Right},
				{Header: "SESSIONS", Align: table.Right},
				{Header: "ROOTS", Align: table.Right},
				{Header: "LAST ACTIVE"},
				{Header: "ORIGIN"},
			}, rows)
			fmt.Fprintln(stderr)
			printNotes(stderr, report.Notes)
			printProblems(stderr, report.Problems)
			fmt.Fprint(stderr, "下一步：atrace sessions --workspace <ID 或名字>"+NL)
			return nil
		},
	}
	sourceOpts.add(command)
	outputOpts.add(command)
	command.Flags().BoolVar(&includeEmpty, "all", false, "连没有会话的工作区也列出来")
	command.Flags().StringVar(&workspaceSource, "workspace-source", "auto", "工作区来源：auto（默认）/ app-state（只看应用态）/ native（只看各来源原生）")
	return command
}
