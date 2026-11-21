package app

import (
	"context"
	"strings"

	"github.com/gzzyj/sesscope/internal/session"
	"github.com/gzzyj/sesscope/internal/source"
)

// SessionList 是 sessions 命令的结果。
type SessionList struct {
	Items    []session.Summary
	Located  []source.Located
	Problems []source.Problem
	Labels   map[string]string
}

// Sessions 跨源列出会话：各源各自返回全部摘要，这里统一过滤/排序/截断，
// 保证四源的 --workspace/--grep/--sort/--limit 语义完全一致。
func (e *Env) Sessions(ctx context.Context, names []string, locator source.Locator, query source.Query) (*SessionList, error) {
	selection, err := e.Select(ctx, names, locator)
	if err != nil {
		return nil, err
	}
	readable := selection.Readable()
	if len(readable) == 0 {
		return nil, selection.NoStoreError()
	}
	list := &SessionList{Located: readable, Problems: selection.Problems, Labels: map[string]string{}}
	all := make([]session.Summary, 0, 64)
	for _, item := range readable {
		summaries, err := item.Source.Sessions(ctx, item.Store, source.Query{IncludeChildren: true})
		if err != nil {
			list.Problems = append(list.Problems, source.Problem{Source: item.Source.ID(), Store: item.Store.Path, Err: err})
			continue
		}
		all = append(all, summaries...)
	}
	list.Items = source.ApplyQuery(all, query)
	list.Labels = e.workspaceLabels(ctx, readable)
	return list, nil
}

// workspaceLabels 汇总"目录/工作区键 → 人看得懂的名字"，优先用应用态（DeepWorks）的名字。
// 取不到就留空，由调用方回退到 project id 或目录名。
func (e *Env) workspaceLabels(ctx context.Context, readable []source.Located) map[string]string {
	labels := map[string]string{}
	for _, item := range readable {
		lister, ok := item.Source.(source.WorkspaceLister)
		if !ok {
			continue
		}
		workspaces, err := lister.Workspaces(ctx, item.Store)
		if err != nil {
			continue
		}
		for _, workspace := range workspaces {
			if workspace.Name == "" {
				continue
			}
			if workspace.Key != "" {
				labels["key:"+workspace.Key] = workspace.Name
			}
			if workspace.Directory != "" {
				labels["dir:"+normalizeDir(workspace.Directory)] = workspace.Name
			}
		}
	}
	return labels
}

// WorkspaceLabel 按"工作区键 → 目录"的顺序找一个展示名。
func (l *SessionList) WorkspaceLabel(item session.Summary) string {
	if name, ok := l.Labels["dir:"+normalizeDir(item.Directory)]; ok {
		return name
	}
	if name, ok := l.Labels["key:"+item.WorkspaceKey]; ok {
		return name
	}
	return item.WorkspaceKey
}

func normalizeDir(path string) string {
	return strings.ToLower(strings.TrimRight(strings.ReplaceAll(path, string(rune(92)), "/"), "/"))
}
