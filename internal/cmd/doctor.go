package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/gzzyj/sesscope/internal/app"
	"github.com/gzzyj/sesscope/internal/cli"
	"github.com/gzzyj/sesscope/internal/table"
)

func newDoctorCommand(env *app.Env) *cobra.Command {
	var sourceOpts sourceOptions
	var outputOpts outputOptions
	var drift bool
	command := &cobra.Command{
		Use:     "doctor",
		Short:   "逐存储体检：路径、权限、格式版本、缺能力、未知记录类型",
		GroupID: "discover",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rows, problems, err := env.Doctor(cmd.Context(), sourceOpts.sources, sourceOpts.locator(), drift)
			if err != nil {
				return wrap(err)
			}
			if outputOpts.asJSON {
				return cli.WriteJSON(cmd.OutOrStdout(), "sesscope.doctor.v1", map[string]any{
					"stores": rows, "problems": problems,
				})
			}
			stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
			if len(rows) == 0 {
				fmt.Fprintln(stderr, "没有找到任何存储。")
				return nil
			}
			tableRows := make([][]string, 0, len(rows)*3)
			worst := "ok"
			for _, row := range rows {
				for _, check := range row.Checks {
					if check.Level == "error" || (check.Level == "warn" && worst == "ok") {
						worst = check.Level
					}
					tableRows = append(tableRows, []string{
						string(row.Source), check.Level, check.Code, check.Title, cli.TruncateDisplay(check.Detail, 60),
					})
				}
				if len(row.Checks) == 0 {
					tableRows = append(tableRows, []string{string(row.Source), "info", "-", "没有体检项", cli.ShortenPath(row.Store.Path)})
				}
			}
			cli.RenderTable(stdout, []table.Column{
				{Header: "SOURCE"}, {Header: "LEVEL"}, {Header: "CODE"}, {Header: "TITLE"}, {Header: "DETAIL"},
			}, tableRows)
			printProblems(stderr, problems)
			fmt.Fprintf(stderr, NL+"体检结论：%s"+NL, worst)
			return nil
		},
	}
	sourceOpts.add(command)
	outputOpts.add(command)
	command.Flags().BoolVar(&drift, "drift", false, "抽样打开会话，报告代码不认识的记录/事件类型")
	return command
}
