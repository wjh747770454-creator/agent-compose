# Directory-only runtime bootstrap spec

## 背景与目标

BoxLite 和 Microsandbox 采用 directory-only mount：runtime 只把整个 session 目录挂到 guest `/data`。因此 guest 内常规路径必须由启动 bootstrap 暴露出来，否则无 Jupyter 的 agent、cell、loader command 或普通 exec 可能看不到 session workspace/home。

本方案目标：

- BoxLite/Microsandbox 在任何 guest command 运行前完成 guest path bootstrap。
- `/workspace` 暴露 session workspace。
- `/root` 暴露 session home，且在 guest 内表现为真实挂载点而不是 symlink。
- Docker driver 保持现有细粒度 bind mount 行为，不引入该 bootstrap。
- bootstrap 幂等，支持 session resume、runtime restart、daemon restart 和已有 running sandbox 自愈。

## 现状和 harness 约束

- `AGENTS.md` 指定支持 runtime driver 为 `docker`、`boxlite`、`microsandbox`，默认 driver 为 `docker`。
- `docs/design/runtime_mount_manifest_driver_specific_design.md` 定义 Docker 使用细粒度 directory/file binds，BoxLite/Microsandbox 只挂 `<session> -> /data`。
- `docs/design/runtime_environment_variables_design.md` 定义 agent-compose 不显式注入 `HOME`，guest image 默认 home 为 `/root`。
- `docs/design/agent-compose-runtime_contract.md` 定义 Go host 负责 session lifecycle、目录准备、runtime driver 调度和 persistence；JS runtime 不负责修复 guest 文件系统布局。
- `TESTING.md` 要求跨 runtime-driver behavior 的变更增加证明行为的测试；`Taskfile.yml` 主门禁为 `task lint`、`task build`、`task test`，真实 runtime smoke 为 `task test:runtime-smoke`。

当前实现风险：

- Docker 可直接把 `<session>/home/.codex`、`<session>/home/.claude`、`<session>/home/.gitconfig` 等路径挂到 `/root/...`。
- BoxLite/Microsandbox 只有 `<session> -> /data`，必须在 guest 内建立 `/workspace` 和 `/root`。
- 既有 bootstrap 主要绑定在 Jupyter 启动路径；无 Jupyter session 和已有 running sandbox 仍可能缺少兼容路径。

## 核心概念或领域模型

### Directory-only runtime

`directory-only runtime` 指 runtime driver 只消费目录 source mount，不能依赖单文件 source mount。当前包括：

- `boxlite`
- `microsandbox`

其 mount manifest 保持单一目录挂载：

```text
host:  <session>
guest: /data
```

### Guest path bootstrap

`guest path bootstrap` 是 BoxLite/Microsandbox 在 guest 内执行的幂等初始化逻辑，负责把 `/data` 下的 session 内容暴露为跨 driver 兼容路径：

| Guest path | Source | 形态 |
| --- | --- | --- |
| `/workspace` | `/data/workspace` | symlink 或等价目录入口 |
| `/root` | `/data/home` | guest 内 `mount --bind` |
| `/data/state` | `/data/state` | session mount 内目录 |
| `/data/runtime` | `/data/runtime` | session mount 内目录 |
| `/data/logs` | `/data/logs` | session mount 内目录 |

`/root` 必须是 mount point，不能是 symlink。这样 Codex、Claude、Gemini、OpenCode、Git 和普通 shell command 都继续使用 `HOME=/root`，同时实际读写 session home。

## 架构和组件边界

### Go runtime driver

BoxLite/Microsandbox runtime driver 负责：

- 在 sandbox/box 创建并启动成功后执行 bootstrap。
- 在 stopped sandbox/box 重新 start 后执行 bootstrap。
- 在 `Exec` / `ExecStream` 前执行 bootstrap guard，使已有 running sandbox 可自愈。
- 将 bootstrap 失败作为 session start 或 exec 的明确错误返回。

Docker runtime 不执行该 bootstrap，继续依赖 driver-specific manifest 中的细粒度 bind mount。

### Shared bootstrap helper

`directoryOnlyGuestSessionBootstrapCommand(config)` 应作为 `pkg/driver` 内的共享 helper，而不是只服务 Jupyter launch。

bootstrap 必须在 guest cwd `/` 下执行，避免 `/workspace` 尚未就绪时 chdir 失败。`/root` 的处理规则：

- 先确认 `/data/home` 存在。
- 如果 `/root` 已是 `/data/home` 的 bind mount，保持不变。
- 如果 `/root` 是旧实现留下的 symlink，迁移为真实目录并 bind mount。
- 如果 `/root` 是 image 原始目录，首次迁移时保存为 `/root.image`，再创建真实 `/root` 目录并 bind mount。
- 如果 `/root` 已是未知 mount point，返回可诊断错误，不覆盖。

该方案只使用一个 runtime 层面的目录挂载；`mount --bind /data/home /root` 是 guest 内 VFS 重挂载，不要求 BoxLite/Microsandbox 增加额外 virtiofs export。

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

## 工作流和失败语义

### Session start/resume

BoxLite/Microsandbox 启动或恢复 runtime 时：

1. runtime 挂载 `<session> -> /data`。
2. driver 在 guest cwd `/` 执行 bootstrap。
3. bootstrap 验证 `/data/workspace` 和 `/data/home`。
4. bootstrap 建立 `/workspace`，并将 `/data/home` bind mount 到 `/root`。
5. 如启用 Jupyter，再启动 Jupyter 并等待 readiness。

bootstrap 失败时，`EnsureSession` 返回错误，session 不应被视为 ready。错误信息应包含 driver、session id 和 bootstrap stdout/stderr 摘要。

### Existing running sandbox self-heal

`Exec` / `ExecStream` 前应执行轻量 bootstrap guard。guard 未通过时执行完整 bootstrap；bootstrap 仍失败时不执行原始 command，并返回可诊断错误。

guard 至少验证：

- `/root` 是 mount point。
- `/root` 不是 symlink。
- `/root` 与 `/data/home` 指向同一目录实体。
- `/workspace` 指向 session workspace。

## 测试、质量门禁和验收标准

### Unit tests

应覆盖：

- bootstrap command 生成的 `/root` bind mount 逻辑。
- 旧版 `/root -> /data/home` symlink 的迁移逻辑。
- `/data/home` 缺失时不删除或移动 `/root`。
- Docker manifest 仍包含 `/root/...` 细粒度 mount，且不调用 directory-only bootstrap。
- BoxLite/Microsandbox manifest 仍只包含 `<session> -> /data`。

### Driver behavior tests

应覆盖：

- BoxLite/Microsandbox 无 Jupyter `EnsureSession` 会执行 bootstrap。
- BoxLite/Microsandbox `Exec` / `ExecStream` 在原始 command 前执行 bootstrap guard。
- bootstrap 失败时原始 command 不执行。

### Runtime smoke

涉及真实 BoxLite/Microsandbox 的变更完成后运行：

```bash
SMOKE_RUNTIME_DRIVERS=boxlite task test:runtime-smoke
SMOKE_RUNTIME_DRIVERS=microsandbox task test:runtime-smoke
```

smoke 应验证：

- `/root` 是 mount point 且不是 symlink。
- `/root/.codex/config.toml` 来自 `/data/home/.codex/config.toml`。
- `/root/.gitconfig` 和 `/root/.claude.json` 来自 session home。
- `/workspace` 可用于非 Jupyter command/cell exec。

常规质量门禁仍为：

```bash
task lint
task build
task test
```

## 首版不做事项

- 不改变 Docker driver mount manifest。
- 不增加多个 BoxLite/Microsandbox virtiofs export。
- 不新增 session metadata 字段记录 bootstrap 状态。
- 不新增 `GUEST_HOME` 或自动注入 `HOME`。
- 不通过 `CODEX_HOME`、JS runtime runner 或 provider-specific workaround 代替 guest path bootstrap。
- 不处理 Codex SDK/CLI 版本收敛。

## 关键假设和已确认决策

- 主修复边界是 Go runtime driver lifecycle。
- BoxLite/Microsandbox 继续保持 directory-only manifest：`<session> -> /data`。
- `/root` 推荐通过 guest 内 `mount --bind /data/home /root` 暴露，而不是 symlink。
- bootstrap 必须可重复执行，并能覆盖已有 running sandbox 的自愈场景。
- Docker driver 不受该问题影响，仍使用现有细粒度 bind mount。

## 运行时能力阻塞记录

2026-07-05 在本地 BoxLite smoke 中，已通过 `sudo usermod -aG kvm $(id -un)` 和 `sg kvm` 排除 host `/dev/kvm` 权限问题，并通过 `IMAGE_REGISTRY=registry-mirrors.dev.in.chaitin.net` 排除默认 Docker Hub manifest 拉取问题。BoxLite runtime 能启动并进入 guest bootstrap，但 guest 内执行：

```bash
mount --bind /data/home /root
```

失败为：

```text
mount: /root: permission denied
directory-only home target is not a mount point /root
```

该结果命中本方案停止条件：不得静默退回 `/root -> /data/home` symlink。BoxLite 方向在确认 runtime 能授予 guest 执行 bind mount 所需能力，或产品重新决策 `/root` 暴露方案前，不应标记完成。
