package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gzzyj/sesscope/internal/app"
	"github.com/gzzyj/sesscope/internal/cli"
	"github.com/gzzyj/sesscope/internal/session"
	"github.com/gzzyj/sesscope/internal/source"
)

// sourceOptions 是"选源 + 定位存储"的公共参数组。
type sourceOptions struct {
	sources  []string
	store    string
	home     string
	pid      int
	legacyDB string
}

func (o *sourceOptions) add(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringSliceVarP(&o.sources, "source", "s", nil, "限定来源：auto（默认，全部已探测）/ opencode / dsh / codex / claude，可逗号分隔")
	flags.StringVarP(&o.store, "store", "d", "", "直接指定存储（库文件 / 会话目录 / jsonl 文件）")
	flags.StringVar(&o.home, "home", "", "产品数据根目录（如 ~/.dsh、~/.codex、~/.claude）")
	flags.IntVarP(&o.pid, "pid", "p", 0, "从进程推导存储（目前仅 opencode 与 dsh web 支持）")
	flags.StringVar(&o.legacyDB, "db", "", "（已弃用，等同 --store）")
	_ = flags.MarkHidden("db")
}

func (o *sourceOptions) locator() source.Locator {
	store := o.store
	if store == "" {
		store = o.legacyDB
	}
	return source.Locator{Store: store, Home: o.home, PID: o.pid}
}

// outputOptions 是输出形态。
type outputOptions struct {
	asJSON bool
	format string
	quiet  bool
}

func (o *outputOptions) add(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.BoolVarP(&o.asJSON, "json", "j", false, "输出 JSON（机器可读，带 schema 字段）")
	flags.StringVar(&o.format, "format", "", "输出格式（命令自解释：table/json 或 canonical/legacy）")
	flags.BoolVarP(&o.quiet, "quiet", "q", false, "不打印摘要")
}

// filterOptions 是列表类命令的过滤/排序参数。
type filterOptions struct {
	workspace string
	grep      string
	limit     int
	sortBy    string
	all       bool
}

func (o *filterOptions) add(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVarP(&o.workspace, "workspace", "w", "", "只看某个工作区（id 前缀 / 路径 / 名字子串）")
	flags.StringVarP(&o.grep, "grep", "g", "", "标题 / 目录 / id 里包含该子串")
	flags.IntVarP(&o.limit, "limit", "n", 30, "最多列多少条（0 表示不限）")
	flags.StringVar(&o.sortBy, "sort", "updated", "排序：updated / created / tokens / messages")
	flags.BoolVarP(&o.all, "all", "a", false, "连子 agent 会话一起列")
}

// timeOptions 是时间窗。
type timeOptions struct {
	since string
	until string
}

func (o *timeOptions) add(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVar(&o.since, "since", "", "只看该时间之后更新的会话（RFC3339 或 7d / 24h）")
	flags.StringVar(&o.until, "until", "", "只看该时间之前更新的会话（RFC3339）")
}

func (o *timeOptions) bounds() (*int64, *int64, error) {
	since, err := parseWhen(o.since, true)
	if err != nil {
		return nil, nil, fmt.Errorf("--since：%w", err)
	}
	until, err := parseWhen(o.until, false)
	if err != nil {
		return nil, nil, fmt.Errorf("--until：%w", err)
	}
	return since, until, nil
}

func parseWhen(text string, fromNow bool) (*int64, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, nil
	}
	if stamp, err := time.Parse(time.RFC3339, trimmed); err == nil {
		ms := stamp.UnixMilli()
		return &ms, nil
	}
	if duration, err := time.ParseDuration(trimmed); err == nil {
		base := time.Now()
		if !fromNow {
			base = base.Add(duration)
		} else {
			base = base.Add(-duration)
		}
		ms := base.UnixMilli()
		return &ms, nil
	}
	return nil, fmt.Errorf("看不懂的时间 %q（用 RFC3339 或 7d / 24h 这类相对时长）", text)
}

// wrap 把用例层错误映射成退出码（cli 不认识业务错误，映射集中在这里）。
func wrap(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, app.ErrNoStore):
		return cli.BadInput(err)
	case errors.Is(err, source.ErrUnsupported):
		return cli.Unsupported(err)
	case errors.Is(err, source.ErrAmbiguous), errors.Is(err, source.ErrNotFound):
		return cli.BadInput(err)
	default:
		return err
	}
}

// printProblems 把"某源坏了"这类问题写到 stderr，不吞。
func printProblems(stderr io.Writer, problems []source.Problem) {
	for _, problem := range problems {
		fmt.Fprintf(stderr, "提示：%v"+NL, problem)
	}
}

// printNotes 打印一行行提示。
func printNotes(stderr io.Writer, notes []string) {
	for _, note := range notes {
		fmt.Fprintln(stderr, note)
	}
}

// sessionLabelOf 给 stderr 摘要用：id «title»。
func sessionLabelOf(value *session.Session) string {
	if value == nil {
		return "-"
	}
	id := value.Ref.ID
	title := ""
	if value.Meta.Title != nil {
		title = *value.Meta.Title
	}
	if title == "" {
		return id
	}
	return id + "  «" + title + "»"
}

// NL 是换行。
var NL = string(rune(10))
