package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/gzzyj/sesscope/internal/app"
	"github.com/gzzyj/sesscope/internal/cli"
	"github.com/gzzyj/sesscope/internal/source"
)

func newVersionCommand(version string, env *app.Env) *cobra.Command {
	var outputOpts outputOptions
	command := &cobra.Command{
		Use:     "version",
		Short:   "打印版本与已实现来源",
		GroupID: "meta",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !outputOpts.asJSON {
				fmt.Fprintf(cmd.OutOrStdout(), "sesscope %s"+NL, version)
				fmt.Fprintf(cmd.OutOrStdout(), "  平台      %s/%s（adapter %s）"+NL, runtime.GOOS, runtime.GOARCH, env.Platform.Kind())
				for _, item := range env.Registry.All() {
					fmt.Fprintf(cmd.OutOrStdout(), "  %-10s %s"+NL, item.ID(), describeCapabilities(item.Capabilities()))
				}
				return nil
			}
			sources := make([]map[string]any, 0, len(env.Registry.All()))
			for _, item := range env.Registry.All() {
				sources = append(sources, map[string]any{
					"id":           item.ID(),
					"capabilities": item.Capabilities(),
				})
			}
			return cli.WriteJSON(cmd.OutOrStdout(), "sesscope.version.v1", map[string]any{
				"version": version, "sources": sources,
				"platform": map[string]any{
					"goos": runtime.GOOS, "goarch": runtime.GOARCH, "adapter": env.Platform.Kind(),
				},
			})
		},
	}
	outputOpts.add(command)
	return command
}

func describeCapabilities(caps source.Capabilities) string {
	flags := make([]string, 0, 8)
	if caps.ProcessDiscovery {
		flags = append(flags, "进程发现")
	}
	if caps.WorkspaceCatalog {
		flags = append(flags, "原生工作区")
	}
	if caps.Tokens {
		flags = append(flags, "token")
	}
	if caps.ReasoningText {
		flags = append(flags, "思考文本")
	}
	if caps.CompactionRecords {
		flags = append(flags, "压缩记录")
	}
	if !caps.TurnHints {
		flags = append(flags, "轮次派生")
	}
	return fmt.Sprintf("%s（%s）", string(caps.ListCost), join(flags))
}

func join(values []string) string {
	out := ""
	for index, value := range values {
		if index > 0 {
			out += " / "
		}
		out += value
	}
	return out
}
