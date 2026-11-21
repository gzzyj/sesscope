package trace

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func ptr[T any](value T) *T { return &value }

func testOptions() Options {
	return Options{
		ExportPath:  "/tmp/session-export.json",
		GeneratedAt: time.Date(2026, 11, 20, 10, 22, 4, 130000000, time.UTC),
	}
}

// textPart 造一个 text/reasoning part。
func textPart(id, kind string, created, start, end int64, text string) Part {
	return Part{
		ID:            id,
		Type:          kind,
		TimeCreatedMs: ptr(created),
		TimeUpdatedMs: ptr(end),
		Data: &PartData{
			Type: ptr(kind),
			Text: ptr(text),
			Time: &TimeRange{Start: ptr(start), End: ptr(end)},
		},
	}
}

func syntheticTextPart(id string, created int64, text string) Part {
	part := textPart(id, "text", created, created, created+10, text)
	part.Data.Synthetic = ptr(true)
	return part
}

func toolPart(id, callID, name, status string, created int64, start, end *int64) Part {
	return Part{
		ID:            id,
		Type:          "tool",
		TimeCreatedMs: ptr(created),
		TimeUpdatedMs: end,
		Data: &PartData{
			Type:   ptr("tool"),
			Tool:   ptr(name),
			CallID: ptr(callID),
			State: &ToolState{
				Status: status,
				Input:  json.RawMessage("{\"command\":\"ls\"}"),
				Output: json.RawMessage("\"done\""),
				Time:   &TimeRange{Start: start, End: end},
			},
		},
	}
}

func userMessage(id string, created int64, parts ...Part) Message {
	return Message{MessageID: id, Role: "user", TimeCreatedMs: ptr(created), Parts: parts}
}

func assistantMessage(id string, created int64, completed *int64, data *MessageData, parts ...Part) Message {
	message := Message{
		MessageID:     id,
		Role:          "assistant",
		TimeCreatedMs: ptr(created),
		Parts:         parts,
		Data:          data,
	}
	if data == nil {
		message.Data = &MessageData{}
	}
	message.Data.Role = ptr("assistant")
	if completed != nil {
		message.Data.Time = &TimeRange{Completed: completed}
	}
	return message
}

func TestNormalizeSyntheticContinue(t *testing.T) {
	doc := &Export{Messages: []Message{
		userMessage("msg_u1", 1000, syntheticTextPart("prt_1", 1000, "Continue if you have next steps")),
	}}
	data, err := NormalizeExport(doc, testOptions())
	if err != nil {
		t.Fatalf("归一化失败：%v", err)
	}
	turn := data.Turns[0]
	if turn.Kind != "continue" {
		t.Errorf("kind = %q，期望 continue", turn.Kind)
	}
	if turn.Input.SyntheticText == nil || *turn.Input.SyntheticText != "Continue if you have next steps" {
		t.Errorf("syntheticText = %v", turn.Input.SyntheticText)
	}
	if !turn.Input.SyntheticOnly {
		t.Error("syntheticOnly 应为 true")
	}
	if data.Stats.UserTurns != 0 {
		t.Errorf("自动续跑不应计入用户输入，userTurns = %d", data.Stats.UserTurns)
	}
	if !hasWarning(data, "synthetic-continue") {
		t.Error("缺少 synthetic-continue 提示")
	}
}

func TestNormalizeCompaction(t *testing.T) {
	compaction := Part{
		ID:            "prt_c1",
		Type:          "compaction",
		TimeCreatedMs: int64Ptr(2000),
		TimeUpdatedMs: int64Ptr(3000),
		DurationMs:    int64Ptr(81714),
		Data:          &PartData{Type: ptr("compaction"), Auto: ptr(true)},
	}
	doc := &Export{Messages: []Message{userMessage("msg_u1", 2000, compaction)}}
	data, err := NormalizeExport(doc, testOptions())
	if err != nil {
		t.Fatalf("归一化失败：%v", err)
	}
	turn := data.Turns[0]
	if turn.Kind != "compaction" {
		t.Errorf("kind = %q，期望 compaction", turn.Kind)
	}
	if turn.Compaction == nil || !turn.Compaction.Auto || *turn.Compaction.DurationMs != 81714 {
		t.Errorf("compaction = %+v", turn.Compaction)
	}
	if data.Stats.CompactionMs != 81714 {
		t.Errorf("compactionMs = %d", data.Stats.CompactionMs)
	}
	if !hasWarning(data, "auto-compaction") {
		t.Error("缺少 auto-compaction 提示")
	}
}

func TestNormalizeToolGapModelSpan(t *testing.T) {
	stepStart := Part{ID: "prt_s", Type: "step-start", TimeCreatedMs: int64Ptr(500), Data: &PartData{Type: ptr("step-start")}}
	tool := toolPart("prt_t1", "call_1", "bash", "completed", 1000, int64Ptr(1000), int64Ptr(1500))
	completed := int64(1800)
	doc := &Export{Messages: []Message{
		userMessage("msg_u1", 100),
		assistantMessage("msg_a1", 500, &completed, nil, stepStart, tool),
	}}
	data, err := NormalizeExport(doc, testOptions())
	if err != nil {
		t.Fatalf("归一化失败：%v", err)
	}
	step := data.Turns[0].Steps[0]
	if step.ModelSpanSource != "tool-gap" {
		t.Errorf("modelSpanSource = %q，期望 tool-gap", step.ModelSpanSource)
	}
	if step.ModelStartMs == nil || *step.ModelStartMs != 500 || step.ModelEndMs == nil || *step.ModelEndMs != 1000 {
		t.Errorf("模型片段 = %v..%v，期望 500..1000", step.ModelStartMs, step.ModelEndMs)
	}
	if step.ModelMs == nil || *step.ModelMs != 500 {
		t.Errorf("modelMs = %v，期望 500", step.ModelMs)
	}
	if step.StepMs == nil || *step.StepMs != 1300 {
		t.Errorf("stepMs = %v，期望 1300", step.StepMs)
	}
	if step.Tools[0].DurationMs == nil || *step.Tools[0].DurationMs != 500 {
		t.Errorf("工具耗时 = %v，期望 500", step.Tools[0].DurationMs)
	}
	if step.Tools[0].ArgsText != "{\n  \"command\": \"ls\"\n}" {
		t.Errorf("参数文本 = %q", step.Tools[0].ArgsText)
	}
}

func TestNormalizeAbortedStep(t *testing.T) {
	completed := int64(2000)
	data := &MessageData{Error: &MessageErr{
		Name: ptr("MessageAbortedError"),
		Data: &MessageErrorData{Message: ptr("Aborted")},
	}}
	doc := &Export{Messages: []Message{
		userMessage("msg_u1", 1000),
		assistantMessage("msg_a1", 1200, &completed, data, textPart("prt_1", "reasoning", 1200, 1300, 1900, "想一想")),
	}}
	result, err := NormalizeExport(doc, testOptions())
	if err != nil {
		t.Fatalf("归一化失败：%v", err)
	}
	step := result.Turns[0].Steps[0]
	if step.Status != "error" {
		t.Errorf("status = %q，期望 error", step.Status)
	}
	if step.Error == nil || step.Error.Name != "MessageAbortedError" || step.Error.Message != "Aborted" {
		t.Errorf("error = %+v", step.Error)
	}
	if result.Stats.Errors != 1 {
		t.Errorf("errors = %d，期望 1", result.Stats.Errors)
	}
	warning := findWarning(result, "aborted-step")
	if warning == nil || !strings.Contains(warning.Detail, "第 1 轮第 1 步") {
		t.Errorf("aborted-step 提示 = %+v", warning)
	}
}

func TestNormalizeUnfinishedStep(t *testing.T) {
	tool := toolPart("prt_t1", "call_1", "bash", "running", 1000, int64Ptr(1000), nil)
	doc := &Export{Messages: []Message{
		userMessage("msg_u1", 100),
		assistantMessage("msg_a1", 900, nil, nil, textPart("prt_1", "text", 950, 950, 980, "开始"), tool),
	}}
	result, err := NormalizeExport(doc, testOptions())
	if err != nil {
		t.Fatalf("归一化失败：%v", err)
	}
	step := result.Turns[0].Steps[0]
	if step.Status != "running" {
		t.Errorf("status = %q，期望 running", step.Status)
	}
	if step.StepMs != nil {
		t.Errorf("未完成的步骤不应有 stepMs，得到 %v", *step.StepMs)
	}
	if step.Tools[0].DurationMs != nil {
		t.Errorf("未结束的工具不应有耗时，得到 %v", *step.Tools[0].DurationMs)
	}
	if result.Stats.Running != 1 || result.Stats.ToolRunning != 1 {
		t.Errorf("running = %d / %d", result.Stats.Running, result.Stats.ToolRunning)
	}
	// 未完成的步骤没有结束时间，因此不贡献"最后活动时间"（和 Node 版口径一致）
	if result.Meta.LastMs != nil {
		t.Errorf("末步未完成时 lastMs 应为 nil，得到 %v", *result.Meta.LastMs)
	}
	if result.Meta.FirstMs == nil || *result.Meta.FirstMs != 100 {
		t.Errorf("firstMs = %v，期望 100", result.Meta.FirstMs)
	}
}

func TestNormalizeAttachments(t *testing.T) {
	// 直接构造附件元数据 JSON，避免在源码里写一堆转义引号
	meta, err := json.Marshal(map[string]string{"n": "报告.sql"})
	if err != nil {
		t.Fatalf("构造附件元数据失败：%v", err)
	}
	payload := base64.StdEncoding.EncodeToString(meta)
	doc := &Export{Messages: []Message{
		userMessage("msg_u1", 1000, textPart("prt_1", "text", 1000, 1000, 1010,
			"测试该接口 [dw-attachment:"+payload+"]")),
	}}
	result, err := NormalizeExport(doc, testOptions())
	if err != nil {
		t.Fatalf("归一化失败：%v", err)
	}
	turn := result.Turns[0]
	if turn.Kind != "user" {
		t.Errorf("kind = %q", turn.Kind)
	}
	if turn.Input.Text != "测试该接口" {
		t.Errorf("text = %q，期望「测试该接口」", turn.Input.Text)
	}
	if len(turn.Input.Attachments) != 1 || turn.Input.Attachments[0] != "报告.sql" {
		t.Errorf("attachments = %v", turn.Input.Attachments)
	}
}

func TestNormalizeNegativeTurn(t *testing.T) {
	completed := int64(1500)
	doc := &Export{Messages: []Message{
		userMessage("msg_u1", 2000),
		assistantMessage("msg_a1", 1400, &completed, nil, textPart("prt_1", "text", 1400, 1400, 1500, "ok")),
	}}
	result, err := NormalizeExport(doc, testOptions())
	if err != nil {
		t.Fatalf("归一化失败：%v", err)
	}
	if result.Turns[0].EndMs == nil || *result.Turns[0].EndMs != 1500 {
		t.Errorf("turn end = %v", result.Turns[0].EndMs)
	}
	warning := findWarning(result, "negative-turn")
	if warning == nil {
		t.Fatal("缺少 negative-turn 提示")
	}
	if !strings.Contains(warning.Detail, "2026-11-20T") {
		// 这里只校验它把 ISO 时间写进了文案，不校验具体时刻
		if !strings.Contains(warning.Detail, "turn_start=") {
			t.Errorf("negative-turn 文案 = %q", warning.Detail)
		}
	}
}

func TestNormalizeRejectsEmptyMessages(t *testing.T) {
	if _, err := NormalizeExport(&Export{}, testOptions()); err == nil {
		t.Fatal("没有 messages 时应当报错")
	}
}

func TestNormalizeModelAsJSONString(t *testing.T) {
	// session.model 在导出里既可能是对象，也可能是“对象序列化后的字符串”，这里构造后者
	inner, err := json.Marshal(map[string]string{"id": "GLM-5.3", "providerID": "dth", "variant": "high"})
	if err != nil {
		t.Fatalf("构造 model：%v", err)
	}
	quoted, err := json.Marshal(string(inner))
	if err != nil {
		t.Fatalf("构造 model 字符串：%v", err)
	}
	doc := &Export{
		Session:  &Session{Model: quoted},
		Messages: []Message{userMessage("msg_u1", 1000, textPart("prt_1", "text", 1000, 1000, 1010, "hi"))},
	}
	result, err := NormalizeExport(doc, testOptions())
	if err != nil {
		t.Fatalf("归一化失败：%v", err)
	}
	model := result.Meta.Model
	if model == nil || model.ID == nil || *model.ID != "GLM-5.3" || model.Provider == nil || *model.Provider != "dth" {
		t.Fatalf("model = %+v", model)
	}
}

func TestNormalizeEmptySlicesMarshalAsArrays(t *testing.T) {
	doc := &Export{Messages: []Message{
		userMessage("msg_u1", 1000, textPart("prt_1", "text", 1000, 1000, 1010, "hi")),
	}}
	result, err := NormalizeExport(doc, testOptions())
	if err != nil {
		t.Fatalf("归一化失败：%v", err)
	}
	encoded, err := EncodeDataJSON(result)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	text := string(encoded)
	q := string(rune(34)) // 双引号，避免源码里堆转义
	for _, needle := range []string{
		q + "attachments" + q + ": []",
		q + "steps" + q + ": []",
		q + "warnings" + q + ": [",
	} {
		if !strings.Contains(text, needle) {
			t.Errorf("期望出现 %s（空数组不能写成 null）", needle)
		}
	}
	if !strings.Contains(text, q+"slowestTool"+q+": null") {
		t.Error("没有工具时 slowestTool 应为 null")
	}
}

func hasWarning(data *Data, code string) bool { return findWarning(data, code) != nil }

func findWarning(data *Data, code string) *Warning {
	for index := range data.Warnings {
		if data.Warnings[index].Code == code {
			return &data.Warnings[index]
		}
	}
	return nil
}
