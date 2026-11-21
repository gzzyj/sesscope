# atrace（Go）

把本机四个 Agent（**opencode / dsh / codex / claude**）的会话读成同一份领域模型，
再导出、做确定性归因、渲染成 **DeepSeek Harness 轨迹风格**的耗时页面（单文件自包含 HTML）。

一个静态二进制：UI 用 `go:embed` 编进去，SQLite 用纯 Go 实现（不需要 cgo），
zstd 用 `klauspost/compress`（纯 Go）。

> 原名 `opencode-trace`（绑定单一产品）；现在覆盖四个来源，改名 `atrace`。
> 模块路径 `github.com/gzzyj/atrace`，旧命令名在 `make build` 后留一个软链（弃用）。

## 快速开始

```bash
make build

# 1) 有哪些来源、存储在哪、能不能读
./out/atrace sources

# 2) 跨来源列会话（拿到 id）
./out/atrace sessions --limit 20
./out/atrace sessions --source dsh --limit 10
./out/atrace sessions --workspace demo --all

# 3) 导出 + 出页面
./out/atrace export --session <会话 id> --html out/trace.html

# 4) 不调模型的确定性归因
./out/atrace analyze --session <会话 id>
```

会话 id 支持**前缀**；跨来源歧义时会列出候选并退出码 2（不猜）。
也可以用 `source:id` 明确指定，例如 `--session dsh:session-abc`。

## 命令

| 段 | 命令 | 作用 |
|---|---|---|
| 发现 | `sources`（别名 `ps`/`processes`） | 每源每存储的状态、格式版本、会话数、运行中进程 |
| 发现 | `doctor` | 逐存储体检；`--drift` 报告代码不认识的记录类型 |
| 发现 | `workspaces` | 工作区（跨源 + DeepWorks 应用态叠加） |
| 发现 | `sessions` | 跨源会话列表（`--workspace/--grep/--since/--sort/--limit/--all`） |
| 取数 | `export` | 会话 → 规范 JSON（`atrace/session.v1`）；可顺带 `--html/--data` |
| 分析 | `analyze` | 确定性归因：事件链 + 补偿链 + 证据（不联网、不调模型） |
| 渲染 | `render` | 导出 JSON（新旧格式都认）→ 自包含 HTML |
| 元 | `version` / `help` | 版本 + 各源能力摘要 / 帮助 |

契约（命令树、参数、退出码、扩展方式）见 [docs/cli.md](docs/cli.md)。
领域模型与三层数据流见 [docs/session-model.md](docs/session-model.md)。
四个来源的发现契约、映射与缺口见 [docs/sources.md](docs/sources.md)。

## 数据从哪来

| 来源 | 存储 | 本机实测 |
|---|---|---|
| opencode | `~/.config/com.deepexi.deepworks/…/opencode/opencode.db`、`~/.local/share/opencode/opencode.db` | 52 会话（旧 schema 可读；新 schema 已识别、尚未实现读取） |
| dsh | `~/.dsh/sessions/<slug>/session-<uuid>/session.v{3,4}.jsonl.zstd` | 314 个会话目录（v3/v4 共存） |
| codex | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl.zst` | 88 个 rollout（同时存在 7 个 cli_version） |
| claude | `~/.claude/projects/<cwd-slug>/<uuid>.jsonl` | 19 个会话文件 |

定位顺序：`--store` > `--home` > `--pid`（仅 opencode）> 环境变量
（`OPENCODE_TRACE_DB`/`DSH_HOME`/`CODEX_HOME`/`CLAUDE_CONFIG_DIR`）> 默认路径。

**只读**：opencode 库以 `mode=ro` 打开（能读 WAL）；文件型来源只读文件，不写任何东西。

## 三个不妥协的地方

1. **缺能力必须说出来**：思考加密、工具状态推断、列表 mtime、torn tail……都会变成诊断，
   在 CLI stderr、页面「数据提示」、`doctor` 三处可见；绝不用 0 或空串冒充。
2. **不静默降级**：新版 schema 标 `unsupported`（读它退出码 3），不返回 0 个会话；
   跨源前缀歧义列候选并退出码 2。
3. **口径只有一份**：轮次/步骤/统计由 `trace.Normalize` 统一派生；四个适配器只负责
   "把存储映射成领域模型"，归因只认工具语义（shell/read/write/edit/ask…），不认产品名。

## 页面能看什么

顶部三泳道总览时间轴（输入/模型/工具：条长=耗时，红色=报错，琥珀=未完成，绿色=等人工，
空闲可折叠），中间按轮次组织的台账，右侧检查器（LLM 输出、思考、工具参数/结果、每个 part 的计时、
归因页签），另有耗时排行与"数据提示"。

## 验证

```bash
make verify     # gofmt + go vet + go test（含四源适配器夹具、黄金用例、CLI 契约）
make smoke      # 真机四源：sources → sessions → 每源导出 1 个会话 → 页面冒烟，报告写 out/smoke/report.json
node tools/cdp-check.mjs --html <页面>   # 钉死会话的 40 项界面回归
node tools/cdp-smoke.mjs --html <页面>   # 任意会话的冒烟（10–15 项，按页面能力）
```

| 检查 | 覆盖 |
|---|---|
| 黄金用例 | opencode 导出 → 页面数据逐字节 + HTML 指纹 + 10 项统计数字 |
| 等价性 | 直连 opencode 库与旧导出产出的页面数据逐字节一致 |
| 适配器夹具 | dsh/codex/claude 各一份 map 构造的 fixture（现场压 zstd），断言角色顺序、轮次、工具配对、token、诊断 |
| CLI 契约 | 每条命令的 `--help`、退出码、`--json` 的 schema、旧单横线参数兼容、歧义/不支持/未发现三种失败路径 |

## 代码结构

```
main.go                     只做装配：cli.NewRoot + cmd.Register
internal/cli/               cobra 框架层：根命令、分组、退出码、旧参数兼容、表格/JSON/时间格式化
internal/cmd/               命令层：只翻译参数 → 调 app
internal/app/               用例层：定位→取数→派生→输出，返回结构体
internal/session/           领域模型（纯类型）+ toolsem（工具语义归一）
internal/source/            源抽象：Source 接口、能力位、Store/Locator/Query、共享过滤排序、前缀解析
internal/source/opencode/   SQLite 适配器（v1 可读；v2 已识别）
internal/source/dsh/        JSONL+zstd 适配器（v3/v4）
internal/source/codex/      rollout 适配器
internal/source/claude/     projects JSONL 适配器
internal/source/jsonl/      逐行读 + zstd + torn tail 容忍
internal/trace/             视图层：旧导出适配、Normalize（唯一派生算法）、渲染、页面资源
internal/attribution/       确定性归因（读 trace.Data，按工具语义判定）
internal/export/            规范导出 atrace/session.v1
internal/deepworks/         应用态工作区（叠加层）
internal/table/             CJK 宽度对齐表格
internal/testfixture/       opencode 测试库
tools/                      CDP 自检脚本
```

## 已知限制

- opencode 新版 schema（`session_v2/session_message/session_part`）只做了识别与如实报错，
  读取映射尚未实现（本机该库 0 会话，只有 schema）。
- codex 的思考内容多为加密；工具状态与起止时间由"是否出现输出记录"推断（有诊断）。
- claude 本机样本未出现 thinking / compaction，能力位按"不保证"处理。
- 文件型来源的列表 UPDATED 取自文件修改时间；消息数未知时显示 `-`（不为列数字去全扫）。
- 大会话没有虚拟滚动：几百条消息没问题，再大就得优化。
