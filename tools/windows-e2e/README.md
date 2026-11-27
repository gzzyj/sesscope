# Windows E2E（QEMU）

这套脚本用一台 Windows 虚拟机验证 sesscope 的完整流程：发现 → 导出 → 渲染 → 归因 → UI。

> 注意：`run-e2e.cmd` 会把夹具装到真实用户目录（`%USERPROFILE%\.dsh`、`%LOCALAPPDATA%\opencode` 等），
> 并启动/结束 opencode sidecar；只应在一次性 Windows VM 里跑，不要在日常机器上执行。

## 1. 生成夹具

```bash
go run ./tools/windows-e2e/gen -out out/windows-e2e
```

夹具覆盖四个来源，并把 cwd 写成 `C:\Users\fastagi\work\demo`：

| 来源 | 落盘位置（夹具） |
|---|---|
| opencode | `fixtures/home/AppData/Local/opencode/opencode.db`（v1 SQLite） |
| dsh | `fixtures/home/.dsh/sessions/.../session.v4.jsonl.zstd` |
| codex | `fixtures/home/.codex/sessions/2026/11/27/rollout-*.jsonl.zst` |
| claude | `fixtures/home/.claude/projects/.../<uuid>.jsonl` |
| DeepWorks | `fixtures/deepworks/workspace-state.json` 等（用 `DEEPWORKS_CONFIG_HOME` 指向） |

## 2. 打包并起 HTTP 服务

```bash
mkdir -p /tmp/sesscope-e2e
tools/windows-e2e/package.sh /tmp/sesscope-e2e
cp tools/windows-e2e/bootstrap.cmd /tmp/sesscope-e2e/
python3 tools/windows-e2e/serve.py --root /tmp/sesscope-e2e --port 18081
```

## 3. 在 Windows 里跑

把 `bootstrap.cmd` 下载到 guest（或通过共享盘/RDP 拷贝），执行：

```bat
C:\Users\<user>\bootstrap.cmd http://<host>:18081
```

它会解压到 `%USERPROFILE%\sesscope-e2e`，把夹具装到真实用户目录，启动一个真实的
DeepWorks opencode sidecar 作为进程发现样本，然后依次执行：

`version` / `sources` / `sources --pid` / `workspaces` / `sessions` / `doctor` /
四个来源的 `export --html` / `analyze --session` / `analyze --in` / `render` /
`ui --no-browser` / `--home` / `--store`。

结果写到 `%USERPROFILE%\sesscope-e2e\out`，并 zip 后 PUT 回
`<base>/upload/windows-e2e-results.tgz`（`serve.py` 会落到 `<root>/results/`）。

## 4. 判定标准

- 每个命令的 `.exit` 文件都是 `0`；
- `sources` 能同时发现 opencode（DeepWorks sidecar + 本地夹具）、dsh、codex、claude；
- `sources --pid` 能按命令行里的 `...\DeepWorks\...\sidecars\...` 选中 sidecar 库，并在规则里写明；
- 四个来源的 `.html` 都能生成，`ui --no-browser` 也生成页面；
- `analyze --session` 与 `analyze --in` 输出一致。

## 5. QEMU 说明

本仓库不附带 Windows 镜像。验证时使用 QEMU/KVM 全系统虚拟化（Windows 10 LTSC），
guest 内走真实 Windows API/PowerShell；不是 Wine，也不是交叉编译后的"只编译不运行"。
具体一次验证记录见 [docs/windows.md](../../docs/windows.md)。
