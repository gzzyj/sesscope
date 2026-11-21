package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gzzyj/sesscope/internal/table"
)

// WriteJSON 输出带 schema 的 JSON：{"schema": "...", "result": ...}。
// 字段一旦发布只增不改名；脚本据此判断版本。
func WriteJSON(out io.Writer, schema string, payload any) error {
	envelope := map[string]any{"schema": schema, "result": payload}
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(envelope)
}

// RenderTable 用 CJK 宽度对齐的表格输出。
func RenderTable(out io.Writer, columns []table.Column, rows [][]string) {
	fmt.Fprint(out, table.Render(columns, rows))
}

// FormatTimeLocal 把毫秒时间戳格式化成本地时间。
func FormatTimeLocal(ms int64) string {
	return time.UnixMilli(ms).Local().Format("2006-01-02 15:04")
}

// FormatTimePtr 同 FormatTimeLocal，nil 显示 "-"。
func FormatTimePtr(ms *int64) string {
	if ms == nil || *ms <= 0 {
		return "-"
	}
	return FormatTimeLocal(*ms)
}

// FormatCount 把大数字缩成 1.2k / 3.4m。
func FormatCount(value int64) string {
	if value < 1000 {
		return strconv.FormatInt(value, 10)
	}
	if value < 1000000 {
		return fmt.Sprintf("%.1fk", float64(value)/1000)
	}
	return fmt.Sprintf("%.1fm", float64(value)/1000000)
}

// FormatCountPtr 同 FormatCount，nil 显示 "-"。
func FormatCountPtr(value *int64) string {
	if value == nil {
		return "-"
	}
	return FormatCount(*value)
}

// FormatBytes 人类可读体积。
func FormatBytes(size int64) string {
	switch {
	case size < 0:
		return "-"
	case size < 1024:
		return strconv.FormatInt(size, 10) + " B"
	case size < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
	}
}

// FormatDuration 人类可读时长。
func FormatDuration(ms int64) string {
	if ms < 0 {
		return "-"
	}
	if ms < 1000 {
		return strconv.FormatInt(ms, 10) + " ms"
	}
	if ms < 60000 {
		return fmt.Sprintf("%.1f s", float64(ms)/1000)
	}
	if ms < 3600000 {
		return fmt.Sprintf("%d m %d s", ms/60000, (ms%60000)/1000)
	}
	hours := float64(ms) / 3600000
	if hours < 48 {
		return fmt.Sprintf("%.1f h", hours)
	}
	return fmt.Sprintf("%.1f d", hours/24)
}

// TruncateDisplay 按显示宽度截断（CJK 记 2 列）。
func TruncateDisplay(text string, width int) string {
	if table.DisplayWidth(text) <= width {
		return text
	}
	runes := []rune(text)
	for len(runes) > 0 && table.DisplayWidth(string(runes)+"…") > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

// ShortenPath 把家目录缩成 ~，供表格展示。
func ShortenPath(path string) string {
	home, err := osUserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

// PortAlive 判断本地端口是否在监听。
func PortAlive(port int) bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 120*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
