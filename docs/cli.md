# sesscope 命令设计（契约）

> 本文描述 CLI 的**契约**：命令树、参数语义、资源定位、输出约定、退出码、扩展方式。
> 实现必须与本文一致；不一致时要改文档或改实现，不允许长期漂移。
> 框架：cobra（v1.10）+ pflag；命令按业务四段分组。

## 1. 设计目标

真实使用中最常见的四个卡点是：**不知道数据在哪、不知道有哪些会话、不知道某段耗时算谁的、
不知道换了个 Agent 还能不能用**。命令设计围绕这四点，遵守四条硬约束：

| 约束 | 含义 | 落地方式 |
|---|---|---|
| 不用记路径 | 用户只该关心"哪个来源/哪个会话" | sources 发现 → --store/--pid/--home 定位 → 默认路径兜底 |
| 可管道 | stdout 只放数据，stderr 放诊断 | 摘要/提示一律 stderr；--json 给机器读 |
| 单一职责 | 每条命令只做一件事，靠组合完成工作 | 发现 / 取数 / 分析 / 渲染四段，互不越界 |
| 失败显性 | 缺能力、格式不支持、读取不完整都要说出来 | 诊断进 stderr + 页面「数据提示」+ doctor；不静默降级 |

## 2. 命令树

    发现
      sources     （别名 ps / procs / processes）每源每存储的状态、格式版本、会话数、运行中进程
      doctor      逐存储体检；--drift 抽样报告"代码不认识的记录类型"
      workspaces  （别名 ws）工作区（跨源 + DeepWorks 应用态叠加）
      sessions    （别名 ls）跨源会话列表
      ui          （别名 browse / pick）交互式浏览：来源 → 工作区 → 会话 → 浏览器打开
    取数
      export      （别名 dump）会话 → 规范 JSON（sesscope/session.v1）；可顺带 --html / --data
    分析
      analyze     不调用模型的确定性归因（事件链 + 补偿链 + 证据）
    渲染
      render      导出 JSON（新旧格式都认）→ 自包含 HTML
    元信息
      version     版本 + 各来源能力摘要
      audit       字段门禁：检查抽象会话里"该有却没有"的字段
      help        帮助（等价于 <命令> --help）

典型四步链路：

    sesscope sources                                  # 1. 有哪些来源、存储在哪、能不能读
    sesscope sessions --limit 20                      # 2. 跨来源列会话（拿到 source:id）
    sesscope export --session dsh:session-59b8… --html trace.html  # 3. 导出并出页面
    sesscope analyze --session dsh:session-59b8…      # 4. 归因

## 3. 资源定位（统一规则）

所有要读数据的命令（sources / doctor / workspaces / sessions / export / analyze）共享同一套定位：

| 顺序 | 参数/来源 | 说明 |
|---|---|---|
| ① | --store <path>（旧 --db 等价） | 直接指定存储：SQLite 库文件 / 会话目录 / jsonl 文件 |
| ② | --home <dir> | 产品数据根：opencode 的 share 目录、~/.dsh、~/.codex、~/.claude |
| ③ | --pid <pid> | 从进程环境推导存储（opencode；Windows 无进程环境权限，改按命令行/唯一默认库推断并打印规则） |
| ④ | 环境变量 | OPENCODE_TRACE_DB、DSH_HOME、CODEX_HOME、CLAUDE_CONFIG_DIR |
| ⑤ | 已知默认路径 | 各来源文档化的默认根（见 docs/sources.md） |

--source 限定来源：auto（默认，全部已探测）/ opencode / dsh / codex / claude，可逗号分隔。
它同时决定会话 id 的解析命名空间：id 只在来源内唯一，`--session` 必须带来源（见 §6）。

**失败必须显性**：

| 情况 | 行为 |
|---|---|
| 一个可读存储都没有 | 报错并列出尝试过的路径与规则，退出码 2 |
| 存储存在但格式不支持 | 如实标成 unsupported + 原因；读它时退出码 3，**不返回 0 个会话** |
| 会话 id 没带来源 | 说明 `<source>:<id>` 与 `--source` 两种写法，退出码 2，**不跨源扫描** |
| 同一来源内前缀命中多个会话 | 列出候选（SOURCE/ID/TITLE），退出码 2，**不猜** |
| 某来源读取失败 | 其它来源照常输出；失败逐条写 stderr（提示：<source>：<原因>） |

## 4. 输出约定

| 通道 | 内容 |
|---|---|
| stdout | 表格，或 --json 的 JSON，或产物（export/render） |
| stderr | 存储路径、发现证据（--explain）、每源降级说明、摘要、"下一步"提示、弃用提示 |

- 表格按**显示宽度**对齐（CJK/emoji 记 2 列）。
- --json 统一外壳：{"schema":"sesscope.<命令>.v1","result": …}；字段只增不改名。
- 文件型来源的 UPDATED* 表示取自文件修改时间，表格打 * 并在 stderr 解释。
- 消息数未知时显示 -（例如 dsh/codex 只读头部，不为了列个数字去全扫）。

**退出码**：

| 码 | 含义 |
|---|---|
| 0 | 成功（含"没有匹配结果"这类正常空集） |
| 1 | 运行期失败（写文件、渲染等） |
| 2 | 输入/参数问题（坏 JSON、存储不存在、参数冲突、前缀歧义、未知子命令） |
| 3 | 来源或格式不支持（新版 schema、旧格式导出只支持 opencode 等） |

## 5. 逐命令规格

### sources（别名 ps / procs / processes）

| 参数 | 默认 | 说明 |
|---|---|---|
| --source / -s | auto | 限定来源 |
| --store / -d | 空 | 指定存储 |
| --home | 空 | 产品数据根 |
| --pid / -p | 0 | 从进程推导（opencode）。Linux 读 /proc/<pid>/environ；Windows 按命令行里的程序路径或唯一默认库推断 |
| --explain | false | 打印每个存储命中的规则与证据 |
| --json / -j | false | 机器可读 |

输出列：SOURCE · STATUS · FORMAT · STORE · SESSIONS · LIVE · NOTE。
STATUS 取值：ok / unsupported / unreadable / missing。

### doctor

| 参数 | 默认 | 说明 |
|---|---|---|
| --drift | false | 抽样打开至多 3 个会话，报告未注册的记录/事件类型 |
| 定位参数 | — | 同 sources |

输出列：SOURCE · LEVEL · CODE · TITLE · DETAIL；最后一行给体检结论（ok/warn/error）。

### workspaces（别名 ws）

| 参数 | 默认 | 说明 |
|---|---|---|
| --workspace-source | auto | auto / app-state（只看 DeepWorks）/ native（只看各源原生） |
| --all | false | 连没有会话的工作区也列 |

输出列：SOURCE · ID · NAME · KIND · DIRECTORY · PORT · SESSIONS · ROOTS · LAST ACTIVE · ORIGIN。

> 为什么要叠加应用态：opencode 的 project 表按 worktree 懒创建（本机只有 2 行），
> 而 DeepWorks "我的工作区"有 4 个，且 session.workspace_id 一直是 NULL。
> 只看库会少列工作区——这是当初"我的 4 个工作区只看到 2 个"的根因。

### sessions（别名 ls）

| 参数 | 默认 | 说明 |
|---|---|---|
| --workspace / -w | 空 | 工作区 id 前缀 / 路径 / 名字子串 |
| --grep / -g | 空 | 标题、目录、id 子串 |
| --limit / -n | 30 | 0 表示不限 |
| --sort | updated | updated / created / tokens / messages |
| --all / -a | false | 含子 agent 会话（标题加 └） |
| --since / --until | 空 | RFC3339 或 7d / 24h 这类相对时长 |
| --json / -j | false | 机器可读 |

四源共用同一套过滤/排序语义（source.ApplyQuery），不因来源不同而漂移。

### ui（交互式浏览）

逐级选择来源 → 工作区 → 会话，把选中的会话渲染成自包含 HTML 并用浏览器打开；全程可回退。

| 按键 | 作用 |
|---|---|
| 数字 | 选择当前页条目 |
| a | 工作区层：全部会话 |
| n / p | 会话层：下一页 / 上一页 |
| o | 详情层：渲染并在浏览器打开（打开后按 Enter 回到会话列表） |
| b | 返回上一层 |
| s | 直接回到来源选择 |
| w | 详情层：回到工作区 |
| q | 退出 |

| 参数 | 默认 | 说明 |
|---|---|---|
| --source / -s | 全部 | 限定可选来源 |
| --no-browser | false | 只渲染并打印路径，不自动打开浏览器 |
| --per-page | 15 | 会话列表每页条数 |

非交互环境（管道）也能用：把按键按行喂给 stdin，方便脚本化冒烟。

### export（别名 dump）

| 参数 | 默认 | 说明 |
|---|---|---|
| --session | 必填 | 会话，必须带来源：`<source>:<id>`（或配合 --source；支持 id 前缀） |
| --out / -o | - | 导出 JSON 路径，- 为 stdout |
| --format | canonical | canonical（规范格式）/ legacy（旧 opencode 形状，仅 opencode） |
| --html | 空 | 同时渲染 HTML |
| --data | 空 | 同时写归一化数据（需配 --html） |
| --generated-at | 当前时间 | 固定生成时间（RFC3339），可复现 |
| --chain-gap | 6 | 补偿链允许夹带的无关步数 |
| --quiet / -q | false | 不打印摘要 |

### analyze

| 参数 | 默认 | 说明 |
|---|---|---|
| --in / --session | 二选一 | 读导出 JSON（新旧都认）或直连来源（`<source>:<id>`，或配合 --source） |
| --verbose / -v | false | 打印证据与补偿动作 |
| --limit / -n | 20 | 最多打印多少条事件链 |
| --chain-gap | 6 | 补偿链宽容度 |
| --json / -j | false | 机器可读 |

### audit（字段门禁）

逐个打开会话，按 domain model 检查必填字段，按"来源 + 字段"聚合；有必需字段空缺时退出码 1。

| 参数 | 默认 | 说明 |
|---|---|---|
| --source / -s | auto | 限定来源 |
| --limit / -n | 10 | 每源最多审计多少个（按 updated 倒序；0 = 全部） |
| --json / -j | false | 机器可读（scanned / totals / issues） |

两级结果：

| 级别 | 含义 | 例子 |
|---|---|---|
| violation | 身份/结构字段缺失，一定是适配器 bug，门禁失败 | meta.id、message.created_ms、tool.status、tool.command |
| note | 可选字段缺失，只报告不拦截 | tokens、最后一条 message.completed_ms（可能还在写）、空会话的 title |

不算空缺：能力位声明没有这项数据（dsh 不写 reasoning token）、源写了诊断解释
（encrypted-reasoning / empty-session）、工具还在运行或已报错。

### render

| 参数 | 默认 | 说明 |
|---|---|---|
| --in / -i | - | 输入 JSON（新旧格式都认），- 为 stdin |
| --out / -o | - | 输出 HTML，- 为 stdout |
| --data | 空 | 同时写归一化数据 |
| --export-path | 取 --in | 写进 meta.exportPath |
| --fixture-tool-error | false | 造"工具报错"样本页 |
| --generated-at | 当前时间 | 可复现构建 |
| --chain-gap | 6 | 同上 |
| --quiet / -q | false | 摘要开关 |

## 6. 跨命令约定

- **会话 id 只在来源内唯一**：四个来源各是一套命名空间，不存在全局 id；`<source>:<id>` 才是完整身份。
- **--session 必须带来源**：写成 `<source>:<id>`，或同时给 `--source <source>`。
  裸 id 在 `--source` 未收敛到单一来源时直接退出码 2，**不跨来源扫描**——
  否则同一条命令的结果会随"本机装了哪些产品、哪个库读得动"而变。
- **前缀匹配**：前缀只在选中的来源内匹配；同源内命中多个会话时报错并列出候选，**不猜**。
- **旧用法兼容**：sesscope -in x -out y、无参 + 管道输入都等价于 render；
  -db、-session、-limit 这类**单横线长参数**由兼容层自动改写成 -- 形式，并在 stderr 提示弃用一次。
- **帮助输出**：子命令 --help 写 stdout（cobra 惯例）；只有参数错误时的"用法"写 stderr。
- **交互保护**：需要 stdin 但 stdin 是终端时，直接给用法，不挂起等待。

## 7. 扩展一条新命令

1. internal/cmd/<name>.go：实现 newXxxCommand(env *app.Env) *cobra.Command，
   用公共参数组（sourceOpts / outputOpts / filterOpts / timeOpts），RunE 只做参数校验 + 调用 app；
2. internal/cmd/root.go：在 Register 里挂上，并设置 GroupID（discover/fetch/analyze/render/meta）；
3. 业务逻辑放 internal/app，返回结构体；格式化放 internal/cli；
4. 单元测试：成功路径 + 至少一个错误路径（退出码 + stderr 文案 + --json 的 schema 字段）；
5. 如果是读写存储：走 source 抽象，不要在命令里拼路径；
6. 如果输出表格：用 cli.RenderTable（CJK 宽度已处理）；
7. 更新本文与 README。

## 8. 明确不做

| 不做 | 原因 |
|---|---|
| render --session 直连来源 | 会和 export --html 重复；取数与渲染分开才好测 |
| 交互式 TUI | 页面已经是那个角色；CLI 要能进脚本 |
| 自动选"最好的"存储 | 多存储时静默选择会让人以为看错了数据；宁可全部列出 |
| 不支持的格式静默降级 | 结构不同就是不同，必须显式报"不支持"（退出码 3） |
| 文件型来源为了列数字去全扫 | 312 个 dsh 会话全扫代价高；列表只读头部，消息数宁可缺失（显示 -） |
