// Package table 渲染等宽列的对齐表格（中英文混排按显示宽度对齐）。
package table

import (
	"strings"
)

// Align 是列对齐方式。
type Align int

const (
	// Left 左对齐。
	Left Align = iota
	// Right 右对齐（数字列用）。
	Right
)

// Column 描述一列的标题与对齐方式。
type Column struct {
	Header string
	Align  Align
}

// Render 生成表格：列间两个空格，行尾不补空格。
func Render(columns []Column, rows [][]string) string {
	widths := make([]int, len(columns))
	for index, column := range columns {
		widths[index] = DisplayWidth(column.Header)
	}
	for _, row := range rows {
		for index := range columns {
			if index >= len(row) {
				continue
			}
			if width := DisplayWidth(row[index]); width > widths[index] {
				widths[index] = width
			}
		}
	}

	var out strings.Builder
	writeRow(&out, columns, widths, headerCells(columns))
	separator := make([]string, len(columns))
	for index, width := range widths {
		separator[index] = strings.Repeat("-", width)
	}
	writeRow(&out, columns, widths, separator)
	for _, row := range rows {
		writeRow(&out, columns, widths, row)
	}
	return out.String()
}

func headerCells(columns []Column) []string {
	cells := make([]string, len(columns))
	for index, column := range columns {
		cells[index] = column.Header
	}
	return cells
}

func writeRow(out *strings.Builder, columns []Column, widths []int, cells []string) {
	line := make([]string, 0, len(columns))
	for index := range columns {
		cell := ""
		if index < len(cells) {
			cell = cells[index]
		}
		padding := widths[index] - DisplayWidth(cell)
		if padding < 0 {
			padding = 0
		}
		if columns[index].Align == Right {
			cell = strings.Repeat(" ", padding) + cell
		} else {
			cell = cell + strings.Repeat(" ", padding)
		}
		line = append(line, cell)
	}
	out.WriteString(strings.TrimRight(strings.Join(line, "  "), " "))
	out.WriteString("\n")
}

// DisplayWidth 计算终端显示宽度：CJK/全角/emoji 记 2 列。
func DisplayWidth(text string) int {
	width := 0
	for _, r := range text {
		width += runeWidth(r)
	}
	return width
}

func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 32 || (r >= 0x7f && r < 0xa0):
		return 0
	case isWide(r):
		return 2
	default:
		return 1
	}
}

func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115f: // 谚文字母
		return true
	case r >= 0x2e80 && r <= 0xa4cf && r != 0x303f: // CJK 部首..彝文
		return true
	case r >= 0xac00 && r <= 0xd7a3: // 谚文音节
		return true
	case r >= 0xf900 && r <= 0xfaff: // CJK 兼容
		return true
	case r >= 0xfe10 && r <= 0xfe19: // 竖排标点
		return true
	case r >= 0xfe30 && r <= 0xfe6f: // CJK 兼容形式
		return true
	case r >= 0xff00 && r <= 0xff60: // 全角 ASCII
		return true
	case r >= 0xffe0 && r <= 0xffe6: // 全角符号
		return true
	case r >= 0x1f300 && r <= 0x1f64f: // emoji
		return true
	case r >= 0x1f900 && r <= 0x1f9ff:
		return true
	case r >= 0x20000 && r <= 0x3fffd: // CJK 扩展
		return true
	}
	return false
}
