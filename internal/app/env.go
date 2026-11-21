// Package app 是用例层：把"源定位 → 取数 → 派生 → 输出"串起来。
//
// 这一层不碰命令行参数解析（cmd 的事）、也不碰格式化（cli 的事），
// 只返回结构化结果，方便单测与后续接 Web。
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/gzzyj/sesscope/internal/session"
	"github.com/gzzyj/sesscope/internal/source"
	"github.com/gzzyj/sesscope/internal/source/all"
)

// NL 是换行（集中定义，避免到处写字面量）。
var NL = string(rune(10))

// Env 是用例层的依赖。
type Env struct {
	Registry *source.Registry
	Stdout   io.Writer
	Stderr   io.Writer
	Now      time.Time
}

// NewEnv 装配全部来源。
func NewEnv() *Env {
	return &Env{Registry: source.NewRegistry(all.Sources()...)}
}

// Clock 返回"现在"，便于测试固定时间。
func (e *Env) Clock() time.Time {
	if e.Now.IsZero() {
		return time.Now()
	}
	return e.Now
}

// Selection 是"已选源 + 已定位存储"的结果。
type Selection struct {
	Sources  []source.Source
	Located  []source.Located
	Problems []source.Problem
}

// Select 解析 --source 并按定位器找存储。
func (e *Env) Select(ctx context.Context, names []string, locator source.Locator) (*Selection, error) {
	selected, err := e.Registry.Select(names)
	if err != nil {
		return nil, err
	}
	located, problems := e.Registry.Locate(ctx, locator, selected)
	return &Selection{Sources: selected, Located: located, Problems: problems}, nil
}

// Readable 返回存在且可读的存储。
func (s *Selection) Readable() []source.Located {
	return source.Readable(s.Located)
}

// Notes 把"哪些存储不可用/为什么"整理成给人看的一段话（写 stderr）。
func (s *Selection) Notes() []string {
	notes := make([]string, 0, len(s.Located)+len(s.Problems))
	for _, item := range s.Located {
		if !item.Store.Exists {
			notes = append(notes, fmt.Sprintf("%s：%s 不存在（%s）", item.Store.Source, item.Store.Path, item.Store.Rule))
			continue
		}
		if !item.Store.Readable {
			notes = append(notes, fmt.Sprintf("%s：%s 读不了 —— %s", item.Store.Source, item.Store.Path, item.Store.Detail))
		}
	}
	for _, problem := range s.Problems {
		notes = append(notes, problem.Error())
	}
	return notes
}

// ErrNoStore 表示"一个可读存储都没有"，CLI 据此退出码 2。
var ErrNoStore = errors.New("没有可用的会话存储")

// NoStoreError 在"一个可读存储都没有"时给出可操作的错误，列出尝试过的路径。
func (s *Selection) NoStoreError() error {
	tried := make([]string, 0, len(s.Located))
	for _, item := range s.Located {
		tried = append(tried, fmt.Sprintf("  %s  %s（%s）", item.Store.Source, item.Store.Path, item.Store.Rule))
	}
	sort.Strings(tried)
	builder := strings.Builder{}
	builder.WriteString(ErrNoStore.Error() + "。已尝试：")
	builder.WriteString(NL)
	builder.WriteString(strings.Join(tried, NL))
	builder.WriteString(NL)
	builder.WriteString("用 --store 指定存储，或 --source 限定来源后重试。")
	return fmt.Errorf("%w：%s", ErrNoStore, builder.String())
}

// OpenSession 按选择器（id 前缀或 source:id）唯一确定并打开一个会话。
// 歧义时报 *source.AmbiguousError（带候选），绝不猜。
func (e *Env) OpenSession(ctx context.Context, selection *Selection, selector string, opts source.OpenOptions) (*session.Session, source.Store, error) {
	readable := selection.Readable()
	if len(readable) == 0 {
		return nil, source.Store{}, selection.NoStoreError()
	}
	candidates, problems := source.Candidates(ctx, readable)
	if len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintf(e.Stderr, "提示：%v"+NL, problem)
		}
	}
	matched, err := source.Find(candidates, selector)
	if err != nil {
		return nil, source.Store{}, err
	}
	switch len(matched) {
	case 0:
		return nil, source.Store{}, fmt.Errorf("%w：%q", source.ErrNotFound, selector)
	case 1:
		hit := matched[0]
		item, ok := e.Registry.Get(hit.Ref.Source)
		if !ok {
			return nil, source.Store{}, fmt.Errorf("来源 %s 未注册", hit.Ref.Source)
		}
		store, ok := findStore(readable, hit.Ref)
		if !ok {
			return nil, source.Store{}, fmt.Errorf("找不到 %s 对应的存储", hit.Ref.ID)
		}
		opened, err := item.Open(ctx, store, hit.Ref, opts)
		return opened, store, err
	default:
		return nil, source.Store{}, &source.AmbiguousError{Selector: selector, Candidates: matched}
	}
}

func findStore(located []source.Located, ref session.Ref) (source.Store, bool) {
	for _, item := range located {
		if item.Store.Source == ref.Source && item.Store.Path == ref.Store {
			return item.Store, true
		}
	}
	// Ref.Store 可能为空（某些来源不填），退化为"该来源的第一个可读存储"
	for _, item := range located {
		if item.Store.Source == ref.Source {
			return item.Store, true
		}
	}
	return source.Store{}, false
}
