// Package jsonl 读"一行一个 JSON"的会话文件，支持 zstd 压缩与 torn tail。
//
// 三类来源都长这样：dsh 的 session.v*.jsonl.zstd、codex 的 rollout-*.jsonl.zst、
// claude 的 <uuid>.jsonl。差别只在记录形状，不在物理格式。
package jsonl

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// IncompleteError 表示文件读到一半失败了（最常见：会话还在写，尾部被截断）。
// 调用方应当保留已解析的内容，并把 Complete 标成 false + 记一条诊断，而不是整份丢弃。
type IncompleteError struct {
	Path  string
	Lines int
	Err   error
}

func (e *IncompleteError) Error() string {
	return fmt.Sprintf("%s：读到第 %d 行之后不完整（%v）", e.Path, e.Lines, e.Err)
}

func (e *IncompleteError) Unwrap() error { return e.Err }

// IsCompressed 判断是不是 zstd 文件。
func IsCompressed(path string) bool {
	lowered := strings.ToLower(path)
	return strings.HasSuffix(lowered, ".zst") || strings.HasSuffix(lowered, ".zstd")
}

// Open 打开一个 JSONL 文件（按扩展名决定是否解压）。
func Open(path string) (io.ReadCloser, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if !IsCompressed(path) {
		return file, nil
	}
	decoder, err := zstd.NewReader(file)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("初始化 zstd 解码 %s：%w", path, err)
	}
	return &decodedFile{file: file, decoder: decoder}, nil
}

type decodedFile struct {
	file    *os.File
	decoder *zstd.Decoder
}

func (d *decodedFile) Read(buffer []byte) (int, error) { return d.decoder.Read(buffer) }

func (d *decodedFile) Close() error {
	d.decoder.Close()
	return d.file.Close()
}

// Each 逐行回调。单行上限 64 MB（工具输出可能很大，但不该无上限）。
// 读到损坏/截断时返回 *IncompleteError，调用方可据此降级而不是报错退出。
func Each(ctx context.Context, path string, fn func(number int, line []byte) error) error {
	reader, err := Open(path)
	if err != nil {
		return err
	}
	defer reader.Close()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	number := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		number++
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		// Scanner 会复用底层数组：回调里要长期持有就先拷贝。
		if err := fn(number, line); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return &IncompleteError{Path: path, Lines: number, Err: err}
	}
	return nil
}
