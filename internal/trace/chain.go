package trace

// Chain 是一条"补偿链"：从某个故障触发开始，到恢复（或转入正常工作）为止的连续步区间。
// 事件链（Episode）回答"哪里坏了"，补偿链回答"为此连续付出了多少步"。
type Chain struct {
	Code       string     `json:"code"`
	Label      string     `json:"label"`
	Confidence string     `json:"confidence"`
	Turn       int        `json:"turn"`
	StartStep  int        `json:"startStep"`
	EndStep    int        `json:"endStep"`
	Steps      int        `json:"steps"`
	StartMs    int64      `json:"startMs"`
	EndMs      int64      `json:"endMs"`
	WindowMs   int64      `json:"windowMs"`
	Subject    []string   `json:"subject"`
	Episodes   []int      `json:"episodes"`
	Evidence   []Evidence `json:"evidence"`
	Outcome    string     `json:"outcome"`
}
