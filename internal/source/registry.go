package source

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zhouyuejie/opencode-trace/internal/session"
)

// Located 是"某个源 + 某个具体存储"的组合。
type Located struct {
	Source Source
	Store  Store
}

// Problem 是一个源/存储级的失败。聚合发现时必须逐条带出，不允许吞掉。
type Problem struct {
	Source session.SourceID
	Store  string
	Err    error
}

func (p Problem) Error() string {
	if p.Store == "" {
		return fmt.Sprintf("%s：%v", p.Source, p.Err)
	}
	return fmt.Sprintf("%s（%s）：%v", p.Source, p.Store, p.Err)
}

// Registry 持有所有已注册的源。装配点只有 internal/source/all 一处，不用 init() 隐式注册。
type Registry struct {
	order []Source
	byID  map[session.SourceID]Source
}

// NewRegistry 按给定顺序注册源。
func NewRegistry(list ...Source) *Registry {
	registry := &Registry{
		order: make([]Source, 0, len(list)),
		byID:  make(map[session.SourceID]Source, len(list)),
	}
	for _, item := range list {
		registry.order = append(registry.order, item)
		registry.byID[item.ID()] = item
	}
	return registry
}

// All 返回全部源（顺序固定，便于输出稳定）。
func (r *Registry) All() []Source { return r.order }

// IDs 返回全部源 id。
func (r *Registry) IDs() []session.SourceID {
	out := make([]session.SourceID, 0, len(r.order))
	for _, item := range r.order {
		out = append(out, item.ID())
	}
	return out
}

// Get 按 id 取源。
func (r *Registry) Get(id session.SourceID) (Source, bool) {
	item, ok := r.byID[id]
	return item, ok
}

// Select 解析 --source 的取值：空或 "auto" 表示全部；支持逗号分隔；未知 id 直接报错并列已知值。
func (r *Registry) Select(names []string) ([]Source, error) {
	cleaned := make([]string, 0, len(names))
	for _, name := range names {
		for _, piece := range strings.Split(name, ",") {
			piece = strings.TrimSpace(piece)
			if piece != "" {
				cleaned = append(cleaned, piece)
			}
		}
	}
	if len(cleaned) == 0 {
		return r.order, nil
	}
	wantsAll := false
	for _, name := range cleaned {
		if name == "auto" || name == "*" {
			wantsAll = true
		}
	}
	if wantsAll {
		return r.order, nil
	}
	seen := map[session.SourceID]bool{}
	out := make([]Source, 0, len(cleaned))
	for _, name := range cleaned {
		item, ok := r.byID[session.SourceID(name)]
		if !ok {
			return nil, fmt.Errorf("不认识的来源 %q（可选：%s）", name, strings.Join(knownIDs(r), " / "))
		}
		if seen[item.ID()] {
			continue
		}
		seen[item.ID()] = true
		out = append(out, item)
	}
	return out, nil
}

func knownIDs(r *Registry) []string {
	out := make([]string, 0, len(r.order)+1)
	out = append(out, "auto")
	for _, item := range r.order {
		out = append(out, string(item.ID()))
	}
	return out
}

// Locate 逐源定位存储：错误逐条收集成 Problem，不因一个源坏了就吞掉另一个源。
func (r *Registry) Locate(ctx context.Context, l Locator, selected []Source) ([]Located, []Problem) {
	located := make([]Located, 0, len(selected))
	problems := make([]Problem, 0, 4)
	for _, item := range selected {
		stores, err := item.Locate(ctx, l)
		if err != nil {
			problems = append(problems, Problem{Source: item.ID(), Err: err})
			continue
		}
		for _, store := range stores {
			located = append(located, Located{Source: item, Store: store})
		}
	}
	return located, problems
}

// Readable 过滤出"存在且可读"的存储。
func Readable(located []Located) []Located {
	out := make([]Located, 0, len(located))
	for _, item := range located {
		if item.Store.Exists && item.Store.Readable {
			out = append(out, item)
		}
	}
	return out
}

// Candidates 在可读存储里收集候选会话（用于前缀解析）。
func Candidates(ctx context.Context, located []Located) ([]Candidate, []Problem) {
	candidates := make([]Candidate, 0, 64)
	problems := make([]Problem, 0, 4)
	for _, item := range located {
		summaries, err := item.Source.Sessions(ctx, item.Store, Query{IncludeChildren: true})
		if err != nil {
			problems = append(problems, Problem{Source: item.Source.ID(), Store: item.Store.Path, Err: err})
			continue
		}
		for _, summary := range summaries {
			candidates = append(candidates, Candidate{
				Ref:       summary.Ref,
				Title:     summary.Title,
				UpdatedMs: summary.UpdatedMs,
			})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if cmp := compareInt64(left.UpdatedMs, right.UpdatedMs); cmp != 0 {
			return cmp > 0
		}
		if left.Ref.Source != right.Ref.Source {
			return left.Ref.Source < right.Ref.Source
		}
		return left.Ref.ID < right.Ref.ID
	})
	return candidates, problems
}
