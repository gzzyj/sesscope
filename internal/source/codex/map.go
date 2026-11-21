package codex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/gzzyj/sesscope/internal/session"
	"github.com/gzzyj/sesscope/internal/session/toolsem"
	"github.com/gzzyj/sesscope/internal/source"
	"github.com/gzzyj/sesscope/internal/source/jsonl"
)

type toolRef struct {
	message int
	part    int
}

// pendingSpan 是 item_completed 里记录、但要等对应 response_item 落地后才能挂上的区间。
// codex 先写 item_completed（带 started_at_ms/completed_at_ms），再写 response_item；
// 顺序反过来会让"先建 part 再补时间"变成"先补时间再建 part"，所以这里延后到 finalize。
type pendingSpan struct {
	id     string
	kind   string // reasoning | agent
	turnID string
	start  int64
	end    int64
}

type mapper struct {
	out            *session.Session
	opts           source.OpenOptions
	turnID         string
	turnNumber     int
	tools          map[string]toolRef
	pending        []pendingSpan
	lastAssistant  int
	userFromItems  bool
	systemFromUser []int
	encryptedSeen  bool
	derivedStatus  bool
	truncated      bool
}

func mapRollout(ctx context.Context, path string, store source.Store, opts source.OpenOptions) (*session.Session, error) {
	if opts.MaxTextBytes <= 0 {
		opts.MaxTextBytes = 256 << 10
	}
	out := &session.Session{
		Ref: session.Ref{Source: session.Codex, Store: store.Path, Object: path},
		Meta: session.Meta{Provenance: session.Provenance{
			Source: session.Codex, Store: store.Path, Format: formatName, LoadedAt: time.Now().UnixMilli(), Complete: true,
		}},
		Diagnostics: make([]session.Diagnostic, 0, 3),
		Unknown:     map[string]int{},
	}
	m := &mapper{out: out, opts: opts, tools: map[string]toolRef{}, lastAssistant: -1}
	err := jsonl.Each(ctx, path, m.handle)
	if err != nil {
		var incomplete *jsonl.IncompleteError
		if errors.As(err, &incomplete) {
			out.Meta.Provenance.Complete = false
			out.Diagnostics = append(out.Diagnostics, session.Diagnostic{
				Level: "warn", Code: "torn-tail", Title: "文件尾部不完整", Detail: incomplete.Error(),
			})
		} else {
			return nil, err
		}
	}
	m.finalize()
	return out, nil
}

func (m *mapper) handle(number int, line []byte) error {
	var record rolloutRecord
	if err := json.Unmarshal(line, &record); err != nil {
		m.out.Unknown["line:unparsable"]++
		return nil
	}
	switch record.Type {
	case "session_meta":
		var meta sessionMeta
		if err := json.Unmarshal(record.Payload, &meta); err == nil {
			m.applyMeta(meta)
		}
	case "turn_context":
		var data turnContext
		if err := json.Unmarshal(record.Payload, &data); err == nil {
			m.turnID = data.TurnID
		}
	case "event_msg":
		m.applyEvent(record)
	case "response_item":
		m.applyResponseItem(record)
	case "token_usage_record":
		var data tokenUsage
		if err := json.Unmarshal(record.Payload, &data); err == nil {
			m.applyTokens(data)
		}
	case "compacted":
		m.applyCompacted(record)
	default:
		if knownIgnored[record.Type] {
			return nil
		}
		m.out.Unknown[record.Type]++
	}
	return nil
}

func (m *mapper) applyMeta(meta sessionMeta) {
	id := meta.SessionID
	if id == "" {
		id = meta.ID
	}
	if id != "" {
		m.out.Ref.ID = id
		m.out.Meta.ID = session.Str(id)
	}
	if meta.Cwd != "" {
		m.out.Meta.Directory = session.Str(meta.Cwd)
		m.out.Meta.WorkspaceKey = meta.Cwd
	}
	if meta.CLIVersion != "" {
		m.out.Meta.Version = session.Str(meta.CLIVersion)
	}
	if meta.Originator != "" {
		m.out.Meta.Agent = session.Str(meta.Originator)
	}
	if meta.ModelProvider != "" {
		m.out.Meta.Model = &session.ModelRef{Provider: session.Str(meta.ModelProvider)}
	}
	if ms, ok := parseISO(meta.Timestamp); ok {
		m.out.Meta.CreatedMs = session.I64(ms)
	}
}

func (m *mapper) applyEvent(record rolloutRecord) {
	var event eventPayload
	if err := json.Unmarshal(record.Payload, &event); err != nil {
		m.out.Unknown["event_msg:unparsable"]++
		return
	}
	at, _ := parseISO(record.Timestamp)
	switch event.Type {
	case "task_started":
		m.turnNumber++
	case "task_complete":
		if event.Error != nil && event.Error.Message != "" {
			if m.lastAssistant >= 0 {
				m.out.Messages[m.lastAssistant].Error = &session.Error{Name: session.Str("TaskError"), Message: session.Str(event.Error.Message)}
			}
		}
		if m.lastAssistant >= 0 && m.out.Messages[m.lastAssistant].CompletedMs == nil && event.CompletedAtMs != nil {
			m.out.Messages[m.lastAssistant].CompletedMs = event.CompletedAtMs
		}
	case "turn_aborted":
		if m.lastAssistant >= 0 {
			m.out.Messages[m.lastAssistant].Error = &session.Error{Name: session.Str("Aborted")}
			m.out.Messages[m.lastAssistant].CompletedMs = session.I64(at)
		}
	case "item_completed":
		m.applyCompletedItem(event, at)
	case "thread_settings_applied":
		return
	default:
		if knownIgnored["event_msg/"+event.Type] {
			return
		}
		m.out.Unknown["event_msg/"+event.Type]++
	}
}

// applyCompletedItem：codex 只有 item_completed{UserMessage} 才是"人类轮次"。
// response_item 里 role=user 的内容混有 AGENTS.md / 环境注入，不能当轮次。
func (m *mapper) applyCompletedItem(event eventPayload, at int64) {
	if len(event.Item) == 0 {
		return
	}
	var completed item
	if err := json.Unmarshal(event.Item, &completed); err != nil {
		return
	}
	switch completed.Type {
	case "Reasoning", "AgentMessage":
		m.captureModelSpan(completed, event)
		return
	case "UserMessage":
		// 人类轮次锚：只有它能把 role=user 的注入内容区分开（见下方注释）。
	default:
		// 工具类 item（McpToolCall / CommandExecution ...）的 started/completed
		// 只差 0-1ms，不如"调用记录 → 结果记录"的时间差可靠，继续用后者。
		return
	}
	m.userFromItems = true
	parts := make([]session.Part, 0, len(completed.Content))
	for index, block := range completed.Content {
		text := m.clip(block.Text)
		if strings.TrimSpace(text) == "" {
			continue
		}
		parts = append(parts, session.Part{
			ID:        completed.ID + "#" + itoa(index),
			Kind:      session.PartText,
			Type:      "text",
			CreatedMs: session.I64(at),
			UpdatedMs: session.I64(at),
			Text:      &text,
		})
	}
	created := at
	if event.StartedAtMs != nil {
		created = *event.StartedAtMs
	}
	id := completed.ID
	if id == "" {
		id = "user-" + itoa(len(m.out.Messages))
	}
	m.out.Messages = append(m.out.Messages, session.Message{
		ID:          id,
		Role:        session.RoleUser,
		PayloadRole: session.Str("user"),
		CreatedMs:   session.I64(created),
		UpdatedMs:   session.I64(at),
		TurnHint:    session.TurnHint{TurnID: m.turnID},
		Parts:       parts,
	})
}

// captureModelSpan 记下 item_completed 里 Reasoning / AgentMessage 的真实起止。
//
// rollout 里每条 response_item 只带一个"写入时刻"，真正的起止在 item_completed 的
// started_at_ms/completed_at_ms。思考内容常常是加密的（没有 Part 可挂），但这段模型
// 活动确实发生过，所以区间单独存进 Message.ModelSpans，派生层用它画模型泳道。
func (m *mapper) captureModelSpan(completed item, event eventPayload) {
	if event.StartedAtMs == nil || event.CompletedAtMs == nil {
		return
	}
	start, end := *event.StartedAtMs, *event.CompletedAtMs
	if end < start {
		return
	}
	kind := "reasoning"
	if completed.Type == "AgentMessage" {
		kind = "agent"
	}
	m.pending = append(m.pending, pendingSpan{id: completed.ID, kind: kind, turnID: m.turnID, start: start, end: end})
}

// applyPendingSpans 在 finalize 阶段把区间挂到具体的消息/部件上（此时 response_item 已落地）。
func (m *mapper) applyPendingSpans() {
	unattached := 0
	for _, span := range m.pending {
		index, partIndex := m.spanOwner(span)
		if index < 0 {
			unattached++
			continue
		}
		message := &m.out.Messages[index]
		if span.kind == "agent" {
			message.CreatedMs = session.I64(span.start)
			message.CompletedMs = session.I64(span.end)
			for position := range message.Parts {
				part := &message.Parts[position]
				if part.Kind != session.PartText {
					continue
				}
				part.CreatedMs = session.I64(span.start)
				part.UpdatedMs = session.I64(span.end)
				part.StartMs = session.I64(span.start)
				part.EndMs = session.I64(span.end)
			}
		}
		message.ModelSpans = append(message.ModelSpans, session.ModelSpan{StartMs: span.start, EndMs: span.end})
		if partIndex >= 0 {
			part := &message.Parts[partIndex]
			part.CreatedMs = session.I64(span.start)
			part.UpdatedMs = session.I64(span.end)
			part.StartMs = session.I64(span.start)
			part.EndMs = session.I64(span.end)
		}
	}
	if unattached > 0 {
		m.out.Diagnostics = append(m.out.Diagnostics, session.Diagnostic{
			Level: "info", Code: "model-span-unattached", Title: "有模型活动区间挂不上步骤",
			Detail: itoa(unattached) + " 段 item_completed 区间找不到所属 assistant 消息（rollout 可能缺 response_item）。",
		})
	}
}

// spanOwner 找区间该挂到哪条消息：
//  1. AgentMessage：按消息 id 精确匹配（response_item 里就是同一个 id）；
//  2. Reasoning：优先找同 id 的思考 part（有文本时）；
//  3. 都没有（思考被加密）：挂到同一轮里起点最近的 assistant 步骤。
func (m *mapper) spanOwner(span pendingSpan) (int, int) {
	if span.kind == "agent" {
		for index := range m.out.Messages {
			if m.out.Messages[index].ID == span.id {
				return index, -1
			}
		}
		return -1, -1
	}
	for index := range m.out.Messages {
		for partIndex := range m.out.Messages[index].Parts {
			if m.out.Messages[index].Parts[partIndex].ID == span.id {
				return index, partIndex
			}
		}
	}
	if index := m.nearestAssistant(span, func(start int64) bool { return start <= span.start }, true); index >= 0 {
		return index, -1
	}
	return m.nearestAssistant(span, func(start int64) bool { return start >= span.start }, false), -1
}

// nearestAssistant 在同一轮的 assistant 消息里挑起点最近的一条。
func (m *mapper) nearestAssistant(span pendingSpan, keep func(int64) bool, preferLater bool) int {
	best := -1
	var bestStart int64
	for index := range m.out.Messages {
		message := m.out.Messages[index]
		if message.Role != session.RoleAssistant || message.TurnHint.TurnID != span.turnID || message.CreatedMs == nil {
			continue
		}
		start := *message.CreatedMs
		if !keep(start) {
			continue
		}
		if best < 0 || (preferLater && start > bestStart) || (!preferLater && start < bestStart) {
			best, bestStart = index, start
		}
	}
	return best
}

func (m *mapper) applyResponseItem(record rolloutRecord) {
	var item responseItem
	if err := json.Unmarshal(record.Payload, &item); err != nil {
		m.out.Unknown["response_item:unparsable"]++
		return
	}
	at, _ := parseISO(record.Timestamp)
	switch item.Type {
	case "message":
		m.applyMessage(item, at)
	case "reasoning":
		m.applyReasoning(item, at)
	case "function_call", "custom_tool_call":
		m.applyToolCall(item, at)
	case "function_call_output", "custom_tool_call_output":
		m.applyToolOutput(item, at)
	default:
		if knownIgnored["response_item/"+item.Type] {
			return
		}
		m.out.Unknown["response_item/"+item.Type]++
	}
}

func (m *mapper) applyMessage(item responseItem, at int64) {
	role := session.RoleSystem
	payloadRole := item.Role
	switch item.Role {
	case "assistant":
		role = session.RoleAssistant
	case "user", "developer", "system":
		role = session.RoleSystem
	}
	parts := make([]session.Part, 0, len(item.Content))
	for index, block := range item.Content {
		text := m.clip(block.Text)
		if strings.TrimSpace(text) == "" {
			continue // 空 content 块（占位/图片说明缺失）不产生空文本部件
		}
		parts = append(parts, session.Part{
			ID:        item.ID + "#" + itoa(index),
			Kind:      session.PartText,
			Type:      "text",
			CreatedMs: session.I64(at),
			UpdatedMs: session.I64(at),
			Text:      &text,
		})
	}
	id := item.ID
	if id == "" {
		id = "item-" + itoa(len(m.out.Messages))
	}
	message := session.Message{
		ID:          id,
		Role:        role,
		PayloadRole: session.Str(payloadRole),
		CreatedMs:   session.I64(at),
		UpdatedMs:   session.I64(at),
		TurnHint:    session.TurnHint{TurnID: m.turnID},
		Parts:       parts,
	}
	if item.Role == "user" || item.Role == "developer" || item.Role == "system" {
		message.Synthetic = true
	}
	if role == session.RoleAssistant && m.lastAssistant >= 0 {
		m.closeOpenAssistant(m.lastAssistant)
	}
	m.out.Messages = append(m.out.Messages, message)
	if role == session.RoleAssistant {
		m.lastAssistant = len(m.out.Messages) - 1
		return
	}
	if item.Role == "user" {
		m.systemFromUser = append(m.systemFromUser, len(m.out.Messages)-1)
	}
}

// closeOpenAssistant 给上一条 assistant 补结束时间：它最后一个 part 的 end。
// codex 只在 task_complete 写整轮结束；中途由工具调用聚合出来的 assistant 消息没有结束记录，
// 但"最后一条工具结果写完"就是这条消息的结束——这是用真实 part 时间收口，不是编造。
func (m *mapper) closeOpenAssistant(index int) {
	message := &m.out.Messages[index]
	if message.CompletedMs != nil {
		return
	}
	var end *int64
	for position := range message.Parts {
		part := message.Parts[position]
		if part.EndMs != nil && (end == nil || *part.EndMs > *end) {
			end = part.EndMs
		}
		if part.Tool != nil && part.Tool.EndMs != nil && (end == nil || *part.Tool.EndMs > *end) {
			end = part.Tool.EndMs
		}
	}
	if end != nil {
		message.CompletedMs = end
	}
}

func (m *mapper) applyReasoning(item responseItem, at int64) {
	text := ""
	for _, block := range item.Summary {
		if block.Text != "" {
			if text != "" {
				text += string(rune(10))
			}
			text += block.Text
		}
	}
	if text == "" {
		if item.EncryptedContent != "" {
			m.encryptedSeen = true
		}
		return
	}
	part := session.Part{
		ID:        item.ID,
		Kind:      session.PartReasoning,
		Type:      "reasoning",
		CreatedMs: session.I64(at),
		UpdatedMs: session.I64(at),
		StartMs:   session.I64(at),
		EndMs:     session.I64(at),
		Text:      session.Str(m.clip(text)),
	}
	m.appendToLastAssistant(part)
}

func (m *mapper) applyToolCall(item responseItem, at int64) {
	args := item.Arguments
	if len(args) == 0 {
		args = item.Input
	}
	args = toolsem.Unwrap(args)
	if len(args) > 0 && args[0] != '{' {
		// custom_tool_call 的 input 是脚本文本：包成 JSON 字符串，保持字节可控。
		args = marshalString(string(args))
	}
	call := &session.ToolCall{
		CallID:  session.Str(item.CallID),
		Name:    session.Str(item.Name),
		Args:    args,
		StartMs: session.I64(at),
	}
	call.Kind, call.Target, call.Command = toolsem.Classify(session.Codex, item.Name, args)
	index := m.ensureAssistant(at)
	message := &m.out.Messages[index]
	message.Parts = append(message.Parts, session.Part{
		ID:        item.ID,
		Kind:      session.PartToolCall,
		Type:      "tool-call",
		CreatedMs: session.I64(at),
		UpdatedMs: session.I64(at),
		StartMs:   session.I64(at),
		Tool:      call,
	})
	m.tools[item.CallID] = toolRef{message: index, part: len(message.Parts) - 1}
}

func (m *mapper) applyToolOutput(item responseItem, at int64) {
	ref, ok := m.tools[item.CallID]
	if !ok {
		m.out.Unknown["tool-output:unpaired"]++
		return
	}
	message := &m.out.Messages[ref.message]
	part := &message.Parts[ref.part]
	if part.Tool == nil {
		return
	}
	text := m.clip(decodeOutput(item.Output))
	part.Tool.Output = marshalString(text)
	part.Tool.EndMs = session.I64(at)
	part.EndMs = session.I64(at)
	part.UpdatedMs = session.I64(at)
	// codex 不写工具状态：有输出就认为完成——这是推断，必须说出来。
	status := "completed"
	part.Tool.Status = &status
	m.derivedStatus = true
	message.UpdatedMs = session.I64(at)
}

func (m *mapper) applyTokens(data tokenUsage) {
	tokens := &session.Tokens{
		Input:     data.Usage.InputTokens,
		Output:    data.Usage.OutputTokens,
		Reasoning: data.Usage.ReasoningOutputTokens,
		Total:     data.Usage.TotalTokens,
		CacheRead: data.Usage.CachedInputTokens,
	}
	index := m.findByTurnID(data.TurnID)
	if index < 0 {
		index = m.lastAssistant
	}
	if index >= 0 && index < len(m.out.Messages) {
		m.out.Messages[index].Tokens = tokens
	}
}

func (m *mapper) findByTurnID(turnID string) int {
	if turnID == "" {
		return -1
	}
	for index := len(m.out.Messages) - 1; index >= 0; index-- {
		message := m.out.Messages[index]
		if message.Role == session.RoleAssistant && message.TurnHint.TurnID == turnID {
			return index
		}
	}
	return -1
}

func (m *mapper) applyCompacted(record rolloutRecord) {
	var data compactedPayload
	_ = json.Unmarshal(record.Payload, &data)
	at, _ := parseISO(record.Timestamp)
	id := "compaction-" + itoa(len(m.out.Messages))
	m.out.Messages = append(m.out.Messages, session.Message{
		ID:          id,
		Role:        session.RoleUser,
		PayloadRole: session.Str("user"),
		CreatedMs:   session.I64(at),
		UpdatedMs:   session.I64(at),
		Parts: []session.Part{{
			ID:        id,
			Kind:      session.PartCompaction,
			Type:      "compaction",
			CreatedMs: session.I64(at),
			UpdatedMs: session.I64(at),
		}},
	})
	m.out.Diagnostics = append(m.out.Diagnostics, session.Diagnostic{
		Level: "info", Code: "compaction-no-duration", Title: "压缩没有耗时",
		Detail: "codex 只记录 compacted 事件，不写起止时间；页面把这段压缩画成零时长。",
	})
}

func (m *mapper) ensureAssistant(at int64) int {
	if m.lastAssistant >= 0 {
		return m.lastAssistant
	}
	index := len(m.out.Messages)
	id := "assistant-" + itoa(index)
	m.out.Messages = append(m.out.Messages, session.Message{
		ID:          id,
		Role:        session.RoleAssistant,
		PayloadRole: session.Str("assistant"),
		CreatedMs:   session.I64(at),
		UpdatedMs:   session.I64(at),
		TurnHint:    session.TurnHint{TurnID: m.turnID},
	})
	m.lastAssistant = index
	return index
}

func (m *mapper) appendToLastAssistant(part session.Part) {
	index := m.ensureAssistant(*componentTime(part))
	message := &m.out.Messages[index]
	message.Parts = append(message.Parts, part)
}

func componentTime(part session.Part) *int64 {
	if part.CreatedMs != nil {
		return part.CreatedMs
	}
	now := time.Now().UnixMilli()
	return &now
}

// finalize：老版本没有 item_completed{UserMessage} 时，把 role=user 的 response_item 提升成轮次。
func (m *mapper) finalize() {
	m.applyPendingSpans()
	if !m.userFromItems {
		for _, index := range m.systemFromUser {
			message := &m.out.Messages[index]
			if message.Synthetic {
				message.Role = session.RoleUser
				message.PayloadRole = session.Str("user")
				message.Synthetic = false
			}
		}
		if len(m.systemFromUser) > 0 {
			m.out.Diagnostics = append(m.out.Diagnostics, session.Diagnostic{
				Level: "info", Code: "derived-turn", Title: "轮次由 role=user 的记录派生",
				Detail: "这份 rollout 没有 item_completed{UserMessage}，只能退回到 response_item 的 user 角色；注入内容可能被当成人类输入。",
			})
		}
	}
	if m.encryptedSeen {
		m.out.Diagnostics = append(m.out.Diagnostics, session.Diagnostic{
			Level: "info", Code: "encrypted-reasoning", Title: "思考内容加密，本来源不提供",
			Detail: "codex 的 reasoning 只存 encrypted_content（或空 summary），页面不显示思考文本。",
		})
	}
	if m.derivedStatus {
		m.out.Diagnostics = append(m.out.Diagnostics, session.Diagnostic{
			Level: "info", Code: "derived-tool-status", Title: "工具状态是推断的",
			Detail: "codex 不写工具状态；这里按\"是否出现 output 记录\"推断完成，工具起止时间也取自记录时间戳。",
		})
	}
	if m.truncated {
		m.out.Diagnostics = append(m.out.Diagnostics, session.Diagnostic{
			Level: "warn", Code: "truncated-text", Title: "文本/工具输出按上限截断",
			Detail: "只保留前 " + itoa(m.opts.MaxTextBytes) + " 字节。",
		})
	}
	if len(m.out.Messages) == 0 {
		m.out.Diagnostics = append(m.out.Diagnostics, session.Diagnostic{
			Level: "info", Code: "empty-session", Title: "这个 rollout 里没有对话记录",
			Detail: "只有元信息/系统类记录；导出后消息为空，不是读取失败。",
		})
	}
	if len(m.out.Messages) > 0 {
		if last := m.out.Messages[len(m.out.Messages)-1]; last.UpdatedMs != nil {
			m.out.Meta.UpdatedMs = last.UpdatedMs
		}
	}
	if title := firstUserText(m.out.Messages); title != "" {
		m.out.Meta.Title = session.Str(title)
	}
}

func firstUserText(messages []session.Message) string {
	for _, message := range messages {
		if message.Role != session.RoleUser {
			continue
		}
		text := ""
		for _, part := range message.Parts {
			if part.Text != nil && *part.Text != "" {
				text += *part.Text
			}
		}
		if strings.TrimSpace(text) != "" {
			return firstLine(text)
		}
	}
	return ""
}

func (m *mapper) clip(text string) string {
	if len(text) <= m.opts.MaxTextBytes {
		return text
	}
	m.truncated = true
	return text[:m.opts.MaxTextBytes]
}

func decodeOutput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return string(raw)
}

func marshalString(text string) []byte {
	encoded, err := json.Marshal(text)
	if err != nil {
		return nil
	}
	return encoded
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	digits := make([]byte, 0, 12)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}
