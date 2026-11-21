package cmd

import (
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/gzzyj/sesscope/internal/app"
	"github.com/gzzyj/sesscope/internal/cli"
	"github.com/gzzyj/sesscope/internal/table"
)

func newAuditCommand(env *app.Env) *cobra.Command {
	var sourceOpts sourceOptions
	var limit int
	var asJSON bool
	command := &cobra.Command{
		Use:   "audit",
		Short: "字段门禁：检查抽象会话里该有却没有的字段",
		Long: `逐个打开会话，按 domain model 检查必填字段，并按"来源 + 字段"聚合。

不算空缺的情况（避免误报）：
  - 来源的能力位声明没有这项数据（例如 dsh 不写 reasoning token）；
  - 来源自己写了诊断解释（例如 codex 思考加密 → encrypted-reasoning）；
  - 工具还在运行中（没有结束时间、没有输出是正常的）。

有缺口时退出码 1，可以直接放进 CI / make check 当门禁。`,
		GroupID: "meta",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
			report, err := env.Audit(cmd.Context(), sourceOpts.sources, sourceOpts.locator(), app.AuditOptions{LimitPerSource: limit})
			if err != nil {
				return wrap(err)
			}
			if asJSON {
				if err := cli.WriteJSON(stdout, "sesscope.audit.v1", report); err != nil {
					return cli.RuntimeFailure(err)
				}
			} else {
				printAudit(stdout, stderr, report, limit)
			}
			if report.Violations > 0 {
				return cli.RuntimeFailure(fmt.Errorf("字段门禁未通过：%d 处必需字段空缺（见上表，--json 拿机读结果）", report.Violations))
			}
			return nil
		},
	}
	sourceOpts.add(command)
	command.Flags().IntVarP(&limit, "limit", "n", 10, "每个来源最多审计多少个会话（按 updated 倒序；0 表示全部）")
	command.Flags().BoolVarP(&asJSON, "json", "j", false, "输出 JSON（机器可读，带 scanned/totals/issues）")
	return command
}

func printAudit(stdout, stderr io.Writer, report *app.AuditReport, limit int) {
	if report.Sessions == 0 {
		fmt.Fprintln(stderr, "没有可审计的会话（试试 --source / --store）")
		printProblems(stderr, report.Problems)
		return
	}
	rows := make([][]string, 0, len(report.Issues))
	for _, issue := range report.Issues {
		covered := "-"
		if total := report.Totals[issue.Source][issue.Scope]; total > 0 {
			covered = strconv.Itoa(issue.Count) + "/" + strconv.Itoa(total)
		}
		sample := ""
		if len(issue.Samples) > 0 {
			sample = issue.Samples[0]
		}
		rows = append(rows, []string{
			string(issue.Source),
			issue.Level,
			issue.Field,
			issue.Scope,
			covered,
			cli.TruncateDisplay(sample, 52),
		})
	}
	if len(rows) == 0 {
		fmt.Fprintf(stdout, "字段门禁通过：%d 个会话没有发现空缺"+NL, report.Sessions)
	} else {
		cli.RenderTable(stdout, []table.Column{
			{Header: "SOURCE"},
			{Header: "LEVEL"},
			{Header: "FIELD"},
			{Header: "SCOPE"},
			{Header: "MISSING", Align: table.Right},
			{Header: "SAMPLE"},
		}, rows)
		fmt.Fprintln(stdout)
	}
	fmt.Fprint(stderr, auditSummary(report))
	if limit > 0 {
		fmt.Fprintf(stderr, "每源最多审计 %d 个（--limit 0 看全部）"+NL, limit)
	}
	printProblems(stderr, report.Problems)
}

func auditSummary(report *app.AuditReport) string {
	missing, notes := 0, 0
	for _, issue := range report.Issues {
		if issue.Level == "violation" {
			missing += issue.Count
		} else {
			notes += issue.Count
		}
	}
	summary := fmt.Sprintf("扫描 %d 个来源 / %d 个会话：必需字段空缺 %d 处，可选项空缺 %d 处",
		len(report.Scanned), report.Sessions, missing, notes)
	if missing > 0 {
		return summary + "（门禁未通过，退出码 1）" + NL
	}
	return summary + "（门禁通过）" + NL
}
