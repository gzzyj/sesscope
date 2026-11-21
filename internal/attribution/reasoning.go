package attribution

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/gzzyj/atrace/internal/trace"
)

// 归因里唯一"像推理"的部分（为什么这么做），模型在运行时已经写进 reasoning 了。
// 这里不做推理，只做"对账"：按原因挑出该步 reasoning 里对应的那句话，原样引用。
// 引用带出处（轮/步），可核对，不是我们的判断。
var selfExplainKeywords = map[string]*regexp.Regexp{
	CodeEncodingLoss:    regexp.MustCompile("(?i)(乱码|编码|garbled|encoding|GBK|U[+]FFFD)"),
	CodeAuthBlocked:     regexp.MustCompile("(?i)(auth|login|授权|登录|token|project_id|credential)"),
	CodeAuthSourceRead:  regexp.MustCompile("(?i)(auth|login|授权|登录|token|project_id|参数|传参)"),
	CodePathProbe:       regexp.MustCompile("(?i)(找不到|not found|no such|路径|path)"),
	CodeTruncatedOutput: regexp.MustCompile("(?i)(截断|truncat|分页|分片|offset)"),
	CodeServiceBusy:     regexp.MustCompile("(?i)(超时|timeout|锁|lock|重试|retry|busy)"),
	CodeRetrySameTarget: regexp.MustCompile("(?i)(超时|timeout|锁|lock|重试|retry|再试)"),
	CodeProviderError:   regexp.MustCompile("(?i)(报错|error|失败|failed|额度|status)"),
	CodeEditMismatch:    regexp.MustCompile("(?i)(oldString|匹配|match|重读|re-read)"),
}

// attachSelfExplanation 给每条事件链补一条"模型自述"证据（能对上就补，对不上就不补）。
func attachSelfExplanation(data *trace.Data, episodes []trace.Episode) {
	if data == nil || len(episodes) == 0 {
		return
	}
	steps := make(map[string]*trace.Step, len(data.Turns)*4)
	for turnIndex := range data.Turns {
		turn := &data.Turns[turnIndex]
		for stepIndex := range turn.Steps {
			step := &turn.Steps[stepIndex]
			steps[fmt.Sprintf("%d|%d", step.TurnIndex, step.Index)] = step
		}
	}
	for index := range episodes {
		episode := &episodes[index]
		pattern, ok := selfExplainKeywords[episode.Code]
		if !ok {
			continue
		}
		step, ok := steps[fmt.Sprintf("%d|%d", episode.Turn, episode.Step)]
		if !ok {
			continue
		}
		if sentence, ok := findSelfExplanation(step, pattern); ok {
			episode.Evidence = append(episode.Evidence, trace.Evidence{
				Kind: "reasoning",
				Text: "模型当时说：" + sentence,
			})
		}
	}
}

func findSelfExplanation(step *trace.Step, pattern *regexp.Regexp) (string, bool) {
	for _, span := range step.Reasoning {
		for _, sentence := range splitSentences(span.Text) {
			if len([]rune(sentence)) < 6 || !pattern.MatchString(sentence) {
				continue
			}
			return truncateRunes(sentence, 120), true
		}
	}
	return "", false
}

func splitSentences(text string) []string {
	replacer := strings.NewReplacer("\n", "。", "\r", "。", "！", "。", "？", "。", ". ", "。")
	return strings.Split(replacer.Replace(text), "。")
}

func truncateRunes(text string, limit int) string {
	trimmed := strings.TrimSpace(text)
	runes := []rune(strings.Join(strings.Fields(trimmed), " "))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}
