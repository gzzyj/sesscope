# opencode-trace 命令设计

> 这份文档描述 CLI 的**契约**：命令树、参数语义、资源定位、输出约定、退出码、扩展方式。
> 实现必须与本文一致；不一致时要改文档或改实现，不允许长期漂移。

## 1. 设计目标

真实使用中最常见的三个卡点是：**不知道库在哪、不知道 session id、不知道某段耗时算谁的**。
命令设计就围绕这三点，遵守三条硬约束：

| 约束 | 含义 | 落地方式 |
|---|---|---|
| 不用记路径 | 用户只该关心"哪个进程/哪个会话" | `processes` 发现 → `-pid` 定位 → 库路径由工具推出来 |
| 可管道 | `stdout` 只放数据，`stderr` 放诊断 | 摘要/提示一律写 stderr；`-json` 给机器读 |
| 单一职责 | 每条命令只做一件事，靠组合完成工作 | 发现 / 取数 / 分析 / 渲染四段，互不越界 |

## 2. 命令树

```
opencode-trace
├── processes      发现：本机 opencode 进程、端口、各自在用的库
├── workspaces     发现：有哪些工作区（project / workspace）
├── sessions       发现：有哪些会话（拿到 session id）
├── export         取数：会话 → session-export.json
├── analyze        分析：不调用模型的确定性归因（事件链 + 补偿链 + 证据）
├── render         渲染：session-export.json → 自包含 HTML
├── version        版本
└── help [命令]    帮助
```

典型的四步链路（不需要知道库路径）：

```bash
opencode-trace processes                          # 1. 拿到 PID
opencode-trace workspaces -pid 23762              # 2. 拿到工作区
opencode-trace sessions   -pid 23762 -limit 20    # 3. 拿到 session id
opencode-trace export     -pid 23762 -session ses_e1dc9f03 -html trace.html   # 4. 出页面
```

## 3. 资源定位（统一规则）

所有要读库的命令（`workspaces` / `sessions` / `export` / `analyze`）共享同一套解析顺序：

```
-pid <PID>            ① 从该进程环境推库（最可靠，推荐）
  ↓ 未给
-db <path>            ② 显式指定
  ↓ 未给
$OPENCODE_TRACE_DB    ③ 环境变量
  ↓ 未设
已知路径依次尝试        ④ DeepWorks 数据目录 → ~/.local/share/opencode
```

`-pid` 的推导方式：读 `/proc/<PID>/environ` 里的 `XDG_DATA_HOME`，拼 `$XDG_DATA_HOME/opencode/opencode.db`；
没有 `XDG_DATA_HOME` 时退回 `$HOME/.local/share/opencode/opencode.db`。

**失败必须显性**（这是硬要求）：

| 情况 | 行为 |
|---|---|
| 库不存在 | 报错，列出尝试过的路径，退出码 2（`workspaces` 例外：没有库也能列工作区，只是没有会话数） |
| 缺 `session/message/part` 但有 `session_message` | 报「新版 opencode 库，本工具暂不支持」，**不显示 0 个会话** |
| PID 不是 opencode 进程 | 报错并提示先跑 `processes` |
| 磁盘上多出的库（无进程指向） | `processes` 也列出来，并标注能否读取 |

## 4. 输出约定

| 通道 | 内容 |
|---|---|
| stdout | 表格，或 `-json` 的 JSON；`export`/`render` 的产物 |
| stderr | 库路径、摘要（条数/耗时/产物大小）、"下一步"提示 |

- 表格按**显示宽度**对齐（CJK/emoji 记 2 列），不会错位；
- `-json` 的字段稳定，供脚本/看板消费；
- 路径在表格里缩成 `~/…`，完整路径在 stderr。

**退出码**：

| 码 | 含义 |
|---|---|
| 0 | 成功（含"没有匹配结果"这类正常空集） |
| 1 | 运行期失败（写文件错误等） |
| 2 | 输入/参数问题（坏 JSON、库不存在、参数冲突、未知子命令） |

## 5. 逐命令规格

### processes（别名 ps / procs）

| 参数 | 默认 | 说明 |
|---|---|---|
| `-json` | false | 机器可读（pid/name/port/db_path/db_exists/workspaces/sessions/note） |

输出列：`PID · 进程 · 端口 · 已运行 · 工作区 · 会话 · 会话库`。无进程指向的库单独一行（PID 为 `-`）。

### workspaces（别名 workspace / ws）

| 参数 | 默认 | 说明 |
|---|---|---|
| `-db <path>` | 空 | 显式指定库（只影响会话数） |
| `-pid <int>` | 0 | 用某个 opencode 进程的库 |
| `-json` | false | 机器可读（含完整 id） |
| `-all` | false | 连 0 会话 / 目录不存在的工作区也列 |
| `-source <mode>` | auto | `deepworks`（读状态文件）/ `db`（只看 opencode 库）/ `auto` |

**数据源**：工作区清单来自 DeepWorks 自己的
`~/.config/com.deepexi.deepworks/workspace-state.json`（含 `ws_` id、名称、路径），
端口来自 `deepworks-port-state.json`，会话数按 `session.directory` 与工作区路径对齐。

> 为什么不能只看 opencode 库：opencode 的 `project` 表是按 worktree 懒创建的
> （本机只有 2 行），而 DeepWorks 的"我的工作区"有 4 个，且 `session.workspace_id` 全是 NULL。
> 只看库会少列两个工作区——这就是最初"我的 4 个工作区只看到 2 个"的原因。

输出列：`ID · NAME · KIND · DIRECTORY · PORT · SESSIONS · ROOTS · LAST ACTIVE · SOURCE`
（`PORT` 会探测是否在监听，未运行标"未运行"）。

### sessions（别名 session / ls）

| 参数 | 默认 | 说明 |
|---|---|---|
| `-db` / `-pid` | — | 同上 |
| `-workspace <str>` | 空 | `ws_` id / 工作区路径 / 目录名 / project id 前缀，都支持 |
| `-grep <str>` | 空 | 标题、目录、id 子串 |
| `-limit <int>` | 30 | 0 表示不限 |
| `-all` | false | 含子 agent 会话（标题前加 `└`） |
| `-sort <str>` | updated | `updated` / `created` / `tokens` / `messages` |
| `-json` | false | 机器可读（含 token、part 数、时间戳、is_child） |

### export（别名 dump）

| 参数 | 默认 | 说明 |
|---|---|---|
| `-db` / `-pid` | — | 同上 |
| `-session <str>` | 必填 | 会话 id，支持前缀；歧义时列出候选 |
| `-out <path>` | `-` | 导出 JSON 路径，`-` 为 stdout |
| `-html <path>` | 空 | 顺带渲染 HTML |
| `-data <path>` | 空 | 顺带写归一化数据（需配 `-html`） |
| `-generated-at <rfc3339>` | 当前时间 | 固定 `meta.generatedAt`，可复现 |
| `-chain-gap <int>` | 6 | 补偿链允许夹带的无关步数 |
| `-quiet` | false | 不打摘要 |

### analyze

| 参数 | 默认 | 说明 |
|---|---|---|
| `-in <path>` | — | 直接分析导出 JSON（与 `-session` 二选一） |
| `-session <str>` | — | 从库分析，支持前缀 |
| `-db` / `-pid` | — | 同上 |
| `-json` | false | 机器可读（summary / episodes / chains） |
| `-v` | false | 打印证据与补偿动作 |
| `-limit <int>` | 20 | 最多打印多少条事件链 |
| `-chain-gap <int>` | 6 | 补偿链边界宽容度 |

### render

| 参数 | 默认 | 说明 |
|---|---|---|
| `-in <path>` | `-` | 输入 JSON，`-` 为 stdin |
| `-out <path>` | `-` | 输出 HTML，`-` 为 stdout |
| `-data <path>` | 空 | 同时写归一化数据 |
| `-export-path <str>` | 取 `-in` | 写进 `meta.exportPath` |
| `-fixture-tool-error` | false | 造"工具报错"样本页 |
| `-generated-at` | 当前时间 | 可复现构建 |
| `-chain-gap <int>` | 6 | 同上 |
| `-quiet` / `-version` | false | 摘要开关 / 版本 |

## 6. 跨命令约定

- **前缀匹配**：session id 支持唯一前缀；歧义时报错并列出全部候选，**不猜**。
- **表格 vs JSON**：所有列表命令都有 `-json`；字段名稳定（camelCase 的 `id`、`time_created_iso` 等）。
- **旧用法兼容**：`opencode-trace -in x -out y` 等价于 `render`；无参数且 stdin 是管道时也等同 `render`。
- **顶层 `-h`**：只打印命令列表和"从零开始"的顺序，**不打印某个子命令的参数表**（踩过这个坑）。
- **交互保护**：需要 stdin 但 stdin 是终端时，直接给用法，不挂起等待。

## 7. 扩展一条新子命令

按这个顺序做，缺一不可：

1. `commands_<name>.go`：实现 `run<Name>(args, stdout, stderr) int`，用 `flag.NewFlagSet` + `flags.Usage`；
2. `main.go`：在 `switch args[0]` 注册（含别名），并在 `printUsage` 里加一行；
3. `help.go`：加进 `helpFor`；
4. 单元测试：`main_commands_test.go` 覆盖成功路径 + 至少一个错误路径（退出码 + stderr 文案）；
5. 如果是读写库：复用 `openDatabase(dbPath, pid, stderr)`，不要自己拼路径；
6. 如果输出表格：用 `internal/table`，不要手写对齐；
7. 更新 `README.md` 与本文。

## 8. 明确不做

| 不做 | 原因 |
|---|---|
| `render -session` 直连库 | 会和 `export -html` 重复；取数与渲染分开才好测 |
| 交互式 TUI | 页面已经是那个角色；CLI 要能进脚本 |
| 自动选"最好的"库 | 多库时静默选择会让人以为看错了数据；宁可报错让人选 |
| 新版 schema 静默降级 | 结构不同就是不同，必须显式报"不支持" |
