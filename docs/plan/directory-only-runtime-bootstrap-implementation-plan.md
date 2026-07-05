# Directory-only runtime bootstrap implementation plan

## 阶段 1：锁定实现边界和测试基线

目标：确认本次只改 BoxLite/Microsandbox directory-only guest path bootstrap，不改变 Docker manifest、API、CLI、proto、数据库 schema 或 JS runtime 主逻辑。

依赖：

- `docs/spec/directory-only-runtime-bootstrap-spec.md`
- `AGENTS.md`
- `TESTING.md`
- `Taskfile.yml`
- `.github/workflows/ci.yml`

实施工作：

1. 复核 `pkg/driver/boxlite_guest_cgo.go` 中 `directoryOnlyGuestSessionBootstrapCommand`、`jupyterLaunchCommand` 的当前职责。
2. 复核 `pkg/driver/boxlite_cgo.go` 的 `EnsureSession`、`execWithStream`、`executeBox` 调用路径。
3. 复核 `pkg/driver/microsandbox_runtime.go` 的 `EnsureSession`、`Exec`、`ExecStream`、`launchJupyter` 调用路径。
4. 复核 `pkg/driver/runtime_mount_manifest_test.go`、`pkg/driver/runtime_mount_manifest_*_smoke_test.go` 的现有覆盖，记录需要更新的断言。

测试和验证：

- 本阶段不要求代码测试，但后续每个阶段必须保持 `go test ./pkg/driver` 可运行。
- 计划执行完成后的权威门禁为 `task lint`、`task build`、`task test`。
- CI 对应门禁包括 `.github/workflows/ci.yml` 中的 `go test ./cmd/... ./pkg/...`、`./scripts/test-coverage.sh`、runtime SDK/package 和 proto-client build；本次不触达 JS runtime、proto 或 proto-client 时，不新增这些包的实现工作。

验收标准：

- 实施范围只包含 runtime driver bootstrap、相关测试和必要文档同步。
- 未引入新配置项、proto 字段、数据库迁移或 Docker manifest 语义变化。

适用的 harness 约束或命令：

```bash
go test ./pkg/driver
task lint
task build
task test
```

## 阶段 2：重构 directory-only bootstrap helper

目标：把 directory-only bootstrap helper 从 Jupyter 专用命令片段提升为可被 driver lifecycle 复用的幂等 guest 命令，并将 `/root` 从 symlink 语义改为 guest 内 bind mount。

依赖：

- 阶段 1 完成。

实施工作：

1. 在 `pkg/driver/boxlite_guest_cgo.go` 保留或重命名 `directoryOnlyGuestSessionBootstrapCommand(config)`，确保它仍位于 `pkg/driver` 内可被 BoxLite/Microsandbox 复用。
2. 保持 `/workspace` 暴露到 `/data/workspace`，不得为 `/data/state`、`/data/runtime`、`/data/logs` 生成自指向 symlink。
3. 将 `/root` 逻辑改为：
   - `/data/home` 缺失时返回非零状态。
   - `/root` 已是 `/data/home` 的 bind mount 时保持不变。
   - 旧版 `/root -> /data/home` symlink 可迁移为真实目录后 bind mount。
   - image 原始 `/root` 首次迁移时保存为 `/root.image`。
   - `/root` 是未知 mount point 时失败，不覆盖。
4. 确保 bootstrap 命令能在 guest cwd `/` 下执行，不依赖 `/workspace` 预先存在。
5. 如需要，增加小型 helper 生成 bootstrap probe 命令，但不新增持久 bootstrap 状态。

测试和验证：

1. 更新 `pkg/driver/runtime_mount_manifest_test.go` 中 `TestDirectoryOnlyGuestSessionBootstrapUsesDataMountRoot`。
2. 新增或扩展 unit tests，断言 bootstrap command 包含 `/root` bind mount、mount point/probe、防 symlink 回退和 `/data/home` 缺失保护。
3. 断言 Docker manifest 测试仍包含 `/root/...` 细粒度 mount，BoxLite/Microsandbox manifest 仍只有 `<session> -> /data`。

验收标准：

- `directoryOnlyGuestSessionBootstrapCommand` 不再生成 `/root -> /data/home` symlink。
- helper 的命令文本可证明不会在 `/data/home` 缺失时删除或移动 `/root`。
- 阶段结束后 `go test ./pkg/driver -run 'TestDirectoryOnly|TestPrepareRuntimeMountManifest|TestRuntimeMountManifest'` 通过。

适用的 harness 约束或命令：

```bash
go test ./pkg/driver -run 'TestDirectoryOnly|TestPrepareRuntimeMountManifest|TestRuntimeMountManifest'
```

## 阶段 3：接入 BoxLite lifecycle 和 exec guard

目标：BoxLite 在无 Jupyter start/resume 和每次 exec 前都能完成或自愈 directory-only bootstrap。

依赖：

- 阶段 2 完成。

实施工作：

1. 在 `pkg/driver/boxlite_cgo.go` 增加 BoxLite 专用 bootstrap 执行方法，例如 `ensureDirectoryOnlyGuestSessionBootstrap(ctx, box, session)` 或等价命名。
2. 在 `EnsureSession` 中，box 创建并 start 成功后执行 bootstrap；复用已有 running box 时也执行 bootstrap guard。
3. 在 `execWithStream` 中，若 stopped box 被重新 start，立即执行 bootstrap；执行用户 `spec` 前执行 bootstrap guard。
4. bootstrap 执行应使用 cwd `/`，避免 `/workspace` 未就绪导致 chdir 失败。
5. bootstrap 失败时返回带 driver、session id 或 box id、stdout/stderr 摘要的错误；原始 command 不执行。
6. 确保 Jupyter path 继续可用。`jupyterLaunchCommand` 可以保留内部 bootstrap，但重复执行必须无副作用。

测试和验证：

1. 用 fake 或可替代 wrapper 增加 deterministic tests，证明：
   - BoxLite 无 Jupyter `EnsureSession` 会执行 bootstrap。
   - BoxLite `Exec`/`ExecStream` 在原始 command 前执行 bootstrap guard。
   - bootstrap 失败时原始 command 不执行。
2. 对现有 BoxLite cgo 单元测试保持兼容；需要 cgo/BoxLite 的真实行为留给 smoke。

验收标准：

- BoxLite 无 Jupyter session 不再依赖 Jupyter launch 才创建 `/workspace` 和 `/root`。
- Existing running BoxLite box 在部署新代码后可通过 exec 前 guard 自愈。
- 阶段结束后 `go test ./pkg/driver` 通过；若本地缺少 BoxLite cgo 依赖，记录跳过原因并至少运行不需要 cgo 的相关测试。

适用的 harness 约束或命令：

```bash
go test ./pkg/driver
```

风险和停止条件：

- 如果 BoxLite guest 不允许 `mount --bind`，停止实现并记录 runtime 能力限制；不得退回 `/root` symlink 方案。
- 如果 BoxLite exec API 无法可靠获取 bootstrap stdout/stderr，至少保留 exit status 和可定位的 command context。

执行记录：

- 2026-07-05 执行 `sg kvm -c 'IMAGE_REGISTRY=registry-mirrors.dev.in.chaitin.net SMOKE_RUNTIME_DRIVERS=boxlite task test:runtime-smoke'` 时，BoxLite runtime 成功越过 host KVM 和 image registry 阶段，但 guest bootstrap 中 `mount --bind /data/home /root` 返回 `permission denied`。
- 该结果已触发 BoxLite 停止条件。后续不得以 `/root -> /data/home` symlink 降级完成；需先确认 BoxLite guest 可获得 bind mount 能力，或回到 spec 重新决策方案。

## 阶段 4：接入 Microsandbox lifecycle 和 exec guard

目标：Microsandbox 在无 Jupyter start/resume 和每次 exec 前都能完成或自愈 directory-only bootstrap。

依赖：

- 阶段 2 完成。
- 阶段 3 的共享 helper 和错误语义可复用。

实施工作：

1. 在 `pkg/driver/microsandbox_runtime.go` 增加 Microsandbox 专用 bootstrap 执行方法，例如 `ensureDirectoryOnlyGuestSessionBootstrap(ctx, sandbox, session)` 或等价命名。
2. 在 `EnsureSession` 中，`getOrCreateSandbox` 返回后对 created、restarted 和已有 running sandbox 执行 bootstrap guard；Jupyter launch 之前必须完成。
3. 在 `Exec` 和 `ExecStream` 中，连接 sandbox 后、执行用户 command 前执行 bootstrap guard。
4. bootstrap 执行使用 cwd `/`，并沿用 `execOptions(ctx, ExecSpec{Cwd: "/"})` 的环境注入策略。
5. bootstrap 失败时返回带 driver、session id 或 sandbox name、stdout/stderr 摘要的错误；原始 command 不执行。

测试和验证：

1. 增加 deterministic tests，证明：
   - Microsandbox 无 Jupyter `EnsureSession` 会执行 bootstrap。
   - Microsandbox `Exec`/`ExecStream` 在原始 command 前执行 bootstrap guard。
   - bootstrap 失败时原始 command 不执行。
2. 保持 Jupyter path 的 readiness 逻辑不被 bootstrap 错误吞掉。

验收标准：

- Microsandbox 无 Jupyter session 不再依赖 `launchJupyter` 才创建 guest compatible paths。
- Existing running Microsandbox sandbox 可通过 exec 前 guard 自愈。
- 阶段结束后 `go test ./pkg/driver` 通过。

适用的 harness 约束或命令：

```bash
go test ./pkg/driver
```

风险和停止条件：

- 如果 Microsandbox guest 不允许 `mount --bind`，停止实现并记录 runtime 能力限制；不得退回 `/root` symlink 方案。
- 如果 `ExecStream` bootstrap 事件流与用户 stream 混淆，必须隔离 bootstrap 输出，不把 bootstrap stdout/stderr 当作用户 command 输出。

## 阶段 5：更新真实 runtime smoke 覆盖

目标：用真实 BoxLite/Microsandbox smoke 证明 directory-only bootstrap 在无 Jupyter start 和 exec 路径中生效。

依赖：

- 阶段 3 完成。
- 阶段 4 完成。

实施工作：

1. 更新 `pkg/driver/runtime_mount_manifest_boxlite_smoke_test.go`，避免只手动 `getOrCreateBox` + `startBox` 后等待旧 marker；优先通过 `EnsureSession` 覆盖 BoxLite lifecycle bootstrap。
2. 更新 `pkg/driver/runtime_mount_manifest_microsandbox_smoke_test.go`，保留 `EnsureSession` 覆盖，并增加对 exec guard 的只读验证。
3. 更新 `pkg/driver/runtime_mount_manifest_smoke_test.go` 的断言：
   - `/root` 是 mount point。
   - `/root` 不是 symlink。
   - `/root/.codex/config.toml`、`/root/.gitconfig`、`/root/.claude.json` 来自 session home。
   - `/workspace` 可作为非 Jupyter command/cell exec 工作目录。
4. 保留 `SMOKE_KEEP_TMP` 失败保留目录能力，便于排查真实 runtime 差异。

测试和验证：

```bash
SMOKE_RUNTIME_DRIVERS=boxlite task test:runtime-smoke
SMOKE_RUNTIME_DRIVERS=microsandbox task test:runtime-smoke
```

验收标准：

- 两个 driver 的 directory-only smoke 都能证明 `/root` bind mount 语义。
- smoke 不依赖 Jupyter readiness 来间接触发 bootstrap。
- OCI image smoke 仍按 `Taskfile.yml` 中既有 `RuntimeMountManifestDirectoryOnlyStarts|UsesGoContainerRegistryOCIImage` 范围运行。

适用的 harness 约束或命令：

```bash
task test:runtime-smoke
```

风险和停止条件：

- 真实 runtime smoke 依赖本机 BoxLite/Microsandbox artifacts。若环境缺失，只能记录未运行原因，不得把未运行写成通过。

## 阶段 6：文档同步和全量质量门禁

目标：完成必要文档同步，并通过仓库权威质量门禁。

依赖：

- 阶段 2 至阶段 5 完成。

实施工作：

1. 如实现后语义与现有设计文档冲突，更新：
   - `docs/design/runtime_mount_manifest_driver_specific_design.md`
   - `docs/design/runtime_environment_variables_design.md`
   - `docs/design/agent-compose-runtime_contract.md`
2. 保持 `docs/spec/directory-only-runtime-bootstrap-spec.md` 与实际实现一致。
3. 不更新 proto-client、runtime SDK package、Docker compose 或 image build 脚本，除非实现实际触达这些边界。

测试和验证：

```bash
task lint
task build
task test
go test ./cmd/... ./pkg/...
```

如本地具备真实 runtime 依赖，再运行：

```bash
SMOKE_RUNTIME_DRIVERS=boxlite task test:runtime-smoke
SMOKE_RUNTIME_DRIVERS=microsandbox task test:runtime-smoke
```

验收标准：

- `task lint`、`task build`、`task test` 通过，或明确记录环境型失败原因。
- CI 相关 Go 测试范围 `go test ./cmd/... ./pkg/...` 通过。
- 无 proto、API、CLI、数据库 schema 或 compose 行为变更。
- 文档不再描述 BoxLite/Microsandbox 通过 `/root -> /data/home` symlink 暴露 home。

适用的 harness 约束或命令：

```bash
task lint
task build
task test
go test ./cmd/... ./pkg/...
```

## 首版不做的事项

- 不改变 Docker driver mount manifest。
- 不增加多个 BoxLite/Microsandbox virtiofs export。
- 不新增 session metadata 字段记录 bootstrap 状态。
- 不新增 `GUEST_HOME` 或自动注入 `HOME`。
- 不通过 `CODEX_HOME`、JS runtime runner 或 provider-specific workaround 代替 guest path bootstrap。
- 不处理 Codex SDK/CLI 版本收敛。
- 不新增 proto、Connect API、CLI flag、数据库迁移或 proto-client 发布工作。

## 计划规则

- 阶段按依赖顺序执行；每个阶段完成后项目应保持可构建、可测试。
- 不混入 runtime cache、image resolver、Jupyter proxy 或 JS runtime provider 的无关重构。
- bootstrap 失败必须阻止原始 command 执行，并返回可诊断错误。
- 如果任一真实 runtime 不支持 guest 内 `mount --bind /data/home /root`，停止后续实现并回到 spec 更新，不得静默降级为 `/root` symlink。
