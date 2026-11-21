package opencode

import (
	"database/sql"
	"fmt"
)

// writeNewSchemaDB 造一个"新版 opencode"结构的库（只有 session_message 等新表）。
func writeNewSchemaDB(path string) error {
	conn, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return fmt.Errorf("建库：%w", err)
	}
	defer conn.Close()
	statements := []string{
		"create table project (id text primary key, worktree text not null)",
		"create table session_message (id text primary key, session_id text not null, seq integer not null, data text not null)",
		"create table session_inbox (id text primary key, session_id text not null)",
	}
	for _, statement := range statements {
		if _, err := conn.Exec(statement); err != nil {
			return fmt.Errorf("建表：%w", err)
		}
	}
	return nil
}
