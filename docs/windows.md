# Windows 支持：实现与 QEMU 验证

> 结论：sesscope 的发现、取数、归因、渲染流程在 Windows 10 上完整跑通。
> 2026-11-27 的 QEMU/KVM 实机验证中，19 条命令全部退出码 0，四个来源都能发现并导出页面。

## 1. 设计：把"这台机器"收进一个适配器

新增 `internal/platform`，启动时用 `runtime.GOOS` 选择实现：

```
platform.Detect() ─► linuxAdapter  (/proc)
                  ├► windowsAdapter (PowerShell Get-CimInstance / tasklist)
                  └► posixAdapter   (ps，macOS/其它 Unix)
```

`Adapter` 只暴露 OS 事实，不认识任何 Agent 产品：

| 方法 | 作用 |
|---|---|
| `HomeDir` | `%USERPROFILE%` / `$HOME` |
| `AppDataDir` | `%APPDATA%` / `$XDG_CONFIG_HOME` 或 `~/.config` |
| `LocalAppDataDir` | `%LOCALAPPDATA%` / `$XDG_DATA_HOME` 或 `~/.local/share` |
| `XDGDataDir` | 严格 XDG（opencode 在 Windows 上仍用 xdg-basedir） |
| `Processes/Process` | 列进程/读单个进程；环境变量读不到时用 `EnvironError` 说明 |
| `Run` | 执行命令（Windows 进程发现没有 /proc，只能走命令） |
| `OpenBrowser` | `rundll32` / `open` / `xdg-open` |
| `PrepareConsole` | Windows 启动时把控制台输出代码页设为 UTF-8 |

上层只依赖接口：`opencode/dsh/codex/claude` 的 `New(p)` 都接收同一个适配器；
`app.NewEnv()` 负责 `Detect()`，`sesscope version` 会打印实际使用的适配器。
新增 OS 只需加一个实现，发现/取数/归因/渲染一行不用改。

## 2. Windows 上的发现路径

| 来源 | 默认路径（按优先级） |
|---|---|
| opencode | `%APPDATA%\com.deepexi.deepworks\deepworks-engine-data\xdg\data\opencode\opencode.db`（DeepWorks sidecar）<br>`%APPDATA%\opencode\opencode.db`<br>`%LOCALAPPDATA%\opencode\opencode.db`<br>`%USERPROFILE%\.local\share\opencode\opencode.db`（xdg-basedir）<br>`%USERPROFILE%\.opencode\opencode.db`（旧版） |
| dsh | `%USERPROFILE%\.dsh\sessions`（`DSH_HOME` 可覆盖） |
| codex | `%USERPROFILE%\.codex\sessions`（`CODEX_HOME` 可覆盖） |
| claude | `%USERPROFILE%\.claude\projects`（`CLAUDE_CONFIG_DIR` 可覆盖） |
| DeepWorks 应用态 | `%APPDATA%\com.deepexi.deepworks`（`workspace-state.json` / `deepworks-port-state.json` / `deepworks-server-tokens.json`） |

环境变量 `OPENCODE_TRACE_DB` / `OPENCODE_DB` 仍然优先；
`--store` / `--home` / `--pid` 的优先级不变。

## 3. Windows 进程发现与 `--pid`

- 主路径：`powershell.exe -NoProfile -Command "Get-CimInstance Win32_Process ..."`，
  输出 UTF-8 JSON（pid/ppid/name/command/started）。PowerShell 不可用时退回 `tasklist /FO CSV`，
  拿不到命令行/端口/启动时间，会在 `Note` 里写明"退化了什么"。
- Windows 没有免特权读取其它进程环境变量的接口。`--pid` 的推断顺序：
  1. 先看命令行里的可执行文件路径：路径含 `DeepWorks` → DeepWorks sidecar 库；
     否则优先独立安装的本地库；
  2. 再在"唯一可读默认库"或"唯一存在的默认库"时推断；
  3. 多个候选 → 报错并列出候选，要求 `--store` 指定，绝不猜。
- 规则会出现在 `sources --explain` 的"规则/说明"里，例如：
  `l3:pid 4356（按命令行里的程序路径推断）`。

## 4. SQLite 的 Windows 盘符

`file:C:\Users\...` 会被 URL 解析当成 scheme，因此 `OpenReadOnly` 统一走 URI：

```go
uri := url.URL{Scheme: "file", Path: "/C:/Users/.../opencode.db"}
// → file:///C:/Users/.../opencode.db?mode=ro&_pragma=busy_timeout(5000)
```

POSIX 同样走 `file:///home/...`，由 modernc.org/sqlite 统一解析；
这条改动在 Linux 全套测试与 Windows 真实 DeepWorks 库上都验证过。

## 5. QEMU 实机验证（2026-11-27）

环境：QEMU/KVM 全系统虚拟化，Windows 10 Enterprise LTSC 2021（10.0.19044.7725），
guest 通过 tap 网络与宿主同网段；不是 Wine，也不是只交叉编译不运行。

流程：宿主用 `tools/windows-e2e/gen` 生成四源夹具 → 打包 `sesscope.exe` +
夹具 → guest 内解压、装夹具、启动真实 DeepWorks opencode sidecar → 跑下列命令。
（该脚本只用于一次性测试 VM：它会写入真实用户目录并启动/结束 sidecar。）

`
version --json
sources --explain
sources --json
sources --source opencode --pid <pid> --explain
workspaces
workspaces (DEEPWORKS_CONFIG_HOME=夹具)
sessions --limit 20
sessions --limit 8 --json
doctor --drift
export --session opencode:<id> --out ... --html ... --data ...
export --session dsh:<id>        --out ... --html ...
export --session codex:<id>      --out ... --html ...
export --session claude:<id>     --out ... --html ...
analyze --session dsh:<id> --json
analyze --in <dsh 导出 JSON>
render --in <opencode 导出 JSON> --out ... --data ...
ui --source opencode --no-browser   (管道喂按键)
sources --source dsh --home <夹具>
sessions --source claude --store <夹具>
`

结果：**19 条命令全部退出码 0**（每个命令都落一个 `.exit` 文件）。

真实发现结果（节选）：

`
SOURCE    STATUS   FORMAT                 STORE                                                                                        SESSIONS  LIVE
opencode  ok       opencode/sqlite:v1     ~\AppData\Roaming\com.deepexi.deepworks\deepworks-engine-data\xdg\data\opencode\opencode.db        26  pid 4356 :49952
opencode  ok       opencode/sqlite:v1     ~\AppData\Local\opencode\opencode.db                                                             4  pid 4356 :49952
dsh       ok       dsh/jsonl:v4           ~\.dsh\sessions                                                                                  1  -
codex     ok       codex/rollout:rollout  ~\.codex\sessions                                                                                1  -
claude    ok       claude/jsonl:jsonl     ~\.claude\projects                                                                               1  -
`

`sources --source opencode --pid 4356 --explain`：

`
opencode  C:\Users\fastagi\AppData\Roaming\com.deepexi.deepworks\deepworks-engine-data\xdg\data\opencode\opencode.db
  规则：l3:pid 4356（按命令行里的程序路径推断）
  进程：pid 4356 opencode-x86_64-pc-windows-msvc 49952
        库：C:\Users\fastagi\AppData\Roaming\com.deepexi.deepworks\deepworks-engine-data\xdg\data\opencode\opencode.db（存在=true）
        说明：Windows 不提供免特权读取其它进程环境变量的接口（需要调试权限）；只能按命令行与默认库推断；按命令行里的程序路径推断
`

导出与页面：四个来源的 `.html` 均生成（约 117–119 KB），
`ui --no-browser` 也生成了 164 KB 页面；`analyze --session` 与 `analyze --in` 均成功。
`doctor --drift` 抽样 3 个 opencode 会话、1 个 dsh 会话，无未注册记录。

本次验证产物（本机路径）：
- 结果包：`~/vm-lab/share/results/windows-e2e-results.tgz`
- 关键截图：`~/vm-lab/pid-test.png`（--pid 输出）、`~/vm-lab/e2e-run4.png`（全流程面板）
- 夹具生成器：`tools/windows-e2e/gen`；复现脚本：`tools/windows-e2e/README.md`

## 6. 已知差异与限制

- Windows 读不到其它进程环境变量：`--pid` 依赖命令行/默认库推断，多候选时如实报错（不会静默挑一个）。
- Windows 控制台默认字体可能没有中文字形，直接看屏会出现方框；重定向到文件始终是 UTF-8，
  页面与导出不受影响。程序启动时已把输出代码页设为 65001。
- 路径大小写：Windows 文件系统不区分大小写；工作区去重统一小写 + 正斜杠，展示仍用原始路径。
- `tasklist` 退化路径拿不到命令行/端口/启动时间；`sources --explain` 会写明退化原因。
