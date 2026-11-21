package opencode

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ExportDocument 读取会话并组装成导出文档。
func (db *DB) ExportDocument(sessionID string, now time.Time) (*Document, error) {
	detail, err := db.Load(sessionID)
	if err != nil {
		return nil, err
	}
	record, err := db.sessionRecord(detail.Session.ID)
	if err != nil {
		return nil, err
	}
	partsByMessage := make(map[string][]PartRow, len(detail.Messages))
	for _, part := range detail.Parts {
		partsByMessage[part.MessageID] = append(partsByMessage[part.MessageID], part)
	}

	doc := &Document{
		ExportInfo: ExportInfo{
			ExportedAt: now.Format("2006-01-02T15:04:05.000000-07:00"),
			Source:     "opencode.db (read-only) · " + db.Path,
			SessionID:  detail.Session.ID,
			Scope:      "session + messages + parts (full conversation, reasoning, tool calls)",
			Excluded:   "account/credential tables (never exported)",
			TimeNote:   "duration_ms = time_updated - time_created；turn 时间取自 message.data.time 与 step-start/step-finish part",
		},
		Session:     record,
		Messages:    make([]MessageJSON, 0, len(detail.Messages)),
		TurnTimings: make([]TurnTiming, 0, len(detail.Messages)),
	}

	var (
		firstMs, lastMs int64
		haveActivity    bool
		reasoningTotal  int64
		toolTotal       int64
		roleCounts      = map[string]int{}
		partTypeCounts  = map[string]int{}
	)
	for _, message := range detail.Messages {
		data, keys := decodeData(message.Data)
		role := stringField(data, "role", "assistant")
		roleCounts[role]++

		parts := partsByMessage[message.ID]
		partJSON := make([]PartJSON, 0, len(parts))
		partTimes := make([]PartTimeJSON, 0, len(parts))
		for _, part := range parts {
			partData, _ := decodeData(part.Data)
			partType := stringField(partData, "type", "unknown")
			partTypeCounts[partType]++
			partJSON = append(partJSON, PartJSON{
				ID:            part.ID,
				Type:          partType,
				TimeCreatedMs: part.TimeCreatedMs,
				TimeUpdatedMs: part.TimeUpdatedMs,
				DurationMs:    part.TimeUpdatedMs - part.TimeCreatedMs,
				Data:          rawValue(part.Data),
			})
			start, end := effectiveSpan(partData, partType, part.TimeCreatedMs, part.TimeUpdatedMs)
			partTimes = append(partTimes, PartTimeJSON{
				PartID:     part.ID,
				Type:       partType,
				StartMs:    start,
				EndMs:      end,
				DurationMs: spanDelta(start, end),
			})
		}

		if message.TimeCreatedMs < firstMs || !haveActivity {
			firstMs = message.TimeCreatedMs
			haveActivity = true
		}
		if message.TimeUpdatedMs > lastMs {
			lastMs = message.TimeUpdatedMs
		}
		for _, span := range partTimes {
			if span.EndMs != nil && *span.EndMs > lastMs {
				lastMs = *span.EndMs
			}
		}

		doc.Messages = append(doc.Messages, MessageJSON{
			MessageID:      message.ID,
			Role:           role,
			TimeCreatedMs:  message.TimeCreatedMs,
			TimeUpdatedMs:  message.TimeUpdatedMs,
			TimeCreatedISO: isoUTC(message.TimeCreatedMs),
			TimeUpdatedISO: isoUTC(message.TimeUpdatedMs),
			ModelID:        stringPtrField(data, "modelID"),
			ModelProvider:  stringPtrField(data, "providerID"),
			SummaryKeys:    keys,
			Data:           rawValue(message.Data),
			Parts:          partJSON,
			PartTimes:      partTimes,
		})

		if role == "assistant" {
			timing := turnTiming(message, data, parts)
			reasoningTotal += timing.ReasoningDurationMs
			toolTotal += timing.ToolDurationMs
			doc.TurnTimings = append(doc.TurnTimings, timing)
		}
	}

	stats := GlobalStats{
		MessageCount:             len(doc.Messages),
		RoleCounts:               roleCounts,
		PartTypeCounts:           partTypeCounts,
		TotalReasoningDurationMs: reasoningTotal,
		TotalToolDurationMs:      toolTotal,
		TotalTurns:               len(doc.TurnTimings),
	}
	if haveActivity {
		first, last := firstMs, lastMs
		firstISO, lastISO := isoUTC(first), isoUTC(last)
		wall := last - first
		stats.FirstActivityMs = &first
		stats.FirstActivityISO = &firstISO
		stats.LastActivityMs = &last
		stats.LastActivityISO = &lastISO
		stats.TotalWallDurationMs = &wall
	}
	doc.GlobalStats = stats
	return doc, nil
}

// EncodeDocument 输出 JSON（缩进，便于人看也便于 diff）。
func EncodeDocument(doc *Document) ([]byte, error) {
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("序列化导出文档：%w", err)
	}
	return append(encoded, '\n'), nil
}

// sessionRecord 读取 session 整行。
func (db *DB) sessionRecord(id string) (SessionRecord, error) {
	query := "select id, project_id, workspace_id, parent_id, slug, directory, path, title, version, share_url, " +
		"summary_additions, summary_deletions, summary_files, metadata, coalesce(cost, 0), " +
		"coalesce(tokens_input, 0), coalesce(tokens_output, 0), coalesce(tokens_reasoning, 0), " +
		"coalesce(tokens_cache_read, 0), coalesce(tokens_cache_write, 0), " +
		"revert, permission, agent, model, time_created, time_updated, time_compacting, time_archived " +
		"from session where id = ?"
	var (
		record                                     SessionRecord
		workspaceID, parentID, path, shareURL      sql.NullString
		summaryAdd, summaryDel, summaryFiles       sql.NullInt64
		metadata, revert, permission, agent, model sql.NullString
		timeCompacting, timeArchived               sql.NullInt64
	)
	err := db.conn.QueryRow(query, id).Scan(
		&record.ID, &record.ProjectID, &workspaceID, &parentID, &record.Slug, &record.Directory, &path,
		&record.Title, &record.Version, &shareURL, &summaryAdd, &summaryDel, &summaryFiles, &metadata,
		&record.Cost, &record.TokensInput, &record.TokensOutput, &record.TokensReasoning,
		&record.TokensCacheRead, &record.TokensCacheWrite,
		&revert, &permission, &agent, &model, &record.TimeCreated, &record.TimeUpdated,
		&timeCompacting, &timeArchived,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return SessionRecord{}, fmt.Errorf("%w：%s", ErrSessionNotFound, id)
		}
		return SessionRecord{}, fmt.Errorf("读取会话 %s：%w", id, err)
	}
	record.WorkspaceID = nullableString(workspaceID)
	record.ParentID = nullableString(parentID)
	record.Path = nullableString(path)
	record.ShareURL = nullableString(shareURL)
	record.SummaryAdditions = nullableInt(summaryAdd)
	record.SummaryDeletions = nullableInt(summaryDel)
	record.SummaryFiles = nullableInt(summaryFiles)
	record.TimeCompacting = nullableInt(timeCompacting)
	record.TimeArchived = nullableInt(timeArchived)
	record.Metadata = jsonValue(metadata)
	record.Revert = jsonValue(revert)
	record.Permission = jsonValue(permission)
	record.Model = jsonValue(model)
	record.Agent = nullableString(agent)
	return record, nil
}

// turnTiming 复刻"每条 assistant 消息一轮"的时间画像口径。
func turnTiming(message MessageRow, data map[string]any, parts []PartRow) TurnTiming {
	timing := TurnTiming{MessageID: message.ID, TokensByStep: []any{}, CostByStep: []float64{}}
	if tokens, ok := data["tokens"]; ok && tokens != nil {
		timing.TokensByStep = append(timing.TokensByStep, tokens)
	}
	if cost, ok := data["cost"].(float64); ok {
		timing.CostByStep = append(timing.CostByStep, cost)
	}

	start := int64OrNil(nestedNumber(data, "time", "created"))
	if start == nil {
		created := message.TimeCreatedMs
		start = &created
	}
	end := int64OrNil(nestedNumber(data, "time", "completed"))
	for _, part := range parts {
		partData, _ := decodeData(part.Data)
		switch stringField(partData, "type", "") {
		case "step-start":
			timing.Steps++
		case "step-finish":
			candidate := part.TimeUpdatedMs
			if end == nil || candidate > *end {
				end = &candidate
			}
		case "reasoning":
			timing.ReasoningParts++
			spanStart, spanEnd := effectiveSpan(partData, "reasoning", part.TimeCreatedMs, part.TimeUpdatedMs)
			if delta := spanDelta(spanStart, spanEnd); delta != nil {
				timing.ReasoningDurationMs += *delta
			}
		case "text":
			timing.TextParts++
		case "tool":
			timing.ToolCalls++
			spanStart, spanEnd := effectiveSpan(partData, "tool", part.TimeCreatedMs, part.TimeUpdatedMs)
			if delta := spanDelta(spanStart, spanEnd); delta != nil {
				timing.ToolDurationMs += *delta
			}
		}
	}
	timing.TurnStartMs = start
	timing.TurnEndMs = end
	if start != nil && end != nil {
		duration := *end - *start
		timing.TurnDurationMs = &duration
	}
	if start != nil {
		value := isoUTC(*start)
		timing.TurnStartISO = &value
	}
	if end != nil {
		value := isoUTC(*end)
		timing.TurnEndISO = &value
	}
	return timing
}

// effectiveSpan 取 part 的真实执行区间：reasoning/text 用 data.time，tool 用 data.state.time。
func effectiveSpan(data map[string]any, partType string, created, updated int64) (start, end *int64) {
	switch partType {
	case "reasoning", "text":
		start = int64OrNil(nestedNumber(data, "time", "start"))
		end = int64OrNil(nestedNumber(data, "time", "end"))
	case "tool":
		start = int64OrNil(nestedNumber(data, "state", "time", "start"))
		end = int64OrNil(nestedNumber(data, "state", "time", "end"))
	}
	if start == nil {
		value := created
		start = &value
	}
	if end == nil {
		value := updated
		end = &value
	}
	return start, end
}

func spanDelta(start, end *int64) *int64 {
	if start == nil || end == nil {
		return nil
	}
	value := *end - *start
	return &value
}

func decodeData(raw json.RawMessage) (map[string]any, []string) {
	value := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &value); err != nil {
			value = map[string]any{}
		}
	}
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return value, keys
}

// rawValue 把列里的 JSON 文本原样作为 JSON 值输出（解析失败就当字符串，不丢内容）。
func rawValue(raw json.RawMessage) any {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	return trimmed
}

func jsonValue(value sql.NullString) any {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	return rawValue(json.RawMessage(value.String))
}

func stringField(data map[string]any, key, fallback string) string {
	if value, ok := data[key].(string); ok && value != "" {
		return value
	}
	return fallback
}

func stringPtrField(data map[string]any, key string) *string {
	if value, ok := data[key].(string); ok && value != "" {
		return &value
	}
	return nil
}

func nestedNumber(data map[string]any, path ...string) *float64 {
	var current any = data
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = object[key]
		if !ok {
			return nil
		}
	}
	if value, ok := current.(float64); ok {
		return &value
	}
	return nil
}

func int64OrNil(value *float64) *int64 {
	if value == nil {
		return nil
	}
	converted := int64(*value)
	return &converted
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	text := value.String
	return &text
}

func nullableInt(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	number := value.Int64
	return &number
}

func isoUTC(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z")
}
