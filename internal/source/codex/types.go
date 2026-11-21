package codex

import "encoding/json"

// rollout 里的一行。
type rolloutRecord struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// session_meta.payload
type sessionMeta struct {
	SessionID     string `json:"session_id"`
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	Cwd           string `json:"cwd"`
	Originator    string `json:"originator"`
	CLIVersion    string `json:"cli_version"`
	ModelProvider string `json:"model_provider"`
	Source        string `json:"source"`
	ThreadSource  string `json:"thread_source"`
}

// event_msg.payload：type 决定后面哪些字段有效。
type eventPayload struct {
	Type          string          `json:"type"`
	TurnID        string          `json:"turn_id"`
	Item          json.RawMessage `json:"item"`
	StartedAtMs   *int64          `json:"started_at_ms"`
	CompletedAtMs *int64          `json:"completed_at_ms"`
	DurationMs    *int64          `json:"duration_ms"`
	Error         *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// item_completed.payload.item
type item struct {
	Type    string        `json:"type"`
	ID      string        `json:"id"`
	Content []contentItem `json:"content"`
}

// 内容片段（message.content 与 item.content 共用）。
type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// response_item.payload
type responseItem struct {
	Type             string          `json:"type"`
	ID               string          `json:"id"`
	Role             string          `json:"role"`
	Content          []contentItem   `json:"content"`
	Name             string          `json:"name"`
	CallID           string          `json:"call_id"`
	Arguments        json.RawMessage `json:"arguments"`
	Input            json.RawMessage `json:"input"`
	Output           json.RawMessage `json:"output"`
	Summary          []contentItem   `json:"summary"`
	EncryptedContent string          `json:"encrypted_content"`
}

// turn_context.payload
type turnContext struct {
	TurnID   string `json:"turn_id"`
	Cwd      string `json:"cwd"`
	Timezone string `json:"timezone"`
}

// token_usage_record.payload
type tokenUsage struct {
	TurnID string `json:"turn_id"`
	Usage  struct {
		InputTokens           *int64 `json:"input_tokens"`
		CachedInputTokens     *int64 `json:"cached_input_tokens"`
		OutputTokens          *int64 `json:"output_tokens"`
		ReasoningOutputTokens *int64 `json:"reasoning_output_tokens"`
		TotalTokens           *int64 `json:"total_tokens"`
	} `json:"usage"`
}

// compacted.payload
type compactedPayload struct {
	Message string `json:"message"`
}

// knownIgnored 是确认与对话无关的记录类型（见 docs/sources/codex.md）。
var knownIgnored = map[string]bool{
	"world_state":                       true,
	"event_msg/thread_settings_applied": true,
	"event_msg/token_count":             true,
	"response_item/web_search_call":     true,
	"response_item/ghost_snapshot":      true,
}
