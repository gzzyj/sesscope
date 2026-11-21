package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/gzzyj/sesscope/internal/app"
	"github.com/gzzyj/sesscope/internal/attribution"
	"github.com/gzzyj/sesscope/internal/cli"
	"github.com/gzzyj/sesscope/internal/source"
	"github.com/gzzyj/sesscope/internal/trace"
)

func newExportCommand(env *app.Env) *cobra.Command {
	var sourceOpts sourceOptions
	var sessionID, outPath, htmlPath, dataPath, generatedAt, format string
	var chainGap int
	var quiet bool
	command := &cobra.Command{
		Use:     "export",
		Aliases: []string{"dump"},
		Short:   "把会话导出成 JSON（规范格式；可顺带渲染 HTML）",
		Long: `导出会话。默认输出规范格式（sesscope/session.v1），它同时是领域模型的快照：
  --format canonical  （默认）规范格式，render/analyze 都吃
  --format legacy     旧的 opencode 形状 session-export.json（仅 opencode 支持）

--session 必须带来源：写成 <source>:<id>，或同时给 --source <source>。会话 id 只在所属来源内
唯一，裸 id 无法定位；id 支持前缀，同源内命中多个时列出候选并以退出码 2 结束，不猜。`,
		GroupID: "fetch",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
			if sessionID == "" {
				return cli.BadInput(fmt.Errorf("必须用 --session 指定会话（先跑 sessions 拿 id）"))
			}
			if outPath == "-" && (htmlPath == "-" || dataPath == "-") {
				return cli.BadInput(fmt.Errorf("--out 已经是 stdout，--html/--data 不能也用 -"))
			}
			if dataPath != "" && htmlPath == "" {
				return cli.BadInput(fmt.Errorf("--data 需要配合 --html 使用（它写的是页面用的归一化数据）"))
			}
			stamp := env.Clock()
			if generatedAt != "" {
				parsed, err := time.Parse(time.RFC3339, generatedAt)
				if err != nil {
					return cli.BadInput(fmt.Errorf("--generated-at 需要 RFC3339 时间：%w", err))
				}
				stamp = parsed
			}
			selection, err := env.Select(cmd.Context(), sourceOpts.sources, sourceOpts.locator())
			if err != nil {
				return wrap(err)
			}
			opened, store, err := env.OpenSession(cmd.Context(), selection, sessionID, source.OpenOptions{})
			if err != nil {
				return wrap(err)
			}
			var payload []byte
			switch format {
			case "", "canonical":
				payload, err = env.Canonical(opened, stamp)
			case "legacy":
				payload, err = env.Legacy(cmd.Context(), opened, store, stamp)
			default:
				return cli.BadInput(fmt.Errorf("不认识的 --format %q（可选 canonical / legacy）", format))
			}
			if err != nil {
				return wrap(err)
			}
			if err := writeOutput(outPath, payload, stdout); err != nil {
				return cli.RuntimeFailure(err)
			}

			htmlSize := -1
			var view *trace.Data
			if htmlPath != "" {
				view, err = env.View(opened, describe(outPath), stamp, chainGap)
				if err != nil {
					return cli.BadInput(err)
				}
				html, err := env.RenderHTML(view)
				if err != nil {
					return cli.RuntimeFailure(err)
				}
				if err := writeOutput(htmlPath, html, stdout); err != nil {
					return cli.RuntimeFailure(err)
				}
				if htmlPath != "-" {
					htmlSize = len(html)
				}
				if dataPath != "" {
					encoded, err := env.DataJSON(view)
					if err != nil {
						return cli.RuntimeFailure(err)
					}
					if err := writeOutput(dataPath, encoded, stdout); err != nil {
						return cli.RuntimeFailure(err)
					}
				}
			}
			if quiet {
				return nil
			}
			if view != nil {
				fmt.Fprint(stderr, trace.Summary(view, describe(outPath), htmlPath, dataPath, htmlSize))
			} else {
				fmt.Fprintf(stderr, "导出：%s"+NL, describe(outPath))
				fmt.Fprintf(stderr, "会话：%s（%s）· 消息 %d · 诊断 %d"+NL,
					sessionLabelOf(opened), opened.Ref.Source, len(opened.Messages), len(opened.Diagnostics))
			}
			return nil
		},
	}
	sourceOpts.add(command)
	command.Flags().BoolVarP(&quiet, "quiet", "q", false, "不打印摘要")
	command.Flags().StringVar(&sessionID, "session", "", "会话，必须带来源：<source>:<id>（支持 id 前缀；也可配合 --source）")
	command.Flags().StringVarP(&outPath, "out", "o", "-", "输出 JSON 路径，- 表示 stdout")
	command.Flags().StringVar(&htmlPath, "html", "", "可选：同时渲染 HTML 到这个路径")
	command.Flags().StringVar(&dataPath, "data", "", "可选：同时写出归一化数据（需配合 --html）")
	command.Flags().StringVar(&format, "format", "canonical", "导出格式：canonical（默认）/ legacy")
	command.Flags().StringVar(&generatedAt, "generated-at", "", "固定生成时间（RFC3339），用于可复现构建")
	command.Flags().IntVar(&chainGap, "chain-gap", attribution.DefaultChainGap, "补偿链允许夹带的无关步数上限")
	return command
}
