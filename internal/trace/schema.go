package trace

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Export 是 opencode 会话导出的顶层结构（只声明用得到的字段，其余忽略）。
type Export struct {
	ExportInfo  *ExportInfo  `json:"export_info"`
	Session     *Session     `json:"session"`
	GlobalStats *GlobalStats `json:"global_stats"`
	TurnTimings []TurnTiming `json:"turn_timings"`
	Messages    []Message    `json:"messages"`
}

// ExportInfo 记录导出动作本身的时间与来源。
type ExportInfo struct {
	ExportedAt *string `json:"exported_at"`
	Source     *string `json:"source"`
}

// Session 是会话的元信息。
type Session struct {
	ID        *string         `json:"id"`
	Title     *string         `json:"title"`
	Slug      *string         `json:"slug"`
	Directory *string         `json:"directory"`
	Agent     *string         `json:"agent"`
	Version   *string         `json:"version"`
	Cost      *LooseNumber    `json:"cost"`
	Model     json.RawMessage `json:"model"`
}

// GlobalStats 是导出方自己算的统计，只用来对账。
type GlobalStats struct {
	TotalToolDurationMs *int64 `json:"total_tool_duration_ms"`
}

// TurnTiming 是导出方按轮次算的时间，本工具不使用，只在它自相矛盾时提示。
type TurnTiming struct {
	MessageID      *string  `json:"message_id"`
	TurnDurationMs *float64 `json:"turn_duration_ms"`
}

// Message 是一条 user/assistant 消息。
type Message struct {
	MessageID     string       `json:"message_id"`
	Role          string       `json:"role"`
	TimeCreatedMs *int64       `json:"time_created_ms"`
	TimeUpdatedMs *int64       `json:"time_updated_ms"`
	ModelID       *string      `json:"model_id"`
	ModelProvider *string      `json:"model_provider"`
	Data          *MessageData `json:"data"`
	Parts         []Part       `json:"parts"`
}

// MessageData 是消息的负载。
type MessageData struct {
	Role       *string      `json:"role"`
	Finish     *string      `json:"finish"`
	ModelID    *string      `json:"modelID"`
	ProviderID *string      `json:"providerID"`
	Time       *TimeRange   `json:"time"`
	Error      *MessageErr  `json:"error"`
	Tokens     *TokenCounts `json:"tokens"`
}

// MessageErr 是步骤级错误（例如 MessageAbortedError）。
type MessageErr struct {
	Name    *string           `json:"name"`
	Message *string           `json:"message"`
	Data    *MessageErrorData `json:"data"`
}

// MessageErrorData 是 provider 错误的结构化字段。
type MessageErrorData struct {
	Message    *string `json:"message"`
	StatusCode *int64  `json:"statusCode"`
	Retryable  *bool   `json:"isRetryable"`
}

// TimeRange 是 created/completed 或 start/end 这类时间对。
type TimeRange struct {
	Created *int64 `json:"created"`
	Start   *int64 `json:"start"`
	End     *int64 `json:"end"`
	// Completed 只出现在 message.data.time 上。
	Completed *int64 `json:"completed"`
}

// TokenCounts 是导出方的 token 统计。
type TokenCounts struct {
	Total     *int64 `json:"total"`
	Input     *int64 `json:"input"`
	Output    *int64 `json:"output"`
	Reasoning *int64 `json:"reasoning"`
	Cache     *struct {
		Read  *int64 `json:"read"`
		Write *int64 `json:"write"`
	} `json:"cache"`
}

// Part 是消息下的一段内容（text/reasoning/tool/step-start/...）。
type Part struct {
	ID            string    `json:"id"`
	Type          string    `json:"type"`
	TimeCreatedMs *int64    `json:"time_created_ms"`
	TimeUpdatedMs *int64    `json:"time_updated_ms"`
	DurationMs    *int64    `json:"duration_ms"`
	Data          *PartData `json:"data"`
}

// PartData 是 part 的负载。
type PartData struct {
	Type      *string         `json:"type"`
	Text      *string         `json:"text"`
	Synthetic *bool           `json:"synthetic"`
	Auto      *bool           `json:"auto"`
	Time      *TimeRange      `json:"time"`
	Tool      *string         `json:"tool"`
	CallID    *string         `json:"callID"`
	Filename  *string         `json:"filename"`
	URL       *string         `json:"url"`
	State     *ToolState      `json:"state"`
	Input     json.RawMessage `json:"input"`
}

// ToolState 是工具调用的状态（时间在 state.time 上）。
type ToolState struct {
	Status string          `json:"status"`
	Input  json.RawMessage `json:"input"`
	Output json.RawMessage `json:"output"`
	Error  json.RawMessage `json:"error"`
	Title  *string         `json:"title"`
	Time   *TimeRange      `json:"time"`
}

// LooseNumber 接受数字、数字字符串或 null（对应 JS 里的 Number(x) 语义）。
// 其它类型直接报错，不静默当作 0。
type LooseNumber struct {
	Set bool
	V   float64
}

// UnmarshalJSON 实现数字的宽松解析。
func (n *LooseNumber) UnmarshalJSON(raw []byte) error {
	trimmed := strings.TrimSpace(string(raw))
	n.Set = false
	n.V = 0
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if v, err := strconv.ParseFloat(trimmed, 64); err == nil {
		n.Set = true
		n.V = v
		return nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if v, err := strconv.ParseFloat(strings.TrimSpace(asString), 64); err == nil {
			n.Set = true
			n.V = v
			return nil
		}
	}
	return fmt.Errorf("期望数字或 null，得到 %s", trimmed)
}
