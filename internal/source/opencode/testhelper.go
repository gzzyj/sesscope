package opencode

import "os"

// osWriteFile 是 os.WriteFile 的薄封装（测试里造空库用）。
func osWriteFile(path string, content []byte, perm os.FileMode) error {
	return os.WriteFile(path, content, perm)
}
