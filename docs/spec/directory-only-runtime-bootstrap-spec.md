# Directory-only runtime bootstrap spec

## 背景与目标

BoxLite 和 Microsandbox 采用 directory-only mount：runtime 只把整个 session 目录挂到 guest `/data`。因此 guest 内常规路径必须由启动 bootstrap 暴露出来，否则无 Jupyter 的 agent、cell、loader command 或普通 exec 可能看不到 session workspace/home。

2026-07-05 的真实 BoxLite smoke 已证明 guest 内 `mount --bind /data/home /root` 会返回 `permission denied`。首版 `/root` bind mount 方案因此停止。本方案改为：

- 三个 runtime driver 共享同一套逻辑挂载清单。
- Docker 按逻辑清单生成细粒度 bind mounts。
- BoxLite/Microsandbox 仍只把 `<session>` 挂到 `/data`，再通过 guest bootstrap 暴露逻辑路径。
- `/root` 保持 guest image 的真实目录，不整体替换为 symlink，也不再尝试 bind mount。
- 只将声明在逻辑清单中的 home 条目 symlink 到 `/data/home/...`。
- bootstrap 幂等，支持 session resume、runtime restart、daemon restart 和已有 running sandbox 自愈。

## 现状和 harness 约束

- `AGENTS.md` 指定支持 runtime driver 为 `docker`、`boxlite`、`microsandbox`，默认 driver 为 `docker`。
- `docs/design/runtime_mount_manifest_driver_specific_design.md` 定义 Docker 可使用细粒度 directory/file binds，BoxLite/Microsandbox 只挂 `<session> -> /data`。
- `docs/design/runtime_environment_variables_design.md` 定义 agent-compose 不显式注入 `HOME`，guest image 默认 home 为 `/root`。
- `docs/design/agent-compose-runtime_contract.md` 定义 Go host 负责 session lifecycle、目录准备、runtime driver 调度和 persistence；JS runtime 不负责修复 guest 文件系统布局。
- `TESTING.md` 要求跨 runtime-driver behavior 的变更增加证明行为的测试；`Taskfile.yml` 主门禁为 `task lint`、`task build`、`task test`，真实 runtime smoke 为 `task test:runtime-smoke`。

## 核心概念或领域模型

### Logical runtime mount entry

`logical runtime mount entry` 是跨 driver 的唯一语义清单。每个条目描述 session-relative source、guest path、source 类型，以及 directory-only runtime 的暴露方式。Docker 和 directory-only runtime 不应各自维护分散清单。

首版逻辑清单：

| Session source | Guest path | Type | Directory-only exposure |
| --- | --- | --- | --- |
| `workspace` | `/workspace` | dir | symlink `/workspace -> /data/workspace` |
| `state` | `/data/state` | dir | already inside `/data` |
| `runtime` | `/data/runtime` | dir | already inside `/data` |
| `logs` | `/data/logs` | dir | already inside `/data` |
| `home/.codex` | `/root/.codex` | dir | symlink |
| `home/.claude` | `/root/.claude` | dir | symlink |
| `home/.opencode` | `/root/.opencode` | dir | symlink |
| `home/.claude.json` | `/root/.claude.json` | file | symlink |
| `home/.gitconfig` | `/root/.gitconfig` | file | symlink |
| `home/.gemini` | `/root/.gemini` | dir | symlink |
| `home/.config/claude` | `/root/.config/claude` | dir | symlink |
| `home/.config/Claude` | `/root/.config/Claude` | dir | symlink |
| `home/.config/gemini` | `/root/.config/gemini` | dir | symlink |
| `home/.config/opencode` | `/root/.config/opencode` | dir | symlink |
| `home/.local/share/gemini` | `/root/.local/share/gemini` | dir | symlink |

未声明的 `/root` 子路径不保证持久化。该限制必须作为 v2 方案的明确 tradeoff，而不是隐藏行为。

### Driver-specific mount application

Docker:

- 读取同一套逻辑清单。
- 为每个条目生成细粒度 bind mount。
- file 条目继续作为 file source mount。
- directory 条目继续作为 directory source mount。

BoxLite/Microsandbox:

- 读取同一套逻辑清单用于初始化 host source 和生成 bootstrap。
- runtime mount manifest 仍只有 `<session> -> /data`。
- bootstrap 在 guest cwd `/` 下执行。
- `/workspace` 由 symlink 暴露。
- `/data/state`、`/data/runtime`、`/data/logs` 已经位于 `/data` 中，不创建自指向 symlink。
- `/root` 保持真实目录，只处理逻辑清单中的 home 条目 symlink。

## 架构和组件边界

### Go runtime driver

Go runtime driver 负责：

- 维护单一逻辑挂载清单 helper，例如 `runtimeMountEntries(config)`。
- 从该清单派生 Docker 细粒度 manifest。
- 从该清单派生 BoxLite/Microsandbox directory-only manifest 与 guest bootstrap。
- 在 BoxLite/Microsandbox sandbox/box 创建并启动成功后执行 bootstrap。
- 在 stopped sandbox/box 重新 start 后执行 bootstrap。
- 在 `Exec` / `ExecStream` 前执行 bootstrap guard，使已有 running sandbox 可自愈。
- 将 bootstrap 失败作为 session start 或 exec 的明确错误返回。

Docker runtime 不执行 directory-only bootstrap，但它必须消费同一套逻辑清单生成 mounts，避免 Docker 与 BoxLite/Microsandbox 的 home 条目随时间分叉。

### Shared bootstrap helper

`directoryOnlyGuestSessionBootstrapCommand(config)` 应作为 `pkg/driver` 内的共享 helper。它必须从同一套逻辑清单生成 guest path bootstrap，而不是手写另一份 home path 列表。

bootstrap 规则：

- 先验证 `/data/workspace` 和 `/data/home` 存在。
- 对 `/workspace` 创建或修复 symlink 到 `/data/workspace`。
- 对 home 逻辑条目创建或修复 symlink，例如 `/root/.codex -> /data/home/.codex`。
- 对 home 条目先创建 source parent 和 target parent。
- file 条目必须保证 source 是文件；directory 条目必须保证 source 是目录。
- 如果 target 已经是正确 symlink，保持不变。
- 如果 target 是旧实现留下的正确等价路径，可迁移为 symlink。
- 如果 target 是未知 mount point、非预期文件类型，或会覆盖 image 内重要内容，返回可诊断错误，不静默覆盖。
- 不删除、移动或替换整个 `/root`。
- 不创建 `/root -> /data/home` symlink。
- 不执行 `mount --bind /data/home /root`。

### JavaScript runtime

`runtime/javascript` 不承担主修复。它可以增加诊断日志或 preflight，但不得写入、删除或重建 `/root`。

## API、CLI、配置、数据模型或协议变化

首版不新增 API、CLI、proto、数据库 schema 或配置项。

既有配置继续生效：

- `GUEST_WORKSPACE` 默认 `/workspace`
- `GUEST_STATE_ROOT` 默认 `/data/state`
- `GUEST_RUNTIME_ROOT` 默认 `/data/runtime`
- `GUEST_LOG_ROOT` 默认 `/data/logs`

`GUEST_HOME` 不重新引入，agent-compose 仍不显式注入 `HOME`。不以 `CODEX_HOME=/data/home/.codex` 作为产品级修复。

内部 mount manifest 可以继续保持版本 `1`，前提是其 JSON 仍代表 driver-specific applied mounts。实现上必须新增或抽象出逻辑清单源，避免 Docker 与 directory-only bootstrap 维护两份语义列表。

## 工作流和失败语义

### Session start/resume

BoxLite/Microsandbox 启动或恢复 runtime 时：

1. runtime 挂载 `<session> -> /data`。
2. driver 在 guest cwd `/` 执行 bootstrap。
3. bootstrap 验证 `/data/workspace` 和 `/data/home`。
4. bootstrap 建立 `/workspace -> /data/workspace`。
5. bootstrap 为逻辑清单中的 home 条目建立 `/root/... -> /data/home/...` symlink。
6. 如启用 Jupyter，再启动 Jupyter 并等待 readiness。

bootstrap 失败时，`EnsureSession` 返回错误，session 不应被视为 ready。错误信息应包含 driver、session id 和 bootstrap stdout/stderr 摘要。

### Existing running sandbox self-heal

`Exec` / `ExecStream` 前应执行轻量 bootstrap guard。guard 未通过时执行完整 bootstrap；bootstrap 仍失败时不执行原始 command，并返回可诊断错误。

guard 至少验证：

- `/workspace` 指向 `/data/workspace`。
- `/root` 不是 symlink，且不是由 agent-compose 整体替换的路径。
- 每个声明的 home 条目指向 `/data/home/...`。
- `/root/.codex/config.toml`、`/root/.gitconfig`、`/root/.claude.json` 等默认条目可从 session home 访问。

## 测试、质量门禁和验收标准

### Unit tests

应覆盖：

- 逻辑清单包含 Docker 与 directory-only runtime 共享的 workspace/state/runtime/logs/home 条目。
- Docker manifest 由逻辑清单生成，并仍包含 `/root/...` 细粒度 mounts。
- BoxLite/Microsandbox manifest 仍只包含 `<session> -> /data`。
- directory-only bootstrap 不包含 `mount --bind /data/home /root`。
- directory-only bootstrap 不生成 `/root -> /data/home`。
- directory-only bootstrap 生成声明 home 条目的 symlink。
- `/data/home` 缺失时不删除或移动 `/root`。
- 不为 `/data/state`、`/data/runtime`、`/data/logs` 生成自指向 symlink。

### Driver behavior tests

应覆盖：

- BoxLite/Microsandbox 无 Jupyter `EnsureSession` 会执行 bootstrap。
- BoxLite/Microsandbox `Exec` / `ExecStream` 在原始 command 前执行 bootstrap guard。
- bootstrap 失败时原始 command 不执行。
- bootstrap stdout/stderr 不混入用户 command stream。

### Runtime smoke

涉及真实 BoxLite/Microsandbox 的变更完成后运行：

```bash
SMOKE_RUNTIME_DRIVERS=boxlite task test:runtime-smoke
SMOKE_RUNTIME_DRIVERS=microsandbox task test:runtime-smoke
```

smoke 应验证：

- `/root` 是真实目录，不是 `/data/home` symlink。
- `/root/.codex`、`/root/.claude`、`/root/.gitconfig`、`/root/.claude.json` 等声明条目解析到 `/data/home/...`。
- 写入声明 home 条目后，host `<session>/home/...` 可见。
- `/workspace` 可用于非 Jupyter command/cell exec。
- BoxLite/Microsandbox smoke 不依赖 Jupyter readiness 间接触发 bootstrap。

常规质量门禁仍为：

```bash
task lint
task build
task test
```

## 首版不做事项

- 不改变 Docker runtime 可使用 file bind 的能力。
- 不增加多个 BoxLite/Microsandbox virtiofs export。
- 不新增 session metadata 字段记录 bootstrap 状态。
- 不新增 `GUEST_HOME` 或自动注入 `HOME`。
- 不通过 `CODEX_HOME`、JS runtime runner 或 provider-specific workaround 代替 guest path bootstrap。
- 不处理 Codex SDK/CLI 版本收敛。
- 不保证未声明 `/root` 子路径持久化。

## 关键假设和已确认决策

- 主修复边界是 Go runtime driver lifecycle。
- BoxLite/Microsandbox 继续保持 directory-only manifest：`<session> -> /data`。
- Docker、BoxLite、Microsandbox 必须共享同一套逻辑挂载清单。
- `/root` 保持 image 内真实目录；只将声明 home 条目 symlink 到 `/data/home/...`。
- 不再要求 `/root` 是 `/data/home` 的 bind mount。
- Docker driver 不执行 directory-only bootstrap，但必须从同一逻辑清单生成 mounts。

## 运行时能力阻塞和方案变更记录

2026-07-05 在本地 BoxLite smoke 中，已通过 `sudo usermod -aG kvm $(id -un)` 和 `sg kvm` 排除 host `/dev/kvm` 权限问题，并通过 `IMAGE_REGISTRY=registry-mirrors.dev.in.chaitin.net` 排除默认 Docker Hub manifest 拉取问题。BoxLite runtime 能启动并进入 guest bootstrap，但 guest 内执行：

```bash
mount --bind /data/home /root
```

失败为：

```text
mount: /root: permission denied
directory-only home target is not a mount point /root
```

同日连续三次复验相同 smoke 命令仍返回同一 `mount: /root: permission denied`。已复核当前 `build/boxlite/include/boxlite.h` 和 `pkg/driver/boxlite_cgo.go` 中的 BoxLite C options 暴露面，未发现 privileged、capability 或等价的 guest mount 权限开关可在当前实现边界内启用。

因此本方案放弃 `/root` bind mount 目标，改为“真实 `/root` + 声明 home 条目 symlink”。该方案是对旧停止条件的产品决策更新，不允许退回到整个 `/root -> /data/home` symlink。
