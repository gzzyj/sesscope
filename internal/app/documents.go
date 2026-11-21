package app

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/zhouyuejie/opencode-trace/internal/attribution"
	"github.com/zhouyuejie/opencode-trace/internal/export"
	"github.com/zhouyuejie/opencode-trace/internal/session"
	"github.com/zhouyuejie/opencode-trace/internal/source"
	"github.com/zhouyuejie/opencode-trace/internal/trace"
)

// Canonical 输出规范导出 JSON（atrace/session.v1）。
func (e *Env) Canonical(value *session.Session, generatedAt time.Time) ([]byte, error) {
	return export.Encode(export.New(value, generatedAt))
}

// Legacy 输出旧导出 JSON（仅 opencode 支持）。
// 其它来源显式报"没有旧格式"，绝不悄悄换一种输出糊弄脚本。
func (e *Env) Legacy(ctx context.Context, value *session.Session, store source.Store, generatedAt time.Time) ([]byte, error) {
	item, ok := e.Registry.Get(value.Ref.Source)
	if !ok {
		return nil, &source.UnsupportedError{Source: value.Ref.Source, Reason: "来源未注册"}
	}
	exporter, ok := item.(source.LegacyExporter)
	if !ok {
		return nil, &source.UnsupportedError{Source: value.Ref.Source, Store: store.Path, Reason: "--format legacy 只支持 opencode；其它来源请用默认的规范格式"}
	}
	return exporter.LegacyDocument(ctx, store, value.Ref.ID, generatedAt)
}

// LoadSession 解析一段 JSON（规范导出或旧导出）为领域模型。
func (e *Env) LoadSession(raw []byte) (*session.Session, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("内容为空")
	}
	if trimmed[0] != '{' {
		return nil, fmt.Errorf("顶层不是 JSON 对象（首字符 %q）", trimmed[0])
	}
	switch export.Kind(raw) {
	case "canonical":
		document, err := export.Decode(raw)
		if err != nil {
			return nil, err
		}
		return &document.Session, nil
	case "legacy":
		var legacy trace.Export
		if err := trace.DecodeExport(raw, &legacy); err != nil {
			return nil, err
		}
		return trace.ExportToSession(&legacy), nil
	default:
		return nil, fmt.Errorf("无法识别导出格式（既没有 schema 字段，也没有 export_info/messages）")
	}
}

// View 把领域模型归一化成页面数据，并附上确定性归因。
func (e *Env) View(value *session.Session, exportPath string, generatedAt time.Time, chainGap int) (*trace.Data, error) {
	data, err := trace.Normalize(value, trace.Options{ExportPath: exportPath, GeneratedAt: generatedAt})
	if err != nil {
		return nil, err
	}
	data.Episodes = attribution.Analyze(data)
	data.Chains = attribution.BuildChains(data, data.Episodes, chainGap)
	return data, nil
}

// RenderHTML 渲染自包含页面。
func (e *Env) RenderHTML(data *trace.Data) ([]byte, error) {
	return trace.RenderHTML(data)
}

// DataJSON 输出页面用的归一化数据。
func (e *Env) DataJSON(data *trace.Data) ([]byte, error) {
	return trace.EncodeDataJSON(data)
}
