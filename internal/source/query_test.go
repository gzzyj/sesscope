package source

import (
	"errors"
	"strings"
	"testing"

	"github.com/gzzyj/sesscope/internal/session"
)

func allSources() []session.SourceID {
	return []session.SourceID{session.Opencode, session.DSH, session.Codex, session.Claude}
}

func TestParseSelectorQualifiedKeepsSource(t *testing.T) {
	got, err := ParseSelector("dsh:session-59b8", allSources())
	if err != nil {
		t.Fatalf("解析 source:id：%v", err)
	}
	if got.Source != session.DSH || got.Prefix != "session-59b8" {
		t.Errorf("selector = %+v", got)
	}
	if got.String() != "dsh:session-59b8" {
		t.Errorf("String() = %q", got.String())
	}
}

func TestParseSelectorBareRejectsMultipleSources(t *testing.T) {
	_, err := ParseSelector("session-59b8", allSources())
	if !errors.Is(err, ErrSourceRequired) {
		t.Fatalf("裸 id 跨来源应报 ErrSourceRequired，实际：%v", err)
	}
	var required *SourceRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("错误类型 = %T", err)
	}
	if required.Selector != "session-59b8" || len(required.Scope) != 4 {
		t.Errorf("err = %+v", required)
	}
	if !strings.Contains(err.Error(), "--source") {
		t.Errorf("错误信息应给出修法：%s", err)
	}
}

func TestParseSelectorBareBindsSingleSource(t *testing.T) {
	got, err := ParseSelector(" ses_root ", []session.SourceID{session.Opencode})
	if err != nil {
		t.Fatalf("单一来源下裸 id 应可解析：%v", err)
	}
	if got.Source != session.Opencode || got.Prefix != "ses_root" {
		t.Errorf("selector = %+v", got)
	}
}

func TestParseSelectorRejectsSourceOutsideScope(t *testing.T) {
	_, err := ParseSelector("opencode:ses_root", []session.SourceID{session.DSH})
	if !errors.Is(err, ErrSourceMismatch) {
		t.Fatalf("来源冲突应报 ErrSourceMismatch，实际：%v", err)
	}
	if !strings.Contains(err.Error(), "opencode") || !strings.Contains(err.Error(), "dsh") {
		t.Errorf("错误信息应带上双方来源：%s", err)
	}
}

func TestParseSelectorRejectsEmptyID(t *testing.T) {
	if _, err := ParseSelector("dsh:", allSources()); err == nil {
		t.Fatal("\"dsh:\" 的空 id 不该被当成匹配全部")
	}
	if _, err := ParseSelector("   ", allSources()); !errors.Is(err, ErrNotFound) {
		t.Errorf("空选择器应报 ErrNotFound，实际：%v", err)
	}
}

func TestFindNeverCrossesSources(t *testing.T) {
	candidates := []Candidate{
		{Ref: session.Ref{Source: session.DSH, ID: "session-abc"}},
		{Ref: session.Ref{Source: session.Claude, ID: "abc-123"}},
		{Ref: session.Ref{Source: session.Claude, ID: "abc-456"}},
	}
	// dsh 的 id 只是 claude 的语义前缀，跨源不算命中。
	if matched := Find(candidates, Selector{Source: session.DSH, Prefix: "abc"}); len(matched) != 0 {
		t.Errorf("跨源前缀不该命中：%+v", matched)
	}
	// 源内前缀命中多个：交给调用方报歧义，不猜。
	if matched := Find(candidates, Selector{Source: session.Claude, Prefix: "abc"}); len(matched) != 2 {
		t.Errorf("源内前缀应命中 2 个，实际 %d", len(matched))
	}
	if matched := Find(candidates, Selector{Source: session.DSH, Prefix: "session-a"}); len(matched) != 1 {
		t.Errorf("源内前缀应命中 1 个，实际 %d", len(matched))
	}
}
