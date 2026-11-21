// sesscope：跨 Agent 会话探查（发现 / 取数 / 归因 / 渲染）。
//
// 本文件只做装配：命令行框架在 internal/cli，命令在 internal/cmd，
// 用例在 internal/app，来源适配器在 internal/source/*，页面在 internal/trace。
package main

import (
	"io"
	"os"

	"github.com/gzzyj/sesscope/internal/app"
	"github.com/gzzyj/sesscope/internal/cli"
	"github.com/gzzyj/sesscope/internal/cmd"
)

// version 由构建时注入（-X main.version=...）。
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run 是一次完整执行；测试直接调它，不经过 os.Exit。
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	env := app.NewEnv()
	env.Stdout = stdout
	env.Stderr = stderr
	stdio := cli.IO{In: stdin, Out: stdout, Err: stderr}
	root := cli.NewRoot(version, stdio, cmd.RenderFallback(env))
	cmd.Register(root, env, version)
	return cli.Execute(root, args, stdio)
}
