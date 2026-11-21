// Package cmd 是命令行层：只负责把参数翻译成用例层调用，不做取数与格式化。
package cmd

import (
	"github.com/spf13/cobra"

	"github.com/zhouyuejie/opencode-trace/internal/app"
)

// Register 把全部子命令挂到根命令上。
func Register(root *cobra.Command, env *app.Env, version string) {
	root.AddCommand(
		newSourcesCommand(env),
		newDoctorCommand(env),
		newWorkspacesCommand(env),
		newSessionsCommand(env),
		newExportCommand(env),
		newAnalyzeCommand(env),
		newRenderCommand(env),
		newVersionCommand(version, env),
	)
}
