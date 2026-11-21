package opencode

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// ListProjects 列出所有工作区（project），附带会话统计。
func (db *DB) ListProjects() ([]Project, error) {
	const query = `
select p.id, p.worktree, p.vcs, p.name, p.time_created, p.time_updated,
       (select count(*) from session s where s.project_id = p.id) as sessions,
       (select count(*) from session s where s.project_id = p.id and (s.parent_id is null or s.parent_id = '')) as roots,
       (select max(s.time_updated) from session s where s.project_id = p.id) as last_activity
from project p
order by last_activity desc, p.id
`
	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, fmt.Errorf("查询工作区：%w", err)
	}
	defer rows.Close()
	out := make([]Project, 0, 4)
	for rows.Next() {
		var (
			project    Project
			vcs        sql.NullString
			name       sql.NullString
			lastActive sql.NullInt64
		)
		if err := rows.Scan(&project.ID, &project.Worktree, &vcs, &name,
			&project.TimeCreated, &project.TimeUpdated,
			&project.SessionCount, &project.RootCount, &lastActive); err != nil {
			return nil, fmt.Errorf("读取工作区行：%w", err)
		}
		if vcs.Valid {
			value := vcs.String
			project.VCS = &value
		}
		if name.Valid && name.String != "" {
			value := name.String
			project.Name = &value
		}
		if lastActive.Valid {
			value := lastActive.Int64
			project.LastActivityMs = &value
		}
		out = append(out, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历工作区：%w", err)
	}
	return out, nil
}

// ListWorkspaces 列出 workspace 表（DeepWorks 里通常为空）。
func (db *DB) ListWorkspaces() ([]Workspace, error) {
	const query = `
select id, type, name, branch, directory, project_id, time_used
from workspace
order by time_used desc, id
`
	rows, err := db.conn.Query(query)
	if err != nil {
		// 旧库可能没有 workspace 表：当成"没有工作区"，但把原因带回给调用方
		return nil, fmt.Errorf("查询 workspace 表：%w", err)
	}
	defer rows.Close()
	out := make([]Workspace, 0, 4)
	for rows.Next() {
		var (
			workspace Workspace
			branch    sql.NullString
			directory sql.NullString
		)
		if err := rows.Scan(&workspace.ID, &workspace.Type, &workspace.Name, &branch, &directory,
			&workspace.ProjectID, &workspace.TimeUsed); err != nil {
			return nil, fmt.Errorf("读取 workspace 行：%w", err)
		}
		if branch.Valid {
			value := branch.String
			workspace.Branch = &value
		}
		if directory.Valid {
			value := directory.String
			workspace.Directory = &value
		}
		out = append(out, workspace)
	}
	return out, rows.Err()
}

// Counts 返回库里的工作区数与会话数（给 processes 表用，尽量轻）。
func (db *DB) Counts() (workspaces int, sessions int, err error) {
	if err := db.conn.QueryRow("select count(*) from project").Scan(&workspaces); err != nil {
		return 0, 0, fmt.Errorf("数工作区：%w", err)
	}
	if err := db.conn.QueryRow("select count(*) from session").Scan(&sessions); err != nil {
		return 0, 0, fmt.Errorf("数会话：%w", err)
	}
	return workspaces, sessions, nil
}

// DirectoryStats 是按会话目录聚合的统计（用来把 opencode 会话对到 DeepWorks 工作区）。
type DirectoryStats struct {
	Sessions int64
	Roots    int64
	LastMs   int64
}

// SessionStatsByDirectory 按 session.directory 聚合（工作区路径 → 统计）。
func (db *DB) SessionStatsByDirectory() (map[string]DirectoryStats, error) {
	rows, err := db.conn.Query(
		"select directory, count(*), sum(case when parent_id is null or parent_id = '' then 1 else 0 end), max(time_updated) " +
			"from session group by directory")
	if err != nil {
		return nil, fmt.Errorf("按目录统计会话：%w", err)
	}
	defer rows.Close()
	out := make(map[string]DirectoryStats, 16)
	for rows.Next() {
		var (
			directory string
			stats     DirectoryStats
			lastMs    sql.NullInt64
		)
		if err := rows.Scan(&directory, &stats.Sessions, &stats.Roots, &lastMs); err != nil {
			return nil, fmt.Errorf("读取目录统计：%w", err)
		}
		if lastMs.Valid {
			stats.LastMs = lastMs.Int64
		}
		out[directory] = stats
	}
	return out, rows.Err()
}

// SessionFilter 控制会话列表的筛选。
type SessionFilter struct {
	// Workspace 匹配 project id 前缀或 worktree/名称子串（空表示全部）。
	Workspace string
	// Grep 匹配标题/目录/ID 子串（空表示不过滤）。
	Grep string
	// IncludeChildren 为 true 时也列出子 agent 会话。
	IncludeChildren bool
	// Limit 最多返回多少条（<=0 表示不限）。
	Limit int
	// Sort 取 updated（默认）/ created / tokens / messages。
	Sort string
}

const sessionSelect = `
select s.id, s.project_id, s.workspace_id, s.parent_id, s.slug, s.directory, s.title, s.version,
       s.agent, s.model, coalesce(s.cost, 0),
       coalesce(s.tokens_input, 0), coalesce(s.tokens_output, 0), coalesce(s.tokens_reasoning, 0),
       coalesce(s.tokens_cache_read, 0), coalesce(s.tokens_cache_write, 0),
       s.time_created, s.time_updated,
       (select count(*) from message m where m.session_id = s.id) as messages,
       (select count(*) from part p where p.session_id = s.id) as parts
from session s
`

// ListProjectsFirst 返回 project id -> 便于按 worktree 过滤会话。
func (db *DB) projectIndex() (map[string]Project, error) {
	projects, err := db.ListProjects()
	if err != nil {
		return nil, err
	}
	index := make(map[string]Project, len(projects))
	for _, project := range projects {
		index[project.ID] = project
	}
	return index, nil
}

// ListSessions 按筛选条件列出会话。
func (db *DB) ListSessions(filter SessionFilter) ([]SessionRow, error) {
	conditions := make([]string, 0, 3)
	args := make([]any, 0, 3)
	if !filter.IncludeChildren {
		conditions = append(conditions, "(s.parent_id is null or s.parent_id = '')")
	}
	if strings.TrimSpace(filter.Workspace) != "" {
		needle := strings.TrimSpace(filter.Workspace)
		projects, err := db.projectsMatching(needle)
		if err != nil {
			return nil, err
		}
		directories, err := db.directoriesMatching(needle)
		if err != nil {
			return nil, err
		}
		if len(projects) == 0 && len(directories) == 0 {
			return nil, fmt.Errorf("没有匹配 %q 的工作区（先跑 atrace workspaces 看列表）", needle)
		}
		clauses := make([]string, 0, 2)
		if len(projects) > 0 {
			placeholders := make([]string, len(projects))
			for index, id := range projects {
				placeholders[index] = "?"
				args = append(args, id)
			}
			clauses = append(clauses, "s.project_id in ("+strings.Join(placeholders, ",")+")")
		}
		if len(directories) > 0 {
			placeholders := make([]string, len(directories))
			for index, directory := range directories {
				placeholders[index] = "?"
				args = append(args, directory)
			}
			clauses = append(clauses, "s.directory in ("+strings.Join(placeholders, ",")+")")
		}
		conditions = append(conditions, "("+strings.Join(clauses, " or ")+")")
	}
	if needle := strings.TrimSpace(filter.Grep); needle != "" {
		conditions = append(conditions, "(s.title like ? or s.directory like ? or s.id like ?)")
		like := "%" + needle + "%"
		args = append(args, like, like, like)
	}

	orderBy := "s.time_updated desc"
	switch filter.Sort {
	case "", "updated":
	case "created":
		orderBy = "s.time_created desc"
	case "tokens":
		orderBy = "(coalesce(s.tokens_input,0) + coalesce(s.tokens_output,0)) desc"
	case "messages":
		orderBy = "messages desc"
	default:
		return nil, fmt.Errorf("不认识的排序 %q（可选 updated / created / tokens / messages）", filter.Sort)
	}

	query := sessionSelect
	if len(conditions) > 0 {
		query += " where " + strings.Join(conditions, " and ")
	}
	query += " order by " + orderBy
	if filter.Limit > 0 {
		query += fmt.Sprintf(" limit %d", filter.Limit)
	}

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询会话：%w", err)
	}
	defer rows.Close()
	out := make([]SessionRow, 0, 32)
	for rows.Next() {
		session, err := scanSessionRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, session)
	}
	return out, rows.Err()
}

func scanSessionRow(rows *sql.Rows) (SessionRow, error) {
	var (
		session     SessionRow
		workspaceID sql.NullString
		parentID    sql.NullString
		agent       sql.NullString
		model       sql.NullString
	)
	if err := rows.Scan(&session.ID, &session.ProjectID, &workspaceID, &parentID, &session.Slug,
		&session.Directory, &session.Title, &session.Version, &agent, &model, &session.Cost,
		&session.TokensInput, &session.TokensOutput, &session.TokensReason,
		&session.TokensCacheRead, &session.TokensCacheWrite,
		&session.TimeCreated, &session.TimeUpdated,
		&session.MessageCount, &session.PartCount); err != nil {
		return SessionRow{}, fmt.Errorf("读取会话行：%w", err)
	}
	if workspaceID.Valid && workspaceID.String != "" {
		value := workspaceID.String
		session.WorkspaceID = &value
	}
	if parentID.Valid && parentID.String != "" {
		value := parentID.String
		session.ParentID = &value
	}
	if agent.Valid {
		value := agent.String
		session.Agent = &value
	}
	if model.Valid && model.String != "" {
		value := model.String
		session.Model = &value
	}
	return session, nil
}

// projectsMatching 把 -workspace 的输入（id 前缀 / worktree / 名称子串）解析成 project id 列表。
func (db *DB) projectsMatching(needle string) ([]string, error) {
	projects, err := db.projectIndex()
	if err != nil {
		return nil, err
	}
	lowered := strings.ToLower(needle)
	matched := make([]string, 0, 4)
	for id, project := range projects {
		if strings.HasPrefix(id, needle) ||
			strings.Contains(strings.ToLower(id), lowered) ||
			strings.Contains(strings.ToLower(project.Worktree), lowered) ||
			(project.Name != nil && strings.Contains(strings.ToLower(*project.Name), lowered)) {
			matched = append(matched, id)
		}
	}
	sort.Strings(matched)
	return matched, nil
}

// Session 按 id（或 id 前缀）取单个会话。
func (db *DB) Session(idOrPrefix string) (SessionRow, error) {
	trimmed := strings.TrimSpace(idOrPrefix)
	if trimmed == "" {
		return SessionRow{}, errors.New("没有给会话 id")
	}
	exact, err := db.sessionsByCondition("s.id = ?", trimmed, 1)
	if err != nil {
		return SessionRow{}, err
	}
	if len(exact) > 0 {
		return exact[0], nil
	}
	prefixed, err := db.sessionsByCondition("s.id like ?", trimmed+"%", 0)
	if err != nil {
		return SessionRow{}, err
	}
	switch len(prefixed) {
	case 0:
		return SessionRow{}, fmt.Errorf("%w：%s", ErrSessionNotFound, trimmed)
	case 1:
		return prefixed[0], nil
	default:
		ids := make([]string, 0, len(prefixed))
		for _, session := range prefixed {
			ids = append(ids, session.ID)
		}
		return SessionRow{}, fmt.Errorf("会话 id 前缀 %q 不唯一，匹配到 %d 个：\n  %s", trimmed, len(prefixed), strings.Join(ids, "\n  "))
	}
}

func (db *DB) sessionsByCondition(condition string, arg any, limit int) ([]SessionRow, error) {
	query := sessionSelect + " where " + condition + " order by s.time_updated desc"
	if limit > 0 {
		query += fmt.Sprintf(" limit %d", limit)
	}
	rows, err := db.conn.Query(query, arg)
	if err != nil {
		return nil, fmt.Errorf("查询会话：%w", err)
	}
	defer rows.Close()
	out := make([]SessionRow, 0, 2)
	for rows.Next() {
		session, err := scanSessionRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, session)
	}
	return out, rows.Err()
}

// Load 读取会话的全部消息与 part（顺序与导出一致：time_created, id）。
func (db *DB) Load(sessionID string) (*SessionDetail, error) {
	session, err := db.Session(sessionID)
	if err != nil {
		return nil, err
	}
	messages, err := db.messages(session.ID)
	if err != nil {
		return nil, err
	}
	parts, err := db.parts(session.ID)
	if err != nil {
		return nil, err
	}
	return &SessionDetail{Session: session, Messages: messages, Parts: parts}, nil
}

func (db *DB) messages(sessionID string) ([]MessageRow, error) {
	const query = `
select id, session_id, time_created, time_updated, data
from message where session_id = ? order by time_created, id
`
	rows, err := db.conn.Query(query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("查询消息：%w", err)
	}
	defer rows.Close()
	out := make([]MessageRow, 0, 64)
	for rows.Next() {
		var message MessageRow
		var data string
		if err := rows.Scan(&message.ID, &message.SessionID, &message.TimeCreatedMs, &message.TimeUpdatedMs, &data); err != nil {
			return nil, fmt.Errorf("读取消息行：%w", err)
		}
		message.Data = []byte(data)
		out = append(out, message)
	}
	return out, rows.Err()
}

func (db *DB) parts(sessionID string) ([]PartRow, error) {
	const query = `
select id, message_id, session_id, time_created, time_updated, data
from part where session_id = ? order by time_created, id
`
	rows, err := db.conn.Query(query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("查询 part：%w", err)
	}
	defer rows.Close()
	out := make([]PartRow, 0, 256)
	for rows.Next() {
		var part PartRow
		var data string
		if err := rows.Scan(&part.ID, &part.MessageID, &part.SessionID, &part.TimeCreatedMs, &part.TimeUpdatedMs, &data); err != nil {
			return nil, fmt.Errorf("读取 part 行：%w", err)
		}
		part.Data = []byte(data)
		out = append(out, part)
	}
	return out, rows.Err()
}

// directoriesMatching 把 -workspace 的输入（路径前缀/子串、目录名）解析成会话目录列表。
// DeepWorks 的工作区就是目录，opencode 里很多工作区没有对应的 project 行。
func (db *DB) directoriesMatching(needle string) ([]string, error) {
	rows, err := db.conn.Query("select distinct directory from session where directory is not null and directory != ''")
	if err != nil {
		return nil, fmt.Errorf("查会话目录：%w", err)
	}
	defer rows.Close()
	lowered := strings.ToLower(needle)
	matched := make([]string, 0, 8)
	for rows.Next() {
		var directory string
		if err := rows.Scan(&directory); err != nil {
			return nil, fmt.Errorf("读目录：%w", err)
		}
		candidate := strings.ToLower(filepath.ToSlash(directory))
		base := strings.ToLower(filepath.Base(strings.TrimRight(directory, "/")))
		if candidate == lowered || base == lowered ||
			strings.HasPrefix(candidate, strings.ToLower(filepath.ToSlash(needle))) ||
			strings.Contains(candidate, lowered) {
			matched = append(matched, directory)
		}
	}
	sort.Strings(matched)
	return matched, rows.Err()
}
