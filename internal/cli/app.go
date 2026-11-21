package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// IO 是一次执行的输入输出。
type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Fallback 是"没有子命令时的默认动作"（老用法：管道输入当 render）。
type Fallback func(cmd *cobra.Command) error

// NewRoot 建根命令：只负责骨架（分组、帮助、退出码），不认识任何业务类型。
func NewRoot(version string, stdio IO, fallback Fallback) *cobra.Command {
	root := &cobra.Command{
		Use:               "sesscope",
		Short:             "跨 Agent 会话探查：发现 / 取数 / 归因 / 渲染",
		Long:              rootLong(version),
		Version:           version,
		SilenceUsage:      true, // 用法只在参数错误时补一行提示（见 Execute）
		SilenceErrors:     true,
		DisableAutoGenTag: true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.SetIn(stdio.In)
	root.SetOut(stdio.Out)
	root.SetErr(stdio.Err)
	root.SetUsageTemplate(usageTemplate())
	root.SetHelpTemplate(helpTemplate())
	root.AddGroup(
		&cobra.Group{ID: "discover", Title: "发现"},
		&cobra.Group{ID: "fetch", Title: "取数"},
		&cobra.Group{ID: "analyze", Title: "分析"},
		&cobra.Group{ID: "render", Title: "渲染"},
		&cobra.Group{ID: "meta", Title: "元信息"},
	)
	root.InitDefaultHelpCmd()
	root.InitDefaultVersionFlag()
	root.InitDefaultHelpFlag()
	if help := findHelpCommand(root); help != nil {
		help.GroupID = "meta"
		help.Short = "打印帮助（等价于 <命令> --help）"
	}
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error { return BadInput(err) })
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return BadInput(fmt.Errorf("不认识的子命令 %q（可用：sources / doctor / workspaces / sessions / export / analyze / render；--help 看详情）", args[0]))
		}
		if fallback != nil && !stdinIsTerminal(cmd.InOrStdin()) {
			return fallback(cmd)
		}
		return cmd.Help()
	}
	return root
}

func findHelpCommand(root *cobra.Command) *cobra.Command {
	for _, item := range root.Commands() {
		if item.Name() == "help" {
			return item
		}
	}
	return nil
}

// Execute 是唯一入口：负责老参数兼容、执行、错误打印与退出码。
func Execute(root *cobra.Command, args []string, stdio IO) int {
	rewritten, deprecated := RewriteLegacyArgs(root, args)
	if len(deprecated) > 0 {
		fmt.Fprintf(stdio.Err, "提示：单横线长参数（-%s）已弃用，本次已自动兼容；请改用双横线（--%s）。\n",
			strings.Join(deprecated, "、-"), strings.Join(deprecated, "、--"))
	}
	root.SetArgs(rewritten)
	err := root.Execute()
	if err == nil {
		return ExitOK
	}
	// cobra 对未知子命令返回的是自己的错误类型，这里统一成"输入错误 + 可用命令提示"。
	if strings.HasPrefix(err.Error(), "unknown command ") {
		err = BadInput(fmt.Errorf("不认识的子命令：%v（可用：sources / doctor / workspaces / sessions / export / analyze / render）", err))
	}
	code := CodeOf(err)
	fmt.Fprintf(stdio.Err, "sesscope: %v\n", err)
	if code == ExitBadInput {
		if cmd, _, findErr := root.Find(rewritten); findErr == nil && cmd != nil && cmd != root {
			fmt.Fprintf(stdio.Err, "用法：%s --help\n", cmd.CommandPath())
		}
	}
	return code
}

// RewriteLegacyArgs 把 -db/-session 这类老写法改成 --db/--session。
// 只改"确实是某个命令的长参数名"的 token，避免把 -h / 负数值改坏。
func RewriteLegacyArgs(root *cobra.Command, args []string) ([]string, []string) {
	known := map[string]bool{}
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		cmd.Flags().VisitAll(func(flag *pflag.Flag) { known[flag.Name] = true })
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)

	// 老用法：没有子命令，直接 -in/-out → 补一个 render。
	if len(args) > 0 && strings.HasPrefix(args[0], "-") && !strings.HasPrefix(args[0], "--") {
		switch flagName(args[0]) {
		case "in", "out", "data", "export-path", "fixture-tool-error", "generated-at", "chain-gap", "quiet":
			args = append([]string{"render"}, args...)
		}
	}

	out := make([]string, 0, len(args))
	deprecated := make([]string, 0, 2)
	for _, arg := range args {
		if len(arg) > 1 && arg[0] == '-' && !strings.HasPrefix(arg, "--") {
			name := flagName(arg)
			if known[name] {
				out = append(out, "--"+arg[1:])
				deprecated = append(deprecated, name)
				continue
			}
		}
		out = append(out, arg)
	}
	return out, deprecated
}

func flagName(token string) string {
	body := strings.TrimLeft(token, "-")
	if index := strings.IndexByte(body, '='); index >= 0 {
		return body[:index]
	}
	return body
}

func rootLong(version string) string {
	return `sesscope ` + version + ` —— 跨 Agent 会话探查：发现 / 取数 / 归因 / 渲染

把本机的 opencode / dsh / codex / claude 会话读成同一份领域模型，再导出、归因、渲染成自包含页面。

从零开始：
  1) sesscope sources                   # 有哪些来源、存储在哪、能不能读
  2) sesscope sessions --limit 20       # 跨来源列会话（拿到会话 id）
  3) sesscope export --session <id> --html trace.html
  4) sesscope analyze --session <id>    # 不调模型的确定性归因

每个子命令都可以 --help 看自己的参数。`
}

func usageTemplate() string {
	return `用法：
  {{.UseLine}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [命令]{{end}}{{if gt (len .Aliases) 0}}

别名：
  {{.NameAndAliases}}{{end}}{{if .HasAvailableSubCommands}}

发现：
{{range .Commands}}{{if eq .GroupID "discover"}}  {{rpad .Name .NamePadding }} {{.Short}}
{{end}}{{end}}
取数：
{{range .Commands}}{{if eq .GroupID "fetch"}}  {{rpad .Name .NamePadding }} {{.Short}}
{{end}}{{end}}
分析：
{{range .Commands}}{{if eq .GroupID "analyze"}}  {{rpad .Name .NamePadding }} {{.Short}}
{{end}}{{end}}
渲染：
{{range .Commands}}{{if eq .GroupID "render"}}  {{rpad .Name .NamePadding }} {{.Short}}
{{end}}{{end}}
元信息：
{{range .Commands}}{{if eq .GroupID "meta"}}  {{rpad .Name .NamePadding }} {{.Short}}
{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

参数：
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

全局参数：
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}
`
}

func helpTemplate() string {
	return `{{with (or .Long .Short)}}{{.}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}`
}

// StdinIsTerminal 判断 stdin 是不是终端（render 用它决定"没喂数据"的提示）。
func StdinIsTerminal(in io.Reader) bool { return stdinIsTerminal(in) }

// stdinIsTerminal 判断 stdin 是不是终端（决定"无参"是打帮助还是当 render）。
func stdinIsTerminal(in io.Reader) bool {
	file, ok := in.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
