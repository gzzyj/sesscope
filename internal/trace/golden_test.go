package trace

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// -update 会重写 testdata/parity 里的期望文件。
var updateGolden = flag.Bool("update", false, "重写黄金用例期望文件")

const (
	goldenExportPath = "testdata/parity/session-export.json"
	goldenDataPath   = "testdata/parity/expected-trace-data.json"
	goldenHashPath   = "testdata/parity/expected-html.sha256"
)

// goldenOptions 固定 generatedAt，保证每次渲染结果可复现。
func goldenOptions() Options {
	return Options{
		ExportPath:  goldenExportPath,
		GeneratedAt: time.Date(2026, 11, 20, 10, 22, 4, 130000000, time.UTC),
	}
}

// TestGoldenParity 用真实会话导出跑一遍，和期望的数据/HTML 逐字节比对。
// 这条用例同时守住：字段顺序、缩进、数字格式、转义规则、页面骨架。
func TestGoldenParity(t *testing.T) {
	raw, err := os.ReadFile(goldenExportPath)
	if err != nil {
		t.Skipf("没有黄金样例（先跑 make golden）：%v", err)
	}
	var doc Export
	if err := DecodeExport(raw, &doc); err != nil {
		t.Fatalf("解析黄金样例：%v", err)
	}
	data, err := Normalize(&doc, goldenOptions())
	if err != nil {
		t.Fatalf("归一化：%v", err)
	}
	encoded, err := EncodeDataJSON(data)
	if err != nil {
		t.Fatalf("序列化：%v", err)
	}
	html, err := RenderHTML(data)
	if err != nil {
		t.Fatalf("渲染：%v", err)
	}
	sum := sha256.Sum256(html)
	htmlHash := hex.EncodeToString(sum[:]) + "\n"

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenDataPath), 0o755); err != nil {
			t.Fatalf("建目录：%v", err)
		}
		if err := os.WriteFile(goldenDataPath, encoded, 0o644); err != nil {
			t.Fatalf("写期望数据：%v", err)
		}
		if err := os.WriteFile(goldenHashPath, []byte(htmlHash), 0o644); err != nil {
			t.Fatalf("写期望指纹：%v", err)
		}
		t.Log("已更新黄金用例期望文件")
		return
	}

	expectedData, err := os.ReadFile(goldenDataPath)
	if err != nil {
		t.Fatalf("读期望数据：%v", err)
	}
	if string(expectedData) != string(encoded) {
		t.Errorf("数据和期望不一致（长度 %d vs %d）", len(encoded), len(expectedData))
		if diff := firstDifference(string(expectedData), string(encoded)); diff != "" {
			t.Errorf("首个差异：%s", diff)
		}
	}
	expectedHash, err := os.ReadFile(goldenHashPath)
	if err != nil {
		t.Fatalf("读期望指纹：%v", err)
	}
	if string(expectedHash) != htmlHash {
		t.Errorf("HTML 指纹变了：期望 %s，实际 %s", string(expectedHash), htmlHash)
	}
}

func firstDifference(expected, actual string) string {
	limit := len(expected)
	if len(actual) < limit {
		limit = len(actual)
	}
	for index := 0; index < limit; index++ {
		if expected[index] != actual[index] {
			from := index - 60
			if from < 0 {
				from = 0
			}
			to := index + 60
			if to > limit {
				to = limit
			}
			return "偏移 " + itoa(index) + "：期望 ..." + expected[from:to] + "... 实际 ..." + actual[from:to] + "..."
		}
	}
	if len(expected) != len(actual) {
		return "长度不同（" + itoa(len(expected)) + " vs " + itoa(len(actual)) + "）"
	}
	return ""
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 12)
	negative := value < 0
	if negative {
		value = -value
	}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}

// TestGoldenStats 抽查黄金样例的统计数字，防止"跑得通但数字错了"。
func TestGoldenStats(t *testing.T) {
	raw, err := os.ReadFile(goldenExportPath)
	if err != nil {
		t.Skipf("没有黄金样例：%v", err)
	}
	var doc Export
	if err := DecodeExport(raw, &doc); err != nil {
		t.Fatalf("解析黄金样例：%v", err)
	}
	data, err := Normalize(&doc, goldenOptions())
	if err != nil {
		t.Fatalf("归一化：%v", err)
	}
	expected := map[string]int64{
		"turns":        6,
		"steps":        66,
		"toolCalls":    65,
		"errors":       2,
		"running":      1,
		"modelMs":      778791,
		"reasoningMs":  562872,
		"toolMs":       8317,
		"waitHumanMs":  990725,
		"compactionMs": 81714,
	}
	actual := map[string]int64{
		"turns":        int64(data.Stats.Turns),
		"steps":        int64(data.Stats.Steps),
		"toolCalls":    int64(data.Stats.ToolCalls),
		"errors":       int64(data.Stats.Errors),
		"running":      int64(data.Stats.Running),
		"modelMs":      data.Stats.ModelMs,
		"reasoningMs":  data.Stats.ReasoningMs,
		"toolMs":       data.Stats.ToolMs,
		"waitHumanMs":  data.Stats.WaitHumanMs,
		"compactionMs": data.Stats.CompactionMs,
	}
	for key, want := range expected {
		if actual[key] != want {
			t.Errorf("%s = %d，期望 %d", key, actual[key], want)
		}
	}
	if data.Stats.Tokens.Input != 439969 || data.Stats.Tokens.Output != 14197 || data.Stats.Tokens.CacheRead != 3150976 {
		t.Errorf("tokens = %+v", data.Stats.Tokens)
	}
	if len(data.Warnings) != 10 {
		t.Errorf("数据提示 %d 条，期望 10 条", len(data.Warnings))
	}
}
