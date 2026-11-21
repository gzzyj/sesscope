package trace

// Episode 是一条"归因事件链"：一次触发 + 一串补偿动作 + 恢复点。
// 由 internal/attribution 计算后挂到 Data.Episodes 上，页面与 analyze 命令共用。
type Episode struct {
	Code       string     `json:"code"`
	Label      string     `json:"label"`
	Severity   string     `json:"severity"`
	Confidence string     `json:"confidence"`
	Turn       int        `json:"turn"`
	Step       int        `json:"step"`
	StartMs    int64      `json:"startMs"`
	EndMs      int64      `json:"endMs"`
	WindowMs   int64      `json:"windowMs"`
	Evidence   []Evidence `json:"evidence"`
	Response   []string   `json:"response"`
	Outcome    string     `json:"outcome"`
}

// Evidence 是归因的一条证据（命令原文 / 错误码 / 输出片段 / token 轨迹）。
type Evidence struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}
