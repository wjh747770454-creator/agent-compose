# Directory-only runtime bootstrap implementation plan

## 阶段 1：方案重定向和边界复核

目标：把已阻塞的 `/root` bind mount 方案重定向为“统一逻辑挂载清单 + directory-only home 条目 symlink”方案，并确认实现边界仍只在 runtime driver bootstrap/lifecycle/smoke 和必要文档内。

依赖：

- `docs/spec/directory-only-runtime-bootstrap-spec.md`
- `docs/design/runtime_mount_manifest_driver_specific_design.md`
- `AGENTS.md`
- `TESTING.md`
- `Taskfile.yml`
- `.github/workflows/ci.yml`

实施工作：

1. 保留 BoxLite `mount --bind /data/home /root` 失败记录，作为放弃 bind mount 目标的决策依据。
2. 明确新目标：
   - 不整体替换 `/root`。
   - 不创建 `/root -> /data/home` symlink。
   - 只对声明的 home 条目创建 `/root/... -> /data/home/...` symlink。
3. 明确 Docker、BoxLite、Microsandbox 必须共享同一套逻辑挂载清单。
4. 明确不新增 API、CLI、proto、数据库 schema、`GUEST_HOME`、`HOME` 注入或 JS runtime provider workaround。

测试和验证：

- 本阶段是文档和边界重定向，不要求代码测试。
- 若同时修改代码，至少运行 `go test ./pkg/driver`。

验收标准：

- spec/plan/PROGRESS/design 文档不再把 `/root` bind mount 作为目标语义。
- 文档明确 `/root` 保持真实目录，home 条目按逻辑清单 symlink。
- 文档明确统一逻辑清单是后续实现的 source of truth。

## 阶段 2：抽象统一逻辑挂载清单

目标：让 Docker、BoxLite、Microsandbox 都从同一套逻辑清单派生 mounts/bootstrap，避免 provider home 路径分散维护。

依赖：

- 阶段 1 完成。

实施工作：

1. 在 `pkg/driver` 增加逻辑挂载条目抽象，例如 `runtimeMountEntries(config)` 或等价 helper。
2. 条目包含：
   - session-relative source，例如 `workspace`、`home/.codex`。
   - guest path，例如 `/workspace`、`/root/.codex`。
   - source 类型：directory 或 file。
   - directory-only 暴露策略：symlink、already-in-data 或 none。
3. 把 Docker manifest generation 改为从逻辑清单生成细粒度 bind mounts。
4. 把 BoxLite/Microsandbox manifest generation 改为从同一逻辑清单确认 `<session> -> /data` 语义，并为 bootstrap 提供 home/workspace 条目。
5. 保持 manifest JSON 的 driver-specific applied mount 输出不破坏现有 loader；除非确需改 manifest schema，否则不提升版本。

测试和验证：

```bash
go test ./pkg/driver -run 'TestPrepareRuntimeMountManifest|TestRuntimeMountManifest'
go test ./pkg/driver
```

验收标准：

- Docker manifest 与当前细粒度 mount 语义等价。
- BoxLite/Microsandbox manifest 仍只有 `<session> -> /data`。
- 新增测试证明 Docker 和 directory-only bootstrap 来源于同一逻辑清单。
- 不新增配置、proto、数据库 schema 或 Docker compose 行为。

## 阶段 3：改造 directory-only bootstrap 为 home 条目 symlink

目标：替换失败的 `/root` bind mount bootstrap，改为真实 `/root` + 声明 home 条目 symlink。

依赖：

- 阶段 2 完成。

实施工作：

1. 更新 `directoryOnlyGuestSessionBootstrapCommand(config)`：
   - 验证 `/data/workspace` 和 `/data/home`。
   - 创建或修复 `/workspace -> /data/workspace`。
   - 保持 `/root` 为真实目录；如果 `/root` 是旧的整体 symlink，迁移为真实目录。
   - 对逻辑清单中的 home 条目创建 `/root/... -> /data/home/...` symlink。
   - 为 symlink source 和 parent 创建必要目录或文件 parent。
   - 对未知 mount point、非预期 target 类型、会覆盖 image 重要内容的情况 fail fast。
2. 删除 bootstrap 中的 `mount --bind /data/home /root` 逻辑。
3. 保持 bootstrap 使用 cwd `/`。
4. 保持 bootstrap 输出隔离，不混入用户 command stream。

测试和验证：

```bash
go test ./pkg/driver -run 'TestDirectoryOnly|Test.*Bootstrap'
go test ./pkg/driver
```

验收标准：

- bootstrap command 不包含 `mount --bind /data/home /root`。
- bootstrap command 不生成 `/root -> /data/home`。
- unit tests 覆盖 `/root` 真实目录、home 条目 symlink、旧整体 `/root` symlink 迁移、`/data/home` 缺失保护。
- BoxLite/Microsandbox `EnsureSession`、`Exec`、`ExecStream` 仍会在用户 command 前运行 bootstrap guard。

## 阶段 4：更新 BoxLite/Microsandbox smoke 覆盖

目标：用真实 runtime smoke 证明 directory-only bootstrap 在无 Jupyter start 和 exec 路径中生效，且不依赖 guest bind mount 权限。

依赖：

- 阶段 3 完成。

实施工作：

1. 更新 `pkg/driver/runtime_mount_manifest_boxlite_smoke_test.go`：
   - 继续通过 `EnsureSession` 覆盖 lifecycle bootstrap。
   - 验证 `/root` 是目录且不是 `/data/home` symlink。
   - 验证声明 home 条目解析到 `/data/home/...`。
   - 验证写入声明 home 条目后 host session home 可见。
2. 更新 `pkg/driver/runtime_mount_manifest_microsandbox_smoke_test.go`：
   - 保留 `EnsureSession` 覆盖。
   - 增加 exec guard 只读验证。
   - 复用 home 条目 symlink 断言。
3. 更新共享 smoke helper：
   - 不再要求 `/root` 是 mount point。
   - 保留 `SMOKE_KEEP_TMP` 失败保留目录能力。
   - 确认 `RuntimeMountManifestDirectoryOnlyStarts|UsesGoContainerRegistryOCIImage` 仍匹配 `Taskfile.yml` 中 smoke 任务。

测试和验证：

```bash
SMOKE_RUNTIME_DRIVERS=boxlite task test:runtime-smoke
SMOKE_RUNTIME_DRIVERS=microsandbox task test:runtime-smoke
go test ./pkg/driver
```

验收标准：

- 两个 driver 的 smoke 都能证明 directory-only bootstrap 不依赖 Jupyter readiness。
- 两个 driver 的 smoke 都能证明声明 home 条目持久化到 `<session>/home`。
- OCI image smoke 仍按既有 Taskfile 范围执行。
- 若真实 runtime 环境缺失，只能记录未运行原因，不得把未运行写成通过。

## 阶段 5：文档同步和全量质量门禁

目标：完成必要设计文档同步，并通过仓库权威质量门禁。

依赖：

- 阶段 2 至阶段 4 完成。

实施工作：

1. 更新：
   - `docs/design/runtime_mount_manifest_driver_specific_design.md`
   - `docs/zh-CN/design/runtime_mount_manifest_driver_specific_design.md`
   - `docs/design/runtime_environment_variables_design.md`，如 home 语义描述需要同步。
   - `docs/design/agent-compose-runtime_contract.md`，如 runtime contract 描述需要同步。
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
- 文档不再描述 BoxLite/Microsandbox 通过 `/root` bind mount 或整体 `/root` symlink 暴露 home。

## 首版不做的事项

- 不改变 Docker runtime 可使用 file bind 的能力。
- 不增加多个 BoxLite/Microsandbox virtiofs export。
- 不新增 session metadata 字段记录 bootstrap 状态。
- 不新增 `GUEST_HOME` 或自动注入 `HOME`。
- 不通过 `CODEX_HOME`、JS runtime runner 或 provider-specific workaround 代替 guest path bootstrap。
- 不处理 Codex SDK/CLI 版本收敛。
- 不新增 proto、Connect API、CLI flag、数据库迁移或 proto-client 发布工作。
- 不保证未声明 `/root` 子路径持久化。

## 计划规则

- 阶段按依赖顺序执行；每个阶段完成后项目应保持可构建、可测试。
- 不混入 runtime cache、image resolver、Jupyter proxy 或 JS runtime provider 的无关重构。
- bootstrap 失败必须阻止原始 command 执行，并返回可诊断错误。
- 不允许退回整个 `/root -> /data/home` symlink。
- 不再要求 guest 内 `mount --bind /data/home /root`；BoxLite bind mount 阻塞已作为方案变更依据保留。
