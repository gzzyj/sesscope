package trace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// 页面骨架里的占位符（与 assets/shell.html 对应）。
const (
	cssPlaceholder  = "/*__TRACE_CSS__*/"
	dataPlaceholder = "__TRACE_DATA__"
	jsPlaceholder   = "/*__TRACE_JS__*/"
)

// RenderHTML 把归一化数据渲染成自包含 HTML。
func RenderHTML(data *Data) ([]byte, error) {
	shell, err := asset("shell.html")
	if err != nil {
		return nil, err
	}
	css, err := asset("app.css")
	if err != nil {
		return nil, err
	}
	js, err := asset("app.js")
	if err != nil {
		return nil, err
	}
	if bytes.Contains(js, []byte("</script")) {
		return nil, fmt.Errorf("app.js 不能包含 </script")
	}
	payload, err := encodeData(data, false)
	if err != nil {
		return nil, err
	}
	// 和 Node 版一致：把 < 转义成 \u003c，防止数据里的 </script> 提前闭合脚本块；
	// U+2028/U+2029 也一并转义（这两个字符在旧 JS 里算换行）。
	// 注意必须写 "\\u003c"（反斜杠 + u003c 共 6 个字符）；写成 "\u003c" 就只是字符 '<'，等于没转义。
	payload = bytes.ReplaceAll(payload, []byte("<"), []byte("\\u003c"))
	payload = bytes.ReplaceAll(payload, []byte("\u2028"), []byte("\\u2028"))
	payload = bytes.ReplaceAll(payload, []byte("\u2029"), []byte("\\u2029"))

	// 骨架必须各有一个占位符；数据区要在"插入之后不再被扫描"的前提下拼装，
	// 否则会话内容里出现占位符原文（比如本工具的源码被当成会话内容）会被误判成重复占位符。
	for _, placeholder := range []string{cssPlaceholder, dataPlaceholder, jsPlaceholder} {
		if bytes.Count(shell, []byte(placeholder)) != 1 {
			return nil, fmt.Errorf("页面骨架里占位符 %s 必须恰好出现一次", placeholder)
		}
	}
	out := bytes.Replace(shell, []byte(cssPlaceholder), css, 1)
	before, after, found := bytes.Cut(out, []byte(dataPlaceholder))
	if !found {
		return nil, fmt.Errorf("页面骨架里缺少数据占位符 %s", dataPlaceholder)
	}
	after = bytes.Replace(after, []byte(jsPlaceholder), js, 1)
	for _, placeholder := range []string{cssPlaceholder, jsPlaceholder} {
		if bytes.Contains(before, []byte(placeholder)) || bytes.Contains(after, []byte(placeholder)) {
			return nil, fmt.Errorf("页面骨架里有重复的占位符 %s", placeholder)
		}
	}
	combined := make([]byte, 0, len(before)+len(payload)+len(after))
	combined = append(combined, before...)
	combined = append(combined, payload...)
	combined = append(combined, after...)
	return combined, nil
}

// EncodeDataJSON 输出归一化数据本身（-data 用）。
func EncodeDataJSON(data *Data) ([]byte, error) {
	return encodeData(data, true)
}

func encodeData(data *Data, indent bool) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if indent {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(data); err != nil {
		return nil, fmt.Errorf("序列化数据：%w", err)
	}
	// Encode 会补一个换行，Node 版没有，去掉以保持逐字节一致。
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

// Summary 生成 CLI 摘要（写到 stderr，stdout 留给 HTML）。
// htmlBytes < 0 表示 HTML 写到了 stdout，不做文件大小汇报。
func Summary(data *Data, exportPath, htmlPath, dataPath string, htmlBytes int) string {
	var buffer bytes.Buffer
	title := ""
	if data.Meta.Title != nil {
		title = *data.Meta.Title
	} else if data.Meta.SessionID != nil {
		title = *data.Meta.SessionID
	}
	provider, model := "?", "?"
	if data.Meta.Model != nil {
		if data.Meta.Model.Provider != nil {
			provider = *data.Meta.Model.Provider
		}
		if data.Meta.Model.ID != nil {
			model = *data.Meta.Model.ID
		}
	}
	stats := data.Stats
	writef := func(format string, args ...any) { fmt.Fprintf(&buffer, format, args...) }

	writef("导出：%s\n", exportPath)
	writef("会话：%s · %s/%s\n", title, provider, model)
	writef("轮次 %d（用户输入 %d）· 步骤 %d · 工具调用 %d\n", stats.Turns, stats.UserTurns, stats.Steps, stats.ToolCalls)
	writef("报错步骤 %d · 未完成步骤 %d · 未结束工具 %d\n", stats.Errors, stats.Running, stats.ToolRunning)
	writef("模型 %s（思考 %s / 生成 %s）\n", fmtMs(&stats.ModelMs), fmtMs(&stats.ReasoningMs), fmtMs(&stats.TextMs))
	writef("工具 %s · 等待人工 %s · 压缩 %s\n", fmtMs(&stats.ToolMs), fmtMs(&stats.WaitHumanMs), fmtMs(&stats.CompactionMs))
	writef("活跃 %s · 空闲 %s · 跨时 %s\n", fmtMs(&stats.BusyMs), fmtMs(&stats.IdleMs), fmtMs(data.Meta.WallMs))
	writef("tokens 输入 %d · 输出 %d · 推理 %d · 缓存读 %d\n",
		stats.Tokens.Input, stats.Tokens.Output, stats.Tokens.Reasoning, stats.Tokens.CacheRead)
	writef("数据提示 %d 条\n", len(data.Warnings))
	if htmlBytes >= 0 {
		writef("\n输出：%s（%s KB）\n", htmlPath, strconv.Itoa((htmlBytes+512)/1024))
	} else {
		writef("\n输出：HTML 已写到 stdout\n")
	}
	if dataPath != "" {
		writef("      %s\n", dataPath)
	}
	return buffer.String()
}
