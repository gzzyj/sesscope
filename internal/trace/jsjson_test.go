package trace

import (
	"encoding/json"
	"testing"
)

func TestFormatJSFloat(t *testing.T) {
	cases := []struct {
		value float64
		want  string
	}{
		{0, "0"},
		{-0.0, "0"}, // JSON.stringify(-0) === "0"
		{1, "1"},
		{1.5, "1.5"},
		{-2.25, "-2.25"},
		{1e21, "1e+21"},
		{1e-7, "1e-7"},
		{1234567890123, "1234567890123"},
	}
	for _, testCase := range cases {
		if got := formatJSFloat(testCase.value); got != testCase.want {
			t.Errorf("formatJSFloat(%v) = %q，期望 %q", testCase.value, got, testCase.want)
		}
	}
}

func TestJSQuote(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"abc", "\"abc\""},
		{"a\"b", "\"a\\\"b\""},
		{"a\\b", "\"a\\\\b\""},
		{"line\nbreak", "\"line\\nbreak\""},
		{"tab\there", "\"tab\\there\""},
		{"ctl\x01", "\"ctl\\u0001\""},
		{"中文 · 保留", "\"中文 · 保留\""},
		// JSON.stringify 不转义 </script>；页面的防护在 RenderHTML 里做
		{"</script>", "\"</script>\""},
	}
	for _, testCase := range cases {
		got := jsQuote(testCase.input)
		if got != testCase.want {
			t.Errorf("jsQuote(%q) = %s，期望 %s", testCase.input, got, testCase.want)
		}
		var roundTrip string
		if err := json.Unmarshal([]byte(got), &roundTrip); err != nil {
			t.Fatalf("jsQuote(%q) 产出的 %s 不是合法 JSON 字符串：%v", testCase.input, got, err)
		}
		if roundTrip != testCase.input {
			t.Errorf("jsQuote(%q) 往返后变成 %q", testCase.input, roundTrip)
		}
	}
}

func TestStringifyJSON(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"缺失", "", ""},
		{"null", "null", ""},
		{"字符串原样", "\"hello\\nworld\"", "hello\nworld"},
		{"空对象", "{}", "{}"},
		{"空数组", "[]", "[]"},
		{
			// 键序必须保持输入里的顺序（Go 的 map 会按字典序排，是移植时最容易错的地方）
			name:  "保留键序",
			input: "{\"zebra\":1,\"alpha\":2,\"mike\":3}",
			want:  "{\n  \"zebra\": 1,\n  \"alpha\": 2,\n  \"mike\": 3\n}",
		},
		{
			name:  "嵌套与数组",
			input: "{\"cmd\":\"echo\",\"args\":[1,2],\"deep\":{\"b\":true,\"a\":null}}",
			want:  "{\n  \"cmd\": \"echo\",\n  \"args\": [\n    1,\n    2\n  ],\n  \"deep\": {\n    \"b\": true,\n    \"a\": null\n  }\n}",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := stringifyJSON(json.RawMessage(testCase.input)); got != testCase.want {
				t.Errorf("stringifyJSON(%s) =\n%s\n期望\n%s", testCase.input, got, testCase.want)
			}
		})
	}
}

func TestJSStringifyCompact(t *testing.T) {
	value, err := parseJSValue([]byte("{\"a\":1,\"b\":[true,null],\"c\":{}}"))
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	want := "{\"a\":1,\"b\":[true,null],\"c\":{}}"
	if got := value.jsStringify(""); got != want {
		t.Errorf("紧凑输出 = %s，期望 %s", got, want)
	}
}
