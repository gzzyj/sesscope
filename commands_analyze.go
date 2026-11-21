package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/zhouyuejie/opencode-trace/internal/attribution"
	"github.com/zhouyuejie/opencode-trace/internal/opencode"
	"github.com/zhouyuejie/opencode-trace/internal/table"
	"github.com/zhouyuejie/opencode-trace/internal/trace"
)

// runAnalyze 只做本地确定性归因：不联网、不调用任何模型。
func runAnalyze(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("analyze", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inPath := flags.String("in", "", "会话导出 JSON 路径（与 -session 二选一）")
	sessionID := flags.String("session", "", "会话 id（从本地库直接分析，支持前缀）")
	dbPath := flags.String("db", "", dbFlagHelp)
	pid := flags.Int("pid", 0, "用某个 opencode 进程在用的库（先用 processes 查 PID）")
	asJSON := flags.Bool("json", false, "输出 JSON（机器可读）")
	verbose := flags.Bool("v", false, "逐条打印证据与补偿动作")
	limit := flags.Int("limit", 20, "最多打印多少条事件链（0 表示不限）")
	chainGap := flags.Int("chain-gap", attribution.DefaultChainGap, "补偿链允许夹带的无关步数上限（越大链越长）")
	flags.Usage = func() {
		fmt.Fprint(stderr, "opencode-trace analyze —— 不调用模型的确定性归因\n\n用法：\n  opencode-trace analyze -in session-export.json\n  opencode-trace analyze -session ses_xxx\n\n参数：\n")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errorsIsHelp(err) {
			return exitOK
		}
		return exitBadInput
	}
	if *inPath == "" && *sessionID == "" {
		fmt.Fprintln(stderr, "opencode-trace: 用 -in <导出 JSON> 或 -session <会话 id> 指定要分析的对象")
		return exitBadInput
	}

	data, source, code := loadForAnalyze(*inPath, *sessionID, *dbPath, *pid, *chainGap, stdin, stderr)
	if data == nil {
		return code
	}
	episodes := attribution.Analyze(data)
	data.Episodes = episodes
	data.Chains = attribution.BuildChains(data, episodes, *chainGap)
	summary := attribution.Summarize(episodes)

	if *asJSON {
		payload := map[string]any{
			"source":   source,
			"session":  sessionLabel(data),
			"summary":  summary,
			"chains":   data.Chains,
			"episodes": episodes,
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(payload); err != nil {
			fmt.Fprintf(stderr, "opencode-trace: 写 JSON：%v\n", err)
			return exitRuntime
		}
		return exitOK
	}

	fmt.Fprintf(stderr, "会话：%s\n来源：%s\n", sessionLabel(data), source)
	if len(episodes) == 0 {
		fmt.Fprintln(stdout, "没有检测到归因事件链（工具全部一次成功、没有报错、没有压缩）")
		return exitOK
	}

	if len(data.Chains) > 0 {
		chainRows := make([][]string, 0, len(data.Chains))
		for _, chain := range data.Chains {
			chainRows = append(chainRows, []string{
				fmt.Sprintf("第 %d 轮 %d-%d 步", chain.Turn, chain.StartStep, chain.EndStep),
				strconv.Itoa(chain.Steps),
				formatDuration(chain.WindowMs),
				chain.Label,
				chain.Outcome,
			})
		}
		fmt.Fprintf(stdout, "补偿链（一次故障引发的连续补救区间）\n\n")
		fmt.Fprint(stdout, table.Render([]table.Column{
			{Header: "区间"},
			{Header: "步数", Align: table.Right},
			{Header: "耗时", Align: table.Right},
			{Header: "性质"},
			{Header: "结果"},
		}, chainRows))
		fmt.Fprint(stdout, "\n")
	}

	rows := make([][]string, 0, len(summary))
	for _, item := range summary {
		rows = append(rows, []string{
			item.Label,
			strconv.Itoa(item.Count),
			formatDuration(item.WindowMs),
			confidenceOf(episodes, item.Code),
		})
	}
	fmt.Fprintf(stdout, "按原因聚合（窗口耗时＝从触发到恢复到的时间窗，是代价上限）\n\n")
	fmt.Fprint(stdout, table.Render([]table.Column{
		{Header: "原因"},
		{Header: "次数", Align: table.Right},
		{Header: "窗口耗时", Align: table.Right},
		{Header: "置信度"},
	}, rows))

	shown := episodes
	if *limit > 0 && len(shown) > *limit {
		shown = shown[:*limit]
	}
	fmt.Fprintf(stdout, "\n事件链（共 %d 条，显示 %d 条；加 -v 看证据）\n\n", len(episodes), len(shown))
	for _, episode := range shown {
		fmt.Fprintf(stdout, "  第 %d 轮 第 %d 步  %-20s %8s  %s\n",
			episode.Turn, episode.Step, episode.Label, formatDuration(episode.WindowMs), episode.Confidence)
		if *verbose {
			for _, evidence := range episode.Evidence {
				fmt.Fprintf(stdout, "      证据[%s] %s\n", evidence.Kind, evidence.Text)
			}
			for _, response := range episode.Response {
				fmt.Fprintf(stdout, "      动作 %s\n", response)
			}
			fmt.Fprintf(stdout, "      结论 %s\n", episode.Outcome)
		}
	}
	fmt.Fprint(stdout, "\n这些都是本地规则算出来的：没有联网、没有调用模型。\n")
	return exitOK
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
	title := ""
	if data.Meta.Title != nil {
		title = *data.Meta.Title
	}
	id := ""
	if data.Meta.SessionID != nil {
		id = *data.Meta.SessionID
	}
	if title == "" {
		return id
	}
	return id + "  «" + title + "»"
}

// loadForAnalyze：从导出 JSON 或本地库拿到归一化数据。
func loadForAnalyze(inPath, sessionID, dbPath string, pid int, chainGap int, stdin io.Reader, stderr io.Writer) (*trace.Data, string, int) {
	if inPath != "" {
		var raw []byte
		var err error
		if inPath == "-" {
			raw, err = io.ReadAll(stdin)
		} else {
			raw, err = readInput(inPath, stdin)
		}
		if err != nil {
			fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
			return nil, "", exitBadInput
		}
		var document trace.Export
		if err := trace.DecodeExport(raw, &document); err != nil {
			fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
			return nil, "", exitBadInput
		}
		data, err := trace.NormalizeExport(&document, trace.Options{ExportPath: describe(inPath), GeneratedAt: time.Now()})
		if err != nil {
			fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
			return nil, "", exitBadInput
		}
		return data, describe(inPath), exitOK
	}

	database, code := openDatabase(dbPath, pid, stderr)
	if database == nil {
		return nil, "", code
	}
	defer database.Close()
	document, err := database.ExportDocument(sessionID, time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
		return nil, "", exitBadInput
	}
	encoded, err := opencode.EncodeDocument(document)
	if err != nil {
		fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
		return nil, "", exitRuntime
	}
	data, err := buildData(encoded, sessionID, time.Now(), chainGap)
	if err != nil {
		fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
		return nil, "", exitBadInput
	}
	return data, database.Path + " · " + sessionID, exitOK
}

var _ = strings.TrimSpace
