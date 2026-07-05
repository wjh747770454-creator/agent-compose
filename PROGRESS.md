# Directory-only Runtime Bootstrap Progress

本文档把 directory-only runtime bootstrap 的技术方案和实施计划拆成可独立执行、独立验收的任务清单。任务按依赖顺序排列；标记为“可并行”的子任务可以在同一父任务内用 subagent 并行推进，但 subagent 并发度最高不超过 5。

## 文档索引

- 技术方案：[docs/spec/directory-only-runtime-bootstrap-spec.md](docs/spec/directory-only-runtime-bootstrap-spec.md)
- 实施计划：[docs/plan/directory-only-runtime-bootstrap-implementation-plan.md](docs/plan/directory-only-runtime-bootstrap-implementation-plan.md)
- Harness：[AGENTS.md](AGENTS.md)
- 测试标准：[TESTING.md](TESTING.md)
- 任务命令：[Taskfile.yml](Taskfile.yml)
- CI：[.github/workflows/ci.yml](.github/workflows/ci.yml)
- Runtime mount 设计：[docs/design/runtime_mount_manifest_driver_specific_design.md](docs/design/runtime_mount_manifest_driver_specific_design.md)
- Runtime env 设计：[docs/design/runtime_environment_variables_design.md](docs/design/runtime_environment_variables_design.md)
- Runtime contract：[docs/design/agent-compose-runtime_contract.md](docs/design/agent-compose-runtime_contract.md)

## 执行规则

- [ ] 每个任务完成时必须同时完成对应测试方案和验收标准。
- [ ] 不跨阶段提前合并依赖未满足的功能。
- [ ] 每个行为变更任务都必须写明并运行最小可证明测试；阶段性收口时运行 harness 定义的完整门禁。
- [ ] `task lint`、`task build`、`task test` 是最终常规质量门禁；真实 runtime 变更完成后按环境运行 `task test:runtime-smoke`。
- [ ] 不新增 API、CLI、proto、数据库 schema、`GUEST_HOME` 或 Docker manifest 行为，除非先更新 spec 和 plan。
- [ ] 不把 `/root` 静默降级回 symlink；如果 BoxLite 或 Microsandbox 不支持 guest 内 `mount --bind /data/home /root`，停止实现并更新 spec。
- [ ] 每个任务完成后必须把完成总结写成多行 Markdown 结构，包含 `状态`、`变更`、`验证`、`审计与例外`、`下一目标`。

## 阶段 1：锁定实现边界和测试基线

参考文档：[docs/plan/directory-only-runtime-bootstrap-implementation-plan.md](docs/plan/directory-only-runtime-bootstrap-implementation-plan.md#阶段-1锁定实现边界和测试基线)

- [x] 1.1 复核 runtime driver 边界和测试现状
  - 依赖：无。
  - 工作内容：
    - 复核 `pkg/driver/boxlite_guest_cgo.go` 中 `directoryOnlyGuestSessionBootstrapCommand`、`jupyterLaunchCommand` 的职责。
    - 复核 `pkg/driver/boxlite_cgo.go` 中 `EnsureSession`、`execWithStream`、`executeBox` 的调用顺序。
    - 复核 `pkg/driver/microsandbox_runtime.go` 中 `EnsureSession`、`Exec`、`ExecStream`、`launchJupyter` 的调用顺序。
    - 复核 `pkg/driver/runtime_mount_manifest_test.go`、`pkg/driver/runtime_mount_manifest_*_smoke_test.go` 当前断言。
  - 可并行子任务：
    - [x] 可并行：审计 BoxLite lifecycle 和 exec 路径。
    - [x] 可并行：审计 Microsandbox lifecycle 和 exec 路径。
    - [x] 可并行：审计 manifest/unit/smoke 测试覆盖。
  - 测试方案：本任务为边界复核，不要求运行代码测试；若修改文档或代码，至少运行 `go test ./pkg/driver`。
  - 验收标准：
    - 明确本次只触达 `pkg/driver` bootstrap/lifecycle/smoke 测试及必要文档。
    - 明确不新增配置、proto、数据库迁移、Docker manifest 语义或 JS runtime 主修复。
  - 完成总结：
    - 状态：已完成。
    - 变更：
      - 完成 `pkg/driver` directory-only bootstrap、BoxLite lifecycle/exec、Microsandbox lifecycle/exec、manifest unit tests 和真实 runtime smoke tests 的边界复核。
      - 本任务只更新进度记录，未修改生产代码、测试代码、配置、proto、数据库 schema、Docker manifest 语义或 JS runtime 主逻辑。
      - 明确后续实现范围仍限于 `pkg/driver` bootstrap/lifecycle/smoke 测试及必要设计文档同步。
    - 验证：
      - 已复核 `pkg/driver/boxlite_guest_cgo.go` 中 `directoryOnlyGuestSessionBootstrapCommand` 和 `jupyterLaunchCommand` 的职责。
      - 已复核 `pkg/driver/boxlite_cgo.go` 中 `EnsureSession`、`getOrCreateBox`、`execWithStream`、`executeBox` 的调用顺序。
      - 已复核 `pkg/driver/microsandbox_runtime.go` 中 `EnsureSession`、`getOrCreateSandbox`、`connectSandbox`、`Exec`、`ExecStream`、`launchJupyter` 的调用顺序。
      - 已复核 `pkg/driver/runtime_mount_manifest_test.go`、`pkg/driver/runtime_mount_manifest_smoke_test.go`、`pkg/driver/runtime_mount_manifest_boxlite_smoke_test.go`、`pkg/driver/runtime_mount_manifest_microsandbox_smoke_test.go` 和 `Taskfile.yml` 中的 smoke 任务范围。
      - `go test ./pkg/driver`：通过（cached）。
    - 审计与例外：
      - BoxLite 当前 `jupyterLaunchCommand` 内嵌 `directoryOnlyGuestSessionBootstrapCommand`，而 `buildBoxOptions` 在无 Jupyter 时使用 `sleep infinity`，因此无 Jupyter `EnsureSession` 不会创建 `/workspace` 或 `/root`。
      - BoxLite `getOrCreateBox` 可复用或重启 existing box，`execWithStream` 也会在 stopped box 上 `startBox` 后直接 `executeBox`；两条路径都没有 bootstrap guard，用户 command 可先于 guest path bootstrap 执行。
      - Microsandbox 当前只在 `launchJupyter` 中通过 `jupyterLaunchCommand` 间接执行 bootstrap；`EnsureSession` 的 create/restart/existing running 路径以及 `Exec`/`ExecStream` 的 connect/start 路径都没有独立 bootstrap guard。
      - 当前 `directoryOnlyGuestSessionBootstrapCommand` 仍是 `/root -> /data/home` symlink 方案，且在 `/data/workspace` 或 `/data/home` 缺失时跳过 body 而非失败；后续必须改为 `/root` bind mount、迁移保护和可诊断失败。
      - `jupyterLaunchCommand` 也被 Docker runtime Jupyter 路径复用；后续拆分或改造 helper 时必须保证 Docker driver 不执行 directory-only bootstrap 且 Docker manifest 语义不变。
      - Unit tests 当前已覆盖 Docker 细粒度 mounts、BoxLite/Microsandbox `<session> -> /data` directory-only manifest、driver switch 和 manifest validation；但 bootstrap test 仍显式期待 `/root` symlink。
      - Smoke tests 当前只证明旧 Jupyter-bootstrap 路径下 home 文件可见；BoxLite smoke 绕过 `EnsureSession`，共享 smoke helper 未证明 `/root` 是 mount point、非 symlink、与 `/data/home` 同实体，也未证明非 Jupyter exec cwd `/workspace`。
    - 下一目标：2.1。

## 阶段 2：重构 directory-only bootstrap helper

参考文档：[docs/plan/directory-only-runtime-bootstrap-implementation-plan.md](docs/plan/directory-only-runtime-bootstrap-implementation-plan.md#阶段-2重构-directory-only-bootstrap-helper)

- [x] 2.1 改造共享 bootstrap command
  - 依赖：1.1。
  - 工作内容：
    - 在 `pkg/driver/boxlite_guest_cgo.go` 保留或重命名 `directoryOnlyGuestSessionBootstrapCommand(config)`，保持 `pkg/driver` 内复用。
    - 保持 `/workspace` 暴露到 `/data/workspace`，并避免为 `/data/state`、`/data/runtime`、`/data/logs` 生成自指向 symlink。
    - 将 `/root` 改为 guest 内 `mount --bind /data/home /root`。
    - 覆盖 `/data/home` 缺失、旧 `/root -> /data/home` symlink、image 原始 `/root`、未知 mount point 等错误/迁移语义。
    - 确保 bootstrap 可在 guest cwd `/` 下执行。
  - 可并行子任务：
    - [x] 可并行：设计和实现 `/workspace` 与自定义 guest path 的幂等逻辑。
    - [x] 可并行：设计和实现 `/root` bind mount 与迁移逻辑。
    - [x] 可并行：设计 bootstrap guard/probe 命令文本。
  - 测试方案：
    - 更新并运行 `go test ./pkg/driver -run 'TestDirectoryOnly|TestPrepareRuntimeMountManifest|TestRuntimeMountManifest'`。
  - 验收标准：
    - `directoryOnlyGuestSessionBootstrapCommand` 不再生成 `/root -> /data/home` symlink。
    - `/data/home` 缺失时命令不会删除或移动 `/root`。
    - Docker manifest 仍保持 `/root/...` 细粒度 mount；BoxLite/Microsandbox manifest 仍只有 `<session> -> /data`。
  - 完成总结：
    - 状态：已完成。
    - 变更：
      - 将 `directoryOnlyGuestSessionBootstrapCommand(config)` 改为 fail-fast bootstrap：先验证 `/data/workspace` 和 `/data/home` 存在，再处理 guest compatible paths。
      - 保持 `/workspace -> /data/workspace` 暴露逻辑，并继续跳过 `/data/state`、`/data/runtime`、`/data/logs` 的自指向 symlink。
      - 将 `/root` 从 symlink 改为 guest 内 `mount --bind /data/home /root`，并增加 mount point、非 symlink、source/target directory entity 一致性检查。
      - 增加 `/root` 迁移保护：旧 symlink 会被移除并替换为真实目录；image 原始 `/root` 首次迁移为 `/root.image`；未知 mount point 或非目录目标会失败而不覆盖。
      - 拆分 Jupyter launch command：Docker 继续使用不含 directory-only bootstrap 的 `jupyterLaunchCommand`；BoxLite/Microsandbox 使用 `directoryOnlyJupyterLaunchCommand`。
    - 验证：
      - `go test ./pkg/driver -run 'TestDirectoryOnly|TestPrepareRuntimeMountManifest|TestRuntimeMountManifest'`：通过。
      - `go test ./pkg/driver`：通过。
      - `git diff --check`：通过。
    - 审计与例外：
      - 未新增 API、CLI、proto、数据库 schema、配置项、Docker manifest 语义或 JS runtime 主修复。
      - Docker manifest 测试仍覆盖 `/root/...` 细粒度 mounts；BoxLite/Microsandbox manifest 测试仍覆盖 `<session> -> /data` directory-only mount。
      - 本任务只改造 helper 和 Jupyter bootstrap 调用边界；BoxLite/Microsandbox `EnsureSession`、`Exec`、`ExecStream` 独立 guard 尚未接入，仍按后续 3.x/4.x 任务处理。
      - 真实 BoxLite/Microsandbox smoke 未在本任务运行；按计划留到 runtime lifecycle/exec guard 和 smoke 覆盖阶段。
    - 下一目标：2.2。

- [x] 2.2 扩展 bootstrap unit tests
  - 依赖：2.1。
  - 工作内容：
    - 更新 `pkg/driver/runtime_mount_manifest_test.go` 中 `TestDirectoryOnlyGuestSessionBootstrapUsesDataMountRoot`。
    - 增加断言：`/root` bind mount、mount point/probe、防 symlink 回退、`/data/home` 缺失保护。
    - 增加断言：不为 `/data/state`、`/data/runtime`、`/data/logs` 生成自指向 symlink。
  - 可并行子任务：
    - [x] 可并行：补 bootstrap command 文本测试。
    - [x] 可并行：补 manifest 非回归测试。
  - 测试方案：
    - `go test ./pkg/driver -run 'TestDirectoryOnly|TestPrepareRuntimeMountManifest|TestRuntimeMountManifest'`
    - `go test ./pkg/driver`
  - 验收标准：
    - 相关 unit tests 覆盖 spec 中的 `/root` bind mount 和迁移保护规则。
    - 阶段结束后 `go test ./pkg/driver` 通过，或记录环境型失败原因。
  - 完成总结：
    - 状态：已完成。
    - 变更：
      - 扩展 `TestDirectoryOnlyGuestSessionBootstrapUsesDataMountRoot`，显式断言 `/root` bind mount、mount point/probe、非 symlink guard、source/target entity guard、旧 symlink 迁移、image `/root` 迁移到 `/root.image`、未知 mount point 失败文本。
      - 增加 bootstrap command ordering 断言，证明 `/data/home` 缺失保护发生在任何 `/root` 删除或移动之前。
      - 保留并强化不为 `/data/state`、`/data/runtime`、`/data/logs` 生成自指向 symlink 的非回归断言。
      - 保留 `TestJupyterLaunchCommandDoesNotRunDirectoryOnlyBootstrapByDefault`，证明 Docker 默认 Jupyter command 不包含 directory-only bootstrap，而 BoxLite/Microsandbox 使用的 directory-only Jupyter command 包含 bootstrap。
    - 验证：
      - `go test ./pkg/driver -run 'TestDirectoryOnly|TestPrepareRuntimeMountManifest|TestRuntimeMountManifest'`：通过。
      - `go test ./pkg/driver`：通过。
      - `git diff --check`：通过。
    - 审计与例外：
      - 本任务只修改 bootstrap/manifest unit tests 和进度记录，未新增配置、proto、数据库迁移、Docker manifest 语义或 JS runtime 主修复。
      - 测试覆盖已能证明 spec 中 `/root` bind mount 和迁移保护规则的 command 文本；真实 runtime 是否允许 guest 内 `mount --bind /data/home /root` 仍由后续 BoxLite/Microsandbox smoke 验证。
      - 阶段 2 已完成；BoxLite/Microsandbox lifecycle 和 exec guard 尚未接入，按阶段 3、阶段 4 继续。
    - 下一目标：阶段 3。

## 阶段 3：接入 BoxLite lifecycle 和 exec guard

参考文档：[docs/plan/directory-only-runtime-bootstrap-implementation-plan.md](docs/plan/directory-only-runtime-bootstrap-implementation-plan.md#阶段-3接入-boxlite-lifecycle-和-exec-guard)

- [x] 3.1 在 BoxLite EnsureSession 中执行 bootstrap
  - 依赖：2.2。
  - 工作内容：
    - 在 `pkg/driver/boxlite_cgo.go` 增加 BoxLite 专用 bootstrap 执行方法。
    - 在 box 创建并 start 成功后执行 bootstrap。
    - 复用已有 running box 时执行 bootstrap guard。
    - bootstrap 使用 cwd `/`，错误包含 driver、session id 或 box id、stdout/stderr 摘要。
  - 可并行子任务：
    - [x] 可并行：实现 BoxLite bootstrap 执行 wrapper。
    - [x] 可并行：补 BoxLite EnsureSession 行为测试设计。
  - 测试方案：
    - `go test ./pkg/driver -run 'Test.*BoxLite.*Ensure|Test.*Bootstrap'`
    - `go test ./pkg/driver`
  - 验收标准：
    - BoxLite 无 Jupyter `EnsureSession` 不再依赖 Jupyter launch 才创建 `/workspace` 和 `/root`。
    - bootstrap 失败时 `EnsureSession` 返回错误，session 不被视为 ready。
  - 完成总结：
    - 状态：已完成。
    - 变更：
      - 在 `pkg/driver/boxlite_cgo.go` 增加 `ensureDirectoryOnlyGuestSessionBootstrap`，通过 BoxLite exec 执行 directory-only bootstrap。
      - `EnsureSession` 在新建 box start 成功后、已有 box 被 `getOrCreateBox` 复用或重启后，统一执行 bootstrap，再进入 Jupyter readiness 检查。
      - bootstrap exec 使用 `directoryOnlyGuestSessionBootstrapExecSpec`，固定 `Command=sh`、`Args=-lc <bootstrap>`、`Cwd=/`，避免 `/workspace` 尚未就绪时 chdir 失败。
      - bootstrap 失败时通过 `formatDirectoryOnlyGuestSessionBootstrapError` 返回包含 driver、session id、box/runtime id、exit code、stdout/stderr 摘要或底层 exec error 的诊断错误。
      - 增加默认构建可运行的测试，覆盖 bootstrap exec spec 和错误上下文；同时用 `boxlitecgo` build tag 编译验证 cgo 路径。
    - 验证：
      - `go test ./pkg/driver -run 'Test.*BoxLite.*Ensure|Test.*Bootstrap'`：通过。
      - `go test ./pkg/driver`：通过。
      - `go test -tags boxlitecgo ./pkg/driver -run 'Test.*BoxLite.*Ensure|Test.*Bootstrap'`：通过。
      - `go test -tags boxlitecgo ./pkg/driver`：通过。
      - `git diff --check`：通过。
    - 审计与例外：
      - 本任务只接入 BoxLite `EnsureSession` lifecycle bootstrap；`Exec`/`ExecStream` 前 guard 尚未接入，按 3.2 继续。
      - 未新增 API、CLI、proto、数据库 schema、配置项、Docker manifest 语义或 JS runtime 主修复。
      - BoxLite Jupyter command 仍保留内部 directory-only bootstrap，`EnsureSession` 后续 bootstrap 必须保持幂等；当前 helper 已按阶段 2 实现幂等保护。
      - 真实 BoxLite smoke 未在本任务运行；按阶段 5 使用 `SMOKE_RUNTIME_DRIVERS=boxlite task test:runtime-smoke` 验证真实 runtime bind mount 能力。
    - 下一目标：3.2。

- [x] 3.2 在 BoxLite Exec/ExecStream 前执行 bootstrap guard
  - 依赖：3.1。
  - 工作内容：
    - 在 `execWithStream` 中处理 stopped box 重新 start 后的 bootstrap。
    - 在执行用户 `ExecSpec` 前执行 bootstrap guard。
    - bootstrap 失败时不执行原始 command。
    - 避免 bootstrap stdout/stderr 混入用户 command 输出。
  - 可并行子任务：
    - [x] 可并行：实现 exec 前 guard 调用。
    - [x] 可并行：补 bootstrap 失败不执行原始 command 的测试。
  - 测试方案：
    - `go test ./pkg/driver -run 'Test.*BoxLite.*Exec|Test.*Bootstrap'`
    - `go test ./pkg/driver`
  - 验收标准：
    - Existing running BoxLite box 可通过 exec 前 guard 自愈。
    - 原始 command 只在 bootstrap 成功后执行。
  - 完成总结：
    - 状态：已完成。
    - 变更：
      - `Exec` 和 `ExecStream` 不再丢弃 `Session`，并将 session context 传入 `execWithStream`，用于 bootstrap 失败诊断。
      - `execWithStream` 在获取 box、必要时 `startBox` 后，先执行 `ensureDirectoryOnlyGuestSessionBootstrap`，bootstrap 成功后才执行用户 `ExecSpec`。
      - bootstrap guard 继续通过 nil stream 调用 `executeBox`，因此 bootstrap stdout/stderr 不会写入用户 command stream；失败时只进入诊断错误。
      - 新增 `executeUserCommandAfterBootstrap` 的 deterministic tests，证明 bootstrap 失败时原始 command 不执行，bootstrap 成功时才返回用户 command 结果。
    - 验证：
      - `go test ./pkg/driver -run 'Test.*BoxLite.*Exec|Test.*Bootstrap'`：通过。
      - `go test ./pkg/driver`：通过。
      - `go test -tags boxlitecgo ./pkg/driver -run 'Test.*BoxLite.*Exec|Test.*Bootstrap'`：通过。
      - `go test -tags boxlitecgo ./pkg/driver`：通过。
      - `git diff --check`：通过。
    - 审计与例外：
      - 本任务只接入 BoxLite `Exec`/`ExecStream` 前 bootstrap guard，未触达 Microsandbox、API、CLI、proto、数据库 schema、配置项、Docker manifest 语义或 JS runtime 主修复。
      - Existing running BoxLite box 和 stopped 后由 `execWithStream` 重新 `startBox` 的路径都会在用户 command 前执行 bootstrap guard。
      - 真实 BoxLite smoke 未在本任务运行；真实 runtime bind mount 与 exec guard 行为按阶段 5 验证。
    - 下一目标：阶段 4。

## 阶段 4：接入 Microsandbox lifecycle 和 exec guard

参考文档：[docs/plan/directory-only-runtime-bootstrap-implementation-plan.md](docs/plan/directory-only-runtime-bootstrap-implementation-plan.md#阶段-4接入-microsandbox-lifecycle-和-exec-guard)

- [x] 4.1 在 Microsandbox EnsureSession 中执行 bootstrap
  - 依赖：2.2。
  - 工作内容：
    - 在 `pkg/driver/microsandbox_runtime.go` 增加 Microsandbox 专用 bootstrap 执行方法。
    - 在 `getOrCreateSandbox` 返回后，对 created、restarted 和已有 running sandbox 执行 bootstrap guard。
    - 保证 Jupyter launch 之前已完成 bootstrap。
    - bootstrap 使用 cwd `/`，错误包含 driver、session id 或 sandbox name、stdout/stderr 摘要。
  - 可并行子任务：
    - [x] 可并行：实现 Microsandbox bootstrap 执行 wrapper。
    - [x] 可并行：补 Microsandbox EnsureSession 行为测试设计。
  - 测试方案：
    - `go test ./pkg/driver -run 'Test.*Microsandbox.*Ensure|Test.*Bootstrap'`
    - `go test ./pkg/driver`
  - 验收标准：
    - Microsandbox 无 Jupyter `EnsureSession` 不再依赖 `launchJupyter` 才创建 guest compatible paths。
    - bootstrap 失败时 `EnsureSession` 返回错误，session 不被视为 ready。
  - 完成总结：
    - 状态：已完成。
    - 变更：
      - 在 `pkg/driver/microsandbox_runtime.go` 增加 `ensureDirectoryOnlyGuestSessionBootstrap`，通过 `sandbox.Exec` 执行 directory-only bootstrap。
      - `EnsureSession` 在 `getOrCreateSandbox` 返回后立即执行 bootstrap，覆盖 created、restarted 和 existing running sandbox，再进入 Jupyter launch/readiness 逻辑。
      - bootstrap exec 复用 `directoryOnlyGuestSessionBootstrapExecSpec` 和 `execOptions(ctx, spec)`，固定 cwd `/`，避免 `/workspace` 尚未就绪时 chdir 失败。
      - bootstrap 失败时复用 `formatDirectoryOnlyGuestSessionBootstrapError`，返回包含 driver、session id、sandbox/runtime id、exit code、stdout/stderr 摘要或底层 exec error 的诊断错误。
      - 增加 Microsandbox 命名的 focused tests，覆盖 bootstrap exec spec 和错误上下文。
    - 验证：
      - `go test ./pkg/driver -run 'Test.*Microsandbox.*Ensure|Test.*Bootstrap'`：通过。
      - `go test ./pkg/driver`：通过。
      - `git diff --check`：通过。
    - 审计与例外：
      - 本任务只接入 Microsandbox `EnsureSession` lifecycle bootstrap；`Exec`/`ExecStream` 前 guard 尚未接入，按 4.2 继续。
      - 未新增 API、CLI、proto、数据库 schema、配置项、Docker manifest 语义或 JS runtime 主修复。
      - 真实 Microsandbox smoke 未在本任务运行；真实 runtime bind mount 能力和 exec guard 行为按阶段 5 验证。
    - 下一目标：4.2。

- [x] 4.2 在 Microsandbox Exec/ExecStream 前执行 bootstrap guard
  - 依赖：4.1。
  - 工作内容：
    - 在 `Exec` 和 `ExecStream` 连接 sandbox 后、执行用户 command 前执行 bootstrap guard。
    - 沿用 `execOptions(ctx, ExecSpec{Cwd: "/"})` 的环境注入策略。
    - bootstrap 失败时不执行原始 command。
    - 隔离 bootstrap 输出，避免混入用户 stream。
  - 可并行子任务：
    - [x] 可并行：实现 `Exec` guard。
    - [x] 可并行：实现 `ExecStream` guard。
    - [x] 可并行：补 bootstrap 失败不执行原始 command 的测试。
  - 测试方案：
    - `go test ./pkg/driver -run 'Test.*Microsandbox.*Exec|Test.*Bootstrap'`
    - `go test ./pkg/driver`
  - 验收标准：
    - Existing running Microsandbox sandbox 可通过 exec 前 guard 自愈。
    - 原始 command 只在 bootstrap 成功后执行。
  - 完成总结：
    - 状态：已完成。
    - 变更：
      - `Exec` 在连接 sandbox 后、执行用户 command 前先调用 `ensureDirectoryOnlyGuestSessionBootstrap`。
      - `ExecStream` 在连接 sandbox 后、创建用户 stream 前先调用 `ensureDirectoryOnlyGuestSessionBootstrap`。
      - 两条路径都复用 `executeUserCommandAfterBootstrap`，确保 bootstrap 失败时不会执行原始 command。
      - bootstrap guard 使用 `sandbox.Exec` 且不接入用户 stream，因此 bootstrap stdout/stderr 不会混入用户 `ExecStream` 输出。
      - 增加 Microsandbox 命名的 deterministic tests，证明 bootstrap 失败不执行用户 command，bootstrap 成功后才返回用户 command 结果。
    - 验证：
      - `go test ./pkg/driver -run 'Test.*Microsandbox.*Exec|Test.*Bootstrap'`：通过。
      - `go test ./pkg/driver`：通过。
      - `git diff --check`：通过。
    - 审计与例外：
      - 本任务只接入 Microsandbox `Exec`/`ExecStream` 前 bootstrap guard，未触达 API、CLI、proto、数据库 schema、配置项、Docker manifest 语义或 JS runtime 主修复。
      - Existing running Microsandbox sandbox 和 stopped 后由 `connectSandbox(..., true)` 重新 start 的路径都会在用户 command 前执行 bootstrap guard。
      - 真实 Microsandbox smoke 未在本任务运行；真实 runtime bind mount 与 exec guard 行为按阶段 5 验证。
    - 下一目标：阶段 5。

## 阶段 5：更新真实 runtime smoke 覆盖

参考文档：[docs/plan/directory-only-runtime-bootstrap-implementation-plan.md](docs/plan/directory-only-runtime-bootstrap-implementation-plan.md#阶段-5更新真实-runtime-smoke-覆盖)

- [ ] 5.1 更新 BoxLite smoke 覆盖
  - 依赖：3.2。
  - 工作内容：
    - 更新 `pkg/driver/runtime_mount_manifest_boxlite_smoke_test.go`，优先通过 `EnsureSession` 覆盖 BoxLite lifecycle bootstrap。
    - 避免只手动 `getOrCreateBox` + `startBox` 后等待旧 marker。
    - 增加 `/root` 是 mount point、不是 symlink、home 文件来自 session home 的验证。
  - 可并行子任务：
    - [x] 可并行：更新 smoke lifecycle 路径。
    - [x] 可并行：更新 `/root` bind mount 断言 helper。
  - 测试方案：
    - `SMOKE_RUNTIME_DRIVERS=boxlite task test:runtime-smoke`
    - 若环境缺失，记录未运行原因，并至少运行 `go test ./pkg/driver`。
  - 验收标准：
    - BoxLite smoke 不依赖 Jupyter readiness 间接触发 bootstrap。
    - BoxLite smoke 能证明 `/root` bind mount 语义。
  - 完成总结：
    - 状态：未完成；已触发 BoxLite runtime 能力停止条件。
    - 变更：
      - `TestSmokeBoxLiteRuntimeMountManifestDirectoryOnlyStarts` 和 OCI image smoke 均改为通过 `runtime.EnsureSession` 覆盖 BoxLite lifecycle bootstrap，不再手动 `getOrCreateBox` + `startBox`。
      - 增加 `assertBoxLiteRuntimeSmokeGuestPaths`，通过 BoxLite 内部 `executeBox` 直接检查 `EnsureSession` 后的 guest path 状态，避免 public `Exec` guard 掩盖 lifecycle bootstrap 缺失。
      - BoxLite smoke 现在验证 `/root` 是 mount point、不是 symlink、`/root` 与 `/data/home` 指向同一目录实体，且 `/root/.codex/config.toml`、`/root/.gitconfig`、`/root/.claude.json` 来自 session home。
      - BoxLite smoke 现在验证 `/workspace -> /data/workspace` 并可作为 guest cwd，同时写入 home/state marker 供 host-side 持久化断言复用。
    - 验证：
      - `go test -tags boxlitecgo ./pkg/driver -run '^TestSmokeBoxLiteRuntimeMountManifestDirectoryOnlyStarts$'`：通过（未设置 `SMOKE_RUNTIME_DRIVERS`，测试按 smoke gate skip，但完成编译）。
      - `go test ./pkg/driver`：通过（cached）。
      - `go test -tags boxlitecgo ./pkg/driver`：通过。
      - `git diff --check`：通过。
      - `SMOKE_RUNTIME_DRIVERS=boxlite task test:runtime-smoke`：初次失败，host 环境阻塞为 `create runtime: unsupported: /dev/kvm: permission denied`。
      - `sudo setfacl -m u:$(id -un):rw /dev/kvm` 后重跑 `SMOKE_RUNTIME_DRIVERS=boxlite task test:runtime-smoke`：仍失败，BoxLite cgo runtime re-open `/dev/kvm` panic。
      - `sudo usermod -aG kvm $(id -un)` 后通过 `sg kvm -c 'SMOKE_RUNTIME_DRIVERS=boxlite task test:runtime-smoke'` 重跑：KVM 阻塞解除，但默认 `docker.io/library/debian:bookworm-slim` manifest 拉取失败。
      - `sg kvm -c 'IMAGE_REGISTRY=registry-mirrors.dev.in.chaitin.net SMOKE_RUNTIME_DRIVERS=boxlite task test:runtime-smoke'`：BoxLite runtime 启动并进入 bootstrap，失败于 `directory-only guest bootstrap failed ... stderr="mount: /root: permission denied ... directory-only home target is not a mount point /root"`。
    - 审计与例外：
      - 本任务只更新 BoxLite smoke 覆盖，未触达 API、CLI、proto、数据库 schema、配置项、Docker manifest 语义或 JS runtime 主修复。
      - 已通过 sudo/`sg kvm` 排除 host KVM 组权限问题，BoxLite guest 内 `mount --bind /data/home /root` 返回 permission denied。
      - 该结果命中 spec/plan 停止条件：不得退回 `/root -> /data/home` symlink；停止后续实现并更新 spec/plan，等待 BoxLite runtime 能力或产品方案决策。
      - Microsandbox smoke 覆盖暂不推进，避免在 BoxLite 停止条件未解决时继续扩大实现面。
    - 下一目标：停止实现并更新 spec/plan 记录 BoxLite bind mount runtime 能力限制。

- [ ] 5.2 更新 Microsandbox smoke 覆盖
  - 依赖：4.2。
  - 工作内容：
    - 更新 `pkg/driver/runtime_mount_manifest_microsandbox_smoke_test.go`，保留 `EnsureSession` 覆盖。
    - 增加对 exec guard 的只读验证。
    - 复用 `/root` mount point、非 symlink、home 文件来源断言。
  - 可并行子任务：
    - [ ] 可并行：更新 EnsureSession smoke 断言。
    - [ ] 可并行：更新 exec guard smoke 断言。
  - 测试方案：
    - `SMOKE_RUNTIME_DRIVERS=microsandbox task test:runtime-smoke`
    - 若环境缺失，记录未运行原因，并至少运行 `go test ./pkg/driver`。
  - 验收标准：
    - Microsandbox smoke 不依赖 Jupyter readiness 间接触发 bootstrap。
    - Microsandbox smoke 能证明 `/root` bind mount 语义和 exec guard 生效。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：5.3。

- [ ] 5.3 收口 smoke helper 与 OCI smoke 非回归
  - 依赖：5.1、5.2。
  - 工作内容：
    - 更新 `pkg/driver/runtime_mount_manifest_smoke_test.go` 的共享断言。
    - 保留 `SMOKE_KEEP_TMP` 失败保留目录能力。
    - 确认 `RuntimeMountManifestDirectoryOnlyStarts|UsesGoContainerRegistryOCIImage` 仍匹配 `Taskfile.yml` 中 smoke 任务。
  - 可并行子任务：
    - [ ] 可并行：审计 BoxLite smoke task 正则与测试名。
    - [ ] 可并行：审计 Microsandbox smoke task 正则与测试名。
  - 测试方案：
    - `SMOKE_RUNTIME_DRIVERS=boxlite task test:runtime-smoke`
    - `SMOKE_RUNTIME_DRIVERS=microsandbox task test:runtime-smoke`
    - `go test ./pkg/driver`
  - 验收标准：
    - 两个 driver 的 smoke 覆盖均能证明 `/root` bind mount 语义。
    - OCI image smoke 仍按既有 Taskfile 范围执行。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：阶段 6。

## 阶段 6：文档同步和全量质量门禁

参考文档：[docs/plan/directory-only-runtime-bootstrap-implementation-plan.md](docs/plan/directory-only-runtime-bootstrap-implementation-plan.md#阶段-6文档同步和全量质量门禁)

- [ ] 6.1 同步设计文档与 spec
  - 依赖：5.3。
  - 工作内容：
    - 如果实现语义与现有设计文档冲突，更新：
      - `docs/design/runtime_mount_manifest_driver_specific_design.md`
      - `docs/design/runtime_environment_variables_design.md`
      - `docs/design/agent-compose-runtime_contract.md`
    - 保持 `docs/spec/directory-only-runtime-bootstrap-spec.md` 与实际实现一致。
    - 不更新 proto-client、runtime SDK package、Docker compose 或 image build 脚本，除非实现实际触达这些边界。
  - 可并行子任务：
    - [ ] 可并行：审计 runtime mount manifest 设计文档。
    - [ ] 可并行：审计 runtime env 设计文档。
    - [ ] 可并行：审计 runtime contract 文档。
    - [ ] 可并行：审计 spec 与实现一致性。
  - 测试方案：
    - 文档任务不单独要求代码测试；如同时修改代码，运行对应阶段最小测试。
  - 验收标准：
    - 文档不再描述 BoxLite/Microsandbox 通过 `/root -> /data/home` symlink 暴露 home。
    - 文档明确 BoxLite/Microsandbox 仍只导出 `<session> -> /data`。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：6.2。

- [ ] 6.2 运行全量质量门禁并记录例外
  - 依赖：6.1。
  - 工作内容：
    - 运行常规门禁。
    - 运行 CI Go 测试范围。
    - 在具备真实 runtime 依赖时运行 BoxLite/Microsandbox smoke。
    - 记录无法运行的门禁、环境缺失和残余风险。
  - 可并行子任务：
    - [ ] 可并行：运行 `task lint`。
    - [ ] 可并行：运行 `task build`。
    - [ ] 可并行：运行 `task test`。
    - [ ] 可并行：运行 `go test ./cmd/... ./pkg/...`。
    - [ ] 可并行：运行真实 runtime smoke，环境允许时。
  - 测试方案：
    - `task lint`
    - `task build`
    - `task test`
    - `go test ./cmd/... ./pkg/...`
    - `SMOKE_RUNTIME_DRIVERS=boxlite task test:runtime-smoke`
    - `SMOKE_RUNTIME_DRIVERS=microsandbox task test:runtime-smoke`
  - 验收标准：
    - 常规门禁通过，或完成总结中明确记录环境型失败原因。
    - 无 proto、API、CLI、数据库 schema 或 compose 行为变更。
    - 真实 runtime smoke 的运行结果或未运行原因被记录。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：无。
