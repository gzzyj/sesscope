//go:build windows

package platform

import "syscall"

// setConsoleUTF8 把控制台输出代码页设为 UTF-8。
// 不设的话，中文在默认 GBK 代码页的 cmd/PowerShell 里会显示成乱码，
// 虽然重定向到文件时字节仍是 UTF-8。
func setConsoleUTF8() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	outputCP := kernel32.NewProc("SetConsoleOutputCP")
	_, _, _ = outputCP.Call(65001)
}
