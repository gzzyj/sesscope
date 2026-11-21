package dsh

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gzzyj/atrace/internal/session"
	"github.com/gzzyj/atrace/internal/session/toolsem"
	"github.com/gzzyj/atrace/internal/source"
	"github.com/gzzyj/atrace/internal/source/jsonl"
)

type toolRef struct {
	message int
	part    int
}

type mapper struct {
	out         *session.Session
	opts        source.OpenOptions
	stepMessage map[[2]int]int
	stepStart   map[[2]int]int64
	tools       map[string]toolRef
	model       *session.ModelRef
	currentTurn int
	compaction  int
	truncated   bool
}

// mapSession 流式读一个会话文件，映射成领域模型。
//
// 事件顺序（实测）：turn/start -> step/start -> [system/message] -> user/message ->
// assistant/message -> tool/call -> tool/result -> step/end -> turn/end。
// 因此工具调用挂在"最近一个 (turn,step) 的 assistant 消息"上。
func mapSession(ctx context.Context, path string, store source.Store, opts source.OpenOptions) (*session.Session, error) {
	if opts.MaxTextBytes <= 0 {
		opts.MaxTextBytes = 256 << 10
	}
	out := &session.Session{
		Ref: session.Ref{Source: session.DSH, Store: store.Path, Object: path},
		Meta: session.Meta{
			Provenance: session.Provenance{
				Source:   session.DSH,
				Store:    store.Path,
				Format:   formatName,
				LoadedAt: time.Now().UnixMilli(),
				Complete: true,
			},
		},
		Diagnostics: make([]session.Diagnostic, 0, 2),
		Unknown:     map[string]int{},
	}
	m := &mapper{
		out:         out,
		opts:        opts,
		stepMessage: map[[2]int]int{},
		stepStart:   map[[2]int]int64{},
		tools:       map[string]toolRef{},
		compaction:  -1,
	}
	err := jsonl.Each(ctx, path, m.handle)
	if err != nil {
		var incomplete *jsonl.IncompleteError
		if errors.As(err, &incomplete) {
			out.Meta.Provenance.Complete = false
			out.Diagnostics = append(out.Diagnostics, session.Diagnostic{
				Level: "warn", Code: "torn-tail", Title: "文件尾部不完整（会话可能正在写）",
				Detail: incomplete.Error(),
			})
		} else {
			return nil, err
		}
	}
	m.finalize()
	return out, nil
}

func (m *mapper) handle(number int, line []byte) error {
	var env envelope
	if err := json.Unmarshal(line, &env); err != nil {
		m.out.Unknown["line:unparsable"]++
		return nil
	}
	switch env.Type {
	case "session":
		var head header
		if err := json.Unmarshal(env.Data, &head); err == nil {
			m.applyHeader(head)
		}
	case "session/title":
		var data titleData
		if err := json.Unmarshal(env.Data, &data); err == nil && data.Title != "" {
			m.out.Meta.Title = session.Str(data.Title)
		}
	case "request/header":
		var data configData
		if err := json.Unmarshal(env.Data, &data); err == nil {
			m.applyModel(data.Header.Config.Provider, data.Header.Config.Model)
		}
	case "model/selection":
		var data selectionData
		if err := json.Unmarshal(env.Data, &data); err == nil {
			m.applyModel(data.Provider, data.Model)
		}
	case "turn/start":
		var data turnData
		if err := json.Unmarshal(env.Data, &data); err == nil {
			m.currentTurn = data.Turn
		}
	case "step/start":
		var data stepData
		if err := json.Unmarshal(env.Data, &data); err == nil {
			m.stepStart[[2]int{data.Turn, data.Step}] = env.Time
		}
	case "step/end":
		var data stepData
		if err := json.Unmarshal(env.Data, &data); err == nil {
			if index, ok := m.stepMessage[[2]int{data.Turn, data.Step}]; ok {
				m.out.Messages[index].CompletedMs = session.I64(env.Time)
			}
		}
	case "turn/end":
		var data turnData
		if err := json.Unmarshal(env.Data, &data); err == nil {
			m.closeTurn(data.Turn, env.Time)
		}
	case "user/message":
		m.appendUserMessage(env)
	case "assistant/message":
		m.appendAssistantMessage(env)
	case "tool/call":
		m.applyToolCall(env)
	case "tool/result":
		m.applyToolResult(env)
	case "compaction/start":
		m.startCompaction(env)
	case "compaction/end":
		m.endCompaction(env)
	case "system/message":
		m.appendSystemMessage(env)
	default:
		if knownIgnored[env.Type] {
			return nil
		}
		m.out.Unknown[env.Type]++
	}
	return nil
}

// closeTurn 给该轮最后一条还没有结束时间的 assistant 消息补上完成时间。
func (m *mapper) closeTurn(turn int, at int64) {
	for index := len(m.out.Messages) - 1; index >= 0; index-- {
		message := &m.out.Messages[index]
		if message.Role != session.RoleAssistant {
			continue
		}
		if message.TurnHint.Turn != turn {
			break
		}
		if message.CompletedMs == nil {
			message.CompletedMs = session.I64(at)
		}
		return
	}
}

func (m *mapper) applyHeader(head header) {
	if head.ID != "" {
		m.out.Ref.ID = head.ID
		m.out.Meta.ID = session.Str(head.ID)
	}
	m.out.Meta.Version = session.Str("v" + itoa(head.Version))
	m.out.Meta.Provenance.Format = formatName + ":v" + itoa(head.Version)
	if head.Cwd != "" {
		m.out.Meta.Directory = session.Str(head.Cwd)
		m.out.Meta.WorkspaceKey = head.Cwd
	}
	if head.AgentPreset != "" {
		m.out.Meta.Agent = session.Str(head.AgentPreset)
	}
	if head.ParentSession != "" {
		m.out.Meta.ParentID = session.Str(head.ParentSession)
	}
	if head.CreatedAt > 0 {
		m.out.Meta.CreatedMs = session.I64(head.CreatedAt)
	}
}

func (m *mapper) applyModel(provider, model string) {
	if provider == "" && model == "" {
		return
	}
	reference := &session.ModelRef{}
	if provider != "" {
		reference.Provider = session.Str(provider)
	}
	if model != "" {
		reference.ID = session.Str(model)
	}
	m.model = reference
}

func (m *mapper) appendUserMessage(env envelope) {
	var data userData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		m.out.Unknown["user/message:unparsable"]++
		return
	}
	// dsh 会把 system-reminder / 技能清单这类注入内容也作为 user/message 投递。
	// 它们不是人类输入：映射成 system 角色（仍保留在导出里），否则每一段注入都会
	// 单独开一轮、把真正的轮次结构打散。
	synthetic := data.Source != nil && data.Source.Kind != "" && data.Source.Kind != "user"
	role := session.RoleUser
	payloadRole := "user"
	if synthetic {
		role = session.RoleSystem
		payloadRole = "system"
	}
	parts := make([]session.Part, 0, len(data.Content))
	for index, block := range data.Content {
		if block.Type != "text" {
			continue
		}
		text := m.clip(block.Text)
		parts = append(parts, session.Part{
			ID:        data.ID + "#" + itoa(index),
			Kind:      session.PartText,
			Type:      "text",
			CreatedMs: session.I64(env.Time),
			UpdatedMs: session.I64(env.Time),
			Text:      &text,
			Synthetic: session.Bool(synthetic),
		})
	}
	message := session.Message{
		ID:          data.ID,
		Role:        role,
		PayloadRole: session.Str(payloadRole),
		CreatedMs:   session.I64(env.Time),
		UpdatedMs:   session.I64(env.Time),
		Synthetic:   synthetic,
		Parts:       parts,
	}
	if message.ID == "" {
		message.ID = "user-" + itoa(len(m.out.Messages))
	}
	m.out.Messages = append(m.out.Messages, message)
}

func (m *mapper) appendAssistantMessage(env envelope) {
	var data assistantData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		m.out.Unknown["assistant/message:unparsable"]++
		return
	}
	starts, ends := blockSpans(data.Stream)
	parts := make([]session.Part, 0, len(data.Message.Content))
	for index, block := range data.Message.Content {
		created, updated := env.Time, env.Time
		if value, ok := starts[index]; ok {
			created = value
		}
		if value, ok := ends[index]; ok {
			updated = value
		}
		switch block.Type {
		case "reasoning", "text":
			text := m.clip(block.Text)
			kind := session.PartReasoning
			if block.Type == "text" {
				kind = session.PartText
			}
			parts = append(parts, session.Part{
				ID:        data.Message.ID + "#" + itoa(index),
				Kind:      kind,
				Type:      block.Type,
				CreatedMs: session.I64(created),
				UpdatedMs: session.I64(updated),
				StartMs:   session.I64(created),
				EndMs:     session.I64(updated),
				Text:      &text,
			})
		case "tool-call":
			args := toolsem.Unwrap(block.Arguments)
			call := &session.ToolCall{
				CallID:  session.Str(block.ID),
				Name:    session.Str(block.Name),
				Args:    args,
				StartMs: session.I64(created),
			}
			call.Kind, call.Target, call.Command = toolsem.Classify(session.DSH, block.Name, args)
			m.tools[block.ID] = toolRef{message: len(m.out.Messages), part: len(parts)}
			parts = append(parts, session.Part{
				ID:        data.Message.ID + "#" + itoa(index),
				Kind:      session.PartToolCall,
				Type:      "tool-call",
				CreatedMs: session.I64(created),
				UpdatedMs: session.I64(updated),
				StartMs:   session.I64(created),
				Tool:      call,
			})
		}
	}
	created := env.Time
	if value, ok := m.stepStart[[2]int{data.Turn, data.Step}]; ok {
		created = value
	}
	message := session.Message{
		ID:          data.Message.ID,
		Role:        session.RoleAssistant,
		PayloadRole: session.Str("assistant"),
		CreatedMs:   session.I64(created),
		UpdatedMs:   session.I64(env.Time),
		TurnHint:    session.TurnHint{Turn: data.Turn, Step: data.Step},
		Model:       m.model,
		Tokens:      tokensFromUsage(data.Usage),
		Finish:      finishReason(data.Stream),
		Parts:       parts,
	}
	if message.ID == "" {
		message.ID = "assistant-" + itoa(len(m.out.Messages))
	}
	if data.Interrupted {
		message.Error = &session.Error{Name: session.Str("Aborted")}
	}
	m.out.Messages = append(m.out.Messages, message)
	m.stepMessage[[2]int{data.Turn, data.Step}] = len(m.out.Messages) - 1
}

func (m *mapper) applyToolCall(env envelope) {
	var data toolCallData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		m.out.Unknown["tool/call:unparsable"]++
		return
	}
	ref, ok := m.tools[data.CallID]
	if !ok {
		index := m.latestAssistant(data.Turn, data.Step)
		if index < 0 {
			m.out.Unknown["tool/call:orphan"]++
			return
		}
		args := toolsem.Unwrap(data.Arguments)
		call := &session.ToolCall{CallID: session.Str(data.CallID), Name: session.Str(data.Name), Args: args, StartMs: session.I64(env.Time)}
		call.Kind, call.Target, call.Command = toolsem.Classify(session.DSH, data.Name, args)
		message := &m.out.Messages[index]
		message.Parts = append(message.Parts, session.Part{
			ID:        data.CallID,
			Kind:      session.PartToolCall,
			Type:      "tool-call",
			CreatedMs: session.I64(env.Time),
			UpdatedMs: session.I64(env.Time),
			StartMs:   session.I64(env.Time),
			Tool:      call,
		})
		m.tools[data.CallID] = toolRef{message: index, part: len(message.Parts) - 1}
		return
	}
	message := &m.out.Messages[ref.message]
	if ref.part >= len(message.Parts) {
		return
	}
	call := message.Parts[ref.part].Tool
	if call == nil {
		return
	}
	if len(call.Args) == 0 && len(data.Arguments) > 0 {
		args := toolsem.Unwrap(data.Arguments)
		call.Args = args
		call.Kind, call.Target, call.Command = toolsem.Classify(session.DSH, data.Name, args)
	}
	if call.StartMs == nil {
		call.StartMs = session.I64(env.Time)
		message.Parts[ref.part].StartMs = call.StartMs
	}
}

func (m *mapper) applyToolResult(env envelope) {
	var data toolResultData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		m.out.Unknown["tool/result:unparsable"]++
		return
	}
	ref, ok := m.tools[data.Message.ToolCallID]
	if !ok {
		m.out.Unknown["tool/result:unpaired"]++
		return
	}
	message := &m.out.Messages[ref.message]
	if ref.part >= len(message.Parts) {
		return
	}
	part := &message.Parts[ref.part]
	if part.Tool == nil {
		return
	}
	text := m.clip(joinBlocks(data.Message.Content))
	part.Tool.Output = marshalString(text)
	part.Tool.EndMs = session.I64(env.Time)
	part.EndMs = session.I64(env.Time)
	part.UpdatedMs = session.I64(env.Time)
	status := "completed"
	if data.Message.IsError {
		status = "error"
		if data.Error != nil {
			part.Tool.Error = marshalError(data.Error.Name, data.Error.Code)
		}
	}
	part.Tool.Status = &status
	message.UpdatedMs = session.I64(env.Time)
}

func (m *mapper) startCompaction(env envelope) {
	var data compactionData
	_ = json.Unmarshal(env.Data, &data)
	id := data.CompactionID
	if id == "" {
		id = "compaction-" + itoa(len(m.out.Messages))
	}
	m.out.Messages = append(m.out.Messages, session.Message{
		ID:          "compaction:" + id,
		Role:        session.RoleUser,
		PayloadRole: session.Str("user"),
		CreatedMs:   session.I64(env.Time),
		UpdatedMs:   session.I64(env.Time),
		TurnHint:    session.TurnHint{Turn: data.Turn},
		Parts: []session.Part{{
			ID:        "compaction:" + id,
			Kind:      session.PartCompaction,
			Type:      "compaction",
			CreatedMs: session.I64(env.Time),
			UpdatedMs: session.I64(env.Time),
			StartMs:   session.I64(env.Time),
		}},
	})
	m.compaction = len(m.out.Messages) - 1
}

func (m *mapper) endCompaction(env envelope) {
	if m.compaction < 0 || m.compaction >= len(m.out.Messages) {
		return
	}
	message := &m.out.Messages[m.compaction]
	if len(message.Parts) == 0 {
		return
	}
	part := &message.Parts[0]
	part.EndMs = session.I64(env.Time)
	part.UpdatedMs = session.I64(env.Time)
	if part.StartMs != nil {
		duration := env.Time - *part.StartMs
		part.DurationMs = &duration
	}
	message.UpdatedMs = session.I64(env.Time)
	m.compaction = -1
}

func (m *mapper) appendSystemMessage(env envelope) {
	var data systemMessageData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		m.out.Unknown["system/message:unparsable"]++
		return
	}
	text := m.clip(joinBlocks(data.Message.Content))
	id := data.Message.ID
	if id == "" {
		id = "system-" + itoa(len(m.out.Messages))
	}
	m.out.Messages = append(m.out.Messages, session.Message{
		ID:          id,
		Role:        session.RoleSystem,
		PayloadRole: session.Str("system"),
		CreatedMs:   session.I64(env.Time),
		UpdatedMs:   session.I64(env.Time),
		Parts: []session.Part{{
			ID:        "system:" + id,
			Kind:      session.PartText,
			Type:      "text",
			CreatedMs: session.I64(env.Time),
			UpdatedMs: session.I64(env.Time),
			Text:      &text,
		}},
	})
}

func (m *mapper) latestAssistant(turn, step int) int {
	if index, ok := m.stepMessage[[2]int{turn, step}]; ok {
		return index
	}
	for index := len(m.out.Messages) - 1; index >= 0; index-- {
		if m.out.Messages[index].Role == session.RoleAssistant {
			return index
		}
	}
	return -1
}

// finalize 补两件收尾工作：
//  1. 若某轮第一条是 assistant（文件里没有 user/message），补一条 synthetic 用户消息，避免整轮被丢弃；
//  2. 收尾元信息与截断诊断。
func (m *mapper) finalize() {
	messages := make([]session.Message, 0, len(m.out.Messages)+2)
	hasUser := false
	synthetic := 0
	for _, message := range m.out.Messages {
		if message.Role == session.RoleUser {
			hasUser = true
		}
		if message.Role == session.RoleAssistant && !hasUser {
			messages = append(messages, session.Message{
				ID:          "synthetic:" + message.ID,
				Role:        session.RoleUser,
				PayloadRole: session.Str("user"),
				CreatedMs:   message.CreatedMs,
				UpdatedMs:   message.CreatedMs,
				Synthetic:   true,
				TurnHint:    message.TurnHint,
				Parts: []session.Part{{
					ID:        "synthetic:" + message.ID,
					Kind:      session.PartText,
					Type:      "text",
					CreatedMs: message.CreatedMs,
					UpdatedMs: message.CreatedMs,
					Text:      session.Str(""),
					Synthetic: session.Bool(true),
				}},
			})
			hasUser = true
			synthetic++
		}
		messages = append(messages, message)
	}
	m.out.Messages = messages
	if synthetic > 0 {
		m.out.Diagnostics = append(m.out.Diagnostics, session.Diagnostic{
			Level:  "info",
			Code:   "synthetic-turn-input",
			Title:  "补了 " + itoa(synthetic) + " 条空的用户输入",
			Detail: "这些轮次在文件里没有 user/message（只有 turn/start），补占位以免整轮丢失；不是编造内容。",
		})
	}
	if len(messages) > 0 {
		if last := messages[len(messages)-1]; last.UpdatedMs != nil {
			m.out.Meta.UpdatedMs = last.UpdatedMs
		}
	}
	if m.model != nil {
		m.out.Meta.Model = m.model
	}
	if m.truncated {
		m.out.Diagnostics = append(m.out.Diagnostics, session.Diagnostic{
			Level:  "warn",
			Code:   "truncated-text",
			Title:  "文本/工具输出按上限截断",
			Detail: "只保留前 " + itoa(m.opts.MaxTextBytes) + " 字节。",
		})
	}
}

func (m *mapper) clip(text string) string {
	if len(text) <= m.opts.MaxTextBytes {
		return text
	}
	m.truncated = true
	return text[:m.opts.MaxTextBytes]
}

func blockSpans(records []streamRecord) (map[int]int64, map[int]int64) {
	starts := map[int]int64{}
	ends := map[int]int64{}
	for _, record := range records {
		if record.Chunk == nil || record.Time == nil {
			continue
		}
		switch record.Chunk.Type {
		case "block-start":
			starts[record.Chunk.Index] = *record.Time
		case "block-end":
			ends[record.Chunk.Index] = *record.Time
		}
	}
	return starts, ends
}

func finishReason(records []streamRecord) *string {
	for index := len(records) - 1; index >= 0; index-- {
		chunk := records[index].Chunk
		if chunk != nil && chunk.Type == "finish" && chunk.Reason != nil && chunk.Reason.Kind != "" {
			return session.Str(chunk.Reason.Kind)
		}
	}
	return nil
}

func tokensFromUsage(value *usage) *session.Tokens {
	if value == nil {
		return nil
	}
	tokens := &session.Tokens{
		Input:     value.InputTokens,
		Output:    value.OutputTokens,
		Total:     value.TotalTokens,
		Reasoning: value.ReasoningTokens,
	}
	if value.CachedInputTokens != nil {
		tokens.CacheRead = value.CachedInputTokens
	}
	if value.CacheReadTokens != nil {
		tokens.CacheRead = value.CacheReadTokens
	}
	if value.CacheWriteTokens != nil {
		tokens.CacheWrite = value.CacheWriteTokens
	}
	if tokens.Input == nil && tokens.Output == nil && tokens.Total == nil {
		return nil
	}
	return tokens
}

func joinBlocks(blocks []contentBlock) string {
	out := ""
	for _, block := range blocks {
		if block.Type != "text" {
			continue
		}
		if out != "" {
			out += string(rune(10))
		}
		out += block.Text
	}
	return out
}

func marshalString(text string) []byte {
	encoded, err := json.Marshal(text)
	if err != nil {
		return nil
	}
	return encoded
}

func marshalError(name, code string) []byte {
	payload := map[string]string{}
	if name != "" {
		payload["name"] = name
	}
	if code != "" {
		payload["code"] = code
	}
	if len(payload) == 0 {
		return nil
	}
	encoded, err := json.Marshal(payload)
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
