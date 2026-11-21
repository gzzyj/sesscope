package source

import (
	"fmt"
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

// Selector 是"来源内"的会话选择器：来源必填，ID 是该来源命名空间内的前缀。
//
// 会话 id 只在所属来源内唯一：opencode 的 ses_…、dsh 的 session-<uuid>、
// codex/claude 的文件名 uuid 都是各产品自己的命名空间，既不保证跨源唯一，
// 也不保证跨版本稳定。把四个来源的 id 拼成一个全局命名空间来解析，
// 解析结果会随"本机装了哪些产品、哪个库读得动"而变——那是不可复现的。
type Selector struct {
	Source session.SourceID
	Prefix string
}

// String 返回规范形式 "source:prefix"，错误信息与列表输出都用它。
func (s Selector) String() string { return string(s.Source) + ":" + s.Prefix }

// ParseSelector 把用户输入解析成"来源内"选择器。
//
// 规则（两种合法写法，都强制带来源）：
//   - "dsh:session-abc"：显式来源；该来源必须在 scope 里，否则报 *SourceMismatchError。
//   - "session-abc"：仅当 scope 恰好只有一个来源时绑定到它；否则报 *SourceRequiredError。
//
// 空选择器报 ErrNotFound；"dsh:" 这种空 id 直接报错，不当成"匹配全部"。
func ParseSelector(raw string, scope []session.SourceID) (Selector, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Selector{}, ErrNotFound
	}
	if index := strings.Index(trimmed, ":"); index > 0 {
		candidate := session.SourceID(trimmed[:index])
		if isKnownSource(candidate) {
			prefix := strings.TrimSpace(trimmed[index+1:])
			if prefix == "" {
				return Selector{}, fmt.Errorf("%q：<source>:<id> 里的 id 不能为空", raw)
			}
			if !containsSource(scope, candidate) {
				return Selector{}, &SourceMismatchError{Source: candidate, Scope: scope}
			}
			return Selector{Source: candidate, Prefix: prefix}, nil
		}
	}
	switch len(scope) {
	case 0:
		return Selector{}, ErrNotFound
	case 1:
		return Selector{Source: scope[0], Prefix: trimmed}, nil
	default:
		return Selector{}, &SourceRequiredError{Selector: trimmed, Scope: scope}
	}
}

// Find 在候选里按"来源 + 源内前缀"过滤。
// 契约：跨来源永远不会互相命中，所以不存在"更长的前缀能消歧"这种跨源歧义。
func Find(candidates []Candidate, selector Selector) []Candidate {
	matched := make([]Candidate, 0, 4)
	for _, item := range candidates {
		if item.Ref.Source != selector.Source {
			continue
		}
		if !strings.HasPrefix(item.Ref.ID, selector.Prefix) {
			continue
		}
		matched = append(matched, item)
	}
	return matched
}

func isKnownSource(id session.SourceID) bool {
	switch id {
	case session.Opencode, session.DSH, session.Codex, session.Claude:
		return true
	default:
		return false
	}
}

func containsSource(scope []session.SourceID, want session.SourceID) bool {
	for _, id := range scope {
		if id == want {
			return true
		}
	}
	return false
}
