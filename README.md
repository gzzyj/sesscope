# opencode-trace（Go）

从 opencode 的**本地 SQLite 库**里列出工作区、列出会话、把某个会话导出成 JSON，
再渲染成 **DeepSeek Harness 轨迹风格**的耗时页面（单文件自包含 HTML）。

一个静态二进制（约 6 MB，UI 用 `go:embed` 编进去，SQLite 用纯 Go 实现，不需要 cgo）。

```bash
# 不知道库在哪？先看进程（会给出 PID、端口、库路径、工作区/会话数）
./out/opencode-trace processes

# 用某个进程的库：-pid；也可以直接给 -db
./out/opencode-trace workspaces -pid 23762             # 这个进程有哪些工作区
./out/opencode-trace sessions   -pid 23762 -limit 20   # 有哪些会话（id 在这里）
./out/opencode-trace export     -pid 23762 -session ses_e1dc9f03 -html trace.html
```

本机实测（`opencode-trace processes`）：

```
PID    进程                              端口   已运行        工作区  会话  会话库
23753  opencode-router-x86_64-…                1 天 15 小时       2    52  ~/.config/…/opencode/opencode.db
23762  opencode-x86_64-…               33723  1 天 15 小时       2    52  ~/.config/…/opencode/opencode.db
-      （无进程）                          -                         0     0  ~/.local/share/opencode/opencode.db（读不了：新版 schema）
```

> 库是从进程的 `XDG_DATA_HOME` 推出来的（`$XDG_DATA_HOME/opencode/opencode.db`），
> 所以 `-pid` 不需要你关心路径。没有进程指向的库也会列出来，并标明能否读取。

## 子命令

| 子命令 | 作用 |
|---|---|
| `processes` | 列出本机 opencode 进程、端口、各自在用的会话库（**不知道库在哪就先跑这个**） |
| `workspaces` | 列出 DeepWorks 的「我的工作区」（id / 名称 / 路径 / API 端口 / 会话数） |
| `sessions` | 列出会话：`-workspace` 过滤、`-grep` 搜索、`-sort` 排序、`-all` 含子 agent |
| `export` | 把会话导出成 `session-export.json`，可同时 `-html` 出页面 |
| `analyze` | **不调用模型**的确定性归因：事件链 + 证据（详见下文） |
| `render` | 把 `session-export.json` 渲染成自包含 HTML（原有能力） |
| `version` / `help` | 版本 / 帮助 |

每个子命令都支持 `-h`；会话 id 支持**前缀匹配**（`-session ses_e1dc`）。
旧用法 `opencode-trace -in x -out y` 仍然有效，等价于 `render`。

## 快速开始

```bash
make build                       # → out/opencode-trace（Go 1.21+）

# 1) 看看有什么
./out/opencode-trace workspaces
./out/opencode-trace sessions -limit 20

# 2) 导出一个会话（JSON），并直接出页面
./out/opencode-trace export -session ses_e1dc9f03 -html out/trace.html

# 3) 也可以分两步（导出 → 渲染），或用管道
./out/opencode-trace export -session ses_e1dc9f03 -out session-export.json
./out/opencode-trace render -in session-export.json -out out/trace.html
./out/opencode-trace export -session ses_e1dc9f03 | ./out/opencode-trace render -out out/trace.html
```

## 数据从哪来

库路径按这个顺序找，找到即用：

1. `-db <路径>`（显式指定）
2. 环境变量 `OPENCODE_TRACE_DB`
3. `~/.config/com.deepexi.deepworks/deepworks-engine-data/xdg/data/opencode/opencode.db`（DeepWorks 内置 sidecar 的真实库）
4. `~/.local/share/opencode/opencode.db`（独立安装的 opencode）

**只读打开**（`mode=ro`），不会改动机器上的任何数据；能读到 WAL 里刚写入的内容。
打开时会校验表结构：缺 `session`/`message`/`part` 直接报“不是 opencode 会话库”；
没有 `project` 表则明确说是**旧版库**（列不了工作区），而不是给一个空列表。

本机现状（实测）：

| 项 | 值 |
|---|---|
| 工作区（project） | 2 个：`global`（worktree `/`）、`fastagi-monorepo`（`/opt/deepexi/fastagi-monorepo`，git） |
| workspace 表 | 空（DeepWorks 用 project/worktree 当工作区，页面会提示这一点） |
| 会话 | 52 个（32 顶层 + 20 子 agent） |
| 消息 / part | 3104 条 / 13923 个 |

> 之前那份 `session-export.json` 来自**另一台机器**（会话目录是 `D:/开发/...`），本机库里没有；
> `render` 照样能吃那份文件——`export/render` 的分工就是这样：一个管取，一个管画。

## 三个命令的细节

### workspaces

```
ID                                        KIND     NAME              DIRECTORY                      SESSIONS  ROOTS  LAST ACTIVE
----------------------------------------  -------  ----------------  -----------------------------  --------  -----  ----------------
global                                    project  global            /                                    51     31  2026-11-19 09:55
e11846010a47a720edaef2f8cda3f9316d045b02  project  fastagi-monorepo  /opt/deepexi/fastagi-monorepo         1      1  2026-10-20 16:07
```

参数：`-db`、`-json`（机器可读，含完整 id）、`-all`（连 0 会话的工作区也列）。
库路径和工作区口径提示写 stderr，stdout 只有表格，方便 `| grep` / `| head`。

### sessions

```
SESSION                         WORKSPACE  TITLE                          MSG  TOK IN  TOK OUT  UPDATED
------------------------------  ---------  -----------------------------  ---  ------  -------  ----------------
ses_e1dc9f035ffeUp7gPZj4vPJlfv  global     FastAGI 继续追问身份            26  129.5k     3.9k  2026-11-19 09:55
ses_e1dcb6cd6ffeH2EUQNgiy55ihp  global     FastAGI 智能体列表              70    1.8m    38.9k  2026-11-17 15:59
```

| 参数 | 默认 | 说明 |
|---|---|---|
| `-workspace` | 空 | 只看某个工作区：project id 前缀 / worktree / 名字子串 |
| `-grep` | 空 | 标题、目录、id 里包含该子串 |
| `-limit` | 30 | 最多列多少条（0 表示不限） |
| `-all` | false | 连子 agent 会话一起列（默认只列顶层，子会话在标题前加 `└`） |
| `-sort` | updated | `updated` / `created` / `tokens` / `messages` |
| `-json` | false | 输出 JSON（含 token、part 数、时间戳、is_child 等） |

### export

| 参数 | 默认 | 说明 |
|---|---|---|
| `-session` | 必填 | 会话 id（支持前缀）；歧义时会列出候选 |
| `-out` | `-` | 导出的 JSON 路径，`-` 表示 stdout |
| `-html` | 空 | 可选：同时渲染 HTML 到这个路径 |
| `-data` | 空 | 可选：同时写页面用的归一化数据（配合 `-html`） |
| `-generated-at` | 当前时间 | 固定 `meta.generatedAt`，用于可复现构建 |
| `-db` / `-quiet` | — | 库路径 / 不打摘要 |

摘要（消息数、part 数、角色分布、时间区间、产物大小）写 **stderr**，stdout 只留给 JSON/HTML。

## analyze：不请求模型的归因

```bash
opencode-trace analyze -session ses_edf5d4f5        # 表格：按原因聚合 + 事件链
opencode-trace analyze -in session-export.json -v   # 带证据（命令原文/错误码/输出片段）
opencode-trace analyze -session ses_edf5d4f5 -json  # 机器可读，便于接看板
```

**纯本地规则，不联网、不调用任何模型**。输出长这样（真实数据）：

```
原因                       次数   窗口耗时  置信度
-------------------------  ----  ---------  ------
路径探查（找不到→换路径）     1  57 m 53 s  B
上下文压缩                   10   17 m 7 s  A
同一目标重复失败              2    3 m 3 s  A
编辑锚点失配→重读             2      5.6 s  B
输出编码损坏（乱码）          1       7 ms  A
```

### 补偿链：一次故障引发的连续补救

事件链说"哪里坏了"，**补偿链说"为此连续付出了多少步"**——后者才是业务侧关心的代价：

```
区间             步数      耗时  性质                                                        结果
第 1 轮 2-33 步    32  5 m 57 s  授权/登录补偿链（CLI 跑不通 → 探目录 → 读脚本 → 重建环境）  最后一次失败在第 17 步；之后 16 步无失败
```

规则（可调 `-chain-gap`，默认 6）：
1. 从首个故障步骤出发，取其**主题词**（环境变量名 / 脚本名 / 隐藏目录，如 `FASTDATA_KIT_HOME`、`fastdata.cmd`、`.fastdata-agent-kit`）；
2. 向后延伸，主题词**随链传递扩展**（模型在补救中发现的新脚本/目录也算同一条链）；
3. 允许中间夹 `-chain-gap` 步无关动作；
4. **遇到 write/edit 就停**——那是"真正开始产出"的边界；
5. 结论只给可证伪的事实："最后一次失败在第 N 步；之后 M 步无失败"。

### 归因规则（置信度 A = 结构化字段，B = 结构化 + 序列模式）

| 原因 | 判据 | 级别 |
|---|---|---|
| 接口/服务报错 | `message.data.error` 的 `name/statusCode/isRetryable` | A |
| 用户中止 | `MessageAbortedError`（与真故障区分开） | A |
| 上下文压缩 | `compaction` part + 压缩前 `input_tokens` | A |
| 输出编码损坏 | 输出含 `U+FFFD` / GBK-当-UTF8 特征（`ϵͳ`、`锟斤拷`） | A |
| 工具报错 | `state.status=error` + `state.error` | A |
| 服务忙/锁超时重试 | 同一（工具+目标）连续失败 ≥2 次，错误码含 LOCK/TIMEOUT | A |
| 输出被截断 | 输出尾部 `Results are truncated` / `exceeded N bytes` | B |
| 路径探查 | 找不到文件 → 后续换工具/路径重试 | B |
| 授权被拒→读源码 | 授权类错误 → 4 步内 `read` 源码文件 | B |

两条硬性约束（都是踩过坑才定的）：
1. **纯文本关键词不参与判定**——`服务不可用` 在真实数据里命中 272 次，全是系统提示词和源码里的 `Line 429:`；`未授权` 命中 406 次，全是文件名。
2. **事件链会去重**：重试链覆盖的失败不再单独记一条 `tool_error`，避免重复计数。

## 导出格式与保真度

导出结构沿用既有口径：`export_info` / `session` / `global_stats` / `turn_timings` / `messages[]`。
关键点：

- **`data` 列原样保留**。库里 `message.data` / `part.data` 是 JSON 文本，工具参数的键序有意义，
  所以不解析再重组，而是作为 JSON 值原样嵌入（键序、数字写法都不动）。
- `part.duration_ms = time_updated - time_created`；`part_times` 另外给出 `reasoning/text` 的 `data.time`、
  `tool` 的 `data.state.time`（真实执行区间，和页面的口径一致）。
- `turn_timings` 每条 assistant 消息一行：steps / reasoning_parts / tool_calls / 时长 / tokens。
- 时间戳 ISO 统一按 **UTC** 输出，避免跨时区对不上。

**保真核对**（拿库里的原始行逐条比，不是抽查）：

| 会话 | 消息 | part | 结果 |
|---|---|---|---|
| `ses_edf5d4f5…`（691 条消息） | 691 | 2891 | data 文本、键序、时间戳、part 顺序、`role_counts`/`part_type_counts` 全部一致 |
| `ses_e1dc9f03…`（26 条消息） | 26 | 97 | 同上 |

## render（原有能力，未改动）

| 参数 | 默认 | 说明 |
|---|---|---|
| `-in` / `-out` | `-` | 输入 JSON / 输出 HTML，`-` 表示 stdin / stdout |
| `-data` | 空 | 同时写归一化数据 JSON |
| `-fixture-tool-error` | false | 造一份“工具报错”样本页，验证红色渲染 |
| `-generated-at` | 当前时间 | 固定 `meta.generatedAt` |
| `-export-path` / `-quiet` | — | 写进 meta 的路径 / 不打摘要 |

页面本身（总览时间轴、轮次台账、检查器、耗时排行、报错红色、深链 `#turn=4`）见文末“页面能看什么”。

## 验证

```bash
make verify     # gofmt + go vet + go test
node tools/cdp-check.mjs --html out/opencode-trace.html      # 40 项界面回归（钉死某个会话）
node tools/cdp-smoke.mjs --html out/demo/trace.html          # 10 项冒烟（任意会话都能开）
```

| 检查 | 结果 |
|---|---|
| `go test ./...` | 全绿：查询/过滤/排序/前缀解析/导出/表格对齐/CLI 退出码/`</script>` 注入防护/黄金用例 |
| 导出保真（对库逐条核） | 691 条消息 + 2891 part 全一致；26 条 + 97 part 全一致 |
| 页面冒烟（真实会话） | 两个会话各 10/10：条/行渲染、点模型条开检查器、点工具条看参数结果、搜索、收起展开、0 异常 |
| 原有 render 回归 | 40/40（`out/verify/report.json`） |
| 大会话 | 691 条消息 → 1522 条记录、43 轮、831 工具、10 个真实报错，导出+渲染 0.38 s |

## 代码结构

```
main.go                 子命令路由 + render 实现 + 文件读写
commands.go             workspaces / sessions / export 三个子命令与表格输出
internal/opencode/
  db.go                 库发现、只读打开、schema 校验
  store.go              项目/workspace/会话查询与筛选、消息与 part 读取
  export.go             组装导出文档（turn_timings、global_stats）
  export_types.go       导出 JSON 的结构定义
internal/table/         等宽表格（CJK 按显示宽度对齐）
internal/trace/         归一化 + 渲染（沿用之前的实现，未改动）
internal/testfixture/   测试用的临时 SQLite 库
tools/                  CDP 自检（回归 + 冒烟）
```

## 已知限制

- **只认旧版 SQLite schema**（`session` / `message` / `part`）。本机已发现**新版 opencode 库**
  （`session_message` / `session_inbox` / `job` 表，`~/.local/share/opencode/opencode.db` 即是），
  `processes` 会明确标成「读不了：新版 schema」，不会假装 0 个会话。要支持得另写一层读取。
- 只读，不提供写入/删除会话的能力（改库的事不在这条工具链里）。
- 子 agent 会话默认不列（`-all` 可见），导出时也只导一个会话本身。
- 大会话没有虚拟滚动：691 条消息 → 3.9 MB HTML，打开没问题，但再大就得优化。
- `workspace` 表为空时用 project 顶替“工作区”概念，页面/提示里会写明。
- 时间戳统一 UTC 输出（本地时区显示由页面/终端负责）。

## 页面能看什么（render 的产出）

顶部一条三泳道总览时间轴（输入 / 模型 / 工具：条长=耗时，红色=报错，琥珀斜纹=未完成，绿色=等人工，
空闲可折叠成断口），中间按轮次组织的台账，右侧检查器点开看这一轮的 LLM 输出、思考、工具参数、
工具结果与每个 part 的计时，另有耗时排行与 10 类数据提示（口径差异都写在页面上，不藏）。

**归因在页面上有四处**（另有补偿链的整段底纹）：

1. 顶部概览条：`归因 3 类 · 8 次 · 1 m 48 s`
2. 时间轴：补偿链铺**整段淡色底纹**（一眼看出"这段时间都在补救"），底部每个触发点一个小三角旗；悬停看性质与边界
3. 右侧「总览」：归因聚合列表（原因 / 次数 / 窗口耗时 / 置信度），点击跳第一条
4. 台账行 + 检查器：命中步骤带「归因 N」徽标，检查器多出「归因」页签，列出证据、补偿动作、结论
