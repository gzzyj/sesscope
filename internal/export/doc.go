// Package export 是规范导出格式（sesscope/session.v1）的编解码。
//
// 规范导出 = 领域模型的 JSON 快照 + 一层外壳（schema/generated_at）。
// 旧导出（opencode 的 export_info/session/global_stats/messages）仍然可读，
// 由 internal/trace 的 DecodeExport 处理；两者在 render/analyze 里自动判别。
package export

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gzzyj/sesscope/internal/session"
)

// Document 是规范导出文档。
type Document struct {
	Schema      string          `json:"schema"`
	GeneratedAt string          `json:"generated_at"`
	Session     session.Session `json:"session"`
}

// New 用领域模型和生成时间构造文档。
func New(value *session.Session, generatedAt time.Time) *Document {
	doc := &Document{
		Schema:      session.Schema,
		GeneratedAt: generatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
	if value != nil {
		doc.Session = *value
	}
	return doc
}

// Encode 输出缩进 JSON（便于人看也便于 diff），不转义 HTML。
func Encode(doc *Document) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		return nil, fmt.Errorf("序列化规范导出：%w", err)
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{10}), nil
}

// Decode 解析规范导出文档。
func Decode(raw []byte) (*Document, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("内容为空")
	}
	if trimmed[0] != '{' {
		return nil, fmt.Errorf("顶层不是 JSON 对象（首字符 %q）", trimmed[0])
	}
	var doc Document
	if err := json.Unmarshal(trimmed, &doc); err != nil {
		return nil, fmt.Errorf("解析规范导出：%w", err)
	}
	if doc.Schema != session.Schema {
		return nil, fmt.Errorf("不认识的导出 schema %q（期望 %q）", doc.Schema, session.Schema)
	}
	return &doc, nil
}

// Kind 判别一段 JSON 是哪种导出格式：canonical | legacy | unknown。
// 判别只看顶层特征字段，不解析全量内容。
func Kind(raw []byte) string {
	var probe struct {
		Schema     *string         `json:"schema"`
		ExportInfo json.RawMessage `json:"export_info"`
		Messages   json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "unknown"
	}
	if probe.Schema != nil && *probe.Schema != "" {
		return "canonical"
	}
	if len(probe.ExportInfo) > 0 || len(probe.Messages) > 0 {
		return "legacy"
	}
	return "unknown"
}
