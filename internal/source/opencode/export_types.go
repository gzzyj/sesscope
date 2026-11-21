package opencode

// Document 是导出的完整 JSON 文档（render 直接吃这个）。
type Document struct {
	ExportInfo  ExportInfo    `json:"export_info"`
	Session     SessionRecord `json:"session"`
	GlobalStats GlobalStats   `json:"global_stats"`
	TurnTimings []TurnTiming  `json:"turn_timings"`
	Messages    []MessageJSON `json:"messages"`
}

// ExportInfo 记录导出动作本身（和既有导出口径一致）。
type ExportInfo struct {
	ExportedAt string `json:"exported_at"`
	Source     string `json:"source"`
	SessionID  string `json:"session_id"`
	Scope      string `json:"scope"`
	Excluded   string `json:"excluded"`
	TimeNote   string `json:"time_note"`
}

// SessionRecord 是 session 表的整行；能解析成 JSON 的列按 JSON 值输出。
type SessionRecord struct {
	ID               string  `json:"id"`
	ProjectID        string  `json:"project_id"`
	WorkspaceID      *string `json:"workspace_id"`
	ParentID         *string `json:"parent_id"`
	Slug             string  `json:"slug"`
	Directory        string  `json:"directory"`
	Path             *string `json:"path"`
	Title            string  `json:"title"`
	Version          string  `json:"version"`
	ShareURL         *string `json:"share_url"`
	SummaryAdditions *int64  `json:"summary_additions"`
	SummaryDeletions *int64  `json:"summary_deletions"`
	SummaryFiles     *int64  `json:"summary_files"`
	Metadata         any     `json:"metadata"`
	Cost             float64 `json:"cost"`
	TokensInput      int64   `json:"tokens_input"`
	TokensOutput     int64   `json:"tokens_output"`
	TokensReasoning  int64   `json:"tokens_reasoning"`
	TokensCacheRead  int64   `json:"tokens_cache_read"`
	TokensCacheWrite int64   `json:"tokens_cache_write"`
	Revert           any     `json:"revert"`
	Permission       any     `json:"permission"`
	Agent            *string `json:"agent"`
	Model            any     `json:"model"`
	TimeCreated      int64   `json:"time_created"`
	TimeUpdated      int64   `json:"time_updated"`
	TimeCompacting   *int64  `json:"time_compacting"`
	TimeArchived     *int64  `json:"time_archived"`
}

// GlobalStats 是整份会话的汇总。
type GlobalStats struct {
	MessageCount             int            `json:"message_count"`
	RoleCounts               map[string]int `json:"role_counts"`
	PartTypeCounts           map[string]int `json:"part_type_counts"`
	FirstActivityMs          *int64         `json:"first_activity_ms"`
	FirstActivityISO         *string        `json:"first_activity_iso"`
	LastActivityMs           *int64         `json:"last_activity_ms"`
	LastActivityISO          *string        `json:"last_activity_iso"`
	TotalWallDurationMs      *int64         `json:"total_wall_duration_ms"`
	TotalReasoningDurationMs int64          `json:"total_reasoning_duration_ms"`
	TotalToolDurationMs      int64          `json:"total_tool_duration_ms"`
	TotalTurns               int            `json:"total_turns"`
}

// TurnTiming 是一条 assistant 消息的时间画像。
type TurnTiming struct {
	MessageID           string    `json:"message_id"`
	TurnStartMs         *int64    `json:"turn_start_ms"`
	TurnEndMs           *int64    `json:"turn_end_ms"`
	TurnDurationMs      *int64    `json:"turn_duration_ms"`
	TurnStartISO        *string   `json:"turn_start_iso"`
	TurnEndISO          *string   `json:"turn_end_iso"`
	Steps               int       `json:"steps"`
	ReasoningParts      int       `json:"reasoning_parts"`
	ReasoningDurationMs int64     `json:"reasoning_duration_ms"`
	ToolCalls           int       `json:"tool_calls"`
	ToolDurationMs      int64     `json:"tool_duration_ms"`
	TextParts           int       `json:"text_parts"`
	TokensByStep        []any     `json:"tokens_by_step"`
	CostByStep          []float64 `json:"cost_by_step"`
}

// MessageJSON 是一条消息（含 part）。
type MessageJSON struct {
	MessageID      string         `json:"message_id"`
	Role           string         `json:"role"`
	TimeCreatedMs  int64          `json:"time_created_ms"`
	TimeUpdatedMs  int64          `json:"time_updated_ms"`
	TimeCreatedISO string         `json:"time_created_iso"`
	TimeUpdatedISO string         `json:"time_updated_iso"`
	ModelID        *string        `json:"model_id"`
	ModelProvider  *string        `json:"model_provider"`
	SummaryKeys    []string       `json:"summary_keys"`
	Data           any            `json:"data"`
	Parts          []PartJSON     `json:"parts"`
	PartTimes      []PartTimeJSON `json:"part_times"`
}

// PartJSON 是一段内容。
type PartJSON struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	TimeCreatedMs int64  `json:"time_created_ms"`
	TimeUpdatedMs int64  `json:"time_updated_ms"`
	DurationMs    int64  `json:"duration_ms"`
	Data          any    `json:"data"`
}

// PartTimeJSON 是单个 part 的有效时间区间（tool 用 state.time，reasoning/text 用 time）。
type PartTimeJSON struct {
	PartID     string `json:"part_id"`
	Type       string `json:"type"`
	StartMs    *int64 `json:"start_ms"`
	EndMs      *int64 `json:"end_ms"`
	DurationMs *int64 `json:"duration_ms"`
}
