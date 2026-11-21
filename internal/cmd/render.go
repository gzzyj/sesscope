package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/zhouyuejie/opencode-trace/internal/app"
	"github.com/zhouyuejie/opencode-trace/internal/attribution"
	"github.com/zhouyuejie/opencode-trace/internal/cli"
	"github.com/zhouyuejie/opencode-trace/internal/trace"
)

type renderOptions struct {
	inPath      string
	outPath     string
	dataPath    string
	exportPath  string
	generatedAt string
	fixture     bool
	chainGap    int
	quiet       bool
}

func newRenderCommand(env *app.Env) *cobra.Command {
	opts := &renderOptions{inPath: "-", outPath: "-", chainGap: attribution.DefaultChainGap}
	command := &cobra.Command{
		Use:     "render",
		Short:   "把导出 JSON 渲染成自包含 HTML（新旧格式都认）",
		GroupID: "render",
		Args:    cobra.NoArgs,
		RunE:    func(cmd *cobra.Command, args []string) error { return runRender(env, opts, cmd) },
	}
	flags := command.Flags()
	flags.StringVarP(&opts.inPath, "in", "i", "-", "输入 JSON 路径，- 表示 stdin")
	flags.StringVarP(&opts.outPath, "out", "o", "-", "输出 HTML 路径，- 表示 stdout")
	flags.StringVar(&opts.dataPath, "data", "", "可选：同时写出归一化数据 JSON")
	flags.StringVar(&opts.exportPath, "export-path", "", "写进 meta.exportPath 的路径（默认取 --in）")
	flags.BoolVar(&opts.fixture, "fixture-tool-error", false, "生成「工具报错」样本页：把第一个工具调用标成 error")
	flags.StringVar(&opts.generatedAt, "generated-at", "", "固定生成时间（RFC3339），用于可复现构建")
	flags.BoolVarP(&opts.quiet, "quiet", "q", false, "不打印摘要")
	flags.IntVar(&opts.chainGap, "chain-gap", attribution.DefaultChainGap, "补偿链允许夹带的无关步数上限")
	return command
}

// RenderFallback 是老用法的入口：opencode-trace < x.json > y.html。
func RenderFallback(env *app.Env) cli.Fallback {
	return func(cmd *cobra.Command) error {
		opts := &renderOptions{inPath: "-", outPath: "-", chainGap: attribution.DefaultChainGap}
		return runRender(env, opts, cmd)
	}
}

func runRender(env *app.Env, opts *renderOptions, cmd *cobra.Command) error {
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if opts.outPath == "-" && opts.dataPath == "-" {
		return cli.BadInput(fmt.Errorf("--out 和 --data 不能同时用 -（stdout 只能写一样东西）"))
	}
	if opts.inPath == "-" && cli.StdinIsTerminal(cmd.InOrStdin()) {
		return cli.BadInput(fmt.Errorf("没有给 --in，stdin 又是终端。请用管道喂 JSON，或显式指定 --in <文件>：%s  opencode-trace render < session-export.json > trace.html%s  opencode-trace export --session ses_x | opencode-trace render --out trace.html", NL, NL))
	}
	stamp := env.Clock()
	if opts.generatedAt != "" {
		parsed, err := time.Parse(time.RFC3339, opts.generatedAt)
		if err != nil {
			return cli.BadInput(fmt.Errorf("--generated-at 需要 RFC3339 时间：%w", err))
		}
		stamp = parsed
	}
	raw, err := readInput(opts.inPath, cmd.InOrStdin())
	if err != nil {
		return cli.BadInput(err)
	}
	loaded, err := env.LoadSession(raw)
	if err != nil {
		return cli.BadInput(fmt.Errorf("%s 不是可用的会话导出：%w", describe(opts.inPath), err))
	}
	metaPath := opts.exportPath
	if metaPath == "" {
		metaPath = opts.inPath
	}
	if metaPath == "-" {
		metaPath = "<stdin>"
	}
	data, err := env.View(loaded, metaPath, stamp, opts.chainGap)
	if err != nil {
		return cli.BadInput(err)
	}
	if opts.fixture {
		trace.InjectToolError(data)
		data.Episodes = attribution.Analyze(data)
		data.Chains = attribution.BuildChains(data, data.Episodes, opts.chainGap)
	}
	html, err := env.RenderHTML(data)
	if err != nil {
		return cli.RuntimeFailure(err)
	}
	var dataJSON []byte
	if opts.dataPath != "" {
		dataJSON, err = env.DataJSON(data)
		if err != nil {
			return cli.RuntimeFailure(err)
		}
	}
	if err := writeOutput(opts.outPath, html, stdout); err != nil {
		return cli.RuntimeFailure(err)
	}
	if opts.dataPath != "" {
		if err := writeOutput(opts.dataPath, dataJSON, stdout); err != nil {
			return cli.RuntimeFailure(err)
		}
	}
	if !opts.quiet {
		htmlSize := -1
		if opts.outPath != "-" {
			htmlSize = len(html)
		}
		fmt.Fprint(stderr, trace.Summary(data, metaPath, opts.outPath, opts.dataPath, htmlSize))
	}
	return nil
}
