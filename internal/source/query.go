package source

import (
	"sort"
	"strings"

	"github.com/gzzyj/sesscope/internal/session"
)

// ApplyQuery 是四源共享的过滤/排序/截断语义，避免每个适配器各写一份"差不多"的实现。
//
// 顺序：前缀 → 子会话 → 工作区 → 关键字 → 时间窗 → 排序 → 截断。
func ApplyQuery(list []session.Summary, q Query) []session.Summary {
	matched := make([]session.Summary, 0, len(list))
	for _, item := range list {
		if !MatchSummary(item, q) {
			continue
		}
		matched = append(matched, item)
	}
	SortSummaries(matched, q.Sort)
	if q.Limit > 0 && len(matched) > q.Limit {
		matched = matched[:q.Limit]
	}
	return matched
}

// MatchSummary 判断一条摘要是否满足查询条件。
func MatchSummary(item session.Summary, q Query) bool {
	if q.Prefix != "" && !strings.HasPrefix(item.ID, q.Prefix) {
		return false
	}
	if !q.IncludeChildren && item.IsChild {
		return false
	}
	if q.Workspace != "" && !matchWorkspace(item, q.Workspace) {
		return false
	}
	if q.Grep != "" && !matchGrep(item, q.Grep) {
		return false
	}
	if q.SinceMs != nil {
		stamp := itemStamp(item)
		if stamp == nil || *stamp < *q.SinceMs {
			return false
		}
	}
	if q.UntilMs != nil {
		stamp := itemStamp(item)
		if stamp == nil || *stamp > *q.UntilMs {
			return false
		}
	}
	return true
}

func itemStamp(item session.Summary) *int64 {
	if item.UpdatedMs != nil {
		return item.UpdatedMs
	}
	return item.CreatedMs
}

func matchWorkspace(item session.Summary, needle string) bool {
	lowered := strings.ToLower(strings.TrimSpace(needle))
	if lowered == "" {
		return true
	}
	candidates := []string{item.WorkspaceKey, item.Directory, item.ID}
	for _, candidate := range candidates {
		loweredCandidate := strings.ToLower(candidate)
		if loweredCandidate == "" {
			continue
		}
		if loweredCandidate == lowered || strings.HasPrefix(loweredCandidate, lowered) {
			return true
		}
		if base := lowerBase(candidate); base != "" && base == lowered {
			return true
		}
		if strings.Contains(loweredCandidate, lowered) {
			return true
		}
	}
	return false
}

func matchGrep(item session.Summary, needle string) bool {
	lowered := strings.ToLower(needle)
	for _, candidate := range []string{item.Title, item.Directory, item.ID} {
		if strings.Contains(strings.ToLower(candidate), lowered) {
			return true
		}
	}
	return false
}

func lowerBase(path string) string {
	trimmed := strings.TrimRight(strings.ReplaceAll(path, "\\", "/"), "/")
	if index := strings.LastIndex(trimmed, "/"); index >= 0 {
		return strings.ToLower(trimmed[index+1:])
	}
	return strings.ToLower(trimmed)
}

// SortSummaries 就地排序：默认 updated 倒序，其次 created 倒序、tokens 倒序、messages 倒序；
// 同值时按 id 升序，保证跨源输出稳定（否则每次排序结果可能不同）。
func SortSummaries(list []session.Summary, sortBy string) {
	key := strings.TrimSpace(sortBy)
	sort.SliceStable(list, func(i, j int) bool {
		left, right := list[i], list[j]
		switch key {
		case "created":
			if cmp := compareInt64(left.CreatedMs, right.CreatedMs); cmp != 0 {
				return cmp > 0
			}
		case "tokens":
			if cmp := compareInt64(left.Tokens.Input, right.Tokens.Input); cmp != 0 {
				return cmp > 0
			}
		case "messages":
			if left.Messages != right.Messages {
				return left.Messages > right.Messages
			}
		default: // updated
			if cmp := compareInt64(left.UpdatedMs, right.UpdatedMs); cmp != 0 {
				return cmp > 0
			}
		}
		return left.ID < right.ID
	})
}

// compareInt64 把"可能缺失"的数字按"缺失最小"比较。
func compareInt64(left, right *int64) int {
	switch {
	case left == nil && right == nil:
		return 0
	case left == nil:
		return -1
	case right == nil:
		return 1
	case *left < *right:
		return -1
	case *left > *right:
		return 1
	default:
		return 0
	}
}

// Find 在已定位的存储里按"source:id 前缀"找候选。跨源歧义不猜，交给调用方报错。
func Find(candidates []Candidate, selector string) ([]Candidate, error) {
	trimmed := strings.TrimSpace(selector)
	if trimmed == "" {
		return nil, ErrNotFound
	}
	wantSource, prefix := splitSelector(trimmed)
	matched := make([]Candidate, 0, 4)
	for _, item := range candidates {
		if wantSource != "" && string(item.Ref.Source) != wantSource {
			continue
		}
		if !strings.HasPrefix(item.Ref.ID, prefix) {
			continue
		}
		matched = append(matched, item)
	}
	return matched, nil
}

// splitSelector 解析 "dsh:session-abc" 形式的规范选择器。
func splitSelector(selector string) (sourceID string, prefix string) {
	index := strings.Index(selector, ":")
	if index <= 0 {
		return "", selector
	}
	candidate := selector[:index]
	switch session.SourceID(candidate) {
	case session.Opencode, session.DSH, session.Codex, session.Claude:
		return candidate, selector[index+1:]
	default:
		return "", selector
	}
}
