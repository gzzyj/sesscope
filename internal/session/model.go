// Package session 定义与具体 Agent 产品无关的会话领域模型。
//
// 边界：
//   - 只描述"会话里客观发生了什么"：消息、部件、时间、状态、用量、错误、诊断；
//   - 不认任何存储格式（SQLite / JSONL / zstd），那是 internal/source/* 的事；
//   - 不认页面需要的派生语义（轮次 / 步骤 / 统计），那是 internal/trace 的事。
//
// 指针语义：字段为 nil 表示"源没有提供这个值"，绝不用零值冒充；页面/CLI 依赖这一点
// 来区分"耗时 0ms"和"没有耗时"。这也是本包几乎所有标量都用指针的原因。
package session

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
	Source SourceID
	Store  string // sqlite 文件 / 会话目录 / jsonl 文件
	ID     string // 原生会话 id
	Object string // 存储内定位（表+主键 / 文件+行号），供 --explain 溯源
}

// Provenance 记录"这次读取从哪来、完不完整"。
// Complete=false 用于仍在写入的会话、torn tail、校验失败等场景，必须配一条诊断。
type Provenance struct {
	Source   SourceID
	Store    string
	Format   string // 例如 opencode/v1、dsh/v4、codex/rollout、claude/jsonl
	LoadedAt int64
	Complete bool
}

// ModelRef 是会话或某一步用的模型。
type ModelRef struct {
	Provider *string
	ID       *string
	Variant  *string
}

// Tokens 是用量。字段为 nil 表示源没有提供。
type Tokens struct {
	Total      *int64
	Input      *int64
	Output     *int64
	Reasoning  *int64
	CacheRead  *int64
	CacheWrite *int64
}

// Error 是消息级或工具级错误。
// Detail 对应 opencode 的 data.error.data.message（provider 给的结构化文案），
// Message 对应 data.error.message（错误对象自身文案），两者优先级由视图层决定。
type Error struct {
	Name       *string
	Message    *string
	Detail     *string
	Code       *string
	StatusCode *int64
	Retryable  *bool
}

// Diagnostic 是一处显式降级（缺能力、格式未证实、读取不完整……）。
// 与页面 trace.Warning 同形，可以直接透传，不需要再翻译一次。
type Diagnostic struct {
	Level  string // info | warn | error
	Code   string
	Title  string
	Detail string
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
	Name *string
	URL  *string
}

// ToolCall 是一次工具调用。Args/Output/Error 保持原始字节
// （键序与数字写法有意义，页面按原样展示，见 internal/trace/jsjson.go）。
type ToolCall struct {
	CallID  *string
	Name    *string
	Kind    ToolKind
	Status  *string
	Args    []byte
	Output  []byte
	Error   []byte
	Title   *string
	StartMs *int64
	EndMs   *int64
}

// Part 是消息下的一段内容。
// StartMs/EndMs 是"有效执行区间"：文本/思考来自载荷里的 time.start/end，
// 工具来自工具状态里的 time；与 CreatedMs/UpdatedMs（落库/更新时间）不是一回事。
type Part struct {
	ID         string
	Kind       PartKind
	Type       string // 原生类型原文：页面 Timeline 需要原样输出
	CreatedMs  *int64
	UpdatedMs  *int64
	StartMs    *int64
	EndMs      *int64
	DurationMs *int64
	Text       *string
	Synthetic  *bool
	Auto       *bool
	File       *FileRef
	Tool       *ToolCall
}

// Message 是一条消息（用户输入 / 模型输出 / 工具结果 / 系统注入）。
type Message struct {
	ID          string
	Role        Role    // 存储层角色：轮次切分用它
	PayloadRole *string // 载荷里的角色：步骤展示用它（opencode 的 data.role）
	CreatedMs   *int64
	UpdatedMs   *int64
	CompletedMs *int64 // 步骤真正结束的时间（opencode 的 data.time.completed）
	Model       *ModelRef
	Finish      *string
	Error       *Error
	Tokens      *Tokens
	Synthetic   bool
	Parts       []Part
}

// DeclaredTurnTiming 是源自己声明的轮次耗时，只用于对账，不参与计算。
type DeclaredTurnTiming struct {
	MessageID  *string
	DurationMs *float64
}

// Declared 是源在导出/存储里自己算好的统计，本工具只拿它对账。
// 对不上时给出诊断，绝不直接采用。
type Declared struct {
	TotalToolDurationMs *int64
	TurnTimings         []DeclaredTurnTiming
}

// Meta 是会话级元信息。
type Meta struct {
	ID           *string
	Title        *string
	Slug         *string
	Directory    *string
	Agent        *string
	Version      *string
	WorkspaceKey string
	ParentID     *string
	Model        *ModelRef
	Cost         *float64
	CreatedMs    *int64
	UpdatedMs    *int64
	ExportedAt   *string // 导出动作发生的时间（旧导出里的 export_info.exported_at）
	ExportSource *string // 导出方声明的来源（旧导出里的 export_info.source）
	Provenance   Provenance
}

// Session 是一份完整会话。
type Session struct {
	Ref
	Meta        Meta
	Messages    []Message
	Declared    Declared
	Diagnostics []Diagnostic
	// Unknown 统计"本代码不认识的原生记录/事件类型"，是版本漂移的第一手信号。
	Unknown map[string]int
}

// Summary 是列表用的会话摘要：只依赖头部/索引即可得到，不需要全量解析。
type Summary struct {
	Ref
	WorkspaceKey string
	ParentID     string
	Title        string
	TitleSource  string // native | derived | none
	Agent        string
	Directory    string
	Model        *ModelRef
	CreatedMs    *int64
	UpdatedMs    *int64
	UpdatedKind  string // native | file-mtime
	Messages     int
	ToolCalls    int
	Tokens       Tokens
	Cost         *float64
	IsChild      bool
	Flags        []string // running | compacted | errored | seeded
	Diagnostics  []Diagnostic
}

// Workspace 是源层面的工作区（project / worktree / 目录分组）。
type Workspace struct {
	Source       SourceID
	Key          string // 源内稳定键
	ID           string // 原生 id
	Name         string
	Directory    string
	Kind         string // project | worktree | local | global
	VCS          string
	SessionCount int
	RootCount    int
	LastActiveMs *int64
	Origin       string // native | app-state | derived
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
