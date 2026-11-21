package trace

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Data 是页面直接消费的归一化结果；字段声明顺序就是 JSON 键顺序
// （与 Node 版保持逐字节一致，方便用 diff 验收）。
type Data struct {
	Meta     Meta      `json:"meta"`
	Stats    Stats     `json:"stats"`
	Warnings []Warning `json:"warnings"`
	Turns    []Turn    `json:"turns"`
	// Episodes 是归因事件链（由 external attribution 包计算后填入；没有就是空数组）。
	Episodes []Episode `json:"episodes"`
	// Chains 是补偿链：一次故障引发的连续补救步区间。
	Chains []Chain `json:"chains"`
}

// Meta 是会话级元信息。
type Meta struct {
	SessionID   *string   `json:"sessionId"`
	Title       *string   `json:"title"`
	Slug        *string   `json:"slug"`
	Directory   *string   `json:"directory"`
	Agent       *string   `json:"agent"`
	Version     *string   `json:"version"`
	Model       *ModelRef `json:"model"`
	ExportedAt  *string   `json:"exportedAt"`
	Source      *string   `json:"source"`
	FirstMs     *int64    `json:"firstMs"`
	LastMs      *int64    `json:"lastMs"`
	FirstIso    *string   `json:"firstIso"`
	LastIso     *string   `json:"lastIso"`
	WallMs      *int64    `json:"wallMs"`
	GeneratedAt string    `json:"generatedAt"`
	ExportPath  string    `json:"exportPath"`
}

// ModelRef 是会话用的模型。
type ModelRef struct {
	ID       *string `json:"id"`
	Provider *string `json:"provider"`
	Variant  *string `json:"variant"`
}

// Stats 是整份会话的汇总。
type Stats struct {
	Turns        int    `json:"turns"`
	UserTurns    int    `json:"userTurns"`
	Steps        int    `json:"steps"`
	ToolCalls    int    `json:"toolCalls"`
	Errors       int    `json:"errors"`
	ErrorTools   int    `json:"errorTools"`
	Running      int    `json:"running"`
	ToolRunning  int    `json:"toolRunning"`
	ModelMs      int64  `json:"modelMs"`
	ReasoningMs  int64  `json:"reasoningMs"`
	TextMs       int64  `json:"textMs"`
	ToolMs       int64  `json:"toolMs"`
	WaitHumanMs  int64  `json:"waitHumanMs"`
	StepMs       int64  `json:"stepMs"`
	CompactionMs int64  `json:"compactionMs"`
	BusyMs       int64  `json:"busyMs"`
	IdleMs       int64  `json:"idleMs"`
	Tokens       Tokens `json:"tokens"`
	SlowestTool  *Tool  `json:"slowestTool"`
}

// Tokens 是 token 汇总。
type Tokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	Reasoning  int64 `json:"reasoning"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
}

// Warning 是页面「数据提示」里的一条。
type Warning struct {
	Level  string `json:"level"`
	Code   string `json:"code"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// Turn 是一轮（或压缩段）。
type Turn struct {
	Index      int         `json:"index"`
	Kind       string      `json:"kind"`
	MessageID  string      `json:"messageId"`
	StartMs    *int64      `json:"startMs"`
	EndMs      *int64      `json:"endMs"`
	Input      TurnInput   `json:"input"`
	Compaction *Compaction `json:"compaction"`
	Steps      []Step      `json:"steps"`
}

// TurnInput 是本轮的输入（用户手输 / 自动续跑 / 压缩）。
type TurnInput struct {
	Text          string   `json:"text"`
	Attachments   []string `json:"attachments"`
	SyntheticOnly bool     `json:"syntheticOnly"`
	SyntheticText *string  `json:"syntheticText"`
}

// Compaction 是上下文压缩段。
type Compaction struct {
	DurationMs *int64 `json:"durationMs"`
	Auto       bool   `json:"auto"`
}

// Step 是一次模型调用（一个 assistant message 的一个 step）。
type Step struct {
	Index           int            `json:"index"`
	TurnIndex       int            `json:"turnIndex"`
	MessageID       string         `json:"messageId"`
	Role            string         `json:"role"`
	ModelID         *string        `json:"modelId"`
	Provider        *string        `json:"provider"`
	StartMs         *int64         `json:"startMs"`
	EndMs           *int64         `json:"endMs"`
	StepMs          *int64         `json:"stepMs"`
	ModelStartMs    *int64         `json:"modelStartMs"`
	ModelEndMs      *int64         `json:"modelEndMs"`
	ModelMs         *int64         `json:"modelMs"`
	ModelSpanSource string         `json:"modelSpanSource"`
	ReasoningMs     int64          `json:"reasoningMs"`
	TextMs          int64          `json:"textMs"`
	Finish          *string        `json:"finish"`
	Status          string         `json:"status"`
	Error           *StepError     `json:"error"`
	Tokens          *StepTokens    `json:"tokens"`
	Reasoning       []Span         `json:"reasoning"`
	Texts           []Span         `json:"texts"`
	Tools           []Tool         `json:"tools"`
	Timeline        []TimelineItem `json:"timeline"`
}

// StepError 是步骤级错误。
type StepError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
	// StatusCode / Retryable 来自 provider 错误（如 APIError 的 403），没有则为 null。
	StatusCode *int64 `json:"statusCode"`
	Retryable  *bool  `json:"retryable"`
}

// StepTokens 是这一步的 token 用量（导出里缺字段时为 null）。
type StepTokens struct {
	Total      *int64 `json:"total"`
	Input      *int64 `json:"input"`
	Output     *int64 `json:"output"`
	Reasoning  *int64 `json:"reasoning"`
	CacheRead  *int64 `json:"cacheRead"`
	CacheWrite *int64 `json:"cacheWrite"`
}

// Span 是一段带时间的文本（reasoning / text）。
type Span struct {
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
	Text    string `json:"text"`
}

// Tool 是一次工具调用。TurnIndex/StepIndex 只供内部提示文案使用，不写进页面数据。
type Tool struct {
	CallID     string  `json:"callId"`
	PartID     string  `json:"partId"`
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	StartMs    *int64  `json:"startMs"`
	EndMs      *int64  `json:"endMs"`
	DurationMs *int64  `json:"durationMs"`
	WaitHuman  bool    `json:"waitHuman"`
	ArgsText   string  `json:"argsText"`
	OutputText string  `json:"outputText"`
	ErrorText  *string `json:"errorText"`
	Title      *string `json:"title"`

	TurnIndex int `json:"-"`
	StepIndex int `json:"-"`
}

// TimelineItem 是 step 内 part 的顺序记录（页面的「计时」页签用它追溯）。
type TimelineItem struct {
	Type      string `json:"type"`
	PartID    string `json:"partId"`
	CreatedMs *int64 `json:"createdMs"`
	UpdatedMs *int64 `json:"updatedMs"`
}

// Options 控制归一化里需要外部输入的部分。
type Options struct {
	// ExportPath 原样写进 meta.exportPath。
	ExportPath string
	// GeneratedAt 是 meta.generatedAt。
	GeneratedAt time.Time
}

// Normalize 把 opencode 导出归一化成页面用的 Data。
func Normalize(doc *Export, opts Options) (*Data, error) {
	if doc == nil {
		return nil, fmt.Errorf("导出为空")
	}
	if len(doc.Messages) == 0 {
		return nil, fmt.Errorf("导出里没有 messages：%s", opts.ExportPath)
	}
	session := doc.Session
	if session == nil {
		session = &Session{}
	}

	warnings := make([]Warning, 0, 8)
	turns := make([]Turn, 0, 8)
	current := -1 // turns 的下标；用下标而不是指针，避免切片扩容后指针失效
	for _, message := range doc.Messages {
		parts := sortedParts(message.Parts)
		if message.Role == "user" {
			turns = append(turns, newTurn(len(turns)+1, message, parts, &warnings))
			current = len(turns) - 1
			continue
		}
		if message.Role != "assistant" {
			continue
		}
		if current < 0 {
			warnings = append(warnings, Warning{
				Level:  "warn",
				Code:   "orphan-assistant",
				Title:  "有 assistant 消息没有所属轮次",
				Detail: message.MessageID + " 出现在任何 user message 之前，已跳过。",
			})
			continue
		}
		step := normalizeStep(message, parts, len(turns[current].Steps)+1, turns[current].Index)
		turns[current].Steps = append(turns[current].Steps, step)
	}

	finalizeTurnEnds(turns, &warnings)

	allSteps := make([]Step, 0, len(turns)*4)
	for _, turn := range turns {
		allSteps = append(allSteps, turn.Steps...)
	}
	allTools := make([]Tool, 0, len(allSteps))
	for _, step := range allSteps {
		allTools = append(allTools, step.Tools...)
	}

	busy := activityIntervals(allSteps, allTools, turns)
	firstMs, lastMs := activityBounds(allSteps, allTools, turns)

	runningSteps, errorSteps := statusIndexes(allSteps)
	runningTools := toolIndexesWithStatus(allTools, "running")
	toolErrorIndexes := toolIndexesWithStatus(allTools, "error")
	humanWaits := make([]Tool, 0, 4)
	for _, tool := range allTools {
		if tool.WaitHuman && tool.DurationMs != nil {
			humanWaits = append(humanWaits, tool)
		}
	}

	warnings = append(warnings, structureWarnings(allSteps, allTools, busy, runningSteps, errorSteps, toolErrorIndexes, humanWaits)...)
	warnings = append(warnings, reconciliationWarnings(doc, allTools, humanWaits, session)...)

	data := &Data{
		Meta: Meta{
			SessionID:   session.ID,
			Title:       session.Title,
			Slug:        session.Slug,
			Directory:   session.Directory,
			Agent:       session.Agent,
			Version:     session.Version,
			Model:       parseSessionModel(session.Model),
			ExportedAt:  exportedAt(doc),
			Source:      exportSource(doc),
			FirstMs:     firstMs,
			LastMs:      lastMs,
			FirstIso:    isoPtr(firstMs),
			LastIso:     isoPtr(lastMs),
			WallMs:      subInt64Ptr(firstMs, lastMs),
			GeneratedAt: opts.GeneratedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
			ExportPath:  opts.ExportPath,
		},
		Stats: Stats{
			Turns:        len(turns),
			UserTurns:    countTurns(turns, "user"),
			Steps:        len(allSteps),
			ToolCalls:    len(allTools),
			Errors:       len(errorSteps),
			ErrorTools:   len(toolErrorIndexes),
			Running:      len(runningSteps),
			ToolRunning:  len(runningTools),
			ModelMs:      sumSteps(allSteps, func(step Step) *int64 { return step.ModelMs }),
			ReasoningMs:  sumSteps(allSteps, func(step Step) *int64 { return int64Ptr(step.ReasoningMs) }),
			TextMs:       sumSteps(allSteps, func(step Step) *int64 { return int64Ptr(step.TextMs) }),
			ToolMs:       sumTools(allTools, func(tool Tool) (int64, bool) { return derefInt64(tool.DurationMs), !tool.WaitHuman }),
			WaitHumanMs:  sumTools(humanWaits, func(tool Tool) (int64, bool) { return derefInt64(tool.DurationMs), true }),
			StepMs:       sumSteps(allSteps, func(step Step) *int64 { return step.StepMs }),
			CompactionMs: sumCompaction(turns),
			BusyMs:       sumIntervals(busy),
			IdleMs:       sumIntervals(gapsOf(busy)),
			Tokens: Tokens{
				Input:      sumTokens(allSteps, func(tokens *StepTokens) *int64 { return tokens.Input }),
				Output:     sumTokens(allSteps, func(tokens *StepTokens) *int64 { return tokens.Output }),
				Reasoning:  sumTokens(allSteps, func(tokens *StepTokens) *int64 { return tokens.Reasoning }),
				CacheRead:  sumTokens(allSteps, func(tokens *StepTokens) *int64 { return tokens.CacheRead }),
				CacheWrite: sumTokens(allSteps, func(tokens *StepTokens) *int64 { return tokens.CacheWrite }),
			},
			SlowestTool: slowestTool(allTools),
		},
		Warnings: warnings,
		Turns:    turns,
	}
	return data, nil
}

func finalizeTurnEnds(turns []Turn, warnings *[]Warning) {
	for index := range turns {
		turn := &turns[index]
		var end *int64
		for _, step := range turn.Steps {
			end = maxInt64Ptr(end, step.EndMs)
		}
		if end != nil {
			turn.EndMs = end
		}
		if turn.EndMs == nil {
			turn.EndMs = turn.StartMs
		}
		if turn.StartMs != nil && turn.EndMs != nil && *turn.EndMs < *turn.StartMs {
			*warnings = append(*warnings, Warning{
				Level: "warn",
				Code:  "negative-turn",
				Title: fmt.Sprintf("第 %d 轮结束早于开始", turn.Index),
				Detail: "turn_start=" + isoOf(turn.StartMs) + " turn_end=" + isoOf(turn.EndMs) +
					"（opencode 侧该轮没有写结束时间）。本页把该轮视为未完成，不计算其耗时。",
			})
		}
	}
}

func activityIntervals(steps []Step, tools []Tool, turns []Turn) [][2]int64 {
	intervals := make([][2]int64, 0, len(steps)+len(tools))
	for _, step := range steps {
		if step.ModelStartMs != nil && step.ModelEndMs != nil && *step.ModelEndMs > *step.ModelStartMs {
			intervals = append(intervals, [2]int64{*step.ModelStartMs, *step.ModelEndMs})
		}
	}
	for _, tool := range tools {
		if tool.StartMs != nil && tool.EndMs != nil && *tool.EndMs > *tool.StartMs {
			intervals = append(intervals, [2]int64{*tool.StartMs, *tool.EndMs})
		}
	}
	for _, turn := range turns {
		if turn.Kind == "compaction" && turn.StartMs != nil {
			var duration int64
			if turn.Compaction != nil {
				duration = derefInt64(turn.Compaction.DurationMs)
			}
			intervals = append(intervals, [2]int64{*turn.StartMs, *turn.StartMs + duration})
		}
	}
	return mergeIntervals(intervals)
}

func activityBounds(steps []Step, tools []Tool, turns []Turn) (first, last *int64) {
	for _, step := range steps {
		first = minInt64Ptr(first, step.StartMs)
	}
	for _, turn := range turns {
		first = minInt64Ptr(first, turn.StartMs)
	}
	for _, step := range steps {
		last = maxInt64Ptr(last, step.EndMs)
	}
	for _, tool := range tools {
		last = maxInt64Ptr(last, tool.EndMs)
	}
	return first, last
}

func statusIndexes(steps []Step) (running, failed []int) {
	for index, step := range steps {
		switch step.Status {
		case "error":
			failed = append(failed, index)
		case "running":
			running = append(running, index)
		}
	}
	return running, failed
}

func toolIndexesWithStatus(tools []Tool, status string) []int {
	matched := make([]int, 0, 4)
	for index, tool := range tools {
		if tool.Status == status {
			matched = append(matched, index)
		}
	}
	return matched
}

func structureWarnings(steps []Step, tools []Tool, busy [][2]int64, runningSteps, errorSteps, toolErrors []int, humanWaits []Tool) []Warning {
	out := make([]Warning, 0, 6)

	if len(runningSteps) > 0 {
		parts := make([]string, 0, len(runningSteps))
		for _, index := range runningSteps {
			step := steps[index]
			lastPart := "无"
			if len(step.Tools) > 0 {
				lastPart = step.Tools[len(step.Tools)-1].Name
			}
			parts = append(parts, fmt.Sprintf("第 %d 轮第 %d 步（%s，最后 part: %s）", step.TurnIndex, step.Index, step.MessageID, lastPart))
		}
		out = append(out, Warning{
			Level:  "warn",
			Code:   "unfinished-step",
			Title:  fmt.Sprintf("%d 个步骤在导出时未完成", len(runningSteps)),
			Detail: strings.Join(parts, "；") + "：没有 data.time.completed，工具也没有结束时间。时间轴上只画开始标记（琥珀色「未完成」），不虚构耗时。",
		})
	}

	if len(errorSteps) > 0 {
		parts := make([]string, 0, len(errorSteps))
		for _, index := range errorSteps {
			step := steps[index]
			name, message := "错误", ""
			if step.Error != nil {
				name, message = step.Error.Name, step.Error.Message
			}
			parts = append(parts, fmt.Sprintf("第 %d 轮第 %d 步 · %s · %s", step.TurnIndex, step.Index, name, message))
		}
		out = append(out, Warning{
			Level:  "error",
			Code:   "aborted-step",
			Title:  fmt.Sprintf("%d 个步骤被中止（红色）", len(errorSteps)),
			Detail: strings.Join(parts, "；") + "：中止时间取自 data.time.completed，因此该段红条表示\"开始 → 中止\"，不是模型正常生成完的耗时。",
		})
	}

	if len(toolErrors) == 0 {
		completed, running := 0, 0
		for _, tool := range tools {
			switch tool.Status {
			case "completed":
				completed++
			case "running":
				running++
			}
		}
		out = append(out, Warning{
			Level: "info",
			Code:  "no-tool-error",
			Title: "本会话没有失败的工具调用",
			Detail: fmt.Sprintf("%d 个 tool part 里 completed %d 个、running %d 个，没有 state.status=error。", len(tools), completed, running) +
				"红色只用于步骤级错误（如 MessageAbortedError）与中止；换一份含失败工具的导出时，工具行会自动变红。",
		})
	}

	if len(busy) > 1 {
		gaps := gapsOf(busy)
		if len(gaps) > 0 {
			var longest [2]int64
			collapsible := 0
			for index, gap := range gaps {
				if gap[1]-gap[0] > collapsibleThreshold {
					collapsible++
				}
				if index == 0 || gap[1]-gap[0] > longest[1]-longest[0] {
					longest = gap
				}
			}
			longestMs := longest[1] - longest[0]
			out = append(out, Warning{
				Level: "info",
				Code:  "idle-gaps",
				Title: fmt.Sprintf("%d 处空闲（相邻活动之间无任何模型/工具记录，其中 %d 处 >60s 会在时间轴上折叠）", len(gaps), collapsible),
				Detail: "最长空闲 " + fmtMs(&longestMs) + "（" + isoString(longest[0]) + " 起）。时间轴默认把 >60s 的空闲折叠成 " +
					strconv.Itoa(collapsedGapPx) + "px 断口（可切到「实际时间」看真实跨度）。",
			})
		}
	}

	if len(humanWaits) > 0 {
		parts := make([]string, 0, len(humanWaits))
		for _, tool := range humanWaits {
			parts = append(parts, fmt.Sprintf("第 %d 轮第 %d 步 · %s", tool.TurnIndex, tool.StepIndex, fmtMs(tool.DurationMs)))
		}
		out = append(out, Warning{
			Level:  "info",
			Code:   "human-wait",
			Title:  "question 工具是在等人工回答，不是机器耗时",
			Detail: strings.Join(parts, "；") + "。时间轴用绿色「等待人工」单独成色，避免和 bash/read 这类真实执行耗时混淆。",
		})
	}
	return out
}

func reconciliationWarnings(doc *Export, tools []Tool, humanWaits []Tool, session *Session) []Warning {
	out := make([]Warning, 0, 3)

	if session.Cost == nil || !session.Cost.Set || session.Cost.V == 0 {
		out = append(out, Warning{
			Level: "info", Code: "no-cost", Title: "cost 全程为 0",
			Detail: "DeepWorks 侧没有记账，本页不展示金额；token 用量来自每条 assistant message 的 data.tokens。",
		})
	}

	var stateToolMs int64
	for _, tool := range tools {
		if tool.StartMs != nil && tool.EndMs != nil {
			stateToolMs += *tool.EndMs - *tool.StartMs
		}
	}
	var globalToolMs *int64
	if doc.GlobalStats != nil {
		globalToolMs = doc.GlobalStats.TotalToolDurationMs
	}
	threshold := int64(math.Max(1000, float64(stateToolMs)*0.05))
	if globalToolMs != nil && absInt64(*globalToolMs-stateToolMs) > threshold {
		var partMs int64
		for _, message := range doc.Messages {
			for _, part := range message.Parts {
				if part.Type == "tool" && part.TimeCreatedMs != nil && part.TimeUpdatedMs != nil {
					partMs += *part.TimeUpdatedMs - *part.TimeCreatedMs
				}
			}
		}
		var waitHumanMs int64
		for _, tool := range humanWaits {
			waitHumanMs += derefInt64(tool.DurationMs)
		}
		out = append(out, Warning{
			Level: "info",
			Code:  "tool-duration-definition",
			Title: "导出的工具总耗时口径与本页不同（" + fmtMs(globalToolMs) + " vs " + fmtMs(&stateToolMs) + "）",
			Detail: "导出 global_stats.total_tool_duration_ms = " + fmtMs(globalToolMs) +
				" 是按 tool part 的 time_created→time_updated（存储跨度，含模型开始生成该调用到结果落库的时间）累计：part 跨度合计 " + fmtMs(&partMs) +
				"；本页用 part.data.state.time.start→end（真实执行区间）累计：" + fmtMs(&stateToolMs) +
				"（其中 question 等待人工 " + fmtMs(&waitHumanMs) +
				"，工具真正执行 " + fmtMs(int64Ptr(stateToolMs-waitHumanMs)) + "）" +
				"。看\"工具自己跑了多久\"用本页的 state.time，看\"这条消息整体多久\"用步骤总时长。",
		})
	}

	negative := make([]TurnTiming, 0, 2)
	for _, timing := range doc.TurnTimings {
		if timing.TurnDurationMs != nil && *timing.TurnDurationMs < 0 {
			negative = append(negative, timing)
		}
	}
	if len(negative) > 0 {
		example := ""
		if negative[0].MessageID != nil {
			example = *negative[0].MessageID
		}
		out = append(out, Warning{
			Level: "warn",
			Code:  "export-derived-mismatch",
			Title: fmt.Sprintf("导出里的 turn_timings 有 %d 条负耗时", len(negative)),
			Detail: "例：" + example + " turn_duration_ms=" + formatJSFloat(*negative[0].TurnDurationMs) +
				"。本页完全不使用 turn_timings，所有时长按 part 时间戳重算。",
		})
	}
	return out
}

func newTurn(index int, message Message, parts []Part, warnings *[]Warning) Turn {
	var compacted *Part
	realTexts := make([]Part, 0, len(parts))
	allTexts := make([]Part, 0, len(parts))
	files := make([]Part, 0, 2)
	hasSynthetic := false
	for position := range parts {
		part := parts[position]
		switch part.Type {
		case "compaction":
			if compacted == nil {
				compacted = &parts[position]
			}
		case "text":
			allTexts = append(allTexts, part)
			if isSynthetic(part) {
				hasSynthetic = true
			} else {
				realTexts = append(realTexts, part)
			}
		case "file":
			files = append(files, part)
		}
	}
	text, attachments := splitAttachments(joinText(realTexts))

	kind := "user"
	switch {
	case compacted != nil:
		kind = "compaction"
	case text == "" && len(attachments) == 0 && len(files) == 0:
		if hasSynthetic {
			kind = "continue"
		} else {
			kind = "empty"
		}
	}

	names := make([]string, 0, len(attachments)+len(files))
	names = append(names, attachments...)
	for _, file := range files {
		names = append(names, fileLabel(file))
	}

	turn := Turn{
		Index:     index,
		Kind:      kind,
		MessageID: message.MessageID,
		StartMs:   message.TimeCreatedMs,
		EndMs:     message.TimeCreatedMs,
		Input: TurnInput{
			Text:          text,
			Attachments:   names,
			SyntheticOnly: kind == "continue",
		},
		Steps: make([]Step, 0, 2),
	}
	if kind == "continue" {
		synthetic := joinText(allTexts)
		turn.Input.SyntheticText = &synthetic
	}
	if compacted != nil {
		auto := compacted.Data != nil && compacted.Data.Auto != nil && *compacted.Data.Auto
		turn.Compaction = &Compaction{DurationMs: compacted.DurationMs, Auto: auto}
	}

	if kind == "continue" && turn.Input.SyntheticText != nil && *turn.Input.SyntheticText != "" {
		*warnings = append(*warnings, Warning{
			Level: "info",
			Code:  "synthetic-continue",
			Title: fmt.Sprintf("第 %d 轮是自动续跑提示", index),
			Detail: "该 user message 只含 synthetic 文本（opencode 自动注入），不是用户手输：" +
				jsQuote(truncateRunes(*turn.Input.SyntheticText, 160)),
		})
	}
	if kind == "compaction" {
		*warnings = append(*warnings, Warning{
			Level: "info",
			Code:  "auto-compaction",
			Title: fmt.Sprintf("第 %d 段是自动上下文压缩", index),
			Detail: "该 user message 只含一个 compaction part（auto=" + strconv.FormatBool(turn.Compaction.Auto) +
				"，耗时 " + fmtMs(turn.Compaction.DurationMs) + "），不是用户输入；压缩耗时单列，不并入工具耗时。",
		})
	}
	return turn
}

func normalizeStep(message Message, parts []Part, index, turnIndex int) Step {
	step := Step{
		Index:     index,
		TurnIndex: turnIndex,
		MessageID: message.MessageID,
		Role:      "assistant",
		Reasoning: make([]Span, 0, 2),
		Texts:     make([]Span, 0, 2),
		Tools:     make([]Tool, 0, 2),
		Timeline:  make([]TimelineItem, 0, len(parts)),
	}
	step.StartMs = message.TimeCreatedMs
	if message.Data != nil {
		if message.Data.Role != nil {
			step.Role = *message.Data.Role
		}
		step.Finish = message.Data.Finish
		if message.Data.Time != nil {
			step.EndMs = message.Data.Time.Completed
		}
	}
	if message.ModelID != nil {
		step.ModelID = message.ModelID
	} else if message.Data != nil {
		step.ModelID = message.Data.ModelID
	}
	if message.ModelProvider != nil {
		step.Provider = message.ModelProvider
	} else if message.Data != nil {
		step.Provider = message.Data.ProviderID
	}

	for _, part := range parts {
		step.StartMs = minInt64Ptr(step.StartMs, part.TimeCreatedMs)
		switch part.Type {
		case "reasoning", "text":
			start := part.TimeCreatedMs
			var end *int64
			if part.Data != nil && part.Data.Time != nil {
				if part.Data.Time.Start != nil {
					start = part.Data.Time.Start
				}
				end = part.Data.Time.End
			}
			if end == nil {
				end = part.TimeUpdatedMs
			}
			if start != nil && end != nil {
				span := Span{StartMs: *start, EndMs: *end}
				if part.Data != nil && part.Data.Text != nil {
					span.Text = *part.Data.Text
				}
				if part.Type == "reasoning" {
					step.Reasoning = append(step.Reasoning, span)
				} else {
					step.Texts = append(step.Texts, span)
				}
			}
		case "tool":
			tool := normalizeTool(part)
			tool.TurnIndex, tool.StepIndex = turnIndex, index
			step.Tools = append(step.Tools, tool)
			step.EndMs = maxInt64Ptr(step.EndMs, tool.EndMs)
		}
		if part.Type != "step-start" && part.Type != "step-finish" {
			step.Timeline = append(step.Timeline, TimelineItem{
				Type:      part.Type,
				PartID:    part.ID,
				CreatedMs: part.TimeCreatedMs,
				UpdatedMs: part.TimeUpdatedMs,
			})
		}
	}

	applyModelSpan(&step)
	for _, span := range step.Reasoning {
		step.ReasoningMs += span.EndMs - span.StartMs
	}
	for _, span := range step.Texts {
		step.TextMs += span.EndMs - span.StartMs
	}
	applyStepStatus(&step, message)
	if step.StartMs != nil && step.EndMs != nil && *step.EndMs >= *step.StartMs {
		step.StepMs = int64Ptr(*step.EndMs - *step.StartMs)
	}
	step.Tokens = stepTokens(message)
	return step
}

// applyModelSpan 计算模型片段：reasoning/text 的时间并集；
// 若该 step 只有工具调用（opencode 有时只发 tool part），按 step 起点 → 首个工具开始 估算。
func applyModelSpan(step *Step) {
	var starts, ends []int64
	for _, span := range step.Reasoning {
		starts = append(starts, span.StartMs)
		ends = append(ends, span.EndMs)
	}
	for _, span := range step.Texts {
		starts = append(starts, span.StartMs)
		ends = append(ends, span.EndMs)
	}
	if len(starts) > 0 {
		start, end := starts[0], ends[0]
		for _, value := range starts[1:] {
			if value < start {
				start = value
			}
		}
		for _, value := range ends[1:] {
			if value > end {
				end = value
			}
		}
		step.ModelStartMs = int64Ptr(start)
		step.ModelEndMs = int64Ptr(end)
		step.ModelSpanSource = "parts"
	} else if len(step.Tools) > 0 && step.StartMs != nil {
		var toolStarts []int64
		for _, tool := range step.Tools {
			if tool.StartMs != nil {
				toolStarts = append(toolStarts, *tool.StartMs)
			}
		}
		if len(toolStarts) > 0 {
			earliest := toolStarts[0]
			for _, value := range toolStarts[1:] {
				if value < earliest {
					earliest = value
				}
			}
			if earliest > *step.StartMs {
				step.ModelStartMs = int64Ptr(*step.StartMs)
				step.ModelEndMs = int64Ptr(earliest)
				step.ModelSpanSource = "tool-gap"
			}
		}
	}
	if step.ModelSpanSource == "" {
		step.ModelSpanSource = "none"
	}
	if step.ModelStartMs != nil && step.ModelEndMs != nil {
		step.ModelMs = int64Ptr(*step.ModelEndMs - *step.ModelStartMs)
	}
}

func applyStepStatus(step *Step, message Message) {
	hasStepError := message.Data != nil && message.Data.Error != nil
	hasToolError := false
	for _, tool := range step.Tools {
		if tool.Status == "error" {
			hasToolError = true
			break
		}
	}
	switch {
	case hasStepError:
		step.Status = "error"
		name, text := "Error", ""
		stepError := message.Data.Error
		if stepError.Name != nil {
			name = *stepError.Name
		}
		if stepError.Data != nil && stepError.Data.Message != nil {
			text = *stepError.Data.Message
		} else if stepError.Message != nil {
			text = *stepError.Message
		}
		step.Error = &StepError{Name: name, Message: text}
		if stepError.Data != nil {
			step.Error.StatusCode = stepError.Data.StatusCode
			step.Error.Retryable = stepError.Data.Retryable
		}
	case hasToolError:
		step.Status = "error"
	default:
		completed := message.Data != nil && message.Data.Time != nil && message.Data.Time.Completed != nil
		if completed {
			step.Status = "completed"
		} else {
			step.Status = "running"
		}
	}
}

func stepTokens(message Message) *StepTokens {
	if message.Data == nil || message.Data.Tokens == nil {
		return nil
	}
	tokens := message.Data.Tokens
	out := &StepTokens{
		Total: tokens.Total, Input: tokens.Input, Output: tokens.Output, Reasoning: tokens.Reasoning,
	}
	if tokens.Cache != nil {
		out.CacheRead = tokens.Cache.Read
		out.CacheWrite = tokens.Cache.Write
	}
	return out
}

func normalizeTool(part Part) Tool {
	tool := Tool{PartID: part.ID, Name: "tool", Status: "unknown", StartMs: part.TimeCreatedMs}
	if part.Data != nil {
		if part.Data.CallID != nil {
			tool.CallID = *part.Data.CallID
		}
		if part.Data.Tool != nil {
			tool.Name = *part.Data.Tool
		}
		if state := part.Data.State; state != nil {
			if state.Status != "" {
				tool.Status = state.Status
			}
			if state.Time != nil {
				if state.Time.Start != nil {
					tool.StartMs = state.Time.Start
				}
				tool.EndMs = state.Time.End
			}
			tool.ArgsText = stringifyJSON(state.Input)
			tool.OutputText = stringifyJSON(state.Output)
			if len(state.Error) > 0 && strings.TrimSpace(string(state.Error)) != "null" {
				text := stringifyJSON(state.Error)
				tool.ErrorText = &text
			}
			tool.Title = state.Title
		}
	}
	if tool.CallID == "" {
		tool.CallID = part.ID
	}
	tool.WaitHuman = tool.Name == "question"
	if tool.StartMs != nil && tool.EndMs != nil {
		tool.DurationMs = int64Ptr(*tool.EndMs - *tool.StartMs)
	}
	return tool
}

// stringifyJSON 复刻 JS 行为：缺失/null → ""；字符串原样返回；其余 JSON.stringify(v, null, 2)。
func stringifyJSON(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	if strings.HasPrefix(trimmed, "\"") {
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			return text
		}
	}
	value, err := parseJSValue(raw)
	if err != nil {
		// 解析不了就原样保留，不静默丢掉内容
		return trimmed
	}
	return value.jsStringify("  ")
}

// splitAttachments 抽出 [dw-attachment:<base64>] 引用并解出文件名。
func splitAttachments(text string) (string, []string) {
	const openTag = "[dw-attachment:"
	names := make([]string, 0, 2)
	var out strings.Builder
	rest := text
	for {
		start := strings.Index(rest, openTag)
		if start < 0 {
			out.WriteString(rest)
			break
		}
		end := strings.IndexByte(rest[start:], ']')
		if end < 0 {
			out.WriteString(rest)
			break
		}
		payload := rest[start+len(openTag) : start+end]
		if !isAttachmentPayload(payload) {
			out.WriteString(rest[:start+len(openTag)])
			rest = rest[start+len(openTag):]
			continue
		}
		out.WriteString(rest[:start])
		names = append(names, attachmentLabel(payload))
		rest = rest[start+end+1:]
	}
	return jsTrim(out.String()), names
}

func isAttachmentPayload(payload string) bool {
	if payload == "" {
		return false
	}
	for _, r := range payload {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '+', r == '/', r == '=', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

func attachmentLabel(payload string) string {
	normalized := strings.NewReplacer("-", "+", "_", "/").Replace(payload)
	decoded, err := base64.StdEncoding.DecodeString(normalized)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(normalized, "="))
	}
	if err != nil {
		return "附件"
	}
	var meta map[string]any
	if err := json.Unmarshal(decoded, &meta); err != nil {
		return "附件"
	}
	if value, ok := meta["n"]; ok {
		return renderLabel(value)
	}
	if value, ok := meta["name"]; ok {
		return renderLabel(value)
	}
	return "附件"
}

func renderLabel(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return formatJSFloat(typed)
	case bool:
		return strconv.FormatBool(typed)
	case nil:
		return "附件"
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return "附件"
		}
		return string(encoded)
	}
}

func fileLabel(part Part) string {
	if part.Data != nil {
		if part.Data.Filename != nil {
			return *part.Data.Filename
		}
		if part.Data.URL != nil {
			return *part.Data.URL
		}
	}
	return "文件"
}

func joinText(parts []Part) string {
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Data == nil || part.Data.Text == nil || *part.Data.Text == "" {
			continue
		}
		texts = append(texts, *part.Data.Text)
	}
	return strings.Join(texts, "\n")
}

func isSynthetic(part Part) bool {
	return part.Data != nil && part.Data.Synthetic != nil && *part.Data.Synthetic
}

func sortedParts(parts []Part) []Part {
	sorted := make([]Part, len(parts))
	copy(sorted, parts)
	sort.SliceStable(sorted, func(i, j int) bool { return byCreated(sorted[i], sorted[j]) < 0 })
	return sorted
}

// byCreated 与 Node 版一致：先按 time_created_ms，再按 id 字符串。
func byCreated(a, b Part) int {
	left, right := derefInt64(a.TimeCreatedMs), derefInt64(b.TimeCreatedMs)
	if left != right {
		if left < right {
			return -1
		}
		return 1
	}
	return strings.Compare(a.ID, b.ID)
}

func parseSessionModel(raw json.RawMessage) *ModelRef {
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
	parse := func(data []byte) *ModelRef {
		var model rawModel
		if err := json.Unmarshal(data, &model); err != nil {
			return nil
		}
		out := &ModelRef{ID: model.ID, Provider: model.ProviderID, Variant: model.Variant}
		if out.ID == nil {
			out.ID = model.ModelID
		}
		return out
	}
	if strings.HasPrefix(trimmed, "\"") {
		var asString string
		if err := json.Unmarshal(raw, &asString); err != nil {
			return nil
		}
		return parse([]byte(asString))
	}
	return parse(raw)
}

func exportedAt(doc *Export) *string {
	if doc.ExportInfo == nil {
		return nil
	}
	return doc.ExportInfo.ExportedAt
}

func exportSource(doc *Export) *string {
	if doc.ExportInfo == nil {
		return nil
	}
	return doc.ExportInfo.Source
}

// ---------- 常量与小工具 ----------

const (
	// collapsibleThreshold 是时间轴折叠空闲的阈值，与 app.js 的 gapThreshold 默认值一致。
	collapsibleThreshold = 60000
	// collapsedGapPx 是折叠后固定宽度，与 app.js 的 GAP_PX 一致。
	collapsedGapPx = 20
)

func int64Ptr(value int64) *int64 { return &value }

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func minInt64Ptr(current, candidate *int64) *int64 {
	if candidate == nil {
		return current
	}
	if current == nil || *candidate < *current {
		return candidate
	}
	return current
}

func maxInt64Ptr(current, candidate *int64) *int64 {
	if candidate == nil {
		return current
	}
	if current == nil || *candidate > *current {
		return candidate
	}
	return current
}

func subInt64Ptr(from, to *int64) *int64 {
	if from == nil || to == nil {
		return nil
	}
	return int64Ptr(*to - *from)
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func isoPtr(ms *int64) *string {
	if ms == nil {
		return nil
	}
	text := isoString(*ms)
	return &text
}

func isoString(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z")
}

func isoOf(ms *int64) string {
	if ms == nil {
		return "null"
	}
	return isoString(*ms)
}

func sumIntervals(intervals [][2]int64) int64 {
	var total int64
	for _, interval := range intervals {
		total += interval[1] - interval[0]
	}
	return total
}

func mergeIntervals(intervals [][2]int64) [][2]int64 {
	sorted := make([][2]int64, len(intervals))
	copy(sorted, intervals)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i][0] < sorted[j][0] })
	merged := make([][2]int64, 0, len(sorted))
	for _, interval := range sorted {
		last := len(merged) - 1
		if last >= 0 && interval[0] <= merged[last][1] {
			if interval[1] > merged[last][1] {
				merged[last][1] = interval[1]
			}
			continue
		}
		merged = append(merged, interval)
	}
	return merged
}

func gapsOf(intervals [][2]int64) [][2]int64 {
	gaps := make([][2]int64, 0, len(intervals))
	for i := 1; i < len(intervals); i++ {
		start, end := intervals[i-1][1], intervals[i][0]
		if end > start {
			gaps = append(gaps, [2]int64{start, end})
		}
	}
	return gaps
}

// FormatMs 把毫秒格式化成页面同款文案（CLI 摘要也用它，避免两套格式）。
func FormatMs(ms *int64) string { return fmtMs(ms) }

func fmtMs(ms *int64) string {
	if ms == nil {
		return "—"
	}
	value := *ms
	switch {
	case value < 1000:
		return strconv.FormatInt(int64(math.Round(float64(value))), 10) + " ms"
	case value < 60000:
		return toFixed1(float64(value)/1000) + " s"
	case value < 3600000:
		minutes := value / 60000
		seconds := int64(math.Round(float64(value%60000) / 1000))
		return strconv.FormatInt(minutes, 10) + " m " + strconv.FormatInt(seconds, 10) + " s"
	default:
		return toFixed1(float64(value)/3600000) + " h"
	}
}

// toFixed1 复刻 JS 的 toFixed(1)（四舍五入、远离零）。
func toFixed1(value float64) string {
	return strconv.FormatFloat(math.Round(value*10)/10, 'f', 1, 64)
}

func countTurns(turns []Turn, kind string) int {
	total := 0
	for _, turn := range turns {
		if turn.Kind == kind {
			total++
		}
	}
	return total
}

func sumSteps(steps []Step, pick func(Step) *int64) int64 {
	var total int64
	for _, step := range steps {
		total += derefInt64(pick(step))
	}
	return total
}

func sumTools(tools []Tool, pick func(Tool) (int64, bool)) int64 {
	var total int64
	for _, tool := range tools {
		value, ok := pick(tool)
		if !ok {
			continue
		}
		total += value
	}
	return total
}

func sumTokens(steps []Step, pick func(*StepTokens) *int64) int64 {
	var total int64
	for _, step := range steps {
		if step.Tokens == nil {
			continue
		}
		total += derefInt64(pick(step.Tokens))
	}
	return total
}

func sumCompaction(turns []Turn) int64 {
	var total int64
	for _, turn := range turns {
		if turn.Compaction != nil {
			total += derefInt64(turn.Compaction.DurationMs)
		}
	}
	return total
}

func slowestTool(tools []Tool) *Tool {
	candidates := make([]Tool, 0, len(tools))
	for _, tool := range tools {
		if tool.DurationMs != nil {
			candidates = append(candidates, tool)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return *candidates[i].DurationMs > *candidates[j].DurationMs
	})
	slowest := candidates[0]
	return &slowest
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

// jsTrim 复刻 JS 的 String.prototype.trim（含 \uFEFF 与各类 Unicode 空白）。
func jsTrim(text string) string {
	return strings.TrimFunc(text, func(r rune) bool {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', 0x00A0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
			return true
		}
		return r >= 0x2000 && r <= 0x200A
	})
}
