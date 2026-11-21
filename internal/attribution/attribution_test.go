package attribution

import (
	"encoding/json"
	"testing"

	"github.com/zhouyuejie/opencode-trace/internal/trace"
)

func ptrInt64(value int64) *int64    { return &value }
func ptrString(value string) *string { return &value }

func tool(name, status, args, output, errText string, start, end int64) trace.Tool {
	item := trace.Tool{
		Name:       name,
		Status:     status,
		StartMs:    ptrInt64(start),
		EndMs:      ptrInt64(end),
		DurationMs: ptrInt64(end - start),
		ArgsText:   args,
		OutputText: output,
	}
	if errText != "" {
		item.ErrorText = ptrString(errText)
	}
	return item
}

func step(index int, tools ...trace.Tool) trace.Step {
	return trace.Step{
		Index:     index,
		TurnIndex: 1,
		StartMs:   ptrInt64(int64(index) * 10_000),
		EndMs:     ptrInt64(int64(index)*10_000 + 5_000),
		Status:    "completed",
		Tools:     tools,
	}
}

func oneTurn(steps ...trace.Step) *trace.Data {
	return &trace.Data{Turns: []trace.Turn{{Index: 1, Kind: "user", Steps: steps}}}
}

func codes(episodes []trace.Episode) map[string]int {
	out := map[string]int{}
	for _, episode := range episodes {
		out[episode.Code]++
	}
	return out
}

func TestEncodingLoss(t *testing.T) {
	broken := tool("bash", "completed", "{\"command\":\"tools\\\\fastdata.cmd auth status\"}",
		"ϵͳ�Ҳ���ָ����·����", "", 1000, 1057)
	episodes := Analyze(oneTurn(step(1, broken)))
	if len(episodes) != 1 || episodes[0].Code != CodeEncodingLoss {
		t.Fatalf("期望 1 条 encoding_loss，实际 %v", codes(episodes))
	}
	if episodes[0].Confidence != "A" || episodes[0].WindowMs != 57 {
		t.Errorf("置信度/窗口 = %s / %d", episodes[0].Confidence, episodes[0].WindowMs)
	}
	found := false
	for _, evidence := range episodes[0].Evidence {
		if evidence.Kind == "output" {
			found = true
		}
	}
	if !found {
		t.Error("缺少 output 证据")
	}
}

func TestServiceBusyRetry(t *testing.T) {
	args := "{\"filePath\":\"/opt/app/GatewayServerApp.py\"}"
	first := tool("edit", "error", args, "", "DEEPWORKS_FILE_WRITE_LOCK_TIMEOUT", 1000, 2000)
	second := tool("edit", "error", args, "", "DEEPWORKS_FILE_WRITE_LOCK_TIMEOUT", 20_000, 21_000)
	third := tool("edit", "completed", args, "ok", "", 40_000, 41_000)
	episodes := Analyze(oneTurn(step(1, first), step(2, second), step(3, third)))
	if len(episodes) != 1 {
		t.Fatalf("期望 1 条事件链，实际 %v", codes(episodes))
	}
	episode := episodes[0]
	if episode.Code != CodeServiceBusy {
		t.Errorf("code = %s，期望 service_busy", episode.Code)
	}
	if episode.Confidence != "A" {
		t.Errorf("置信度 = %s，期望 A", episode.Confidence)
	}
	if episode.WindowMs != 20_000 {
		t.Errorf("窗口 = %d，期望 20000（首次失败→最后失败）", episode.WindowMs)
	}
}

func TestProviderErrorNotRetryable(t *testing.T) {
	status := int64(403)
	retryable := false
	input := &trace.Data{Turns: []trace.Turn{{Index: 1, Kind: "user", Steps: []trace.Step{{
		Index: 1, TurnIndex: 1,
		StartMs: ptrInt64(1000), EndMs: ptrInt64(1200), Status: "error",
		Error: &trace.StepError{Name: "APIError", Message: "用户额度不足", StatusCode: &status, Retryable: &retryable},
	}}}}}
	episodes := Analyze(input)
	if len(episodes) != 1 || episodes[0].Code != CodeProviderError {
		t.Fatalf("期望 1 条 provider_error，实际 %v", codes(episodes))
	}
	if episodes[0].Outcome != "不可重试：整轮终止" {
		t.Errorf("outcome = %q", episodes[0].Outcome)
	}
}

func TestUserAbort(t *testing.T) {
	input := &trace.Data{Turns: []trace.Turn{{Index: 1, Kind: "user", Steps: []trace.Step{{
		Index: 1, TurnIndex: 1, StartMs: ptrInt64(1000), EndMs: ptrInt64(2000),
		Error: &trace.StepError{Name: "MessageAbortedError", Message: "Aborted"},
	}}}}}
	episodes := Analyze(input)
	if len(episodes) != 1 || episodes[0].Code != CodeUserAbort {
		t.Fatalf("期望 user_abort，实际 %v", codes(episodes))
	}
	if episodes[0].Severity != "info" {
		t.Errorf("用户中止应当是 info，实际 %s", episodes[0].Severity)
	}
}

func TestTruncatedOutputWithReread(t *testing.T) {
	grep := tool("grep", "completed", "{\"pattern\":\"*.py\",\"path\":\"/opt/app\"}",
		"a.py\nb.py\n(Results are truncated: showing first 100 results. Consider using a more specific path or pattern.)", "", 1000, 1100)
	reread := tool("read", "completed", "{\"filePath\":\"/opt/app/a.py\"}", "content", "", 12_000, 12_500)
	episodes := Analyze(oneTurn(step(1, grep), step(2, reread)))
	found := false
	for _, episode := range episodes {
		if episode.Code == CodeTruncatedOutput {
			found = true
			if episode.Confidence != "B" {
				t.Errorf("截断应当是 B 级，实际 %s", episode.Confidence)
			}
			if len(episode.Response) == 0 {
				t.Error("应当记录到分片重读")
			}
		}
	}
	if !found {
		t.Fatalf("没有识别出截断，实际 %v", codes(episodes))
	}
}

func TestCompaction(t *testing.T) {
	input := oneTurn(step(1, tool("read", "completed", "{\"filePath\":\"/a.txt\"}", "ok", "", 1000, 1100)))
	tokens := int64(12345)
	input.Turns[0].Steps[0].Tokens = &trace.StepTokens{Input: &tokens}
	input.Turns = append(input.Turns, trace.Turn{
		Index: 2, Kind: "compaction", StartMs: ptrInt64(20_000), EndMs: ptrInt64(28_000),
		Compaction: &trace.Compaction{DurationMs: ptrInt64(81_714), Auto: true},
	})
	episodes := Analyze(input)
	if len(episodes) != 1 || episodes[0].Code != CodeCompaction {
		t.Fatalf("期望 1 条 compaction，实际 %v", codes(episodes))
	}
	if episodes[0].WindowMs != 81_714 {
		t.Errorf("窗口 = %d，期望 81714", episodes[0].WindowMs)
	}
	hasTokens := false
	for _, evidence := range episodes[0].Evidence {
		if evidence.Kind == "tokens" {
			hasTokens = true
		}
	}
	if !hasTokens {
		t.Error("压缩事件应当带压缩前 input_tokens")
	}
}

func TestPathProbe(t *testing.T) {
	missing := tool("read", "error", "{\"filePath\":\"/opt/app/manager/flow_others.py\"}", "",
		"File not found: /opt/app/manager/flow_others.py", 1000, 1010)
	probe := tool("bash", "completed", "{\"command\":\"find /opt/app/manager -name flow_star.py\"}",
		"flow_other.py", "", 11_000, 11_100)
	episodes := Analyze(oneTurn(step(1, missing), step(2, probe)))
	found := false
	for _, episode := range episodes {
		if episode.Code == CodePathProbe {
			found = true
			if len(episode.Response) == 0 {
				t.Error("应当记录到后续探查动作")
			}
		}
	}
	if !found {
		t.Fatalf("没有识别出路径探查，实际 %v", codes(episodes))
	}
}

func TestAuthThenSourceRead(t *testing.T) {
	denied := tool("bash", "error", "{\"command\":\"fastdata api-service test --id 1\"}", "",
		"401 unauthorized: missing project_id", 1000, 1200)
	source := tool("read", "completed", "{\"filePath\":\"/opt/kit/fastdata_cli/commands/api_service.py\"}",
		"def test(): pass", "", 12_000, 12_300)
	episodes := Analyze(oneTurn(step(1, denied), step(2, source)))
	found := false
	for _, episode := range episodes {
		if episode.Code == CodeAuthSourceRead {
			found = true
			if episode.Confidence != "B" {
				t.Errorf("期望 B 级，实际 %s", episode.Confidence)
			}
		}
	}
	if !found {
		t.Fatalf("没有识别出「授权被拒→读源码」，实际 %v", codes(episodes))
	}
}

func TestEditMismatch(t *testing.T) {
	failed := tool("edit", "error", "{\"filePath\":\"/opt/app/main.py\"}", "",
		"Could not find oldString in the file. It must match exactly.", 1000, 1100)
	reread := tool("read", "completed", "{\"filePath\":\"/opt/app/main.py\"}", "content", "", 11_000, 11_200)
	episodes := Analyze(oneTurn(step(1, failed), step(2, reread)))
	if len(episodes) != 1 || episodes[0].Code != CodeEditMismatch {
		t.Fatalf("期望 1 条 edit_mismatch，实际 %v", codes(episodes))
	}
	if len(episodes[0].Response) == 0 {
		t.Error("应当记录到重读动作")
	}
}

func TestHappyPathHasNoEpisodes(t *testing.T) {
	clean := tool("read", "completed", "{\"filePath\":\"/a.txt\"}", "fine", "", 1000, 1100)
	input := oneTurn(step(1, clean), step(2, clean))
	if episodes := Analyze(input); len(episodes) != 0 {
		t.Fatalf("正常流程不该有归因，实际 %v", codes(episodes))
	}
}

func TestSummarizeOrdersByWindow(t *testing.T) {
	episodes := []trace.Episode{
		{Code: CodeCompaction, Label: "压缩", WindowMs: 5_000},
		{Code: CodeEncodingLoss, Label: "乱码", WindowMs: 9_000},
		{Code: CodeEncodingLoss, Label: "乱码", WindowMs: 1_000},
	}
	summary := Summarize(episodes)
	if len(summary) != 2 {
		t.Fatalf("聚合行数 = %d", len(summary))
	}
	if summary[0].Code != CodeEncodingLoss || summary[0].Count != 2 || summary[0].WindowMs != 10_000 {
		t.Errorf("第一行 = %+v", summary[0])
	}
}

func TestEpisodeJSONRoundTrip(t *testing.T) {
	broken := tool("bash", "completed", "{\"command\":\"x.cmd\"}", "\ufffd\ufffd", "", 1000, 1100)
	episodes := Analyze(oneTurn(step(1, broken)))
	encoded, err := json.Marshal(episodes)
	if err != nil {
		t.Fatalf("序列化：%v", err)
	}
	var decoded []trace.Episode
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("反序列化：%v", err)
	}
	if len(decoded) != 1 || decoded[0].Code != CodeEncodingLoss {
		t.Errorf("往返后 = %+v", decoded)
	}
}
