package trace

import (
	"encoding/json"
	"strings"

	"github.com/zhouyuejie/opencode-trace/internal/session"
	"github.com/zhouyuejie/opencode-trace/internal/session/toolsem"
)

// ExportToSession 把 opencode 导出文档（旧口径）映射成源无关的领域模型。
//
// 这一层是"旧格式适配器"：所有对 message.data / part.data 的解析都收敛在这里，
// 归一化（normalize.go）从此只认领域模型。映射目标是与旧实现逐字节等价，
// 由 internal/trace/golden_test.go 守住。
func ExportToSession(doc *Export) *session.Session {
	if doc == nil {
		return nil
	}
	source := doc.Session
	if source == nil {
		source = &Session{}
	}
	out := &session.Session{
		Meta: session.Meta{
			ID:           source.ID,
			Title:        source.Title,
			Slug:         source.Slug,
			Directory:    source.Directory,
			Agent:        source.Agent,
			Version:      source.Version,
			Model:        exportModel(source.Model),
			Cost:         exportCost(source.Cost),
			ExportedAt:   exportExportedAt(doc),
			ExportSource: exportSource(doc),
			Provenance: session.Provenance{
				Source:   session.Opencode,
				Format:   "opencode/export",
				Complete: true,
			},
		},
		Messages: make([]session.Message, 0, len(doc.Messages)),
	}
	if doc.GlobalStats != nil {
		out.Declared.TotalToolDurationMs = doc.GlobalStats.TotalToolDurationMs
	}
	for _, timing := range doc.TurnTimings {
		out.Declared.TurnTimings = append(out.Declared.TurnTimings, session.DeclaredTurnTiming{
			MessageID:  timing.MessageID,
			DurationMs: timing.TurnDurationMs,
		})
	}
	for _, message := range doc.Messages {
		out.Messages = append(out.Messages, exportMessage(message))
	}
	return out
}

func exportMessage(message Message) session.Message {
	out := session.Message{
		ID:        message.MessageID,
		Role:      session.Role(message.Role),
		CreatedMs: message.TimeCreatedMs,
		UpdatedMs: message.TimeUpdatedMs,
	}
	if message.Data != nil {
		out.PayloadRole = message.Data.Role
		out.Finish = message.Data.Finish
		if message.Data.Time != nil {
			out.CompletedMs = message.Data.Time.Completed
		}
		if message.Data.Error != nil {
			out.Error = exportError(message.Data.Error)
		}
		if message.Data.Tokens != nil {
			out.Tokens = exportTokens(message.Data.Tokens)
		}
	}
	// 模型：消息列优先于载荷内字段（与旧实现一致）。
	model := &session.ModelRef{}
	present := false
	if message.ModelID != nil {
		model.ID = message.ModelID
		present = true
	} else if message.Data != nil && message.Data.ModelID != nil {
		model.ID = message.Data.ModelID
		present = true
	}
	if message.ModelProvider != nil {
		model.Provider = message.ModelProvider
		present = true
	} else if message.Data != nil && message.Data.ProviderID != nil {
		model.Provider = message.Data.ProviderID
		present = true
	}
	if present {
		out.Model = model
	}
	out.Parts = make([]session.Part, 0, len(message.Parts))
	for _, part := range message.Parts {
		out.Parts = append(out.Parts, exportPart(part))
	}
	return out
}

func exportError(source *MessageErr) *session.Error {
	out := &session.Error{Name: source.Name, Message: source.Message}
	if source.Data != nil {
		out.Detail = source.Data.Message
		out.StatusCode = source.Data.StatusCode
		out.Retryable = source.Data.Retryable
	}
	return out
}

func exportTokens(source *TokenCounts) *session.Tokens {
	out := &session.Tokens{
		Total:     source.Total,
		Input:     source.Input,
		Output:    source.Output,
		Reasoning: source.Reasoning,
	}
	if source.Cache != nil {
		out.CacheRead = source.Cache.Read
		out.CacheWrite = source.Cache.Write
	}
	return out
}

func exportPart(part Part) session.Part {
	out := session.Part{
		ID:         part.ID,
		Kind:       exportPartKind(part.Type),
		Type:       part.Type,
		CreatedMs:  part.TimeCreatedMs,
		UpdatedMs:  part.TimeUpdatedMs,
		DurationMs: part.DurationMs,
	}
	if part.Data == nil {
		return out
	}
	out.Text = part.Data.Text
	out.Synthetic = part.Data.Synthetic
	out.Auto = part.Data.Auto
	if part.Data.Filename != nil || part.Data.URL != nil {
		out.File = &session.FileRef{Name: part.Data.Filename, URL: part.Data.URL}
	}
	if part.Data.Time != nil {
		out.StartMs = part.Data.Time.Start
		out.EndMs = part.Data.Time.End
	}
	// 旧实现只在 state 存在时才认可工具字段（连工具名也取自 state 分支），这里保持一致。
	if state := part.Data.State; state != nil {
		call := &session.ToolCall{
			CallID: part.Data.CallID,
			Name:   part.Data.Tool,
			Args:   state.Input,
			Output: state.Output,
			Error:  state.Error,
			Title:  state.Title,
		}
		if state.Status != "" {
			status := state.Status
			call.Status = &status
		}
		if state.Time != nil {
			call.StartMs = state.Time.Start
			call.EndMs = state.Time.End
		}
		name := ""
		if call.Name != nil {
			name = *call.Name
		}
		call.Kind, call.Target, call.Command = toolsem.Classify(session.Opencode, name, call.Args)
		out.Tool = call
	}
	return out
}

func exportPartKind(raw string) session.PartKind {
	switch raw {
	case "text":
		return session.PartText
	case "reasoning":
		return session.PartReasoning
	case "tool":
		return session.PartToolCall
	case "compaction":
		return session.PartCompaction
	case "file":
		return session.PartFile
	case "step-start":
		return session.PartStepStart
	case "step-finish":
		return session.PartStepFinish
	default:
		return session.PartOther
	}
}

func exportCost(value *LooseNumber) *float64 {
	if value == nil || !value.Set {
		return nil
	}
	cost := value.V
	return &cost
}

// NormalizeExport 是旧调用点的兼容入口：先做一次"旧导出 → 领域模型"的适配，
// 再走统一的归一化。行为与改造前逐字节一致，由黄金用例守住。
func NormalizeExport(doc *Export, opts Options) (*Data, error) {
	return Normalize(ExportToSession(doc), opts)
}

func exportExportedAt(doc *Export) *string {
	if doc.ExportInfo == nil {
		return nil
	}
	return doc.ExportInfo.ExportedAt
}

func exportSource(doc *Export) *string {
	if doc.ExportInfo == nil {
		return nil
	}
	return doc.ExportInfo.Source
}

// exportModel 解析会话级 model 字段：可能是对象，也可能是被编码成字符串的对象
// （opencode 不同版本都出现过）。解析不了就当没有，不猜。
func exportModel(raw json.RawMessage) *session.ModelRef {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	type rawModel struct {
		ID         *string `json:"id"`
		ModelID    *string `json:"modelID"`
		ProviderID *string `json:"providerID"`
		Variant    *string `json:"variant"`
	}
	parse := func(data []byte) *session.ModelRef {
		var model rawModel
		if err := json.Unmarshal(data, &model); err != nil {
			return nil
		}
		out := &session.ModelRef{ID: model.ID, Provider: model.ProviderID, Variant: model.Variant}
		if out.ID == nil {
			out.ID = model.ModelID
		}
		return out
	}
	if strings.HasPrefix(trimmed, "\"") {
		var asString string
		if err := json.Unmarshal(raw, &asString); err != nil {
			return nil
		}
		return parse([]byte(asString))
	}
	return parse(raw)
}
