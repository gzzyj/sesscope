package trace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// 这个文件实现"和 JSON.stringify 完全一致"的序列化：保留键的原始顺序、
// 字符串转义规则一致、数字按 JS 的规则打印。
// 为什么不用 encoding/json 直接处理工具参数：Go 的 map 会丢键序（按字典序输出），
// 而 opencode 的工具参数/结果在页面上是原样展示的，键序必须保持导出时的样子。

type jsKind int

const (
	jsNull jsKind = iota
	jsBool
	jsNumber
	jsString
	jsObject
	jsArray
)

type jsValue struct {
	kind jsKind
	keys []string
	vals []*jsValue
	str  string
	num  string
	b    bool
}

// parseJSValue 解析任意 JSON 值，保留对象键序。
func parseJSValue(raw []byte) (*jsValue, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, err := readJSValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("JSON 之后还有多余内容")
	}
	return value, nil
}

func readJSValue(dec *json.Decoder) (*jsValue, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch typed := token.(type) {
	case json.Delim:
		switch typed {
		case '{':
			value := &jsValue{kind: jsObject}
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("对象键不是字符串：%v", keyToken)
				}
				child, err := readJSValue(dec)
				if err != nil {
					return nil, err
				}
				value.keys = append(value.keys, key)
				value.vals = append(value.vals, child)
			}
			if _, err := dec.Token(); err != nil { // 吃掉 '}'
				return nil, err
			}
			return value, nil
		case '[':
			value := &jsValue{kind: jsArray}
			for dec.More() {
				child, err := readJSValue(dec)
				if err != nil {
					return nil, err
				}
				value.vals = append(value.vals, child)
			}
			if _, err := dec.Token(); err != nil { // 吃掉 ']'
				return nil, err
			}
			return value, nil
		default:
			return nil, fmt.Errorf("非预期的分隔符 %q", typed)
		}
	case string:
		return &jsValue{kind: jsString, str: typed}, nil
	case json.Number:
		formatted, err := formatJSNumber(typed)
		if err != nil {
			return nil, err
		}
		return &jsValue{kind: jsNumber, num: formatted}, nil
	case bool:
		return &jsValue{kind: jsBool, b: typed}, nil
	case nil:
		return &jsValue{kind: jsNull}, nil
	default:
		return nil, fmt.Errorf("非预期的 JSON 值 %T", token)
	}
}

// formatJSNumber 按 ECMAScript 的数字字符串化规则打印。
func formatJSNumber(number json.Number) (string, error) {
	float, err := number.Float64()
	if err != nil {
		return "", fmt.Errorf("数字无法解析：%q", number.String())
	}
	return formatJSFloat(float), nil
}

func formatJSFloat(float float64) string {
	if float == 0 {
		return "0" // JSON.stringify(-0) === "0"
	}
	abs := float
	if abs < 0 {
		abs = -abs
	}
	if abs >= 1e-6 && abs < 1e21 {
		// 这一区间 JS 用普通小数记法，不存在指数。
		text := strconv.FormatFloat(float, 'f', -1, 64)
		return text
	}
	text := strconv.FormatFloat(float, 'e', -1, 64)
	// Go: 1e+21 / 1e-07 ; JS: 1e+21 / 1e-7
	mantissa, exponent, found := strings.Cut(text, "e")
	if !found {
		return text
	}
	sign := ""
	if strings.HasPrefix(exponent, "-") {
		sign = "-"
		exponent = exponent[1:]
	} else {
		sign = "+"
		exponent = strings.TrimPrefix(exponent, "+")
	}
	exponent = strings.TrimLeft(exponent, "0")
	if exponent == "" {
		exponent = "0"
	}
	return mantissa + "e" + sign + exponent
}

// jsQuote 按 JSON.stringify 的规则给字符串加引号。
func jsQuote(text string) string {
	var out strings.Builder
	out.Grow(len(text) + 2)
	out.WriteByte('"')
	for _, r := range text {
		switch r {
		case '"':
			out.WriteString("\\\"")
		case '\\':
			out.WriteString("\\\\")
		case '\b':
			out.WriteString("\\b")
		case '\f':
			out.WriteString("\\f")
		case '\n':
			out.WriteString("\\n")
		case '\r':
			out.WriteString("\\r")
		case '\t':
			out.WriteString("\\t")
		default:
			if r < 0x20 {
				fmt.Fprintf(&out, "\\u%04x", r)
				continue
			}
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}

// jsStringify 等价于 JSON.stringify(value, null, indent)；indent 为空时输出紧凑格式。
func (v *jsValue) jsStringify(indent string) string {
	var out strings.Builder
	v.write(&out, indent, "")
	return out.String()
}

func (v *jsValue) write(out *strings.Builder, indent, prefix string) {
	switch v.kind {
	case jsNull:
		out.WriteString("null")
	case jsBool:
		if v.b {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case jsNumber:
		out.WriteString(v.num)
	case jsString:
		out.WriteString(jsQuote(v.str))
	case jsArray:
		if len(v.vals) == 0 {
			out.WriteString("[]")
			return
		}
		if indent == "" {
			out.WriteString("[")
			for i, child := range v.vals {
				if i > 0 {
					out.WriteString(",")
				}
				child.write(out, indent, prefix)
			}
			out.WriteString("]")
			return
		}
		inner := prefix + indent
		out.WriteString("[\n")
		for i, child := range v.vals {
			if i > 0 {
				out.WriteString(",\n")
			}
			out.WriteString(inner)
			child.write(out, indent, inner)
		}
		out.WriteString("\n" + prefix + "]")
	case jsObject:
		if len(v.keys) == 0 {
			out.WriteString("{}")
			return
		}
		if indent == "" {
			out.WriteString("{")
			for i, key := range v.keys {
				if i > 0 {
					out.WriteString(",")
				}
				out.WriteString(jsQuote(key))
				out.WriteString(":")
				v.vals[i].write(out, indent, prefix)
			}
			out.WriteString("}")
			return
		}
		inner := prefix + indent
		out.WriteString("{\n")
		for i, key := range v.keys {
			if i > 0 {
				out.WriteString(",\n")
			}
			out.WriteString(inner)
			out.WriteString(jsQuote(key))
			out.WriteString(": ")
			v.vals[i].write(out, indent, inner)
		}
		out.WriteString("\n" + prefix + "}")
	}
}
