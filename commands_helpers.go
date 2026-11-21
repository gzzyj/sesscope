package main

import (
	"os"
)

// statFile 是 os.Stat 的薄封装（processes 命令判断库文件是否存在）。
func statFile(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

// homeDir 返回当前用户 HOME（表格里把路径缩成 ~/…）。
func homeDir() (string, error) {
	return os.UserHomeDir()
}
