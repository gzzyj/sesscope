// Package attribution 用**确定性规则**给会话归因：不调用任何模型。
//
// 归因对象不是"文本"，而是事件链（Episode）：一次触发 + 一串补偿动作 + 恢复点。
// 信号分两级：A 级=结构化字段（error/status/compaction/tokens），
// B 级=结构化+序列模式（输出尾部固定标记、失败后紧跟的探查/读源码/重试）。
// 纯文本只用来做证据引用，不参与判定。
package attribution

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/zhouyuejie/opencode-trace/internal/trace"
)

// 原因代码（稳定标识，页面按它上色/聚合）。
const (
	CodeProviderError   = "provider_error"
	CodeUserAbort       = "user_abort"
	CodeCompaction      = "context_compaction"
	CodeEncodingLoss    = "encoding_loss"
	CodeTruncatedOutput = "output_truncated"
	CodeRetrySameTarget = "retry_same_target"
	CodeServiceBusy     = "service_busy"
	CodePathProbe       = "path_probe"
	CodeAuthBlocked     = "auth_blocked"
	CodeAuthSourceRead  = "auth_blocked_source_read"
	CodeEditMismatch    = "edit_mismatch"
	CodeToolError       = "tool_error"
)

var labels = map[string]string{
	CodeProviderError:   "接口/服务报错",
	CodeUserAbort:       "用户中止",
	CodeCompaction:      "上下文压缩",
	CodeEncodingLoss:    "输出编码损坏（乱码）",
	CodeTruncatedOutput: "工具输出被截断",
	CodeRetrySameTarget: "同一目标重复失败",
	CodeServiceBusy:     "服务忙/锁超时重试",
	CodePathProbe:       "路径探查（找不到→换路径）",
	CodeAuthBlocked:     "授权/参数被拒",
	CodeAuthSourceRead:  "授权被拒→读源码确认传参",
	CodeEditMismatch:    "编辑锚点失配→重读",
	CodeToolError:       "工具报错",
}

var (
	truncatedMarker = regexp.MustCompile(`(?i)(Results are truncated: showing first [0-9]+ results|exceeded [0-9]+ bytes)`)
	notFoundPattern = regexp.MustCompile(`(?i)^\s*(file not found|no such file|cannot find|系统找不到指定的路径)`)
	authPattern     = regexp.MustCompile(`(?i)(unauthori[sz]ed|forbidden|permission denied|没有权限|无权限|project[_ ]?id|缺少.{0,6}project)`)
	sourceFile      = regexp.MustCompile(`\.(py|go|ts|tsx|js|java|yaml|yml|cmd|ps1|sh)(:[0-9]+)?$`)
	lockTimeout     = regexp.MustCompile(`(?i)(LOCK_TIMEOUT|timeout|timed out|deadlock|busy)`)
	pathLike        = regexp.MustCompile(`[A-Za-z]:\\[^\s"]+|/[^\s"]+`)
)

// Analyze 扫一遍归一化后的会话，返回事件链（按时间排序）。
func Analyze(data *trace.Data) []trace.Episode {
	if data == nil {
		return []trace.Episode{}
	}
	episodes := make([]trace.Episode, 0, 8)
	flat := flattenSteps(data)
	// 重试链要先算：同一次失败若已归入"重复失败"，就不再单独记一条 tool_error（避免重复计数）
	retries, covered := retryEpisodes(flat)
	episodes = append(episodes, retries...)
	for position, entry := range flat {
		step := entry.step
		if step.Error != nil && step.Error.Name != "" {
			episodes = append(episodes, stepErrorEpisode(entry))
		}
		for toolIndex := range step.Tools {
			tool := &step.Tools[toolIndex]
			if tool.WaitHuman {
				continue // 等人工回答不算故障
			}
			if episode, ok := encodingEpisode(entry, tool); ok {
				episodes = append(episodes, episode)
			}
			if episode, ok := truncatedEpisode(flat, position, entry, tool); ok {
				episodes = append(episodes, episode)
			}
			if tool.Status != "error" {
				continue
			}
			errorText := strings.TrimSpace(derefString(tool.ErrorText))
			switch {
			case notFoundPattern.MatchString(errorText) || notFoundPattern.MatchString(firstLine(tool.OutputText)):
				episodes = append(episodes, pathProbeEpisode(flat, position, entry, tool))
			case authPattern.MatchString(errorText):
				episodes = append(episodes, authEpisode(flat, position, entry, tool))
			case tool.Name == "edit" && strings.Contains(strings.ToLower(errorText), "oldstring"):
				episodes = append(episodes, editMismatchEpisode(flat, position, entry, tool))
			default:
				if !covered[coverageKey(entry.step.TurnIndex, entry.step.Index, derefInt64(tool.StartMs))] {
					episodes = append(episodes, plainToolErrorEpisode(entry, tool))
				}
			}
		}
	}
	episodes = append(episodes, compactionEpisodes(data)...)
	attachSelfExplanation(data, episodes)
	sort.SliceStable(episodes, func(i, j int) bool {
		if episodes[i].StartMs != episodes[j].StartMs {
			return episodes[i].StartMs < episodes[j].StartMs
		}
		return episodes[i].Code < episodes[j].Code
	})
	return dedupe(episodes)
}

type stepEntry struct {
	turn *trace.Turn
	step *trace.Step
}

func flattenSteps(data *trace.Data) []stepEntry {
	out := make([]stepEntry, 0, len(data.Turns)*4)
	for turnIndex := range data.Turns {
		turn := &data.Turns[turnIndex]
		for stepIndex := range turn.Steps {
			out = append(out, stepEntry{turn: turn, step: &turn.Steps[stepIndex]})
		}
	}
	return out
}

func stepErrorEpisode(entry stepEntry) trace.Episode {
	step := entry.step
	code := CodeProviderError
	severity := "error"
	if strings.Contains(step.Error.Name, "Aborted") {
		code, severity = CodeUserAbort, "info"
	}
	evidence := []trace.Evidence{{Kind: "error", Text: step.Error.Name + ": " + step.Error.Message}}
	if step.Error.StatusCode != nil {
		evidence = append(evidence, trace.Evidence{Kind: "status", Text: fmt.Sprintf("statusCode=%d", *step.Error.StatusCode)})
	}
	if step.Error.Retryable != nil {
		evidence = append(evidence, trace.Evidence{Kind: "retryable", Text: fmt.Sprintf("isRetryable=%v", *step.Error.Retryable)})
	}
	episode := trace.Episode{
		Code: code, Label: labels[code], Severity: severity, Confidence: "A",
		Turn: step.TurnIndex, Step: step.Index,
		StartMs: derefInt64(step.StartMs), EndMs: derefInt64(step.EndMs),
		Evidence: evidence, Response: []string{},
	}
	if step.Error.Retryable != nil && !*step.Error.Retryable {
		episode.Outcome = "不可重试：整轮终止"
	} else {
		episode.Outcome = "调用失败"
	}
	episode.WindowMs = maxInt64(0, episode.EndMs-episode.StartMs)
	return episode
}

// encodingEpisode：输出里有 U+FFFD/GBK 乱码特征，说明错误信息在传输中被打坏。
func encodingEpisode(entry stepEntry, tool *trace.Tool) (trace.Episode, bool) {
	if !hasMojibake(tool.OutputText) {
		return trace.Episode{}, false
	}
	start, end := toolSpan(tool)
	episode := trace.Episode{
		Code: CodeEncodingLoss, Label: labels[CodeEncodingLoss], Severity: "warn", Confidence: "A",
		Turn: entry.step.TurnIndex, Step: entry.step.Index, StartMs: start, EndMs: end,
		Evidence: []trace.Evidence{
			{Kind: "command", Text: oneLine(toolCommand(tool), 120)},
			{Kind: "output", Text: oneLine(tool.OutputText, 80)},
			{Kind: "hint", Text: "含 U+FFFD 替换符：错误信息编码已损坏，模型看不到真实原因"},
		},
		Response: []string{},
		Outcome:  "模型只能另找路径",
	}
	episode.WindowMs = maxInt64(0, end-start)
	return episode, true
}

func truncatedEpisode(flat []stepEntry, position int, entry stepEntry, tool *trace.Tool) (trace.Episode, bool) {
	tail := tool.OutputText
	if len(tail) > 400 {
		tail = tail[len(tail)-400:]
	}
	if !truncatedMarker.MatchString(tail) {
		return trace.Episode{}, false
	}
	target := toolTarget(tool)
	start, end := toolSpan(tool)
	response := make([]string, 0, 3)
	for offset := position + 1; offset < len(flat) && offset <= position+6; offset++ {
		for toolIndex := range flat[offset].step.Tools {
			later := &flat[offset].step.Tools[toolIndex]
			if later.Name != "read" && later.Name != "bash" {
				continue
			}
			if target != "" && strings.Contains(toolTarget(later), baseName(target)) {
				response = append(response, fmt.Sprintf("第 %d 轮第 %d 步 %s 分片重读", flat[offset].step.TurnIndex, flat[offset].step.Index, later.Name))
				if spanEnd := derefInt64(later.EndMs); spanEnd > end {
					end = spanEnd
				}
			}
		}
	}
	episode := trace.Episode{
		Code: CodeTruncatedOutput, Label: labels[CodeTruncatedOutput], Severity: "warn", Confidence: "B",
		Turn: entry.step.TurnIndex, Step: entry.step.Index, StartMs: start, EndMs: end,
		Evidence: []trace.Evidence{
			{Kind: "tool", Text: tool.Name + "(" + oneLine(target, 60) + ")"},
			{Kind: "output", Text: oneLine(truncatedMarker.FindString(tail), 80)},
		},
		Response: response,
	}
	if len(response) == 0 {
		episode.Outcome = "截断但未见重读"
	} else {
		episode.Outcome = fmt.Sprintf("分片重读 %d 次", len(response))
	}
	episode.WindowMs = maxInt64(0, end-start)
	return episode, true
}

func pathProbeEpisode(flat []stepEntry, position int, entry stepEntry, tool *trace.Tool) trace.Episode {
	target := toolTarget(tool)
	start, end := toolSpan(tool)
	response := make([]string, 0, 3)
	for offset := position + 1; offset < len(flat) && offset <= position+5; offset++ {
		for toolIndex := range flat[offset].step.Tools {
			later := &flat[offset].step.Tools[toolIndex]
			if later.Name == tool.Name && toolTarget(later) == target {
				continue
			}
			response = append(response, fmt.Sprintf("第 %d 轮第 %d 步 %s(%s)", flat[offset].step.TurnIndex, flat[offset].step.Index, later.Name, oneLine(toolTarget(later), 40)))
			if spanEnd := derefInt64(later.EndMs); spanEnd > end {
				end = spanEnd
			}
		}
	}
	episode := trace.Episode{
		Code: CodePathProbe, Label: labels[CodePathProbe], Severity: "warn", Confidence: "B",
		Turn: entry.step.TurnIndex, Step: entry.step.Index, StartMs: start, EndMs: end,
		Evidence: []trace.Evidence{
			{Kind: "command", Text: oneLine(toolCommand(tool), 100)},
			{Kind: "error", Text: oneLine(derefString(tool.ErrorText), 80)},
		},
		Response: response,
	}
	if len(response) == 0 {
		episode.Outcome = "未继续探查"
	} else {
		episode.Outcome = fmt.Sprintf("改用 %d 种方式探查", len(response))
	}
	episode.WindowMs = maxInt64(0, end-start)
	return episode
}

func authEpisode(flat []stepEntry, position int, entry stepEntry, tool *trace.Tool) trace.Episode {
	start, end := toolSpan(tool)
	response := make([]string, 0, 2)
	readSource := false
	for offset := position + 1; offset < len(flat) && offset <= position+4; offset++ {
		for toolIndex := range flat[offset].step.Tools {
			later := &flat[offset].step.Tools[toolIndex]
			if later.Name == "read" && sourceFile.MatchString(toolTarget(later)) {
				readSource = true
				response = append(response, fmt.Sprintf("第 %d 轮第 %d 步 read %s", flat[offset].step.TurnIndex, flat[offset].step.Index, oneLine(toolTarget(later), 50)))
				if spanEnd := derefInt64(later.EndMs); spanEnd > end {
					end = spanEnd
				}
			}
		}
	}
	code := CodeAuthBlocked
	if readSource {
		code = CodeAuthSourceRead
	}
	episode := trace.Episode{
		Code: code, Label: labels[code], Severity: "warn", Confidence: "B",
		Turn: entry.step.TurnIndex, Step: entry.step.Index, StartMs: start, EndMs: end,
		Evidence: []trace.Evidence{
			{Kind: "command", Text: oneLine(toolCommand(tool), 100)},
			{Kind: "error", Text: oneLine(derefString(tool.ErrorText), 80)},
		},
		Response: response,
	}
	if readSource {
		episode.Outcome = "改为读源码确认传参"
	} else {
		episode.Outcome = "未见补偿动作"
	}
	episode.WindowMs = maxInt64(0, end-start)
	return episode
}

func editMismatchEpisode(flat []stepEntry, position int, entry stepEntry, tool *trace.Tool) trace.Episode {
	target := toolTarget(tool)
	start, end := toolSpan(tool)
	response := make([]string, 0, 2)
	for offset := position + 1; offset < len(flat) && offset <= position+3; offset++ {
		for toolIndex := range flat[offset].step.Tools {
			later := &flat[offset].step.Tools[toolIndex]
			if later.Name == "read" && strings.Contains(toolTarget(later), baseName(target)) {
				response = append(response, fmt.Sprintf("第 %d 轮第 %d 步 read 重读", flat[offset].step.TurnIndex, flat[offset].step.Index))
				if spanEnd := derefInt64(later.EndMs); spanEnd > end {
					end = spanEnd
				}
			}
		}
	}
	episode := trace.Episode{
		Code: CodeEditMismatch, Label: labels[CodeEditMismatch], Severity: "warn", Confidence: "B",
		Turn: entry.step.TurnIndex, Step: entry.step.Index, StartMs: start, EndMs: end,
		Evidence: []trace.Evidence{
			{Kind: "tool", Text: "edit(" + oneLine(target, 60) + ")"},
			{Kind: "error", Text: oneLine(derefString(tool.ErrorText), 90)},
		},
		Response: response,
	}
	if len(response) == 0 {
		episode.Outcome = "未重读，可能再次失败"
	} else {
		episode.Outcome = "重读后重试"
	}
	episode.WindowMs = maxInt64(0, end-start)
	return episode
}

func plainToolErrorEpisode(entry stepEntry, tool *trace.Tool) trace.Episode {
	start, end := toolSpan(tool)
	episode := trace.Episode{
		Code: CodeToolError, Label: labels[CodeToolError], Severity: "warn", Confidence: "A",
		Turn: entry.step.TurnIndex, Step: entry.step.Index, StartMs: start, EndMs: end,
		Evidence: []trace.Evidence{
			{Kind: "tool", Text: tool.Name + "(" + oneLine(toolTarget(tool), 60) + ")"},
			{Kind: "error", Text: oneLine(derefString(tool.ErrorText), 100)},
		},
		Outcome: "工具报错",
	}
	episode.WindowMs = maxInt64(0, end-start)
	return episode
}

// retryEpisodes：同一 (工具, 目标) 连续失败 >=2 次，是"服务/锁/网络"最硬的证据。
// coverageKey 标记"这条失败已经被别的事件链吃掉了"。
func coverageKey(turn, step int, toolStartMs int64) string {
	return fmt.Sprintf("%d|%d|%d", turn, step, toolStartMs)
}

func retryEpisodes(flat []stepEntry) ([]trace.Episode, map[string]bool) {
	out := make([]trace.Episode, 0, 4)
	covered := make(map[string]bool, 8)
	position := 0
	for position < len(flat) {
		var current *trace.Tool
		var currentEntry stepEntry
		for toolIndex := range flat[position].step.Tools {
			tool := &flat[position].step.Tools[toolIndex]
			if tool.Status == "error" && !tool.WaitHuman {
				current, currentEntry = tool, flat[position]
				break
			}
		}
		if current == nil {
			position++
			continue
		}
		target := toolTarget(current)
		attempts := 1
		lastPosition := position
		lastTool := current
		for offset := position + 1; offset < len(flat) && offset <= position+8; offset++ {
			for toolIndex := range flat[offset].step.Tools {
				tool := &flat[offset].step.Tools[toolIndex]
				if tool.Name == current.Name && toolTarget(tool) == target && tool.Status == "error" {
					attempts++
					lastPosition = offset
					lastTool = tool
				}
			}
		}
		if attempts >= 2 {
			covered[coverageKey(flat[position].step.TurnIndex, flat[position].step.Index, derefInt64(current.StartMs))] = true
			for offset := position + 1; offset <= lastPosition; offset++ {
				for toolIndex := range flat[offset].step.Tools {
					tool := &flat[offset].step.Tools[toolIndex]
					if tool.Name == current.Name && toolTarget(tool) == target && tool.Status == "error" {
						covered[coverageKey(flat[offset].step.TurnIndex, flat[offset].step.Index, derefInt64(tool.StartMs))] = true
					}
				}
			}
			start, _ := toolSpan(current)
			_, end := toolSpan(lastTool)
			code := CodeRetrySameTarget
			if lockTimeout.MatchString(derefString(current.ErrorText)) {
				code = CodeServiceBusy
			}
			out = append(out, trace.Episode{
				Code: code, Label: labels[code], Severity: "error", Confidence: "A",
				Turn: currentEntry.step.TurnIndex, Step: currentEntry.step.Index,
				StartMs: start, EndMs: end, WindowMs: maxInt64(0, end-start),
				Evidence: []trace.Evidence{
					{Kind: "tool", Text: current.Name + "(" + oneLine(target, 50) + ")"},
					{Kind: "error", Text: oneLine(derefString(current.ErrorText), 90)},
					{Kind: "count", Text: fmt.Sprintf("同一目标失败 %d 次", attempts)},
				},
				Response: []string{fmt.Sprintf("重试到第 %d 轮第 %d 步", flat[lastPosition].step.TurnIndex, flat[lastPosition].step.Index)},
				Outcome:  fmt.Sprintf("重试 %d 次仍失败", attempts),
			})
			position = lastPosition + 1
			continue
		}
		position++
	}
	return out, covered
}

// compactionEpisodes：上下文压缩（结构化字段），并给出压缩前的 token。
func compactionEpisodes(data *trace.Data) []trace.Episode {
	out := make([]trace.Episode, 0, 4)
	var previousTokens *int64
	for turnIndex := range data.Turns {
		turn := &data.Turns[turnIndex]
		if turn.Compaction != nil {
			duration := derefInt64(turn.Compaction.DurationMs)
			evidence := []trace.Evidence{{Kind: "compaction", Text: fmt.Sprintf("auto=%v 耗时 %s", turn.Compaction.Auto, formatMs(duration))}}
			if previousTokens != nil {
				evidence = append(evidence, trace.Evidence{Kind: "tokens", Text: fmt.Sprintf("压缩前 input_tokens=%d", *previousTokens)})
			}
			start := derefInt64(turn.StartMs)
			out = append(out, trace.Episode{
				Code: CodeCompaction, Label: labels[CodeCompaction], Severity: "info", Confidence: "A",
				Turn: turn.Index, Step: 0, StartMs: start, EndMs: start + duration, WindowMs: duration,
				Evidence: evidence,
				Response: []string{"压缩后继续原任务（上下文被改写）"},
				Outcome:  "已压缩",
			})
		}
		for stepIndex := range turn.Steps {
			if turn.Steps[stepIndex].Tokens != nil && turn.Steps[stepIndex].Tokens.Input != nil {
				previousTokens = turn.Steps[stepIndex].Tokens.Input
			}
		}
	}
	return out
}

// CauseSummary 是按原因聚合的一行。
type CauseSummary struct {
	Code     string `json:"code"`
	Label    string `json:"label"`
	Count    int    `json:"count"`
	WindowMs int64  `json:"windowMs"`
}

// Summarize 按原因聚合（耗时降序）。
func Summarize(episodes []trace.Episode) []CauseSummary {
	byCode := make(map[string]*CauseSummary, len(episodes))
	for _, episode := range episodes {
		entry, ok := byCode[episode.Code]
		if !ok {
			entry = &CauseSummary{Code: episode.Code, Label: episode.Label}
			byCode[episode.Code] = entry
		}
		entry.Count++
		entry.WindowMs += episode.WindowMs
	}
	out := make([]CauseSummary, 0, len(byCode))
	for _, entry := range byCode {
		out = append(out, *entry)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].WindowMs != out[j].WindowMs {
			return out[i].WindowMs > out[j].WindowMs
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func dedupe(episodes []trace.Episode) []trace.Episode {
	seen := make(map[string]bool, len(episodes))
	out := make([]trace.Episode, 0, len(episodes))
	for _, episode := range episodes {
		key := fmt.Sprintf("%s|%d|%d|%d", episode.Code, episode.Turn, episode.Step, episode.StartMs)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, episode)
	}
	return out
}

func hasMojibake(text string) bool {
	if strings.ContainsRune(text, 0xFFFD) {
		return true
	}
	for _, marker := range []string{"ϵͳ", "锟斤拷"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func toolCommand(tool *trace.Tool) string {
	args := decodeArgs(tool.ArgsText)
	if command, ok := args["command"].(string); ok {
		return command
	}
	if path, ok := args["filePath"].(string); ok {
		return path
	}
	return toolTarget(tool)
}

func toolTarget(tool *trace.Tool) string {
	args := decodeArgs(tool.ArgsText)
	for _, key := range []string{"filePath", "path", "file"} {
		if value, ok := args[key].(string); ok && value != "" {
			return value
		}
	}
	if command, ok := args["command"].(string); ok {
		if match := pathLike.FindString(command); match != "" {
			return match
		}
		return strings.TrimSpace(command)
	}
	if pattern, ok := args["pattern"].(string); ok {
		return pattern
	}
	return ""
}

func decodeArgs(text string) map[string]any {
	value := map[string]any{}
	if strings.TrimSpace(text) == "" {
		return value
	}
	_ = json.Unmarshal([]byte(text), &value)
	return value
}

func toolSpan(tool *trace.Tool) (int64, int64) {
	start := derefInt64(tool.StartMs)
	end := derefInt64(tool.EndMs)
	if end == 0 && tool.DurationMs != nil {
		end = start + *tool.DurationMs
	}
	return start, end
}

func baseName(path string) string {
	trimmed := strings.TrimRight(path, "/\\")
	if index := strings.LastIndexAny(trimmed, "/\\"); index >= 0 {
		return trimmed[index+1:]
	}
	return trimmed
}

func oneLine(text string, limit int) string {
	compact := strings.Join(strings.Fields(text), " ")
	if limit > 0 && len(compact) > limit {
		return compact[:limit] + "…"
	}
	return compact
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

func formatMs(ms int64) string {
	switch {
	case ms < 1000:
		return fmt.Sprintf("%d ms", ms)
	case ms < 60000:
		return fmt.Sprintf("%.1f s", float64(ms)/1000)
	case ms < 3600000:
		return fmt.Sprintf("%d m %d s", ms/60000, (ms%60000)/1000)
	default:
		return fmt.Sprintf("%.1f h", float64(ms)/3600000)
	}
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
