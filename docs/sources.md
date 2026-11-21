# 四个来源：发现契约、映射与已知缺口

> 每个来源一个适配器（internal/source/<id>），实现同一套 Source 接口。
> 发现规则、版本判定、映射口径与降级都要能被测试和 doctor 验证。

## 1. 发现契约

| 来源 | 默认根 | 指纹（必须同时满足） | 版本信号 |
|---|---|---|---|
| opencode | <home>/opencode/opencode.db，默认 DeepWorks sidecar 库 + ~/.local/share/opencode/opencode.db | SQLite 表集含 session/message/part（v1）或 session_v2/session_message/session_part（v2） | 表集 |
| dsh | $DSH_HOME/sessions，默认 ~/.dsh/sessions | <slug>/session-<uuid>/session.v*.jsonl.zstd，首行是 {"type":"session","version":N} | 头行 version（v3/v4） |
| codex | $CODEX_HOME/sessions，默认 ~/.codex/sessions | YYYY/MM/DD/rollout-*.jsonl.zst，首行是 session_meta | payload.cli_version |
| claude | $CLAUDE_CONFIG_DIR/projects，默认 ~/.claude/projects | <slug>/<uuid>.jsonl，任一行含 sessionId 且 type ∈ user/assistant/system | 记录里的 version |

- 定位优先级：--store > --home > 环境变量 > 默认根（见 docs/cli.md §3）。
- 只有 opencode 有进程可探测（/proc + XDG_DATA_HOME），其余来源**不做进程发现**；
  列表里的 UPDATED 对文件型来源取自文件修改时间，表格打 *。
- 同一会话多份文件（dsh v3/v4）→ 取最高版本。

## 2. opencode（SQLite）

| 项 | 说明 |
|---|---|
| 存储 | 只读打开（mode=ro），可读 WAL；不写库 |
| 映射 | project/workspace → Workspace；session → Meta；message.data → Message（tokens/error/time.completed）；part.data → Part（state.status/input/output/error/time、compaction.auto、file） |
| 能力 | 进程发现、原生工作区、SQL 过滤、标题、子会话、token、成本、思考文本、工具参数/输出/状态、压缩记录 |
| 缺口 | v2 新 schema 尚未实现读取（本机该库 0 会话，只有 schema）；cost 常为 0（本机实测）；workspace 表常为空，工作区按 project 兜底 |

## 3. dsh（JSONL + zstd）

| 项 | 说明 |
|---|---|
| 存储 | ~/.dsh/sessions/<slug>/session-<uuid>/session.v{3,4}.jsonl.zstd；append-only、多帧 zstd、尾部可能被截断 |
| 映射 | 头行 → Meta；turn/start·step/start·step/end → TurnHint 与步骤起止；user/message → 用户消息（source.kind≠user 的注入内容映射成 system，不切轮次）；assistant/message → 步骤（content：reasoning/text/tool-call；usage；interrupted）；tool/call + tool/result → 工具（isError→error）；compaction/start·end → 压缩段时长；session/title → 标题；request/header.config → 模型 |
| 独立速度优化 | 列会话只读头行；标题/累计 token 取 ~/.dsh/storages/session_projcache（best-effort，读不到就留空） |
| 能力 | 标题、子会话（parentSession）、轮次编号、token、思考文本、工具参数/输出/状态、压缩记录 |
| 缺口 | 无成本；v3 没有 compaction 事件；列表的 UPDATED 是 mtime；消息数未知（显示 -）；torn tail 需容忍 |

## 4. codex（rollout JSONL + zstd）

| 项 | 说明 |
|---|---|
| 存储 | ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl.zst |
| 映射 | session_meta → Meta（cwd/cli_version/model_provider）；turn_context → 轮次 id；event_msg item_completed{UserMessage} → **人类轮次锚**（response_item 里 role=user 的内容混有 AGENTS.md / 环境注入，映射成 system）；response_item message → 步骤；function_call / custom_tool_call（+ *_output） → 工具；token_usage_record → 用量；compacted → 压缩（无起止）；task_complete/turn_aborted → 结束与错误 |
| 能力 | 轮次编号、token、工具参数/输出、压缩记录 |
| 缺口 | 思考多为 encrypted_content（显式诊断）；工具状态与起止时间靠"是否出现输出记录"推断（显式诊断）；无标题（取首条人类输入派生）；无成本；子会话关系未证实 |

## 5. claude（projects JSONL）

| 项 | 说明 |
|---|---|
| 存储 | ~/.claude/projects/<cwd-slug>/<uuid>.jsonl |
| 映射 | user/assistant 记录 → 消息（message.content 块：text / thinking / tool_use / tool_result）；tool_result → 配对工具结果（is_error→error）；isApiErrorMessage + apiErrorStatus → 步骤错误；system{subtype:turn_duration} → 步骤结束时间；turnPosition → 轮次编号；isSidechain/parentUuid → 子会话 |
| 忽略清单 | mode、permission-mode、atis-latch、file-history-snapshot、attachment、api-request*、last-prompt、cost-state、queue-operation、tools-used（都记录在文档里，doctor 可见） |
| 能力 | 子会话、轮次编号、token、工具参数/输出 |
| 缺口 | 本机样本未出现 thinking / compaction，能力位按"不保证"处理并给诊断；工具状态由 tool_result 推断；无标题（派生）；无成本 |

## 6. 版本漂移怎么处理

三层解码：物理层（zstd 多帧/校验/torn tail、sqlite 只读、逐行 JSON）→ 记录层（type 注册表，
**未知只计数不失败**）→ 语义层（映射到领域模型）。

- 每源一个 knownIgnored 清单：确认与对话无关的类型不进 Unknown，避免把已知噪声当漂移信号。
- 未知类型 → Session.Unknown[type]++ + ignored-record-types 诊断；
  doctor --drift 抽样报告"本机出现但代码不认识"的类型（按源/版本/次数）。
- 不按产品版本白名单写死：同一产品不同小版本的记录类型会增删（本机 codex 就同时存在 7 个 cli_version）。
- 能力 = 声明 ∩ 实测：例如 codex 的工具状态能力取决于这份文件里是否真的出现 *_output。
