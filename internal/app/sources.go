package app

import (
	"context"
	"fmt"

	"github.com/zhouyuejie/opencode-trace/internal/session"
	"github.com/zhouyuejie/opencode-trace/internal/source"
)

// StatusRow 是 sources 命令的一行：一个存储的健康度。
type StatusRow struct {
	Source       session.SourceID
	Store        source.Store
	Processes    []source.Process
	SessionCount int
	CountKnown   bool
	Note         string
}

// StatusReport 是 sources 的完整结果。
type StatusReport struct {
	Rows     []StatusRow
	Problems []source.Problem
}

// Status 汇总每个存储：能否读、什么版本、多少会话、有没有运行中的进程。
func (e *Env) Status(ctx context.Context, names []string, locator source.Locator) (*StatusReport, error) {
	selection, err := e.Select(ctx, names, locator)
	if err != nil {
		return nil, err
	}
	report := &StatusReport{Rows: make([]StatusRow, 0, len(selection.Located)), Problems: selection.Problems}
	processesBySource := map[session.SourceID][]source.Process{}
	for _, item := range selection.Sources {
		lister, ok := item.(source.ProcessLister)
		if !ok {
			continue
		}
		processes, err := lister.Processes(ctx)
		if err != nil {
			report.Problems = append(report.Problems, source.Problem{Source: item.ID(), Err: err})
			continue
		}
		processesBySource[item.ID()] = processes
	}
	for _, located := range selection.Located {
		row := StatusRow{Source: located.Store.Source, Store: located.Store, SessionCount: -1}
		row.Processes = processesBySource[located.Store.Source]
		if located.Store.Exists && located.Store.Readable {
			summaries, err := located.Source.Sessions(ctx, located.Store, source.Query{IncludeChildren: true})
			if err != nil {
				row.Note = err.Error()
			} else {
				row.SessionCount = len(summaries)
				row.CountKnown = true
			}
		}
		report.Rows = append(report.Rows, row)
	}
	return report, nil
}

// DoctorRow 是 doctor 命令的一行。
type DoctorRow struct {
	Source session.SourceID
	Store  source.Store
	Checks []source.Check
}

// Doctor 逐存储体检；不支持 Diagnoser 的来源给出"无体检项"而不是假装通过。
func (e *Env) Doctor(ctx context.Context, names []string, locator source.Locator, drift bool) ([]DoctorRow, []source.Problem, error) {
	selection, err := e.Select(ctx, names, locator)
	if err != nil {
		return nil, nil, err
	}
	rows := make([]DoctorRow, 0, len(selection.Located))
	for _, located := range selection.Located {
		row := DoctorRow{Source: located.Store.Source, Store: located.Store}
		diagnoser, ok := located.Source.(source.Diagnoser)
		if !ok {
			row.Checks = []source.Check{{Level: "info", Code: "no-doctor", Title: "该来源没有体检项", Detail: "只支持列会话与取数"}}
		} else {
			row.Checks = diagnoser.Doctor(ctx, located.Store)
		}
		if drift && located.Store.Exists && located.Store.Readable {
			row.Checks = append(row.Checks, driftCheck(ctx, located.Source, located.Store))
		}
		rows = append(rows, row)
	}
	return rows, selection.Problems, nil
}

// driftCheck 用"打开一个会话"的方式探测未知记录/事件类型。
// 这里只做抽样（最多 3 个会话），避免 doctor 变成全量扫描。
func driftCheck(ctx context.Context, item source.Source, store source.Store) source.Check {
	summaries, err := item.Sessions(ctx, store, source.Query{IncludeChildren: true})
	if err != nil {
		return source.Check{Level: "warn", Code: "drift-unavailable", Title: "无法抽样会话", Detail: err.Error()}
	}
	unknown := map[string]int{}
	sampled := 0
	for _, summary := range summaries {
		if sampled >= 3 {
			break
		}
		opened, err := item.Open(ctx, store, summary.Ref, source.OpenOptions{})
		if err != nil {
			continue
		}
		sampled++
		for kind, count := range opened.Unknown {
			unknown[kind] += count
		}
	}
	if len(unknown) == 0 {
		return source.Check{Level: "ok", Code: "drift-clean", Title: fmt.Sprintf("抽样 %d 个会话，没有未注册记录", sampled), Detail: "记录类型注册表与实机数据一致"}
	}
	detail := ""
	for kind, count := range unknown {
		detail += fmt.Sprintf("%s×%d ", kind, count)
	}
	return source.Check{Level: "warn", Code: "drift-unknown-records", Title: "出现未注册的记录类型", Detail: detail + "（可能是新版本；请补 VersionProfile 与夹具）"}
}
