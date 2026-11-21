package opencode

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // 纯 Go 的 SQLite 驱动，不需要 cgo
)

// DB 是只读打开的 opencode 会话库。
type DB struct {
	conn *sql.DB
	Path string
}

// CandidatePaths 返回自动发现时依次尝试的库路径（按优先级）。
// 可通过环境变量 OPENCODE_TRACE_DB 覆盖。
func CandidatePaths() []string {
	home, _ := os.UserHomeDir()
	candidates := make([]string, 0, 4)
	if fromEnv := strings.TrimSpace(os.Getenv("OPENCODE_TRACE_DB")); fromEnv != "" {
		candidates = append(candidates, fromEnv)
	}
	if home != "" {
		candidates = append(candidates,
			// DeepWorks 内置 sidecar 真实使用的库
			filepath.Join(home, ".config", "com.deepexi.deepworks", "deepworks-engine-data", "xdg", "data", "opencode", "opencode.db"),
			// 独立安装的 opencode
			filepath.Join(home, ".local", "share", "opencode", "opencode.db"),
		)
	}
	return candidates
}

// Discover 找到可用的库：显式指定 > 环境变量 > 已知路径。
func Discover(explicit string) (string, error) {
	if explicit != "" {
		info, err := os.Stat(explicit)
		if err != nil {
			return "", fmt.Errorf("指定的库不存在：%s（%w）", explicit, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("指定的路径是目录，不是 SQLite 库：%s", explicit)
		}
		return explicit, nil
	}
	tried := make([]string, 0, 4)
	for _, candidate := range CandidatePaths() {
		tried = append(tried, candidate)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("找不到 opencode 会话库，试过：\n  %s\n用 -db 指定，或设置环境变量 OPENCODE_TRACE_DB", strings.Join(tried, "\n  "))
}

// OpenReadOnly 以只读方式打开任意 SQLite 文件，不做 opencode schema 校验。
// 供"探测版本/体检"使用：新版 schema 也要能打开，才能如实报告"不支持"而不是"打不开"。
func OpenReadOnly(path string) (*sql.DB, error) {
	dsn := "file:" + url.PathEscape(path) + "?mode=ro&_pragma=busy_timeout(5000)"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开库 %s：%w", path, err)
	}
	// 只读库不需要连接池；限制为 1 让行为可预期（含 WAL 读取）。
	conn.SetMaxOpenConns(1)
	return conn, nil
}

// Open 以只读方式打开库（不会改动机器上的数据）。
func Open(path string) (*DB, error) {
	conn, err := OpenReadOnly(path)
	if err != nil {
		return nil, err
	}
	database := &DB{conn: conn, Path: path}
	if err := database.checkSchema(); err != nil {
		conn.Close()
		return nil, err
	}
	return database, nil
}

// Close 关闭库。
func (db *DB) Close() error { return db.conn.Close() }

// checkSchema 确认这是 opencode 的会话库，而不是随便一个 SQLite 文件。
func (db *DB) checkSchema() error {
	required := []string{"session", "message", "part"}
	rows, err := db.conn.Query("select name from sqlite_master where type='table'")
	if err != nil {
		return fmt.Errorf("读取库结构 %s：%w", db.Path, err)
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("读取表名：%w", err)
		}
		have[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("读取库结构：%w", err)
	}
	missing := make([]string, 0, len(required))
	for _, name := range required {
		if !have[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		if have["session_message"] || have["session_inbox"] {
			return fmt.Errorf("%s 是新版 opencode 库（用 session_message 表存消息），本工具目前只支持旧版 session/message/part 结构", db.Path)
		}
		return fmt.Errorf("%s 不是 opencode 会话库（缺少表 %s）", db.Path, strings.Join(missing, ", "))
	}
	if !have["project"] {
		return fmt.Errorf("%s 是旧版 opencode 库（没有 project 表，无法列工作区）；用 -db 指定新版库", db.Path)
	}
	return nil
}

// ErrSessionNotFound 表示按 id 没找到会话。
var ErrSessionNotFound = errors.New("会话不存在")
