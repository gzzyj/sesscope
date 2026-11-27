package opencode

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"

	_ "modernc.org/sqlite" // 纯 Go 的 SQLite 驱动，不需要 cgo
)

// DB 是只读打开的 opencode 会话库。
type DB struct {
	conn *sql.DB
	Path string
}

// OpenReadOnly 以只读方式打开任意 SQLite 文件，不做 opencode schema 校验。
// 供"探测版本/体检"使用：新版 schema 也要能打开，才能如实报告"不支持"而不是"打不开"。
func OpenReadOnly(path string) (*sql.DB, error) {
	dsn := sqliteDSN(path)
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

// sqliteDSN 生成只读 DSN。
//
// Windows 盘符（C:\\...）直接塞进 "file:" 会被当成 URL scheme，所以统一走
// file:///C:/... 这种 URI 形式；POSIX 也会变成 file:///home/...，由 modernc 的
// sqlite 驱动统一解析。
func sqliteDSN(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	slash := filepath.ToSlash(absolute)
	if runtime.GOOS == "windows" && !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	uri := url.URL{Scheme: "file", Path: slash}
	return uri.String() + "?mode=ro&_pragma=busy_timeout(5000)"
}

// ErrSessionNotFound 表示按 id 没找到会话。
var ErrSessionNotFound = errors.New("会话不存在")
