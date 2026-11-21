package app

import (
	"context"

	"github.com/gzzyj/sesscope/internal/audit"
	"github.com/gzzyj/sesscope/internal/session"
	"github.com/gzzyj/sesscope/internal/source"
)

// AuditOptions 控制审计规模。
type AuditOptions struct {
	LimitPerSource int // 每源最多审计多少个（按 updated 倒序取最近的）
}

// AuditReport 是"抽象会话字段门禁"的结果。
type AuditReport struct {
	Scanned  map[session.SourceID]int            `json:"scanned"`
	Totals   map[session.SourceID]map[string]int `json:"totals"`
	Issues   []audit.FieldIssue                  `json:"issues"`
	Problems []source.Problem                    `json:"problems,omitempty"`
	Sessions int                                 `json:"sessions"`
	// Violations 是必须修的空缺数（门禁失败条件）；notes 只报告。
	Violations int `json:"violations"`
}

// Audit 逐个打开会话，检查抽象模型里"该有却没有"的字段。
//
// 每个会话走的是与 export 完全相同的 Open 路径，所以审计结果就是导出产物的问题，
// 不会出现"审计通过、导出缺字段"的两套口径。
func (e *Env) Audit(ctx context.Context, names []string, locator source.Locator, opts AuditOptions) (*AuditReport, error) {
	selection, err := e.Select(ctx, names, locator)
	if err != nil {
		return nil, err
	}
	readable := selection.Readable()
	if len(readable) == 0 {
		return nil, selection.NoStoreError()
	}
	report := &AuditReport{Problems: append([]source.Problem{}, selection.Problems...)}
	aggregator := audit.NewAggregator()
	for _, item := range readable {
		caps := item.Source.Capabilities()
		summaries, err := item.Source.Sessions(ctx, item.Store, source.Query{IncludeChildren: true})
		if err != nil {
			report.Problems = append(report.Problems, source.Problem{Source: item.Store.Source, Store: item.Store.Path, Err: err})
			continue
		}
		source.SortSummaries(summaries, "updated")
		if opts.LimitPerSource > 0 && len(summaries) > opts.LimitPerSource {
			summaries = summaries[:opts.LimitPerSource]
		}
		for _, summary := range summaries {
			opened, err := item.Source.Open(ctx, item.Store, summary.Ref, source.OpenOptions{})
			if err != nil {
				report.Problems = append(report.Problems, source.Problem{Source: item.Store.Source, Store: item.Store.Path, Err: err})
				continue
			}
			aggregator.Add(opened, summary.ID, caps)
			report.Sessions++
		}
	}
	report.Scanned = aggregator.Scanned()
	report.Totals = aggregator.Totals()
	report.Issues = aggregator.Issues()
	report.Violations = aggregator.Violations()
	return report, nil
}
