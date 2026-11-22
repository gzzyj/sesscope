# 会话领域模型与三层数据流

## 1. 为什么分三层

    Source.Open() ─► session.Session ─► trace.Normalize() ─► trace.Data ─► RenderHTML()
      (四个适配器)      (领域模型)          (唯一派生算法)       (页面契约)      (页面)
                                          └── attribution.Analyze ──┘

四个来源共同的只有"消息 / 部件 / 时间 / 状态 / 用量 / 错误"。轮次、步骤、统计、归因都是**派生语义**：

- 放进适配器 → 四份"差不多但不一样"的算法，页面口径必然漂移；
- 放进视图层 → 页面/归因只认派生结果，换源不用改页面。

所以：**适配器只产出 session.Session，trace.Normalize 是唯一一份派生算法。**

## 2. 领域模型（internal/session）

### 会话

| 字段 | 含义 | 缺省语义 |
|---|---|---|
| Ref{Source, Store, ID, Object} | 哪个源、哪个存储、原生 id、存储内定位 | 必填（Object 供 --explain 溯源） |
| Meta.ID/Title/Slug/Directory/Agent/Version | 会话元信息 | nil = 源没提供 |
| Meta.WorkspaceKey | 源内工作区键（opencode=project id，dsh/codex/claude=cwd） | 可为空 |
| Meta.ParentID | 父会话（子 agent） | nil = 顶层 |
| Meta.Model | 会话级模型（取自最后一次模型选择） | nil = 未知 |
| Meta.Cost | 金额 | nil = 不记账 |
| Meta.Provenance{Source,Store,Format,LoadedAt,Complete} | 这次读取从哪来、完不完整 | Complete=false 必须配诊断 |
| Declared | 源自己声明的统计（只用于对账，绝不直接采用） | 可空 |
| Diagnostics | 显式降级清单 | 至少 0 条 |
| Unknown | 未注册的原生记录/事件类型 → 次数（版本漂移信号） | 可空 |

**id 是源内 id，不是全局 id。** ID 直接取自各产品自己的命名空间（opencode 的会话主键 `ses_…`、
dsh 的 `session-<uuid>` 目录名、codex 的 rollout 文件名与记录里的 `session_id`、claude 的 `<uuid>.jsonl`）。
四套命名空间既不保证互不重复，也不保证跨版本稳定，所以**身份是 `Ref{Source, Store, ID}`**：
调用方必须先在来源内定位，再按 ID 解析。CLI 侧对应 `--session <source>:<id>` 或 `--session <id> --source <source>`；
裸 id 不做跨源匹配（否则解析结果会随"本机装了哪些产品、哪个库读得动"而变）。

### 消息与部件

| 字段 | 含义 |
|---|---|
| Message.Role | 存储层角色：**轮次切分只看它**（user 开一轮，assistant 是步骤） |
| Message.PayloadRole | 载荷里的角色：步骤展示用（opencode 的 data.role） |
| Message.CreatedMs / UpdatedMs / CompletedMs | 落库时间 / 更新时间 / 真正结束时间 |
| Message.TurnHint{Turn, Step, TurnID} | 源自带的轮次编号（dsh、codex、claude 有；opencode 没有） |
| Message.ModelSpans | 源显式记录的模型活动区间，可以没有文本（codex 加密思考）；页面按段画模型条 | 空 = 由 reasoning/text part 时间推导 |
| Message.Error{Name, Message, Detail, Code, StatusCode, Retryable} | 步骤级错误 |
| Message.Tokens{Total, Input, Output, Reasoning, CacheRead, CacheWrite} | 每步用量 |
| Part.Kind | text / reasoning / tool-call / tool-result / compaction / file / step-start / step-finish / other |
| Part.Type | 原生类型原文（页面 Timeline 需要原样输出） |
| Part.Encrypted | 内容存在但不可读（如 codex 的 encrypted_content）：有区间、无正文，页面标注「加密」 |
| Part.CreatedMs / UpdatedMs | 落库/更新时间 |
| Part.StartMs / EndMs | **有效执行区间**（文本取载荷 time，工具取 state/事件时间） |
| Part.DurationMs | 源声明的时长（压缩段等） |
| ToolCall{CallID, Name, Kind, Status, Args, Output, Error, Target, Command, Title, StartMs, EndMs} | 一次工具调用 |

### 工具语义（internal/session/toolsem）

归因规则判断的是"读了哪个文件、跑了什么命令"，不是"工具叫 bash 还是 exec_command"。
所以工具调用统一带三元组：**Kind**（shell/read/write/edit/search/list/web/ask/task/other）、
**Target**（归一化路径/对象）、**Command**（命令行文本）。四源共用一张映射表；
参数里多套一层的 JSON 字符串（dsh/codex 的 arguments）由 toolsem.Unwrap 还原。

## 3. 不变量（适配器必须满足）

1. Messages / Parts 按时间稳定排序（同时间按 id）。
2. Part.Kind=tool-call 必有 CallID；能配到结果时 Status 不为 unknown。
3. **缺失时间一律 nil**，不补 0、不虚构；Status=running 只在源明确未结束时使用。
4. 每个降级事实至少 1 条 Diagnostic（缺能力、加密思考、推断状态、截断、torn tail……）。
5. Args/Output/Error 保持原始字节，不重排、不数字归一（键序与数字写法有意义）。

## 4. 规范导出（sesscope/session.v1）

    {
      "schema": "sesscope/session.v1",
      "generated_at": "2026-11-21T07:30:13.383Z",
      "session": { "ref": {...}, "meta": {...}, "messages": [...],
                   "diagnostics": [...], "declared": {...}, "unknown": {...} }
    }

- 不写预计算统计：页面数据由 render/analyze 现算，避免两份口径漂移。
- 旧导出（opencode 的 export_info/session/global_stats/messages）仍然可读；
  render/analyze 按顶层特征字段自动判别，不需要用户指定格式。
- export --format legacy 保留一条兼容通道（仅 opencode；其它来源显式退出码 3）。

## 5. 诊断代码（跨源共用）

| 代码 | 含义 |
|---|---|
| torn-tail | 文件尾部不完整（会话可能正在写） |
| truncated-text | 文本/工具输出按上限截断 |
| mtime-updated | 列表的 UPDATED 取自文件修改时间 |
| encrypted-reasoning | 思考内容加密，本来源不提供 |
| no-reasoning | 本会话没有思考记录 |
| derived-tool-status | 工具状态是推断的（源不写状态） |
| derived-turn | 轮次由 role 记录派生（缺少显式轮次锚） |
| synthetic-turn-input | 补了占位用户输入（文件里没有 user/message） |
| compaction-no-duration | 压缩没有耗时 |
| no-compaction | 本来源没有压缩记录 |
| ignored-record-types | 有未注册的记录类型（见 doctor --drift） |
| unverified-format | 该格式只有 schema、没有实机数据 |
| unreadable-session | 单个会话文件读不了 |
| store-unreadable / store-missing | 存储不可用 |
