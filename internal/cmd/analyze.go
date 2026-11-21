package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/gzzyj/atrace/internal/app"
	"github.com/gzzyj/atrace/internal/attribution"
	"github.com/gzzyj/atrace/internal/cli"
	"github.com/gzzyj/atrace/internal/source"
	"github.com/gzzyj/atrace/internal/table"
	"github.com/gzzyj/atrace/internal/trace"
)

func newAnalyzeCommand(env *app.Env) *cobra.Command {
	var sourceOpts sourceOptions
	var outputOpts outputOptions
	var inPath, sessionID string
	var verbose bool
	var limit, chainGap int
	command := &cobra.Command{
		Use:     "analyze",
		Short:   "不调用模型的确定性归因（事件链 + 补偿链 + 证据）",
		Long:    "只做本地规则判断：不联网、不调用任何模型。用 --in 读导出 JSON（新旧格式都认），或 --session 直连来源。",
		GroupID: "analyze",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
			if inPath == "" && sessionID == "" {
				return cli.BadInput(fmt.Errorf("用 --in <导出 JSON> 或 --session <会话 id> 指定要分析的对象"))
			}
			if inPath != "" && sessionID != "" {
				return cli.BadInput(fmt.Errorf("--in 与 --session 只能给一个"))
			}
			var (
				opened      *trace.Data
				sourceLabel string
			)
			if inPath != "" {
				raw, err := readInput(inPath, cmd.InOrStdin())
				if err != nil {
					return cli.BadInput(err)
				}
				loaded, err := env.LoadSession(raw)
				if err != nil {
					return cli.BadInput(fmt.Errorf("%s：%w", describe(inPath), err))
				}
				view, err := env.View(loaded, describe(inPath), env.Clock(), chainGap)
				if err != nil {
					return cli.BadInput(err)
				}
				opened, sourceLabel = view, describe(inPath)
			} else {
				selection, err := env.Select(cmd.Context(), sourceOpts.sources, sourceOpts.locator())
				if err != nil {
					return wrap(err)
				}
				loaded, store, err := env.OpenSession(cmd.Context(), selection, sessionID, source.OpenOptions{})
				if err != nil {
					return wrap(err)
				}
				view, err := env.View(loaded, loaded.Ref.ID, env.Clock(), chainGap)
				if err != nil {
					return cli.BadInput(err)
				}
				opened, sourceLabel = view, store.Path+" · "+loaded.Ref.ID
			}

			if outputOpts.asJSON {
				return cli.WriteJSON(stdout, "atrace.analyze.v1", map[string]any{
					"source":   sourceLabel,
					"session":  sessionLabel(opened),
					"summary":  attribution.Summarize(opened.Episodes),
					"chains":   opened.Chains,
					"episodes": opened.Episodes,
				})
			}

			fmt.Fprintf(stderr, "会话：%s"+NL+"来源：%s"+NL, sessionLabel(opened), sourceLabel)
			if len(opened.Episodes) == 0 {
				fmt.Fprintln(stdout, "没有检测到归因事件链（工具全部一次成功、没有报错、没有压缩）")
				return nil
			}
			if len(opened.Chains) > 0 {
				chainRows := make([][]string, 0, len(opened.Chains))
				for _, chain := range opened.Chains {
					chainRows = append(chainRows, []string{
						fmt.Sprintf("第 %d 轮 %d-%d 步", chain.Turn, chain.StartStep, chain.EndStep),
						strconv.Itoa(chain.Steps),
						cli.FormatDuration(chain.WindowMs),
						chain.Label,
						chain.Outcome,
					})
				}
				fmt.Fprintf(stdout, "补偿链（一次故障引发的连续补救区间）"+NL+NL)
				cli.RenderTable(stdout, []table.Column{
					{Header: "区间"},
					{Header: "步数", Align: table.Right},
					{Header: "耗时", Align: table.Right},
					{Header: "性质"},
					{Header: "结果"},
				}, chainRows)
				fmt.Fprint(stdout, NL)
			}

			summary := attribution.Summarize(opened.Episodes)
			rows := make([][]string, 0, len(summary))
			for _, item := range summary {
				rows = append(rows, []string{
					item.Label, strconv.Itoa(item.Count), cli.FormatDuration(item.WindowMs), confidenceOf(opened.Episodes, item.Code),
				})
			}
			fmt.Fprintf(stdout, "按原因聚合（窗口耗时＝从触发到恢复的时间窗，是代价上限）"+NL+NL)
			cli.RenderTable(stdout, []table.Column{
				{Header: "原因"}, {Header: "次数", Align: table.Right}, {Header: "窗口耗时", Align: table.Right}, {Header: "置信度"},
			}, rows)

			shown := opened.Episodes
			if limit > 0 && len(shown) > limit {
				shown = shown[:limit]
			}
			fmt.Fprintf(stdout, NL+"事件链（共 %d 条，显示 %d 条；加 --verbose 看证据）"+NL+NL, len(opened.Episodes), len(shown))
			for _, episode := range shown {
				fmt.Fprintf(stdout, "  第 %d 轮 第 %d 步  %-20s %8s  %s"+NL, episode.Turn, episode.Step, episode.Label, cli.FormatDuration(episode.WindowMs), episode.Confidence)
				if verbose {
					for _, evidence := range episode.Evidence {
						fmt.Fprintf(stdout, "      证据[%s] %s"+NL, evidence.Kind, evidence.Text)
					}
					for _, response := range episode.Response {
						fmt.Fprintf(stdout, "      动作 %s"+NL, response)
					}
					fmt.Fprintf(stdout, "      结论 %s"+NL, episode.Outcome)
				}
			}
			fmt.Fprint(stdout, NL+"这些都是本地规则算出来的：没有联网、没有调用模型。"+NL)
			return nil
		},
	}
	sourceOpts.add(command)
	outputOpts.add(command)
	command.Flags().StringVar(&inPath, "in", "", "会话导出 JSON 路径（新旧格式都认；- 表示 stdin）")
	command.Flags().StringVar(&sessionID, "session", "", "会话 id（从本地来源直接分析，支持前缀）")
	command.Flags().BoolVarP(&verbose, "verbose", "v", false, "逐条打印证据与补偿动作")
	command.Flags().IntVarP(&limit, "limit", "n", 20, "最多打印多少条事件链（0 表示不限）")
	command.Flags().IntVar(&chainGap, "chain-gap", attribution.DefaultChainGap, "补偿链允许夹带的无关步数上限")
	return command
}

func confidenceOf(episodes []trace.Episode, code string) string {
	best := ""
	for _, episode := range episodes {
		if episode.Code != code {
			continue
		}
		if episode.Confidence == "A" {
			return "A"
		}
		best = episode.Confidence
	}
	return best
}

func sessionLabel(data *trace.Data) string {
	id, title := "", ""
	if data.Meta.SessionID != nil {
		id = *data.Meta.SessionID
	}
	if data.Meta.Title != nil {
		title = *data.Meta.Title
	}
	if title == "" {
		return id
	}
	return id + "  «" + title + "»"
}
