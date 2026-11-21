// Package session 定义与具体 Agent 产品无关的会话领域模型。
//
// 边界：
//   - 只描述"会话里客观发生了什么"：消息、部件、时间、状态、用量、错误、诊断；
//   - 不认任何存储格式（SQLite / JSONL / zstd），那是 internal/source/* 的事；
//   - 不认页面需要的派生语义（轮次 / 步骤 / 统计），那是 internal/trace 的事。
//
// 指针语义：字段为 nil 表示"源没有提供这个值"，绝不用零值冒充；页面/CLI 依赖这一点
// 来区分"耗时 0ms"和"没有耗时"。这也是本包几乎所有标量都用指针的原因。
//
// 本包同时是"规范导出格式"（atrace/session.v1）的 schema：
// json tag 就是对外契约，只允许新增字段，不允许改名。
package session

// Schema 是规范导出格式的版本标识。
const Schema = "atrace/session.v1"

// SourceID 是会话来源产品的稳定标识。
type SourceID string

const (
	Opencode SourceID = "opencode"
	DSH      SourceID = "dsh"
	Codex    SourceID = "codex"
	Claude   SourceID = "claude"
)

// Ref 定位一个会话：哪个源、哪个存储、原生 id、在存储里的对象。
type Ref struct {
	Source SourceID `json:"source"`
	Store  string   `json:"store,omitempty"`
	ID     string   `json:"id"`
	Object string   `json:"object,omitempty"`
}

// Provenance 记录"这次读取从哪来、完不完整"。
// Complete=false 用于仍在写入的会话、torn tail、校验失败等场景，必须配一条诊断。
type Provenance struct {
	Source   SourceID `json:"source"`
	Store    string   `json:"store,omitempty"`
	Format   string   `json:"format,omitempty"`
	LoadedAt int64    `json:"loaded_at,omitempty"`
	Complete bool     `json:"complete"`
}

// ModelRef 是会话或某一步用的模型。
type ModelRef struct {
	Provider *string `json:"provider,omitempty"`
	ID       *string `json:"id,omitempty"`
	Variant  *string `json:"variant,omitempty"`
}

// Tokens 是用量。字段为 nil 表示源没有提供。
type Tokens struct {
	Total      *int64 `json:"total,omitempty"`
	Input      *int64 `json:"input,omitempty"`
	Output     *int64 `json:"output,omitempty"`
	Reasoning  *int64 `json:"reasoning,omitempty"`
	CacheRead  *int64 `json:"cache_read,omitempty"`
	CacheWrite *int64 `json:"cache_write,omitempty"`
}

// Error 是消息级或工具级错误。
// Detail 对应 opencode 的 data.error.data.message（provider 给的结构化文案），
// Message 对应 data.error.message（错误对象自身文案），两者优先级由视图层决定。
type Error struct {
	Name       *string `json:"name,omitempty"`
	Message    *string `json:"message,omitempty"`
	Detail     *string `json:"detail,omitempty"`
	Code       *string `json:"code,omitempty"`
	StatusCode *int64  `json:"status_code,omitempty"`
	Retryable  *bool   `json:"retryable,omitempty"`
}

// Diagnostic 是一处显式降级（缺能力、格式未证实、读取不完整……）。
// 与页面 trace.Warning 同形，可以直接透传，不需要再翻译一次。
type Diagnostic struct {
	Level  string `json:"level"`
	Code   string `json:"code"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// Role 是消息角色（存储层口径）。
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleSystem    Role = "system"
	RoleDeveloper Role = "developer"
)

// PartKind 是部件语义类别。
type PartKind string

const (
	PartText       PartKind = "text"
	PartReasoning  PartKind = "reasoning"
	PartToolCall   PartKind = "tool-call"
	PartToolResult PartKind = "tool-result"
	PartCompaction PartKind = "compaction"
	PartFile       PartKind = "file"
	PartStepStart  PartKind = "step-start"
	PartStepFinish PartKind = "step-finish"
	PartOther      PartKind = "other"
)

// ToolKind 是工具的语义类别（与产品名解耦，归因规则只认它）。
type ToolKind string

const (
	ToolShell  ToolKind = "shell"
	ToolRead   ToolKind = "read"
	ToolWrite  ToolKind = "write"
	ToolEdit   ToolKind = "edit"
	ToolSearch ToolKind = "search"
	ToolList   ToolKind = "list"
	ToolWeb    ToolKind = "web"
	ToolAsk    ToolKind = "ask"
	ToolTask   ToolKind = "task"
	ToolOther  ToolKind = "other"
)

// Status 是消息/工具的完成状态。
type Status string

const (
	StatusCompleted Status = "completed"
	StatusRunning   Status = "running"
	StatusError     Status = "error"
	StatusPending   Status = "pending"
	StatusUnknown   Status = "unknown"
)

// FileRef 是附件/文件部件。
type FileRef struct {
	Name *string `json:"name,omitempty"`
	URL  *string `json:"url,omitempty"`
}

// ToolCall 是一次工具调用。Args/Output/Error 保持原始字节
// （键序与数字写法有意义，页面按原样展示，见 internal/trace/jsjson.go）。
type ToolCall struct {
	CallID  *string  `json:"call_id,omitempty"`
	Name    *string  `json:"name,omitempty"`
	Kind    ToolKind `json:"kind,omitempty"`
	Status  *string  `json:"status,omitempty"`
	Args    []byte   `json:"args,omitempty"`
	Output  []byte   `json:"output,omitempty"`
	Error   []byte   `json:"error,omitempty"`
	Title   *string  `json:"title,omitempty"`
	StartMs *int64   `json:"start_ms,omitempty"`
	EndMs   *int64   `json:"end_ms,omitempty"`
}

// Part 是消息下的一段内容。
// StartMs/EndMs 是"有效执行区间"：文本/思考来自载荷里的 time.start/end，
// 工具来自工具状态里的 time；与 CreatedMs/UpdatedMs（落库/更新时间）不是一回事。
type Part struct {
	ID         string    `json:"id"`
	Kind       PartKind  `json:"kind"`
	Type       string    `json:"type"`
	CreatedMs  *int64    `json:"created_ms,omitempty"`
	UpdatedMs  *int64    `json:"updated_ms,omitempty"`
	StartMs    *int64    `json:"start_ms,omitempty"`
	EndMs      *int64    `json:"end_ms,omitempty"`
	DurationMs *int64    `json:"duration_ms,omitempty"`
	Text       *string   `json:"text,omitempty"`
	Synthetic  *bool     `json:"synthetic,omitempty"`
	Auto       *bool     `json:"auto,omitempty"`
	File       *FileRef  `json:"file,omitempty"`
	Tool       *ToolCall `json:"tool,omitempty"`
}

// Message 是一条消息（用户输入 / 模型输出 / 工具结果 / 系统注入）。
type Message struct {
	ID          string    `json:"id"`
	Role        Role      `json:"role"`
	PayloadRole *string   `json:"payload_role,omitempty"`
	CreatedMs   *int64    `json:"created_ms,omitempty"`
	UpdatedMs   *int64    `json:"updated_ms,omitempty"`
	CompletedMs *int64    `json:"completed_ms,omitempty"`
	Model       *ModelRef `json:"model,omitempty"`
	Finish      *string   `json:"finish,omitempty"`
	Error       *Error    `json:"error,omitempty"`
	Tokens      *Tokens   `json:"tokens,omitempty"`
	Synthetic   bool      `json:"synthetic,omitempty"`
	Parts       []Part    `json:"parts"`
}

// DeclaredTurnTiming 是源自己声明的轮次耗时，只用于对账，不参与计算。
type DeclaredTurnTiming struct {
	MessageID  *string  `json:"message_id,omitempty"`
	DurationMs *float64 `json:"duration_ms,omitempty"`
}

// Declared 是源在导出/存储里自己算好的统计，本工具只拿它对账。
// 对不上时给出诊断，绝不直接采用。
type Declared struct {
	TotalToolDurationMs *int64               `json:"total_tool_duration_ms,omitempty"`
	TurnTimings         []DeclaredTurnTiming `json:"turn_timings,omitempty"`
}

// Meta 是会话级元信息。
type Meta struct {
	ID           *string    `json:"id,omitempty"`
	Title        *string    `json:"title,omitempty"`
	Slug         *string    `json:"slug,omitempty"`
	Directory    *string    `json:"directory,omitempty"`
	Agent        *string    `json:"agent,omitempty"`
	Version      *string    `json:"version,omitempty"`
	WorkspaceKey string     `json:"workspace_key,omitempty"`
	ParentID     *string    `json:"parent_id,omitempty"`
	Model        *ModelRef  `json:"model,omitempty"`
	Cost         *float64   `json:"cost,omitempty"`
	CreatedMs    *int64     `json:"created_ms,omitempty"`
	UpdatedMs    *int64     `json:"updated_ms,omitempty"`
	ExportedAt   *string    `json:"exported_at,omitempty"`
	ExportSource *string    `json:"export_source,omitempty"`
	Provenance   Provenance `json:"provenance"`
}

// Session 是一份完整会话。
type Session struct {
	Ref         `json:"ref"`
	Meta        Meta         `json:"meta"`
	Messages    []Message    `json:"messages"`
	Declared    Declared     `json:"declared,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	// Unknown 统计"本代码不认识的原生记录/事件类型"，是版本漂移的第一手信号。
	Unknown map[string]int `json:"unknown,omitempty"`
}

// Summary 是列表用的会话摘要：只依赖头部/索引即可得到，不需要全量解析。
type Summary struct {
	Ref          `json:"ref"`
	WorkspaceKey string       `json:"workspace_key,omitempty"`
	ParentID     string       `json:"parent_id,omitempty"`
	Title        string       `json:"title"`
	TitleSource  string       `json:"title_source,omitempty"`
	Agent        string       `json:"agent,omitempty"`
	Directory    string       `json:"directory,omitempty"`
	Model        *ModelRef    `json:"model,omitempty"`
	CreatedMs    *int64       `json:"created_ms,omitempty"`
	UpdatedMs    *int64       `json:"updated_ms,omitempty"`
	UpdatedKind  string       `json:"updated_kind,omitempty"`
	Messages     int          `json:"messages"`
	ToolCalls    int          `json:"tool_calls"`
	Tokens       Tokens       `json:"tokens"`
	Cost         *float64     `json:"cost,omitempty"`
	IsChild      bool         `json:"is_child,omitempty"`
	Flags        []string     `json:"flags,omitempty"`
	Diagnostics  []Diagnostic `json:"diagnostics,omitempty"`
}

// Workspace 是源层面的工作区（project / worktree / 目录分组）。
type Workspace struct {
	Source       SourceID `json:"source"`
	Key          string   `json:"key"`
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Directory    string   `json:"directory,omitempty"`
	Kind         string   `json:"kind,omitempty"`
	VCS          string   `json:"vcs,omitempty"`
	SessionCount int      `json:"session_count"`
	RootCount    int      `json:"root_count"`
	LastActiveMs *int64   `json:"last_active_ms,omitempty"`
	Origin       string   `json:"origin,omitempty"`
}

// ---- 指针构造小工具（适配器用，避免到处写临时变量） ----

// Str 返回字符串指针。
func Str(value string) *string { return &value }

// I64 返回 int64 指针。
func I64(value int64) *int64 { return &value }

// F64 返回 float64 指针。
func F64(value float64) *float64 { return &value }

// Bool 返回 bool 指针。
func Bool(value bool) *bool { return &value }
