package dsh

import (
	"encoding/json"

	"github.com/zhouyuejie/opencode-trace/internal/session"
)

// 文件头的版本号（session.v4.jsonl.zstd）与事件里的 version 字段一致。
const (
	formatName = "dsh/jsonl"
)

// header 是文件第一行。
type header struct {
	Type            string `json:"type"`
	Version         int    `json:"version"`
	ID              string `json:"id"`
	CreatedAt       int64  `json:"createdAt"`
	Cwd             string `json:"cwd"`
	AgentPreset     string `json:"agentPreset"`
	ParentSession   string `json:"parentSession"`
	DelegationDepth int    `json:"delegationDepth"`
	IsSeeded        bool   `json:"isSeeded"`
}

// envelope 是事件信封。
type envelope struct {
	Type   string          `json:"type"`
	Seq    int64           `json:"seq"`
	Time   int64           `json:"time"`
	Data   json.RawMessage `json:"data"`
	Source []int64         `json:"sourceEventSeqs"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type chatMessage struct {
	ID      string         `json:"id"`
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type usage struct {
	InputTokens       *int64 `json:"inputTokens"`
	OutputTokens      *int64 `json:"outputTokens"`
	TotalTokens       *int64 `json:"totalTokens"`
	ReasoningTokens   *int64 `json:"reasoningTokens"`
	CachedInputTokens *int64 `json:"cachedInputTokens"`
	CacheReadTokens   *int64 `json:"cacheReadTokens"`
	CacheWriteTokens  *int64 `json:"cacheWriteTokens"`
}

type assistantData struct {
	Turn        int            `json:"turn"`
	Step        int            `json:"step"`
	Message     chatMessage    `json:"message"`
	Usage       *usage         `json:"usage"`
	Stream      []streamRecord `json:"stream"`
	Interrupted bool           `json:"interrupted"`
}

// streamRecord 是流式分片记录：只取 block-start/block-end 的时间，用来还原每个块的耗时。
type streamRecord struct {
	Type  string `json:"type"`
	Time  *int64 `json:"time"`
	Chunk *struct {
		Type   string `json:"type"`
		Index  int    `json:"index"`
		Reason *struct {
			Kind string `json:"kind"`
		} `json:"reason"`
	} `json:"chunk"`
}

type userData struct {
	ID      string         `json:"id"`
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
	Source  *struct {
		Kind string `json:"kind"`
	} `json:"source"`
}

type toolCallData struct {
	Turn      int             `json:"turn"`
	Step      int             `json:"step"`
	CallID    string          `json:"callId"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolResultData struct {
	Turn    int `json:"turn"`
	Step    int `json:"step"`
	Message struct {
		Role       string         `json:"role"`
		ToolCallID string         `json:"toolCallId"`
		Content    []contentBlock `json:"content"`
		IsError    bool           `json:"isError"`
	} `json:"message"`
	Error *struct {
		Name string `json:"name"`
		Code string `json:"code"`
	} `json:"error"`
}

type stepData struct {
	Turn int `json:"turn"`
	Step int `json:"step"`
}

type turnData struct {
	Turn   int `json:"turn"`
	Reason *struct {
		Kind string `json:"kind"`
	} `json:"reason"`
}

type compactionData struct {
	CompactionID string `json:"compactionId"`
	Turn         int    `json:"turn"`
}

type configData struct {
	Header struct {
		Config struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"config"`
	} `json:"header"`
}

type selectionData struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type titleData struct {
	Title string `json:"title"`
}

type systemMessageData struct {
	Message chatMessage `json:"message"`
}

// knownIgnored 是"确认与对话无关、不需要映射"的事件类型。
// 放在这里而不是塞进 Unknown：Unknown 是版本漂移信号，不能被已知噪声淹没。
var knownIgnored = map[string]bool{
	"agent/inbox/spliced":             true,
	"agent-preset/selected":           true,
	"approval/policy":                 true,
	"command/done":                    true,
	"command/run":                     true,
	"deliverables/presented":          true,
	"job/registered":                  true,
	"job/settled":                     true,
	"model/selection":                 true,
	"permission/preset":               true,
	"plan/mode":                       true,
	"request/context":                 true,
	"request/header":                  true,
	"sandbox/mode":                    true,
	"session/end-seed":                true,
	"session/title-llm-request":       true,
	"subagent/catalog":                true,
	"subagent/descriptor":             true,
	"subagent/model-selection-policy": true,
	"team/member":                     true,
	"team/message/delivered":          true,
	"team/message/queued":             true,
	"team/mode":                       true,
	"team/task":                       true,
	"todo/write":                      true,
	"tool/ptc-dispatch":               true,
	"tool/ptc-dispatch-start":         true,
	"web/deepseek-search-llm-request": true,
	"workspace/changes":               true,
	"compaction/prune":                true,
	"compaction/summary":              true,
}

var _ = session.PartText
