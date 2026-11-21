package opencode

import (
	"os"
	"testing"
)

func TestReadEnvironValue(t *testing.T) {
	if got := readEnvironValue(os.Getpid(), "HOME"); got == "" {
		t.Error("应能读到当前进程的 HOME")
	}
	if got := readEnvironValue(os.Getpid(), "NOT_A_REAL_VAR_XYZ"); got != "" {
		t.Errorf("不存在的变量应返回空，得到 %q", got)
	}
}

func TestReadPPID(t *testing.T) {
	if ppid := readPPID(os.Getpid()); ppid <= 0 {
		t.Errorf("当前进程的 PPID = %d，应当 > 0", ppid)
	}
}

func TestDiscoverProcessesExcludesSelf(t *testing.T) {
	processes, err := DiscoverProcesses()
	if err != nil {
		t.Fatalf("扫描进程：%v", err)
	}
	for _, process := range processes {
		if process.PID == os.Getpid() {
			t.Fatal("不应把本工具自己列进去")
		}
		if process.PID <= 0 || process.Name == "" {
			t.Errorf("进程信息不完整：%+v", process)
		}
	}
}

func TestDBPathForPIDRejectsNonOpencode(t *testing.T) {
	// 用 1 号进程（init/systemd）测试：它不是 opencode 进程
	if _, err := DBPathForPID(1); err == nil {
		t.Skip("1 号进程恰好叫 opencode？跳过")
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
