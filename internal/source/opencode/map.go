package opencode

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/zhouyuejie/opencode-trace/internal/session"
	"github.com/zhouyuejie/opencode-trace/internal/session/toolsem"
	"github.com/zhouyuejie/opencode-trace/internal/source"
)

// 本文件把 SQLite 行映射成源无关的领域模型。
//
// 映射口径与 internal/trace 的旧导出适配器（ExportToSession）必须一致：
// 两条路径（旧 JSON 渲染 / 直连库取数）在同一会话上要产出同样的领域模型，
// 由 map_test.go 的等价性用例守住。

type messageData struct {
	Role       *string `json:"role"`
	Finish     *string `json:"finish"`
	ModelID    *string `json:"modelID"`
	ProviderID *string `json:"providerID"`
	Time       *struct {
		Created   *int64 `json:"created"`
		Completed *int64 `json:"completed"`
	} `json:"time"`
	Error *struct {
		Name    *string `json:"name"`
		Message *string `json:"message"`
		Data    *struct {
			Message     *string `json:"message"`
			StatusCode  *int64  `json:"statusCode"`
			IsRetryable *bool   `json:"isRetryable"`
		} `json:"data"`
	} `json:"error"`
	Tokens *struct {
		Total     *int64 `json:"total"`
		Input     *int64 `json:"input"`
		Output    *int64 `json:"output"`
		Reasoning *int64 `json:"reasoning"`
		Cache     *struct {
			Read  *int64 `json:"read"`
			Write *int64 `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
}

type spanRange struct {
	Start *int64 `json:"start"`
	End   *int64 `json:"end"`
}

type toolStateData struct {
	Status string          `json:"status"`
	Input  json.RawMessage `json:"input"`
	Output json.RawMessage `json:"output"`
	Error  json.RawMessage `json:"error"`
	Title  *string         `json:"title"`
	Time   *spanRange      `json:"time"`
}

type partData struct {
	Type      *string        `json:"type"`
	Text      *string        `json:"text"`
	Synthetic *bool          `json:"synthetic"`
	Auto      *bool          `json:"auto"`
	Time      *spanRange     `json:"time"`
	Tool      *string        `json:"tool"`
	CallID    *string        `json:"callID"`
	Filename  *string        `json:"filename"`
	URL       *string        `json:"url"`
	State     *toolStateData `json:"state"`
}

// mapDetail 把一次 Load 的结果映射成领域模型。
func mapDetail(detail *SessionDetail, store source.Store, opts source.OpenOptions) *session.Session {
	row := detail.Session
	now := time.Now().UnixMilli()
	out := &session.Session{
		Ref: session.Ref{
			Source: session.Opencode,
			Store:  store.Path,
			ID:     row.ID,
			Object: "session:" + row.ID,
		},
		Meta: session.Meta{
			ID:           session.Str(row.ID),
			Title:        session.Str(row.Title),
			Slug:         session.Str(row.Slug),
			Directory:    session.Str(row.Directory),
			Agent:        row.Agent,
			Version:      session.Str(row.Version),
			WorkspaceKey: row.ProjectID,
			ParentID:     row.ParentID,
			Model:        parseModelRaw(row.Model),
			Cost:         &row.Cost,
			CreatedMs:    &row.TimeCreated,
			UpdatedMs:    &row.TimeUpdated,
			Provenance: session.Provenance{
				Source:   session.Opencode,
				Store:    store.Path,
				Format:   formatName(store),
				LoadedAt: now,
				Complete: true,
			},
		},
		Diagnostics: make([]session.Diagnostic, 0, 2),
		Unknown:     map[string]int{},
	}

	partsByMessage := make(map[string][]PartRow, len(detail.Messages))
	for _, part := range detail.Parts {
		partsByMessage[part.MessageID] = append(partsByMessage[part.MessageID], part)
	}

	truncated := false
	declaredToolMs := int64(0)
	for _, message := range detail.Messages {
		converted, toolMs := mapMessage(message, partsByMessage[message.ID], opts, &truncated)
		out.Messages = append(out.Messages, converted)
		if converted.Role == session.RoleAssistant {
			declaredToolMs += toolMs
		}
	}
	if declaredToolMs > 0 {
		out.Declared.TotalToolDurationMs = session.I64(declaredToolMs)
	}
	if truncated {
		out.Diagnostics = append(out.Diagnostics, session.Diagnostic{
			Level:  "warn",
			Code:   "truncated-text",
			Title:  "文本/工具输出按上限截断",
			Detail: "只保留前 " + itoa(opts.MaxTextBytes) + " 字节；要看全文请提高上限或直接查库。",
		})
	}
	if len(row.Title) == 0 {
		out.Diagnostics = append(out.Diagnostics, session.Diagnostic{
			Level:  "info",
			Code:   "empty-title",
			Title:  "会话没有标题",
			Detail: "opencode 该会话的 title 为空，页面上会显示会话 id。",
		})
	}
	return out
}

func mapMessage(row MessageRow, parts []PartRow, opts source.OpenOptions, truncated *bool) (session.Message, int64) {
	var data messageData
	_ = json.Unmarshal(row.Data, &data)

	role := "assistant"
	if data.Role != nil && *data.Role != "" {
		role = *data.Role
	}
	out := session.Message{
		ID:          row.ID,
		Role:        session.Role(role),
		PayloadRole: data.Role,
		CreatedMs:   &row.TimeCreatedMs,
		UpdatedMs:   &row.TimeUpdatedMs,
		Finish:      data.Finish,
	}
	if data.Time != nil {
		out.CompletedMs = data.Time.Completed
	}
	if data.Error != nil {
		converted := &session.Error{Name: data.Error.Name, Message: data.Error.Message}
		if data.Error.Data != nil {
			converted.Detail = data.Error.Data.Message
			converted.StatusCode = data.Error.Data.StatusCode
			converted.Retryable = data.Error.Data.IsRetryable
		}
		out.Error = converted
	}
	if data.Tokens != nil {
		converted := &session.Tokens{
			Total:     data.Tokens.Total,
			Input:     data.Tokens.Input,
			Output:    data.Tokens.Output,
			Reasoning: data.Tokens.Reasoning,
		}
		if data.Tokens.Cache != nil {
			converted.CacheRead = data.Tokens.Cache.Read
			converted.CacheWrite = data.Tokens.Cache.Write
		}
		out.Tokens = converted
	}
	if id := firstNonEmpty(data.ModelID); id != nil {
		model := &session.ModelRef{ID: id}
		if provider := firstNonEmpty(data.ProviderID); provider != nil {
			model.Provider = provider
		}
		out.Model = model
	} else if provider := firstNonEmpty(data.ProviderID); provider != nil {
		out.Model = &session.ModelRef{Provider: provider}
	}

	toolMs := int64(0)
	out.Parts = make([]session.Part, 0, len(parts))
	for _, part := range parts {
		converted, partToolMs := mapPart(part, opts, truncated)
		out.Parts = append(out.Parts, converted)
		if role == "assistant" && converted.Kind == session.PartToolCall {
			toolMs += partToolMs
		}
	}
	return out, toolMs
}

func mapPart(row PartRow, opts source.OpenOptions, truncated *bool) (session.Part, int64) {
	var data partData
	_ = json.Unmarshal(row.Data, &data)

	rawType := "unknown"
	if data.Type != nil && *data.Type != "" {
		rawType = *data.Type
	}
	created, updated := row.TimeCreatedMs, row.TimeUpdatedMs
	duration := updated - created
	out := session.Part{
		ID:         row.ID,
		Type:       rawType,
		Kind:       partKind(rawType),
		CreatedMs:  &created,
		UpdatedMs:  &updated,
		DurationMs: &duration,
		Text:       data.Text,
		Synthetic:  data.Synthetic,
		Auto:       data.Auto,
	}
	if data.Filename != nil || data.URL != nil {
		out.File = &session.FileRef{Name: data.Filename, URL: data.URL}
	}
	if data.Time != nil {
		out.StartMs = data.Time.Start
		out.EndMs = data.Time.End
	}
	if opts.MaxTextBytes > 0 && out.Text != nil && len(*out.Text) > opts.MaxTextBytes {
		cut := (*out.Text)[:opts.MaxTextBytes]
		out.Text = &cut
		*truncated = true
	}
	toolMs := int64(0)
	if state := data.State; state != nil {
		name := ""
		if data.Tool != nil {
			name = *data.Tool
		}
		call := &session.ToolCall{
			CallID: data.CallID,
			Name:   data.Tool,
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
		call.Kind, call.Target, call.Command = toolsem.Classify(session.Opencode, name, state.Input)
		out.Tool = call
		// 与旧导出 GlobalStats.total_tool_duration_ms 同口径：
		// state.time 优先，缺失时退回 part 的创建/更新时间。
		start, end := created, updated
		if state.Time != nil {
			if state.Time.Start != nil {
				start = *state.Time.Start
			}
			if state.Time.End != nil {
				end = *state.Time.End
			}
		}
		toolMs = end - start
	}
	return out, toolMs
}

func partKind(raw string) session.PartKind {
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

// parseModelRaw 解析会话级 model 字段（可能是对象，也可能是被编码成字符串的对象）。
func parseModelRaw(raw *string) *session.ModelRef {
	if raw == nil {
		return nil
	}
	return parseModelJSON([]byte(*raw))
}

func parseModelJSON(raw []byte) *session.ModelRef {
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
		out := &session.ModelRef{ID: firstNonEmpty(model.ID), Provider: firstNonEmpty(model.ProviderID), Variant: firstNonEmpty(model.Variant)}
		if out.ID == nil {
			out.ID = firstNonEmpty(model.ModelID)
		}
		return out
	}
	if trimmed[0] == '"' {
		var asString string
		if err := json.Unmarshal(raw, &asString); err != nil {
			return nil
		}
		return parse([]byte(asString))
	}
	return parse(raw)
}

// firstNonEmpty 复刻旧导出 stringPtrField 的口径：空字符串等同"没有值"。
func firstNonEmpty(value *string) *string {
	if value == nil || *value == "" {
		return nil
	}
	return value
}

func formatName(store source.Store) string {
	if store.Version == "" {
		return store.Format
	}
	return store.Format + ":" + store.Version
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
