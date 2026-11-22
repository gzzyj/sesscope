// Package audit 是"抽象会话"的字段门禁：拿 domain model 逐字段检查，
// 把"该有却没有"的空值找出来，并指明它属于哪一层（会话/消息/部件/工具）。
//
// 两级结果：
//   - violation：身份/结构字段缺失，一定是适配器的 bug，门禁据此失败；
//   - note：可选字段缺失（用量、可读思考、工具输出……），只报告不拦截。
//
// 不算空缺的情况（避免误报）：
//   - 能力位声明没有这项数据（例如 dsh 不写 reasoning token）；
//   - 源自己写了诊断解释（encrypted-reasoning / empty-session）；
//   - 会话还在写（最后一条 assistant 没有结束时间是正常的）。
package audit

import (
	"fmt"
	"sort"

	"github.com/gzzyj/sesscope/internal/session"
	"github.com/gzzyj/sesscope/internal/source"
)

// 检查范围，用于聚合时分母。
const (
	ScopeSession = "session"
	ScopeMessage = "message"
	ScopePart    = "part"
	ScopeTool    = "tool"
)

// 结果级别。
const (
	LevelViolation = "violation"
	LevelNote      = "note"
)

// Issue 是"该有却没有"的一个字段。
type Issue struct {
	Path   string `json:"path"`   // 例如 meta.id / message.completed_ms / tool.command
	Scope  string `json:"scope"`  // session | message | part | tool
	Level  string `json:"level"`  // violation | note
	Detail string `json:"detail"` // 具体到哪条消息/哪个工具
}

// Session 检查一个会话。caps 是该来源声明支持的能力。
func Session(s *session.Session, caps source.Capabilities) []Issue {
	issues := make([]Issue, 0, 8)
	add := func(level, path, scope, detail string) {
		issues = append(issues, Issue{Path: path, Scope: scope, Level: level, Detail: detail})
	}
	violation := func(path, scope, detail string) { add(LevelViolation, path, scope, detail) }
	note := func(path, scope, detail string) { add(LevelNote, path, scope, detail) }

	if s == nil {
		return []Issue{{Path: "session", Scope: ScopeSession, Level: LevelViolation, Detail: "会话为空"}}
	}
	emptySession := hasDiagnostic(s, "empty-session")
	if s.Ref.Source == "" {
		violation("ref.source", ScopeSession, "ref 里没有来源")
	}
	if s.Ref.ID == "" {
		violation("ref.id", ScopeSession, "ref 里没有会话 id")
	}
	if s.Meta.ID == nil || *s.Meta.ID == "" {
		violation("meta.id", ScopeSession, "meta 里没有会话 id")
	}
	if s.Meta.Title == nil || *s.Meta.Title == "" {
		if emptySession {
			note("meta.title", ScopeSession, "空会话（有 empty-session 诊断）")
		} else {
			violation("meta.title", ScopeSession, "没有标题（源内标题或首条人类输入都为空）")
		}
	}
	if s.Meta.CreatedMs == nil {
		violation("meta.created_ms", ScopeSession, "没有会话起始时间")
	}
	if s.Meta.UpdatedMs == nil {
		if emptySession {
			note("meta.updated_ms", ScopeSession, "空会话（有 empty-session 诊断）")
		} else {
			violation("meta.updated_ms", ScopeSession, "没有会话最后活动时间")
		}
	}
	if s.Meta.Provenance.Source == "" {
		violation("meta.provenance.source", ScopeSession, "没有来源标注")
	}
	if s.Meta.Provenance.Format == "" {
		violation("meta.provenance.format", ScopeSession, "没有格式标注")
	}
	if len(s.Messages) == 0 {
		if emptySession {
			note("messages", ScopeSession, "空会话（有 empty-session 诊断）")
		} else {
			violation("messages", ScopeSession, "会话里没有任何消息")
		}
	}
	if s.Meta.Directory == nil || *s.Meta.Directory == "" {
		note("meta.directory", ScopeSession, "没有工作目录")
	}
	if s.Meta.Agent == nil || *s.Meta.Agent == "" {
		note("meta.agent", ScopeSession, "没有 agent 标识")
	}

	encryptedReasoning := hasDiagnostic(s, "encrypted-reasoning")
	lastIndex := len(s.Messages) - 1
	for index := range s.Messages {
		message := s.Messages[index]
		label := messageLabel(message, index)
		if message.ID == "" {
			violation("message.id", ScopeMessage, label)
		}
		if message.Role == "" {
			violation("message.role", ScopeMessage, label)
		}
		if message.CreatedMs == nil {
			violation("message.created_ms", ScopeMessage, label)
		}
		if len(message.Parts) == 0 && message.Error == nil {
			// 失败的模型调用本来就可能没有内容（例如 403 额度不足），有错误就不算结构缺失。
			violation("message.parts", ScopeMessage, label)
		}
		if message.Role == session.RoleAssistant {
			if message.CompletedMs == nil {
				if index == lastIndex {
					note("message.completed_ms", ScopeMessage, label+"（最后一条，可能还在写）")
				} else {
					violation("message.completed_ms", ScopeMessage, label+"（assistant 没有结束时间）")
				}
			}
			if caps.Tokens {
				if message.Tokens == nil {
					note("message.tokens", ScopeMessage, label+"（assistant 没有用量）")
				} else {
					if message.Tokens.Input == nil {
						note("message.tokens.input", ScopeMessage, label)
					}
					if message.Tokens.Output == nil {
						note("message.tokens.output", ScopeMessage, label)
					}
				}
			}
		}
		for partIndex := range message.Parts {
			auditPart(&message.Parts[partIndex], caps, encryptedReasoning, label, violation, note)
		}
	}
	return issues
}

func auditPart(part *session.Part, caps source.Capabilities, encryptedReasoning bool, label string, violation, note func(path, scope, detail string)) {
	if part.ID == "" {
		violation("part.id", ScopePart, label)
	}
	if part.Kind == "" {
		violation("part.kind", ScopePart, label)
	}
	switch part.Kind {
	case session.PartText:
		synthetic := part.Synthetic != nil && *part.Synthetic
		if (part.Text == nil || *part.Text == "") && !synthetic {
			// 合成占位（例如 dsh 补的空用户输入）本来就是空文本，不算缺失。
			violation("part.text", ScopePart, label+"（text 部件没有内容）")
		}
	case session.PartReasoning:
		encrypted := part.Encrypted != nil && *part.Encrypted
		if (part.Text == nil || *part.Text == "") && !encrypted && !encryptedReasoning {
			note("part.text", ScopePart, label+"（reasoning 部件没有内容，也没有加密标注）")
		}
	case session.PartToolCall:
		auditTool(part, caps, label, violation, note)
	}
}

func auditTool(part *session.Part, caps source.Capabilities, label string, violation, note func(path, scope, detail string)) {
	if part.Tool == nil {
		violation("tool", ScopeTool, label+"（tool-call 部件没有 tool 对象）")
		return
	}
	tool := part.Tool
	name := toolName(tool)
	if tool.CallID == nil || *tool.CallID == "" {
		violation("tool.call_id", ScopeTool, name)
	}
	if tool.Name == nil || *tool.Name == "" {
		violation("tool.name", ScopeTool, name)
	}
	if caps.ToolStatus && (tool.Status == nil || *tool.Status == "") {
		violation("tool.status", ScopeTool, name)
	}
	if caps.ToolArgs && len(tool.Args) == 0 {
		note("tool.args", ScopeTool, name)
	}
	// 工具报错/被中止时，参数可能根本没凑齐（opencode 的 aborted write 只留 "{}"），
	// 这时没有 command/target 是真实的，不算结构缺失。
	if statusOf(tool) != string(session.StatusError) {
		if tool.Kind == session.ToolShell && tool.Command == "" {
			violation("tool.command", ScopeTool, name+"（shell 工具没有命令行）")
		}
		switch tool.Kind {
		case session.ToolRead, session.ToolWrite, session.ToolEdit, session.ToolList, session.ToolSearch:
			if tool.Target == "" {
				violation("tool.target", ScopeTool, name+"（"+string(tool.Kind)+" 工具没有目标）")
			}
		}
	}
	status := statusOf(tool)
	if status == string(session.StatusCompleted) || status == string(session.StatusError) {
		if tool.EndMs == nil {
			violation("tool.end_ms", ScopeTool, name+"（已完成但没有结束时间）")
		}
		if caps.ToolOutput && len(tool.Output) == 0 && len(tool.Error) == 0 && status == string(session.StatusCompleted) {
			note("tool.output", ScopeTool, name+"（已完成，但既没有输出也没有错误）")
		}
	}
}

func statusOf(tool *session.ToolCall) string {
	if tool.Status != nil {
		return *tool.Status
	}
	return ""
}

func toolName(tool *session.ToolCall) string {
	if tool.Name != nil && *tool.Name != "" {
		return "tool " + *tool.Name
	}
	if tool.CallID != nil && *tool.CallID != "" {
		return "tool(call_id=" + *tool.CallID + ")"
	}
	return "tool(未命名)"
}

func messageLabel(message session.Message, index int) string {
	id := message.ID
	if id == "" {
		id = "#" + fmt.Sprint(index+1)
	}
	if message.Role != "" {
		return "message " + id + "（" + string(message.Role) + "）"
	}
	return "message " + id
}

func hasDiagnostic(s *session.Session, code string) bool {
	for _, diagnostic := range s.Diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

// FieldIssue 是同一来源同一字段的空缺聚合。
type FieldIssue struct {
	Source  session.SourceID `json:"source"`
	Field   string           `json:"field"`
	Scope   string           `json:"scope"`
	Level   string           `json:"level"`
	Count   int              `json:"count"`
	Samples []string         `json:"samples,omitempty"`
}

// Aggregator 按（来源, 级别, 字段）聚合多份会话的检查结果。
type Aggregator struct {
	scanned map[session.SourceID]int
	totals  map[session.SourceID]map[string]int
	issues  map[session.SourceID]map[string]*FieldIssue
}

// NewAggregator 建一个聚合器。
func NewAggregator() *Aggregator {
	return &Aggregator{
		scanned: map[session.SourceID]int{},
		totals:  map[session.SourceID]map[string]int{},
		issues:  map[session.SourceID]map[string]*FieldIssue{},
	}
}

// Add 记录一个会话的检查结果。sessionID 用于示例采样。
func (a *Aggregator) Add(s *session.Session, sessionID string, caps source.Capabilities) int {
	var sourceID session.SourceID
	if s != nil {
		sourceID = s.Ref.Source
	}
	a.scanned[sourceID]++
	if a.totals[sourceID] == nil {
		a.totals[sourceID] = map[string]int{}
		a.issues[sourceID] = map[string]*FieldIssue{}
	}
	issues := Session(s, caps)
	if s != nil {
		a.totals[sourceID][ScopeSession]++
		a.totals[sourceID][ScopeMessage] += len(s.Messages)
		for _, message := range s.Messages {
			a.totals[sourceID][ScopePart] += len(message.Parts)
			for _, part := range message.Parts {
				if part.Kind == session.PartToolCall {
					a.totals[sourceID][ScopeTool]++
				}
			}
		}
	}
	for _, issue := range issues {
		key := issue.Level + "|" + issue.Path
		bucket := a.issues[sourceID][key]
		if bucket == nil {
			bucket = &FieldIssue{Source: sourceID, Field: issue.Path, Scope: issue.Scope, Level: issue.Level}
			a.issues[sourceID][key] = bucket
		}
		bucket.Count++
		if len(bucket.Samples) < 3 {
			bucket.Samples = append(bucket.Samples, sessionID+": "+issue.Detail)
		}
	}
	return len(issues)
}

// Scanned 返回每个来源扫描过的会话数。
func (a *Aggregator) Scanned() map[session.SourceID]int { return a.scanned }

// Totals 返回每个来源各范围的实体总数（做比例用）。
func (a *Aggregator) Totals() map[session.SourceID]map[string]int { return a.totals }

// Issues 返回排序后的聚合结果：来源 → 违例优先 → 数量倒序 → 字段名。
func (a *Aggregator) Issues() []FieldIssue {
	out := make([]FieldIssue, 0, len(a.issues)*4)
	for _, byField := range a.issues {
		for _, issue := range byField {
			out = append(out, *issue)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		if out[i].Level != out[j].Level {
			return out[i].Level == LevelViolation
		}
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Field < out[j].Field
	})
	return out
}

// Violations 统计违例总数（门禁失败条件）。
func (a *Aggregator) Violations() int {
	total := 0
	for _, byField := range a.issues {
		for _, issue := range byField {
			if issue.Level == LevelViolation {
				total += issue.Count
			}
		}
	}
	return total
}
