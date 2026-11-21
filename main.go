// Command opencode-trace 是一个多子命令工具：
//
//	render      把会话导出 JSON 渲染成 DSH 轨迹风格的自包含 HTML（原有能力）
//	workspaces  列出 opencode 工作区（project / workspace）
//	sessions    列出会话（可按工作区过滤、排序、搜索）
//	export      把某个会话从 SQLite 库里导出成 session-export.json
//
// 兼容旧用法：不带子命令直接给 -in/-out 等于 render。
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zhouyuejie/opencode-trace/internal/attribution"
	"github.com/zhouyuejie/opencode-trace/internal/trace"
)

// version 由 -ldflags "-X main.version=..." 覆盖。
var version = "dev"

// 退出码：0 成功；1 运行期错误（写文件失败等）；2 输入有问题（参数/数据不对）。
const (
	exitOK       = 0
	exitRuntime  = 1
	exitBadInput = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "-help":
			printUsage(stdout)
			return exitOK
		case "-version", "--version", "-v":
			fmt.Fprintf(stdout, "opencode-trace %s\n", version)
			return exitOK
		}
	}
	if len(args) == 0 {
		// 没有参数：终端里给帮助，管道里当作 render（兼容 opencode-trace < x.json > y.html）
		if stdinIsTerminal(stdin) {
			printUsage(stdout)
			return exitOK
		}
		return runRender(args, stdin, stdout, stderr)
	}
	if !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "render":
			return runRender(args[1:], stdin, stdout, stderr)
		case "processes", "ps", "procs":
			return runProcesses(args[1:], stdout, stderr)
		case "workspaces", "workspace", "ws":
			return runWorkspaces(args[1:], stdout, stderr)
		case "sessions", "session", "ls":
			return runSessions(args[1:], stdout, stderr)
		case "export", "dump":
			return runExport(args[1:], stdout, stderr)
		case "analyze":
			return runAnalyze(args[1:], stdin, stdout, stderr)
		case "version":
			fmt.Fprintf(stdout, "opencode-trace %s\n", version)
			return exitOK
		case "help":
			if len(args) > 1 {
				return helpFor(args[1], stdout, stderr)
			}
			printUsage(stdout)
			return exitOK
		default:
			fmt.Fprintf(stderr, "opencode-trace: 不认识的子命令 %q\n\n", args[0])
			printUsage(stderr)
			return exitBadInput
		}
	}
	// 旧用法：opencode-trace -in x -out y（等同于 render）
	return runRender(args, stdin, stdout, stderr)
}

func printUsage(out io.Writer) {
	fmt.Fprintf(out, "opencode-trace %s —— opencode 会话导出 JSON → DSH 轨迹风格耗时 HTML\n\n", version)
	fmt.Fprint(out, "用法：\n")
	fmt.Fprint(out, "  opencode-trace <子命令> [参数]\n")
	fmt.Fprint(out, "  opencode-trace -in export.json -out trace.html      # 旧用法，等同 render\n")
	fmt.Fprint(out, "\n子命令：\n")
	fmt.Fprint(out, "  processes    列出本机 opencode 进程、端口、各自在用的会话库（不知道库在哪就先跑这个）\n")
	fmt.Fprint(out, "  workspaces   列出 opencode 工作区（project / workspace）及其会话数\n")
	fmt.Fprint(out, "  sessions     列出会话（-workspace 过滤、-sort 排序、-grep 搜索）\n")
	fmt.Fprint(out, "  export       把某个会话导出成 session-export.json（还能直接 -html）\n")
	fmt.Fprint(out, "  analyze      不调用模型的确定性归因（事件链 + 证据）\n")
	fmt.Fprint(out, "  render       把 session-export.json 渲染成自包含 HTML\n")
	fmt.Fprint(out, "  version      打印版本\n")
	fmt.Fprint(out, "  help         打印本帮助\n")
	fmt.Fprint(out, "\n怎么用（从零开始）：\n")
	fmt.Fprint(out, "  1) opencode-trace processes                 # 有哪些 opencode 进程、库在哪\n")
	fmt.Fprint(out, "  2) opencode-trace workspaces -pid <PID>     # 这个进程有哪些工作区\n")
	fmt.Fprint(out, "  3) opencode-trace sessions -pid <PID> -limit 20\n")
	fmt.Fprint(out, "  4) opencode-trace export -session <会话 id 或前缀> -html trace.html\n")
	fmt.Fprint(out, "\n库不经过进程也能指定：-db <path>；会话 id 支持前缀匹配。\n")
	fmt.Fprint(out, "\n常用例子：\n")
	fmt.Fprint(out, "  opencode-trace workspaces\n")
	fmt.Fprint(out, "  opencode-trace sessions -limit 20\n")
	fmt.Fprint(out, "  opencode-trace sessions -workspace fastagi-monorepo -all\n")
	fmt.Fprint(out, "  opencode-trace export -session ses_e1dc9f03 -out session-export.json\n")
	fmt.Fprint(out, "  opencode-trace export -session ses_e1dc9f03 -html trace.html\n")
	fmt.Fprint(out, "  opencode-trace export -session ses_e1dc9f03 | opencode-trace render -out trace.html\n")
	fmt.Fprint(out, "\n每个子命令都可以用 -h 看自己的参数，也可以 opencode-trace help <子命令>。\n")
}

// ---------------- render ----------------

func runRender(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("render", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inPath := flags.String("in", "-", "输入：会话导出 JSON 路径，- 表示 stdin")
	outPath := flags.String("out", "-", "输出：HTML 路径，- 表示 stdout")
	dataPath := flags.String("data", "", "可选：同时写出归一化数据 JSON（- 表示 stdout）")
	exportPath := flags.String("export-path", "", "写进 meta.exportPath 的路径（默认取 -in 的值；- 记为 <stdin>）")
	fixture := flags.Bool("fixture-tool-error", false, "生成“工具报错”样本页：把第一个工具调用标成 error")
	generatedAt := flags.String("generated-at", "", "固定 meta.generatedAt（RFC3339），用于可复现构建")
	quiet := flags.Bool("quiet", false, "不打印摘要")
	chainGap := flags.Int("chain-gap", attribution.DefaultChainGap, "补偿链允许夹带的无关步数上限（默认 6）")
	showVersion := flags.Bool("version", false, "打印版本后退出")
	flags.Usage = func() {
		fmt.Fprintf(stderr, "opencode-trace render %s —— 会话导出 JSON → 自包含 HTML\n\n用法：\n  opencode-trace render [flags]\n\n参数：\n", version)
		flags.PrintDefaults()
		fmt.Fprint(stderr, "\n示例：\n  opencode-trace render < session-export.json > trace.html\n")
		fmt.Fprint(stderr, "  opencode-trace export -session ses_x | opencode-trace render -out trace.html\n")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitBadInput
	}
	if *showVersion {
		fmt.Fprintf(stdout, "opencode-trace %s\n", version)
		return exitOK
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "opencode-trace: 不认识的位置参数 %q（用 -in/-out 指定）\n", flags.Arg(0))
		return exitBadInput
	}
	if *outPath == "-" && *dataPath == "-" {
		fmt.Fprintln(stderr, "opencode-trace: -out 和 -data 不能同时用 -（stdout 只能写一样东西）")
		return exitBadInput
	}

	stamp := time.Now()
	if *generatedAt != "" {
		parsed, err := time.Parse(time.RFC3339, *generatedAt)
		if err != nil {
			fmt.Fprintf(stderr, "opencode-trace: -generated-at 需要 RFC3339 时间：%v\n", err)
			return exitBadInput
		}
		stamp = parsed
	}

	if *inPath == "-" && stdinIsTerminal(stdin) {
		fmt.Fprintln(stderr, "opencode-trace: 没有给 -in，stdin 又是终端。请用管道喂 JSON，或显式指定 -in <文件>：")
		fmt.Fprintln(stderr, "  opencode-trace render < session-export.json > trace.html")
		fmt.Fprintln(stderr, "  opencode-trace export -session ses_x | opencode-trace render -out trace.html")
		return exitBadInput
	}

	raw, err := readInput(*inPath, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
		return exitBadInput
	}

	var doc trace.Export
	if err := trace.DecodeExport(raw, &doc); err != nil {
		fmt.Fprintf(stderr, "opencode-trace: %s 不是可用的 opencode 会话导出：%v\n", describe(*inPath), err)
		return exitBadInput
	}

	metaPath := *exportPath
	if metaPath == "" {
		metaPath = *inPath
	}
	if metaPath == "-" {
		metaPath = "<stdin>"
	}
	data, err := trace.Normalize(&doc, trace.Options{ExportPath: metaPath, GeneratedAt: stamp})
	if err != nil {
		fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
		return exitBadInput
	}
	if *fixture {
		trace.InjectToolError(data)
	}
	// 归因是本地确定性规则：不联网、不调用模型
	data.Episodes = attribution.Analyze(data)
	data.Chains = attribution.BuildChains(data, data.Episodes, *chainGap)

	html, err := trace.RenderHTML(data)
	if err != nil {
		fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
		return exitRuntime
	}

	var dataJSON []byte
	if *dataPath != "" {
		dataJSON, err = trace.EncodeDataJSON(data)
		if err != nil {
			fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
			return exitRuntime
		}
	}

	if err := writeOutput(*outPath, html, stdout); err != nil {
		fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
		return exitRuntime
	}
	if *dataPath != "" {
		if err := writeOutput(*dataPath, dataJSON, stdout); err != nil {
			fmt.Fprintf(stderr, "opencode-trace: %v\n", err)
			return exitRuntime
		}
	}

	if !*quiet {
		htmlSize := -1
		if *outPath != "-" {
			htmlSize = len(html)
		}
		fmt.Fprint(stderr, trace.Summary(data, metaPath, *outPath, *dataPath, htmlSize))
	}
	return exitOK
}

func describe(path string) string {
	if path == "-" {
		return "<stdin>"
	}
	return path
}

func stdinIsTerminal(reader io.Reader) bool {
	file, ok := reader.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func readInput(path string, stdin io.Reader) ([]byte, error) {
	if path == "-" {
		raw, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("读 stdin：%w", err)
		}
		if len(raw) == 0 {
			return nil, errors.New("stdin 是空的，没有任何 JSON")
		}
		return raw, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读输入：%w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("输入文件是空的：%s", path)
	}
	return raw, nil
}

// writeOutput 写文件（自动建目录、先写临时文件再改名，避免留下半截文件）；- 表示写 stdout。
func writeOutput(path string, content []byte, stdout io.Writer) error {
	if path == "-" {
		if _, err := stdout.Write(content); err != nil {
			return fmt.Errorf("写 stdout：%w", err)
		}
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("建目录 %s：%w", dir, err)
	}
	temp, err := os.CreateTemp(dir, ".opencode-trace-*")
	if err != nil {
		return fmt.Errorf("在 %s 建临时文件：%w", dir, err)
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return fmt.Errorf("写 %s：%w", tempName, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("关闭 %s：%w", tempName, err)
	}
	if err := os.Chmod(tempName, 0o644); err != nil {
		return fmt.Errorf("设置权限 %s：%w", tempName, err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("改名到 %s：%w", path, err)
	}
	return nil
}
