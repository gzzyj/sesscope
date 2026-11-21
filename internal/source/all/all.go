// Package all 是四个来源适配器的唯一装配点。
//
// 不用 init() 隐式注册：装配关系一眼可见、可测、无副作用。
package all

import (
	"github.com/gzzyj/sesscope/internal/source"
	"github.com/gzzyj/sesscope/internal/source/claude"
	"github.com/gzzyj/sesscope/internal/source/codex"
	"github.com/gzzyj/sesscope/internal/source/dsh"
	"github.com/gzzyj/sesscope/internal/source/opencode"
)

// Sources 返回全部已实现的来源适配器（顺序即默认展示顺序）。
func Sources() []source.Source {
	return []source.Source{
		opencode.New(),
		dsh.New(),
		codex.New(),
		claude.New(),
	}
}
