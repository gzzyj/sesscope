// Package opencode 只读访问 opencode 的 SQLite 会话库：
// 列出工作区（project / workspace）、列出会话、把单个会话导出成 render 能吃的 JSON。
package opencode

import "encoding/json"

// Project 是 opencode 的"工作区"载体（worktree）。本机 workspace 表可能为空。
type Project struct {
	ID          string
	Worktree    string
	VCS         *string
	Name        *string
	TimeCreated int64
	TimeUpdated int64
	// 统计
	SessionCount   int64
	RootCount      int64
	LastActivityMs *int64
}

// Label 返回展示名：优先 name，其次 worktree 的目录名。
func (p Project) Label() string {
	if p.Name != nil && *p.Name != "" {
		return *p.Name
	}
	if name := baseName(p.Worktree); name != "" {
		return name
	}
	return p.ID
}

// Workspace 是 project 下的分支/目录工作区（DeepWorks 目前不写这张表）。
type Workspace struct {
	ID        string
	Type      string
	Name      string
	Branch    *string
	Directory *string
	ProjectID string
	TimeUsed  int64
}

// SessionRow 是会话列表里的一行。
type SessionRow struct {
	ID               string
	ProjectID        string
	WorkspaceID      *string
	ParentID         *string
	Slug             string
	Directory        string
	Title            string
	Version          string
	Agent            *string
	Model            *string
	Cost             float64
	TokensInput      int64
	TokensOutput     int64
	TokensReason     int64
	TokensCacheRead  int64
	TokensCacheWrite int64
	TimeCreated      int64
	TimeUpdated      int64
	MessageCount     int64
	PartCount        int64
}

// IsChild 表示这是子 agent 会话（有 parent_id）。
func (s SessionRow) IsChild() bool { return s.ParentID != nil && *s.ParentID != "" }

// SessionDetail 是导出用的完整会话（含消息与 part）。
type SessionDetail struct {
	Session  SessionRow
	Messages []MessageRow
	Parts    []PartRow
}

// MessageRow 是一条消息。
type MessageRow struct {
	ID            string
	SessionID     string
	TimeCreatedMs int64
	TimeUpdatedMs int64
	Data          json.RawMessage
}

// PartRow 是一段内容。
type PartRow struct {
	ID            string
	MessageID     string
	SessionID     string
	TimeCreatedMs int64
	TimeUpdatedMs int64
	Data          json.RawMessage
}

func baseName(path string) string {
	trimmed := path
	for len(trimmed) > 1 && (trimmed[len(trimmed)-1] == '/' || trimmed[len(trimmed)-1] == '\\') {
		trimmed = trimmed[:len(trimmed)-1]
	}
	for index := len(trimmed) - 1; index >= 0; index-- {
		if trimmed[index] == '/' || trimmed[index] == '\\' {
			return trimmed[index+1:]
		}
	}
	return trimmed
}
