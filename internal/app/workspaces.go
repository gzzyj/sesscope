package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/gzzyj/sesscope/internal/deepworks"
	"github.com/gzzyj/sesscope/internal/session"
	"github.com/gzzyj/sesscope/internal/source"
)

// WorkspaceRow 是 workspaces 命令的一行。
type WorkspaceRow struct {
	Workspace session.Workspace
	Port      int
	Missing   bool
	HasCount  bool
}

// WorkspaceReport 是 workspaces 的完整结果。
type WorkspaceReport struct {
	Rows     []WorkspaceRow
	Notes    []string
	Problems []source.Problem
}

// WorkspaceJSON 是 --json 输出的一行（扁平化，脚本好消费）。
type WorkspaceJSON struct {
	session.Workspace
	Port    int  `json:"port,omitempty"`
	Missing bool `json:"missing,omitempty"`
}

// Items 把内部行结构转成扁平 JSON 行。
func (r *WorkspaceReport) Items() []WorkspaceJSON {
	out := make([]WorkspaceJSON, 0, len(r.Rows))
	for _, row := range r.Rows {
		out = append(out, WorkspaceJSON{Workspace: row.Workspace, Port: row.Port, Missing: row.Missing})
	}
	return out
}

// Workspaces 汇总跨源工作区，并在 opencode 上叠加 DeepWorks 的应用态工作区。
//
// 为什么需要叠加：opencode 的 project 表按 worktree 懒创建（本机只有 2 行），
// 而 DeepWorks 的"我的工作区"有 4 个，且 session.workspace_id 一直是 NULL。
// 只看库会少列工作区——这是当初"我的 4 个工作区只看到 2 个"的根因。
func (e *Env) Workspaces(ctx context.Context, names []string, locator source.Locator, includeAppState, includeEmpty bool) (*WorkspaceReport, error) {
	selection, err := e.Select(ctx, names, locator)
	if err != nil {
		return nil, err
	}
	readable := selection.Readable()
	report := &WorkspaceReport{Problems: selection.Problems}

	type dirStats struct {
		sessions int
		roots    int
		lastMs   *int64
	}
	stats := map[string]dirStats{}
	for _, item := range readable {
		summaries, err := item.Source.Sessions(ctx, item.Store, source.Query{IncludeChildren: true})
		if err != nil {
			report.Problems = append(report.Problems, source.Problem{Source: item.Source.ID(), Store: item.Store.Path, Err: err})
			continue
		}
		for _, summary := range summaries {
			key := normalizeDir(summary.Directory)
			current := stats[key]
			current.sessions++
			if !summary.IsChild {
				current.roots++
			}
			if summary.UpdatedMs != nil && (current.lastMs == nil || *summary.UpdatedMs > *current.lastMs) {
				current.lastMs = summary.UpdatedMs
			}
			stats[key] = current
		}
	}

	// 1) 应用态工作区（DeepWorks）：名字/端口/凭据都在那边。
	if includeAppState {
		state, stateErr := deepworks.ReadState()
		if stateErr != nil {
			report.Notes = append(report.Notes, "没读到 DeepWorks 工作区状态（"+stateErr.Error()+"），退回各来源自己的工作区")
		} else {
			report.Notes = append(report.Notes, "工作区来自 "+state.ConfigDir)
			report.Notes = append(report.Notes, state.Warnings...)
			for _, workspace := range state.Workspaces {
				row := WorkspaceRow{
					Workspace: session.Workspace{
						Source:    session.Opencode,
						Key:       workspace.ID,
						ID:        workspace.ID,
						Name:      workspace.Label(),
						Directory: workspace.Path,
						Kind:      workspace.WorkspaceType,
						Origin:    "app-state",
					},
					Port:    workspace.APIPort,
					Missing: workspace.DirectoryMissing,
				}
				if row.Workspace.Kind == "" {
					row.Workspace.Kind = "local"
				}
				if item, ok := stats[normalizeDir(workspace.Path)]; ok {
					row.Workspace.SessionCount = item.sessions
					row.Workspace.RootCount = item.roots
					row.Workspace.LastActiveMs = item.lastMs
					row.HasCount = true
				}
				report.Rows = append(report.Rows, row)
			}
		}
	}

	// 2) 各来源自己的工作区（opencode = project；其它源若没有原生概念就从会话目录推）。
	known := map[string]bool{}
	for _, row := range report.Rows {
		known[normalizeDir(row.Workspace.Directory)] = true
	}
	for _, item := range readable {
		lister, ok := item.Source.(source.WorkspaceLister)
		if !ok {
			continue
		}
		workspaces, err := lister.Workspaces(ctx, item.Store)
		if err != nil {
			report.Problems = append(report.Problems, source.Problem{Source: item.Source.ID(), Store: item.Store.Path, Err: err})
			continue
		}
		for _, workspace := range workspaces {
			key := normalizeDir(workspace.Directory)
			if key != "" && known[key] {
				continue
			}
			if key != "" {
				known[key] = true
			}
			row := WorkspaceRow{Workspace: workspace, HasCount: true}
			if item, ok := stats[key]; ok {
				row.Workspace.SessionCount = item.sessions
				row.Workspace.RootCount = item.roots
				row.Workspace.LastActiveMs = item.lastMs
			}
			report.Rows = append(report.Rows, row)
		}
	}

	// 3) 只按会话目录兜底：既没有应用态、也没有原生工作区的目录，也要能看到。
	directories := make([]string, 0, len(stats))
	for directory := range stats {
		if directory != "" && !known[directory] {
			directories = append(directories, directory)
		}
	}
	sort.Strings(directories)
	for _, directory := range directories {
		item := stats[directory]
		report.Rows = append(report.Rows, WorkspaceRow{
			Workspace: session.Workspace{
				Source:       session.Opencode,
				Key:          directory,
				ID:           "-",
				Name:         baseName(directory),
				Directory:    directory,
				Kind:         "session-dir",
				Origin:       "derived",
				SessionCount: item.sessions,
				RootCount:    item.roots,
				LastActiveMs: item.lastMs,
			},
			HasCount: true,
		})
	}

	if !includeEmpty {
		filtered := make([]WorkspaceRow, 0, len(report.Rows))
		for _, row := range report.Rows {
			if row.HasCount && row.Workspace.SessionCount == 0 {
				continue
			}
			filtered = append(filtered, row)
		}
		report.Rows = filtered
	}
	return report, nil
}

func baseName(path string) string {
	trimmed := strings.TrimRight(strings.ReplaceAll(path, string(rune(92)), "/"), "/")
	if index := strings.LastIndex(trimmed, "/"); index >= 0 {
		return trimmed[index+1:]
	}
	return trimmed
}

// DescribeProblems 把问题拼成话，给 stderr 用。
func DescribeProblems(problems []source.Problem) []string {
	out := make([]string, 0, len(problems))
	for _, problem := range problems {
		out = append(out, fmt.Sprintf("提示：%v", problem))
	}
	return out
}
