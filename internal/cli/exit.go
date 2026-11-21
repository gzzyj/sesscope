package cli

import (
	"errors"
	"fmt"
)

// 退出码是契约（docs/cli.md）：脚本按它分流。
const (
	ExitOK          = 0 // 成功（含"正常空集"）
	ExitRuntime     = 1 // 运行期失败（写文件、渲染……）
	ExitBadInput    = 2 // 输入问题（参数、歧义、库不存在）
	ExitUnsupported = 3 // 来源或格式不支持
)

// ExitError 让用例层/命令层可以指定退出码，而不必返回裸 int。
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("退出码 %d", e.Code)
	}
	return e.Err.Error()
}

// Unwrap 支持 errors.Is/As 穿透。
func (e *ExitError) Unwrap() error { return e.Err }

// BadInput 标记"用户输入有问题"。
func BadInput(err error) error { return &ExitError{Code: ExitBadInput, Err: err} }

// Unsupported 标记"来源/格式不支持"。
func Unsupported(err error) error { return &ExitError{Code: ExitUnsupported, Err: err} }

// RuntimeFailure 标记"运行期失败"。
func RuntimeFailure(err error) error { return &ExitError{Code: ExitRuntime, Err: err} }

// CodeOf 把错误映射成退出码。
func CodeOf(err error) int {
	if err == nil {
		return ExitOK
	}
	var exit *ExitError
	if errors.As(err, &exit) {
		return exit.Code
	}
	return ExitRuntime
}
