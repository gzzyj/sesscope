package trace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// DecodeExport 解析导出 JSON；错误信息里带上失败位置，避免只报一句 "invalid character"。
func DecodeExport(raw []byte, doc *Export) error {
	if doc == nil {
		return errors.New("目标为空")
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return errors.New("内容为空")
	}
	if trimmed[0] != '{' {
		return fmt.Errorf("顶层不是 JSON 对象（首字符 %q）", trimmed[0])
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if err := decoder.Decode(doc); err != nil {
		var syntax *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		switch {
		case errors.As(err, &syntax):
			return fmt.Errorf("JSON 语法错误（偏移 %d）：%w", syntax.Offset, err)
		case errors.As(err, &typeErr):
			return fmt.Errorf("字段 %s 的类型不对（期望 %s）：%w", typeErr.Field, typeErr.Type, err)
		case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
			return errors.New("JSON 不完整（文件被截断？）")
		default:
			return err
		}
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("JSON 之后还有多余内容")
	}
	return nil
}
