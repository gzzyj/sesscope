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

	"github.com/gzzyj/sesscope/internal/platform"
	"github.com/gzzyj/sesscope/internal/session"
	"github.com/gzzyj/sesscope/internal/source"
	"github.com/gzzyj/sesscope/internal/source/all"
)

// NL 是换行（集中定义，避免到处写字面量）。
var NL = string(rune(10))

// Env 是用例层的依赖。
type Env struct {
	Registry *source.Registry
	Platform platform.Adapter
	Stdout   io.Writer
	Stderr   io.Writer
	Now      time.Time
}

// NewEnv 按当前 OS 装配全部来源。
func NewEnv() *Env { return NewEnvWith(platform.Detect()) }

// NewEnvWith 用指定 OS 适配器装配（测试与交叉验证用）。
func NewEnvWith(p platform.Adapter) *Env {
	if p == nil {
		p = platform.Detect()
	}
	return &Env{Platform: p, Registry: source.NewRegistry(all.Sources(p)...)}
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

// OpenSession 按"来源 + 源内 id 前缀"唯一确定并打开一个会话。
//
// 契约（见 docs/cli.md §6）：
//   - 会话 id 只在所属来源内唯一，选择器必须带来源（--source 或 <source>:<id>）；
//     裸 id 在 --source 没收敛到单一来源时直接报 *source.SourceRequiredError，
//     绝不把四个来源的 id 混成一个全局命名空间去扫。
//   - 候选只在"所选来源"的存储里收集，因此结果不随本机还装了哪些产品而变。
//   - 同源内前缀命中多个会话时报 *source.AmbiguousError（带候选），绝不猜。
func (e *Env) OpenSession(ctx context.Context, selection *Selection, selector string, opts source.OpenOptions) (*session.Session, source.Store, error) {
	readable := selection.Readable()
	if len(readable) == 0 {
		return nil, source.Store{}, selection.NoStoreError()
	}
	chosen, err := source.ParseSelector(selector, selection.SourceIDs())
	if err != nil {
		return nil, source.Store{}, err
	}
	stores := locatedFor(readable, chosen.Source)
	if len(stores) == 0 {
		return nil, source.Store{}, fmt.Errorf("%w：来源 %s 没有可读的存储（用 --store / --home 指定，或 --source 换一个来源）", ErrNoStore, chosen.Source)
	}
	candidates, problems := source.Candidates(ctx, stores)
	if len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintf(e.Stderr, "提示：%v"+NL, problem)
		}
	}
	matched := source.Find(candidates, chosen)
	switch len(matched) {
	case 0:
		return nil, source.Store{}, fmt.Errorf("%w：%s", source.ErrNotFound, chosen)
	case 1:
		hit := matched[0]
		item, ok := e.Registry.Get(hit.Ref.Source)
		if !ok {
			return nil, source.Store{}, fmt.Errorf("来源 %s 未注册", hit.Ref.Source)
		}
		store, ok := findStore(stores, hit.Ref)
		if !ok {
			return nil, source.Store{}, fmt.Errorf("会话 %s 命中的存储 %q 不在本次定位结果里（定位到：%s）",
				hit.Ref.ID, hit.Ref.Store, storePaths(stores))
		}
		opened, err := item.Open(ctx, store, hit.Ref, opts)
		return opened, store, err
	default:
		return nil, source.Store{}, &source.AmbiguousError{Selector: chosen.String(), Candidates: matched}
	}
}

// SourceIDs 返回本次选中的来源 id（顺序与 --source 一致）。
func (s *Selection) SourceIDs() []session.SourceID {
	ids := make([]session.SourceID, 0, len(s.Sources))
	for _, item := range s.Sources {
		ids = append(ids, item.ID())
	}
	return ids
}

// locatedFor 只保留某个来源的存储：候选收集与打开都限定在这个集合里。
func locatedFor(located []source.Located, want session.SourceID) []source.Located {
	out := make([]source.Located, 0, len(located))
	for _, item := range located {
		if item.Store.Source == want {
			out = append(out, item)
		}
	}
	return out
}

// findStore 要求"命中候选的那个存储"与定位结果精确对上。
// 不再退化成"该来源的第一个存储"：那样会把一个存储里解析出的 id 拿到另一个存储里打开，
// 结果看似成功、实际读的是另一份数据（多根目录、多版本库时尤其危险）。
func findStore(stores []source.Located, ref session.Ref) (source.Store, bool) {
	for _, item := range stores {
		if item.Store.Path == ref.Store {
			return item.Store, true
		}
	}
	return source.Store{}, false
}

func storePaths(stores []source.Located) string {
	parts := make([]string, 0, len(stores))
	for _, item := range stores {
		parts = append(parts, item.Store.Path)
	}
	return strings.Join(parts, "、")
}
