// Package source 定义"源无关的会话探查"抽象：定位存储、列会话、取单个会话。
//
// 设计约束（见 docs/architecture.md）：
//   - 必需接口只有三个方法（Locate/Sessions/Open）；工作区、进程、体检、应用态叠加
//     都是可选接口，源不支持就不实现，调用方用类型断言判断，不做空实现。
//   - 能力用 Capabilities 位显式声明，不允许"调用后拿到空值再猜"。
//   - 缺能力必须产出 session.Diagnostic（见各适配器），调用方负责把它显示出来。
package source

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gzzyj/sesscope/internal/session"
)

// ListCost 描述"列出会话"要读多少数据，用于给用户提示代价。
type ListCost string

const (
	ListConstant   ListCost = "constant"    // 索引/表查询
	ListHeaderScan ListCost = "header-scan" // 每个会话只读头部/首帧
	ListFullScan   ListCost = "full-scan"   // 必须全量扫描
)

// OpenCost 描述"打开一个会话"的内存代价。
type OpenCost string

const (
	OpenStream   OpenCost = "stream"
	OpenFullLoad OpenCost = "full-load"
)

// Capabilities 是源能力的显式声明。
type Capabilities struct {
	ProcessDiscovery  bool // 有运行时进程可查（PID/端口）
	WorkspaceCatalog  bool // 有原生工作区概念（project/workspace）
	ServerSideFilter  bool // 过滤/排序能在存储层做
	PrefixLookup      bool // 支持 id 前缀直接定位
	SessionTitles     bool // 标题来自存储（否则由首条用户输入派生）
	SubagentTree      bool // 能识别子会话/父子关系
	TurnHints         bool // 有原生 turn/step 编号
	Tokens            bool
	Cost              bool
	ReasoningText     bool // 思考文本可读（不是加密或未记录）
	ToolArgs          bool
	ToolOutput        bool
	ToolStatus        bool
	CompactionRecords bool
	LiveActivity      bool // 能判断"会话正在写"
	ListCost          ListCost
	OpenCost          OpenCost
}

// Store 是一个候选数据存储及其健康度。Rule/Evidence 让 --explain 能回答
// "为什么选了这个库/目录"。
type Store struct {
	Source    session.SourceID
	Path      string
	Kind      string // sqlite | jsonl-zstd | jsonl-dir
	Format    string // 例如 opencode/sqlite、dsh/jsonl、codex/rollout、claude/jsonl
	Version   string // v1 / v2 / v4 / rollout …
	Exists    bool
	Readable  bool
	ReadOnly  bool
	SizeBytes int64
	ModTime   int64
	Rule      string // l0:--store / l1:env / l2:default / l3:pid
	Evidence  string // 判定依据（表名集合、首行内容、目录名……）
	Detail    string // 不可读/不支持的原因，必须写清楚
}

// Locator 是用户显式给出的定位信息（优先级最高）。
type Locator struct {
	Store string // --store/--db：直接指定存储
	Home  string // --home：产品数据根目录
	PID   int    // --pid：从进程环境推存储
}

// Query 是会话列表的过滤/排序条件。
//
// 契约：适配器"可以"下推 Prefix/Since/Until/Limit 做优化，但调用方必须再跑一次
// ApplyQuery 统一语义——四源的过滤行为不允许有差异。
type Query struct {
	Prefix          string
	Workspace       string
	Grep            string
	SinceMs         *int64
	UntilMs         *int64
	Limit           int
	IncludeChildren bool
	Sort            string // updated | created | tokens | messages
}

// OpenOptions 控制取数时的上限。
type OpenOptions struct {
	MaxTextBytes  int  // >0 时截断单个文本/工具输出，并登记诊断
	IncludeSystem bool // 是否保留 system/developer 消息（默认保留在 Messages 里，不参与轮次）
}

// Source 是每个 Agent 产品必须实现的最小接口。
type Source interface {
	ID() session.SourceID
	Capabilities() Capabilities
	// Locate 返回候选存储（含不可用项，便于 sources/doctor 如实展示）。
	Locate(ctx context.Context, l Locator) ([]Store, error)
	// Sessions 返回该存储里的会话摘要；可以忽略 Query 的过滤，但 Prefix 必须生效。
	Sessions(ctx context.Context, store Store, q Query) ([]session.Summary, error)
	// Open 取一个会话的完整领域模型。
	Open(ctx context.Context, store Store, ref session.Ref, o OpenOptions) (*session.Session, error)
}

// WorkspaceLister 是可选能力：有原生工作区概念的源实现它。
type WorkspaceLister interface {
	Workspaces(ctx context.Context, store Store) ([]session.Workspace, error)
}

// ProcessLister 是可选能力：有可探测运行时的源实现它（仅作提示，不进主链路）。
type ProcessLister interface {
	Processes(ctx context.Context) ([]Process, error)
}

// Diagnoser 是可选能力：给出该存储的体检结果。
type Diagnoser interface {
	Doctor(ctx context.Context, store Store) []Check
}

// LegacyExporter 是可选能力：仍能输出旧格式导出（opencode 的 session-export.json）。
// 只有历史包袱重的来源才实现它；其它来源用规范格式。
type LegacyExporter interface {
	LegacyDocument(ctx context.Context, store Store, id string, generatedAt time.Time) ([]byte, error)
}

// WorkspaceAugmenter 是可选能力：把外部应用态（如 DeepWorks）叠加到工作区清单上。
type WorkspaceAugmenter interface {
	Augment(ctx context.Context, catalog *WorkspaceCatalog) []session.Diagnostic
}

// WorkspaceCatalog 是可被叠加的工作区清单。
type WorkspaceCatalog struct {
	Workspaces []session.Workspace
	Warnings   []string
}

// Process 是一个可探测的运行中实例（只用于展示与 --pid 推导）。
type Process struct {
	PID         int
	PPID        int
	Name        string
	Args        []string
	Port        string
	StartedMs   int64
	Store       string
	StoreExists bool
}

// Check 是体检的一条结果。
type Check struct {
	Level  string
	Code   string
	Title  string
	Detail string
}

// 稳定的错误族，CLI 按它映射退出码。
var (
	ErrNotFound    = errors.New("会话不存在")
	ErrAmbiguous   = errors.New("会话 id 前缀歧义")
	ErrUnsupported = errors.New("来源或格式不支持")
)

// AmbiguousError 带上候选，方便 CLI 直接打表，绝不"猜一个"。
type AmbiguousError struct {
	Selector   string
	Candidates []Candidate
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%q 命中了 %d 个会话，请给更长的前缀或用 source:id 形式", e.Selector, len(e.Candidates))
}

func (e *AmbiguousError) Is(target error) bool { return target == ErrAmbiguous }

// UnsupportedError 说明为什么不支持（版本/表集/记录类型），CLI 据此退出码 3。
type UnsupportedError struct {
	Source session.SourceID
	Store  string
	Reason string
}

func (e *UnsupportedError) Error() string {
	if e.Store == "" {
		return fmt.Sprintf("%s：%s", e.Source, e.Reason)
	}
	return fmt.Sprintf("%s（%s）：%s", e.Source, e.Store, e.Reason)
}

func (e *UnsupportedError) Is(target error) bool { return target == ErrUnsupported }

// Candidate 是前缀匹配到的一个候选会话。
type Candidate struct {
	Ref       session.Ref
	Title     string
	UpdatedMs *int64
}
