package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func describe(path string) string {
	if path == "-" || path == "" {
		return "<stdin>"
	}
	return path
}

// readInput 读入 JSON：- 表示 stdin。
func readInput(path string, stdin io.Reader) ([]byte, error) {
	if path == "-" || path == "" {
		raw, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("读 stdin：%w", err)
		}
		if len(raw) == 0 {
			return nil, errors.New("stdin 是空的，没有任何 JSON")
		}
		return raw, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读输入：%w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("输入文件是空的：%s", path)
	}
	return raw, nil
}

// writeOutput 写文件（自动建目录、先写临时文件再改名，避免留下半截文件）；- 表示写 stdout。
func writeOutput(path string, content []byte, stdout io.Writer) error {
	if path == "-" {
		if _, err := stdout.Write(content); err != nil {
			return fmt.Errorf("写 stdout：%w", err)
		}
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("建目录 %s：%w", dir, err)
	}
	temp, err := os.CreateTemp(dir, ".opencode-trace-*")
	if err != nil {
		return fmt.Errorf("在 %s 建临时文件：%w", dir, err)
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return fmt.Errorf("写 %s：%w", tempName, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("关闭 %s：%w", tempName, err)
	}
	if err := os.Chmod(tempName, 0o644); err != nil {
		return fmt.Errorf("设置权限 %s：%w", tempName, err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("改名到 %s：%w", path, err)
	}
	return nil
}
