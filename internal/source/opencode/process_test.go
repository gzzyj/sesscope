package opencode

import (
	"testing"
)

func TestPortOf(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"opencode", "--port", "4096"}, "4096"},
		{[]string{"opencode", "--port=4097"}, "4097"},
		{[]string{"opencode", "serve", "--port", "4098", "--hostname", "127.0.0.1"}, "4098"},
		{[]string{"opencode"}, ""},
		{[]string{"opencode", "--port"}, ""},
	}
	for _, item := range cases {
		if got := portOf(item.args); got != item.want {
			t.Errorf("portOf(%v) = %q，期望 %q", item.args, got, item.want)
		}
	}
}

func TestIsOpencodeProcessName(t *testing.T) {
	cases := map[string]bool{
		"opencode":         true,
		"OpenCode":         true,
		"opencode-server":  true,
		"opencode-trace":   false,
		"node":             false,
		"deepworks-engine": false,
	}
	for name, want := range cases {
		if got := isOpencodeProcessName(name); got != want {
			t.Errorf("isOpencodeProcessName(%q) = %t，期望 %t", name, got, want)
		}
	}
}

func TestOpenRejectsNewSchema(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/new.db"
	if err := writeNewSchemaDB(path); err != nil {
		t.Fatalf("造新版库：%v", err)
	}
	_, err := Open(path)
	if err == nil {
		t.Fatal("新版 schema 应当报错")
	}
	if !contains(err.Error(), "新版 opencode 库") {
		t.Errorf("错误信息应当说明是新版库，实际：%v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
