// Package testfixture 构造测试用的 opencode SQLite 库（只在测试里使用）。
package testfixture

import (
	"database/sql"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// GlobalProjectID / DemoProjectID 是 fixture 里的两个工作区。
const (
	GlobalProjectID = "global"
	DemoProjectID   = "c0ffee0000000000000000000000000000000000"
)

// IDs 是 fixture 里预置的会话 id，方便测试直接引用。
const (
	RootSessionID   = "ses_root0000000000000000000000000001"
	ChildSessionID  = "ses_child000000000000000000000000002"
	SecondSessionID = "ses_second000000000000000000000003"
	DemoSessionID   = "ses_demo00000000000000000000000004"
)

const schemaDDL = "" +
	"create table project (id text primary key, worktree text not null, vcs text, name text, time_created integer not null, time_updated integer not null);" +
	"create table workspace (id text primary key, type text not null, name text not null default '', branch text, directory text, extra text, project_id text not null, time_used integer not null);" +
	"create table session (id text primary key, project_id text not null, workspace_id text, parent_id text, slug text not null, directory text not null, path text, title text not null, version text not null, share_url text, summary_additions integer, summary_deletions integer, summary_files integer, summary_diffs text, metadata text, cost real, tokens_input integer, tokens_output integer, tokens_reasoning integer, tokens_cache_read integer, tokens_cache_write integer, revert text, permission text, agent text, model text, time_created integer not null, time_updated integer not null, time_compacting integer, time_archived integer);" +
	"create table message (id text primary key, session_id text not null, time_created integer not null, time_updated integer not null, data text not null);" +
	"create table part (id text primary key, message_id text not null, session_id text not null, time_created integer not null, time_updated integer not null, data text not null);"

// Build 在 dir 下创建一个 opencode.db 并写入固定的测试数据，返回库路径。
func Build(dir string) (string, error) {
	path := filepath.Join(dir, "opencode.db")
	conn, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return "", fmt.Errorf("建库：%w", err)
	}
	defer conn.Close()
	if _, err := conn.Exec(schemaDDL); err != nil {
		return "", fmt.Errorf("建表：%w", err)
	}
	for _, statement := range seedStatements() {
		if _, err := conn.Exec(statement.query, statement.args...); err != nil {
			return "", fmt.Errorf("写入 fixture（%s）：%w", statement.name, err)
		}
	}
	return path, nil
}

type statement struct {
	name  string
	query string
	args  []any
}

func seedStatements() []statement {
	return []statement{
		{"project global", "insert into project values (?,?,?,?,?,?)",
			[]any{GlobalProjectID, "/", nil, nil, int64(1_000), int64(9_000)}},
		{"project demo", "insert into project values (?,?,?,?,?,?)",
			[]any{DemoProjectID, "/opt/demo", "git", "demo", int64(1_000), int64(5_000)}},
		{"workspace", "insert into workspace (id,type,name,branch,directory,project_id,time_used) values (?,?,?,?,?,?,?)",
			[]any{"ws_1", "git", "main", "main", "/opt/demo", DemoProjectID, int64(4_500)}},

		// 根会话：2 条消息（user + assistant），assistant 带 reasoning/text/tool 三种 part
		{"session root", insertSession,
			[]any{RootSessionID, GlobalProjectID, nil, nil, "tall-fox", "/work/root", "/work/root",
				"根会话：导出一条流水线", "1.18.18", float64(0.25), int64(1000), int64(200), int64(50), int64(300), int64(10),
				"deepworks", "{\"id\":\"GLM-5.3\",\"providerID\":\"dth\",\"variant\":\"high\"}", int64(1_000), int64(6_000)}},
		{"message user", insertMessage,
			[]any{"msg_u1", RootSessionID, int64(1_000), int64(1_000),
				"{\"role\":\"user\",\"time\":{\"created\":1000},\"agent\":\"deepworks\"}"}},
		{"message assistant", insertMessage,
			[]any{"msg_a1", RootSessionID, int64(1_100), int64(6_000),
				"{\"parentID\":\"msg_u1\",\"role\":\"assistant\",\"modelID\":\"GLM-5.3\",\"providerID\":\"dth\",\"time\":{\"created\":1100,\"completed\":6000},\"finish\":\"tool-calls\",\"tokens\":{\"total\":10,\"input\":6,\"output\":2,\"reasoning\":2,\"cache\":{\"read\":0,\"write\":0}},\"cost\":0.01}"}},
		{"part step-start", insertPart,
			[]any{"prt_s1", "msg_a1", RootSessionID, int64(1_100), int64(1_100), "{\"type\":\"step-start\"}"}},
		{"part reasoning", insertPart,
			[]any{"prt_r1", "msg_a1", RootSessionID, int64(1_200), int64(2_000),
				"{\"type\":\"reasoning\",\"text\":\"先看一眼\",\"time\":{\"start\":1200,\"end\":2000}}"}},
		{"part text", insertPart,
			[]any{"prt_t1", "msg_a1", RootSessionID, int64(2_000), int64(2_500),
				"{\"type\":\"text\",\"text\":\"跑个命令\",\"time\":{\"start\":2000,\"end\":2500}}"}},
		{"part tool", insertPart,
			[]any{"prt_c1", "msg_a1", RootSessionID, int64(2_500), int64(5_000),
				"{\"type\":\"tool\",\"tool\":\"bash\",\"callID\":\"call_1\",\"state\":{\"status\":\"completed\",\"input\":{\"command\":\"ls\"},\"output\":\"a.txt\",\"time\":{\"start\":2600,\"end\":5000}}}"}},
		{"part step-finish", insertPart,
			[]any{"prt_f1", "msg_a1", RootSessionID, int64(5_000), int64(6_000), "{\"type\":\"step-finish\"}"}},

		// 子 agent 会话（parent_id 指向根会话）
		{"session child", insertSession,
			[]any{ChildSessionID, GlobalProjectID, nil, RootSessionID, "tiny-ant", "/work/root/sub", "/work/root/sub",
				"子会话：查个目录", "1.18.18", float64(0), int64(10), int64(2), int64(0), int64(0), int64(0),
				"deepworks", "{\"id\":\"GLM-5.3\",\"providerID\":\"dth\"}", int64(3_000), int64(3_500)}},

		// 另一个根会话（用于排序/过滤测试），更早更新
		{"session second", insertSession,
			[]any{SecondSessionID, GlobalProjectID, nil, nil, "calm-owl", "/work/second", "/work/second",
				"第二个会话：全零 token", "1.18.18", float64(0), int64(0), int64(0), int64(0), int64(0), int64(0),
				"deepworks", "{\"id\":\"GLM-5.3\",\"providerID\":\"dth\"}", int64(500), int64(900)}},

		// demo 项目下的会话
		{"session demo", insertSession,
			[]any{DemoSessionID, DemoProjectID, "ws_1", nil, "bold-elk", "/opt/demo", "/opt/demo",
				"demo 项目会话", "1.18.18", float64(0), int64(5), int64(1), int64(0), int64(0), int64(0),
				"deepworks", "{\"id\":\"Kimi-K3\",\"providerID\":\"dth\"}", int64(4_000), int64(7_000)}},
	}
}

const insertSession = "" +
	"insert into session (id,project_id,workspace_id,parent_id,slug,directory,path,title,version,cost," +
	"tokens_input,tokens_output,tokens_reasoning,tokens_cache_read,tokens_cache_write,agent,model,time_created,time_updated) " +
	"values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)"

const insertMessage = "insert into message (id,session_id,time_created,time_updated,data) values (?,?,?,?,?)"

const insertPart = "insert into part (id,message_id,session_id,time_created,time_updated,data) values (?,?,?,?,?,?)"
