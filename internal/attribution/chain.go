package attribution

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/gzzyj/sesscope/internal/trace"
)

// 补偿链：一次故障引发的连续补救。事件链（Episode）回答"哪里坏了"，
// 补偿链回答"为此连续付出了多少步"——这才是业务侧关心的代价。
const (
	ChainAuthLogin    = "auth_login_compensation"
	ChainEncoding     = "encoding_compensation"
	ChainServiceRetry = "service_retry_compensation"
	ChainGeneric      = "failure_compensation"
)

var chainLabels = map[string]string{
	ChainAuthLogin:    "授权/登录补偿链（CLI 跑不通 → 探目录 → 读脚本 → 重建环境）",
	ChainEncoding:     "编码损坏补偿链（报错不可读 → 绕路探查）",
	ChainServiceRetry: "服务重试补偿链（锁/超时 → 反复重试）",
	ChainGeneric:      "故障补偿链",
}

var (
	envTokenPattern  = regexp.MustCompile(`\b[A-Z][A-Z0-9_]{4,}\b`)
	fileTokenPattern = regexp.MustCompile(`(?i)[\w\-]+\.(cmd|ps1|exe|py|sh|bat|jar)`)
	dotDirPattern    = regexp.MustCompile(`(?:^|[\\/\s"])(\.[A-Za-z0-9_\-]{2,})`)
	envSetupPattern  = regexp.MustCompile(`(?i)(\$env:[A-Z_]+|\bexport\s+[A-Z_]+|\bset\s+[A-Z_]+=)`)
)

// DefaultChainGap 是补偿链允许夹带的无关步数上限（超过就断开）。
const DefaultChainGap = 6

// BuildChains 把离散的事件链聚成"补偿链"。
// 规则：从首个故障步骤出发，按主题（环境变量 / 脚本名 / 隐藏目录）向后延伸，
// 主题词随链传递扩展；允许中间夹 maxGap 步无关动作；
// 一旦遇到 write/edit（真正开始产出）就停——这就是"什么时候开始干正事"的边界。
func BuildChains(data *trace.Data, episodes []trace.Episode, maxGap int) []trace.Chain {
	if data == nil || len(episodes) == 0 {
		return []trace.Chain{}
	}
	if maxGap < 0 {
		maxGap = 0
	}
	flat := flattenSteps(data)
	stepsByTurn := make(map[int][]stepEntry, len(data.Turns))
	for _, entry := range flat {
		stepsByTurn[entry.step.TurnIndex] = append(stepsByTurn[entry.step.TurnIndex], entry)
	}

	chains := make([]trace.Chain, 0, 4)
	usedUpTo := map[int]int{} // turn -> 已并入链的最后一个 step
	for _, episode := range episodes {
		if episode.Severity == "info" {
			continue // 用户中止 / 压缩不算补偿
		}
		entries := stepsByTurn[episode.Turn]
		if len(entries) == 0 {
			continue
		}
		startIndex := indexOfStep(entries, episode.Step)
		if startIndex < 0 {
			continue
		}
		if usedUpTo[episode.Turn] > entries[startIndex].step.Index {
			continue // 已经并入上一条链
		}

		subject := stepTokens(entries[startIndex].step)
		if len(subject) == 0 {
			continue
		}
		endIndex := startIndex
		gap := 0
		for index := startIndex + 1; index < len(entries); index++ {
			step := entries[index].step
			if isRealWorkStep(step) {
				break
			}
			if touches(step, subject) {
				// 主题词随链传递：模型在补偿过程中发现的新脚本/目录也算同一条链
				for token := range stepTokens(step) {
					subject[token] = true
				}
				endIndex = index
				gap = 0
				continue
			}
			gap++
			if gap > maxGap {
				break
			}
			endIndex = index
		}

		startEntry, endEntry := entries[startIndex], entries[endIndex]
		if endIndex == startIndex && !touches(startEntry.step, subject) {
			continue
		}
		chain := trace.Chain{
			Code:       chainCodeFor(episode),
			Label:      chainLabels[chainCodeFor(episode)],
			Confidence: episode.Confidence,
			Turn:       episode.Turn,
			StartStep:  startEntry.step.Index,
			EndStep:    endEntry.step.Index,
			Steps:      endEntry.step.Index - startEntry.step.Index + 1,
			StartMs:    stepStart(startEntry.step),
			EndMs:      stepEnd(endEntry.step),
			Subject:    subjectList(subject),
			Episodes:   []int{},
			Evidence: []trace.Evidence{
				{Kind: "trigger", Text: fmt.Sprintf("第 %d 轮第 %d 步：%s", episode.Turn, episode.Step, episode.Label)},
				{Kind: "subject", Text: strings.Join(subjectList(subject), ", ")},
			},
		}
		if chainHasAuthIntent(entries[startIndex : endIndex+1]) {
			chain.Code = ChainAuthLogin
			chain.Label = chainLabels[ChainAuthLogin]
			chain.Evidence = append(chain.Evidence, trace.Evidence{Kind: "intent", Text: "链内命令出现 auth/login/token/授权 意图"})
		}
		chain.WindowMs = maxInt64(0, chain.EndMs-chain.StartMs)
		// 收集落在链内的其它事件
		for otherIndex, other := range episodes {
			if other.Turn != episode.Turn {
				continue
			}
			if other.Step < chain.StartStep || other.Step > chain.EndStep {
				continue
			}
			chain.Episodes = append(chain.Episodes, otherIndex)
		}
		chain.Outcome = chainOutcome(entries[startIndex : endIndex+1])
		usedUpTo[episode.Turn] = chain.EndStep
		chains = append(chains, chain)
	}
	sort.SliceStable(chains, func(i, j int) bool { return chains[i].StartMs < chains[j].StartMs })
	return chains
}

func chainHasAuthIntent(entries []stepEntry) bool {
	for _, entry := range entries {
		for toolIndex := range entry.step.Tools {
			if authIntentPattern.MatchString(toolCommand(&entry.step.Tools[toolIndex])) {
				return true
			}
		}
	}
	return false
}

func chainCodeFor(episode trace.Episode) string {
	switch episode.Code {
	case CodeEncodingLoss, CodePathProbe:
		return ChainEncoding
	case CodeAuthBlocked, CodeAuthSourceRead:
		return ChainAuthLogin
	case CodeServiceBusy, CodeRetrySameTarget, CodeProviderError:
		return ChainServiceRetry
	default:
		return ChainGeneric
	}
}

func indexOfStep(entries []stepEntry, step int) int {
	for index, entry := range entries {
		if entry.step.Index == step {
			return index
		}
	}
	return -1
}

// stepTokens 提取一个步骤的"主题"：环境变量名、脚本名、隐藏目录。
func stepTokens(step *trace.Step) map[string]bool {
	tokens := map[string]bool{}
	for toolIndex := range step.Tools {
		tool := &step.Tools[toolIndex]
		for _, text := range []string{tool.ArgsText, tool.OutputText, toolCommand(tool)} {
			for _, match := range envTokenPattern.FindAllString(text, -1) {
				tokens[strings.ToLower(match)] = true
			}
			for _, match := range fileTokenPattern.FindAllString(text, -1) {
				tokens[strings.ToLower(match)] = true
			}
			for _, match := range dotDirPattern.FindAllStringSubmatch(text, -1) {
				tokens[strings.ToLower(match[1])] = true
			}
		}
	}
	return tokens
}

func touches(step *trace.Step, subject map[string]bool) bool {
	for token := range stepTokens(step) {
		if subject[token] {
			return true
		}
	}
	// 环境变量重建也视为补偿动作（同一套 kit 环境反复设置）
	for toolIndex := range step.Tools {
		if envSetupPattern.MatchString(toolCommand(&step.Tools[toolIndex])) {
			return true
		}
	}
	return false
}

// isRealWorkStep：出现 write/edit 说明已经转入正常产出，补偿链到此结束。
func isRealWorkStep(step *trace.Step) bool {
	for toolIndex := range step.Tools {
		if step.Tools[toolIndex].Name == "write" || step.Tools[toolIndex].Name == "edit" {
			return true
		}
	}
	return false
}

// chainOutcome 只给可证伪的事实：链内最后一次失败在第几步、之后是否还有失败。
func chainOutcome(entries []stepEntry) string {
	lastFailure := 0
	failures := 0
	for _, entry := range entries {
		for toolIndex := range entry.step.Tools {
			tool := &entry.step.Tools[toolIndex]
			failed := tool.Status == "error" || hasMojibake(tool.OutputText)
			if toolKindOf(tool) == "shell" {
				if failed {
					lastFailure = entry.step.Index
					failures++
				}
			} else if failed {
				failures++
			}
		}
	}
	if failures == 0 {
		return "链内无失败记录"
	}
	if lastFailure == 0 {
		return fmt.Sprintf("链内 %d 次失败（非命令类）", failures)
	}
	tail := 0
	for _, entry := range entries {
		if entry.step.Index > lastFailure {
			tail++
		}
	}
	return fmt.Sprintf("最后一次失败在第 %d 步；之后 %d 步无失败", lastFailure, tail)
}

var authIntentPattern = regexp.MustCompile("(?i)(auth|login|token|credential|授权|登录|凭证)")

func subjectList(subject map[string]bool) []string {
	list := make([]string, 0, len(subject))
	for token := range subject {
		list = append(list, token)
	}
	sort.Strings(list)
	return list
}

func stepStart(step *trace.Step) int64 {
	start := derefInt64(step.StartMs)
	for toolIndex := range step.Tools {
		if value := derefInt64(step.Tools[toolIndex].StartMs); value != 0 && (start == 0 || value < start) {
			start = value
		}
	}
	return start
}

func stepEnd(step *trace.Step) int64 {
	end := derefInt64(step.EndMs)
	for toolIndex := range step.Tools {
		if value := derefInt64(step.Tools[toolIndex].EndMs); value > end {
			end = value
		}
	}
	return end
}
