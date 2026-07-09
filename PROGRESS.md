# Sandbox Naming Progress

本文档把 sandbox 命名收敛的技术规格和实施计划拆成可独立执行、独立验收的任务清单。任务按依赖顺序排列；标记为“可并行”的子任务可以在同一父任务内用 subagent 并行推进，但 subagent 并发度最高不超过 5。

## 文档索引

- 技术规格：[docs/spec/sandbox-naming-spec.md](docs/spec/sandbox-naming-spec.md)
- 实施计划：[docs/plan/sandbox-naming-implementation-plan.md](docs/plan/sandbox-naming-implementation-plan.md)
- Harness：[AGENTS.md](AGENTS.md)
- 测试标准：[TESTING.md](TESTING.md)
- 任务命令：[Taskfile.yml](Taskfile.yml)
- CI 配置：[.github/workflows/ci.yml](.github/workflows/ci.yml)
- 核心设计文档：
  - [docs/design/agent-compose_design.md](docs/design/agent-compose_design.md)
  - [docs/design/agent-compose-runtime_contract.md](docs/design/agent-compose-runtime_contract.md)
  - [docs/design/runtime_environment_variables_design.md](docs/design/runtime_environment_variables_design.md)
  - [docs/design/runtime_mount_manifest_design.md](docs/design/runtime_mount_manifest_design.md)
  - [docs/design/runtime_mount_manifest_driver_specific_design.md](docs/design/runtime_mount_manifest_driver_specific_design.md)
  - [docs/design/octobus_integration.md](docs/design/octobus_integration.md)
  - [docs/design/webhook_design.md](docs/design/webhook_design.md)
  - [docs/design/sandbox-naming-residual-audit.md](docs/design/sandbox-naming-residual-audit.md)
- 部署和用户文档：
  - [README.md](README.md)
  - [.env.example](.env.example)
  - [Dockerfile](Dockerfile)
  - [docker-compose.yml](docker-compose.yml)
  - [docker-compose.override.yml](docker-compose.override.yml)
  - [docs/command-line-manual.md](docs/command-line-manual.md)
  - [docs/zh-CN/command-line-manual.md](docs/zh-CN/command-line-manual.md)

## 执行规则

- [ ] 每个任务完成时必须同时完成对应测试方案和验收标准。
- [ ] 不跨阶段提前合并依赖未满足的功能；允许在同一父任务内并行推进已标记的独立子任务。
- [ ] 涉及生成代码、脚本枚举、质量门禁或覆盖率范围时，必须同步更新相关脚本、生成产物和文档。
- [ ] v1 wire contract 是硬约束：不得修改 `proto/agentcompose/v1/agentcompose.proto`、v1 generated Go 或 v1 Connect service 名称。
- [ ] v2 是破坏式清理面：删除或重命名 v2 `session_id` 字段时必须按 spec 保留 field number/name reserve 策略。
- [ ] 旧 `<DATA_ROOT>/sessions`、旧 SQLite schema 和旧 env 不做自动迁移；必须显式拒绝并给出可诊断错误。
- [ ] 每个任务完成后必须把完成总结写成多行 Markdown 结构，包含 `状态`、`变更`、`验证`、`审计与例外`、`下一目标`。
- [ ] 阶段性收口运行 focused tests；最终收口必须运行 `task lint`、`task build`、`task test` 和 CI 等价补充门禁。

## 1. 阶段 1：建立命名边界和安全基线

参考文档：[docs/plan/sandbox-naming-implementation-plan.md](docs/plan/sandbox-naming-implementation-plan.md#阶段-1建立命名边界和安全基线)

- [x] 1.1 建立残留审计分类和 characterization tests
  - 依赖：无。
  - 工作内容：
    - 新增或更新本次重命名的残留审计说明，明确允许保留 `session` 命名的类别：v1 compatibility、deprecated aliases、auth/browser session、provider-native protocol、migration/error 文案。
    - 增加最小 characterization tests，锁定当前 v1 `SessionService`、v2 `SandboxService`、CLI `inspect session` alias、loader `sessionPolicy/sessionEnv` alias、runtime `sessionId` payload 解析行为。
    - 在测试命名或注释中标出 v1 compatibility 与 sandbox-native 边界，避免后续把 v1 字段泄漏回内部模型。
  - 可并行子任务：
    - [x] 可并行：审计 `cmd pkg proto runtime docs` 中的 `session` 命名并形成允许残留类别清单。
    - [x] 可并行：为 v1/v2 API handler 编写 characterization tests。
    - [x] 可并行：为 CLI、loader alias、runtime parser 编写 characterization tests。
  - 测试方案：
    - `go test ./pkg/agentcompose/api ./pkg/agentcompose/app ./cmd/agent-compose`
    - `cd runtime/javascript && npm run test:unit`
    - `cd runtime/agent-compose-runtime-sdk && npm test`
  - 验收标准：
    - 当前兼容行为被测试固定，后续重命名破坏行为时测试会失败。
    - v1 proto 和 v1 generated code 无 diff。
    - 残留审计分类可被后续最终审计复用。
  - 完成总结：
    - 状态：已完成。
    - 变更：
      - 新增 `docs/design/sandbox-naming-residual-audit.md`，记录 v1 compatibility、deprecated aliases、auth/browser session、provider-native protocol、migration/error 文案五类允许残留，并纳入本文档索引。
      - 新增 v1 compatibility mapping characterization，锁定 v1 `session_id`、`agent_session_id` 和 secret env redaction wire 行为。
      - 新增 v2 `RemoveSandbox` characterization，锁定 `sandbox_id` 输入/输出、running sandbox force 语义、内部 session compatibility delegate 调用和 dashboard notification 行为。
      - 强化 CLI `inspect session` deprecated alias 测试，要求其 JSON 输出与 `inspect sandbox` 一致。
      - 强化 loader deprecated alias 测试，锁定 `sessionPolicy/session_policy`、`sessionEnv/session_env` 在 `scheduler.agent` 和 `scheduler.exec` 中的当前映射。
      - 增加 runtime JS provider state parser characterization，锁定缺失 `sessionId` 返回 null、空白 `sessionId` 当前仍作为兼容字符串读取。
    - 验证：
      - `go test ./pkg/agentcompose/api ./pkg/agentcompose/app ./cmd/agent-compose`
      - `go test ./pkg/loaders`
      - `cd runtime/javascript && npm run test:unit`
      - `cd runtime/agent-compose-runtime-sdk && npm test`
    - 审计与例外：
      - 本任务未修改 `proto/agentcompose/v1/agentcompose.proto`、v1 generated Go 或 v1 Connect generated code。
      - 允许残留分类是阶段 1 基线；最终阶段仍必须执行全仓 `rg` 残留审计并逐项归类或修复。
      - 本任务只锁定当前兼容行为，不引入 sandbox/thread 重命名实现。
    - 下一目标：2.1。

## 2. 阶段 2：配置、部署默认值和旧 env 拒绝

参考文档：[docs/plan/sandbox-naming-implementation-plan.md](docs/plan/sandbox-naming-implementation-plan.md#阶段-2配置部署默认值和旧-env-拒绝)

- [x] 2.1 将 daemon 配置字段和 env 切换到 sandbox 命名
  - 依赖：1.1。
  - 工作内容：
    - 将 `Config.SessionRoot`、`DockerHostSessionRoot`、`SessionStartTimeout`、`SessionStopTimeout` 改为 `SandboxRoot`、`DockerHostSandboxRoot`、`SandboxStartTimeout`、`SandboxStopTimeout`。
    - 默认目录改为 `<DATA_ROOT>/sandboxes`。
    - 支持 `SANDBOX_ROOT`、`DOCKER_HOST_SANDBOX_ROOT`、`SANDBOX_START_TIMEOUT`、`SANDBOX_STOP_TIMEOUT`。
    - 检测旧 env：`SESSION_ROOT`、`DOCKER_HOST_SESSION_ROOT`、`SESSION_START_TIMEOUT`、`SESSION_STOP_TIMEOUT`；旧 env 单独出现时报错，新旧同时出现时按 spec 固定冲突或 warning 行为。
    - 更新依赖配置字段的 Go 调用点和测试 helper。
  - 可并行子任务：
    - [x] 可并行：更新 `pkg/config` 字段、加载逻辑和 config tests。
    - [x] 可并行：更新 `pkg/driver`、`pkg/agentcompose/adapters`、runtime mount manifest 的配置字段使用。
    - [x] 可并行：审计 Windows/UNC/path traversal 相关测试并迁移到 sandbox root 命名。
  - 测试方案：
    - `go test ./pkg/config ./pkg/driver ./pkg/agentcompose/adapters`
    - 覆盖新 env 正常、旧 env 拒绝、新旧同时出现、Windows/UNC host root、path traversal 拒绝。
  - 验收标准：
    - 非兼容边界不再读取旧 env。
    - 默认 sandbox root 为 `<DATA_ROOT>/sandboxes`。
    - 旧 env 错误信息指向新变量名并说明不支持 silent fallback。
  - 完成总结：
    - 状态：已完成。
    - 变更：
      - `pkg/config.Config` 已将 `SessionRoot`、`DockerHostSessionRoot`、`SessionStartTimeout`、`SessionStopTimeout` 切换为 `SandboxRoot`、`DockerHostSandboxRoot`、`SandboxStartTimeout`、`SandboxStopTimeout`。
      - `NewConfig` 默认 sandbox root 改为 `<DATA_ROOT>/sandboxes`，并读取 `SANDBOX_ROOT`、`DOCKER_HOST_SANDBOX_ROOT`、`SANDBOX_START_TIMEOUT`、`SANDBOX_STOP_TIMEOUT`。
      - 新增 legacy env 检测：旧 env 单独出现时报错并指向新 env；新旧同时出现时使用新 env 并记录 deprecated warning。
      - 更新 driver、adapter、store、session lifecycle、app/API 测试 helper 和 runtime mount manifest 相关配置字段调用点。
      - 更新 Docker host sandbox root 的 Windows/UNC/path traversal 测试和 Docker path rebase 测试命名。
    - 验证：
      - `go test ./pkg/config ./pkg/driver ./pkg/agentcompose/adapters`
      - `go test ./pkg/storage/... ./pkg/sessions ./pkg/agentcompose/app`
    - 审计与例外：
      - `rg -n "SessionRoot|DockerHostSessionRoot|SessionStartTimeout|SessionStopTimeout|SESSION_ROOT|DOCKER_HOST_SESSION_ROOT|SESSION_START_TIMEOUT|SESSION_STOP_TIMEOUT" cmd pkg` 仅命中 `pkg/config/config.go` 的 legacy rejection 逻辑和 `pkg/config/config_test.go` 的 legacy 行为测试。
      - 本任务未修改 Dockerfile、Compose、`.env.example` 或 README；部署变量更新留给 2.2。
      - `pkg/storage/sessionstore` 包名和内部 session domain 命名仍按计划留给阶段 3/4 迁移，本任务只切换配置字段和 env。
    - 下一目标：2.2。

- [x] 2.2 更新 Docker/Compose 和 `.env.example` 的部署变量
  - 依赖：2.1。
  - 工作内容：
    - 更新 `Dockerfile` image ENV 为 `SANDBOX_ROOT=/data/sandboxes`。
    - 更新 `docker-compose.yml` 使用 deploy-time `DOCKER_HOST_SANDBOX_ROOT`，不加入本地 build-only 默认。
    - 保持 `docker-compose.override.yml` 只承载本地开发行为。
    - 更新 `.env.example`，按部署用途分组记录新变量；旧变量仅写 breaking-change 注释，不提供可复制默认值。
  - 可并行子任务：
    - [x] 可并行：审计 `Dockerfile`、`docker-compose.yml`、`docker-compose.override.yml` 的变量来源。
    - [x] 可并行：更新 `.env.example` 和 README 中部署变量片段。
  - 测试方案：
    - `task build`
    - 人工检查 compose：远端部署仍只需 `docker-compose.yml` 加用户 `.env`。
  - 验收标准：
    - Compose 变更符合 `AGENTS.md` 部署约束。
    - `.env.example` 不暴露旧变量默认值。
    - 本地 build 行为未进入远端部署 compose。
  - 完成总结：
    - 状态：已完成。
    - 变更：
      - `Dockerfile` image ENV 从 `SESSION_ROOT=/data/sessions` 改为 `SANDBOX_ROOT=/data/sandboxes`。
      - `docker-compose.yml` 改用 deploy-time `DOCKER_HOST_SANDBOX_ROOT`，默认 host 路径为 `${PWD}/data/sandboxes`。
      - `.env.example` 增加 `SANDBOX_ROOT=/data/sandboxes`，将 Docker host root 和 start/stop timeout 示例改为 `SANDBOX_*`，并以 breaking-change 注释记录旧 env 不再接受。
      - `README.md` 和 `AGENTS.md` 更新部署变量、默认 sandbox root、compose 数据挂载和 `.env` 挂载路径说明。
    - 验证：
      - `task build`
      - `docker compose -f docker-compose.yml --env-file .env.example config`
      - `rg -n "SESSION_ROOT|DOCKER_HOST_SESSION_ROOT|SESSION_START_TIMEOUT|SESSION_STOP_TIMEOUT" Dockerfile docker-compose.yml docker-compose.override.yml .env.example README.md AGENTS.md`
    - 审计与例外：
      - 旧 env audit 仅命中 `.env.example` 的 breaking-change 注释；未在 Dockerfile、Compose、README 或 AGENTS 中保留旧默认值。
      - `docker-compose.override.yml` 未修改；本地 `build:` 行为仍只在 override 中，远端 `docker-compose.yml` 单独渲染使用 published images。
      - 本任务只更新部署和用户-facing env 文档；文件存储布局和旧 `<DATA_ROOT>/sessions` 拒绝路径留给阶段 3。
    - 下一目标：3.1。

## 3. 阶段 3：文件存储和旧数据目录拒绝

参考文档：[docs/plan/sandbox-naming-implementation-plan.md](docs/plan/sandbox-naming-implementation-plan.md#阶段-3文件存储和旧数据目录拒绝)

- [ ] 3.1 将文件存储 owner 和目录布局收敛为 sandbox store
  - 依赖：2.1。
  - 工作内容：
    - 将 `pkg/storage/sessionstore` 迁移为 sandbox store；可先保留过渡包名，但导出类型和内部语义必须使用 sandbox。
    - 新目录布局为 `<DATA_ROOT>/sandboxes/<sandbox_id>/metadata.json|workspace|context|home|runtime|state|logs|vm|proxy`。
    - 保持 ID 生成使用 `identity.ResourceSandbox`。
    - 更新 path safety、metadata load/save、workspace/home/runtime/state/logs/proxy/vm helper。
    - 更新所有 Go 调用点和测试 fixture 中的临时目录命名。
  - 可并行子任务：
    - [ ] 可并行：迁移 store 类型、路径 helper 和 metadata tests。
    - [ ] 可并行：迁移 adapter/app/API 调用点。
    - [ ] 可并行：迁移测试 fixture、临时目录和 path safety tests。
  - 测试方案：
    - `go test ./pkg/storage/... ./pkg/sessions ./pkg/agentcompose/adapters`
    - 集成测试覆盖创建 sandbox 后文件树位于 `sandboxes/<sandbox_id>`。
  - 验收标准：
    - 新实例不创建 `<DATA_ROOT>/sessions`。
    - sandbox 文件树与 spec 一致。
    - RemoveSandbox 等删除路径仍不能越过 sandbox root。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：3.2。

- [ ] 3.2 实现旧 `sessions` 目录拒绝路径
  - 依赖：3.1。
  - 工作内容：
    - store 初始化时检测 `<DATA_ROOT>/sessions` 存在且非空，同时 `SANDBOX_ROOT` 未显式指向其他新路径。
    - 返回可诊断错误，包含旧路径、新路径和首版不支持自动迁移说明。
    - 确保拒绝路径不会创建新 schema 或隐藏旧数据。
  - 可并行子任务：
    - [ ] 可并行：编写旧目录 fixture 和错误断言 tests。
    - [ ] 可并行：审计启动路径和 store 初始化路径是否都触发检测。
  - 测试方案：
    - `go test ./pkg/storage/... ./pkg/agentcompose/app`
  - 验收标准：
    - 旧目录拒绝路径有单元测试和至少一个启动/初始化路径测试。
    - 错误文案可定位操作者需要清空旧数据根或使用全新数据根。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：4.1。

## 4. 阶段 4：核心 domain、runtime driver 和 app service graph 重命名

参考文档：[docs/plan/sandbox-naming-implementation-plan.md](docs/plan/sandbox-naming-implementation-plan.md#阶段-4核心-domainruntime-driver-和-app-service-graph-重命名)

- [ ] 4.1 重命名核心 domain 和 provider thread 字段
  - 依赖：3.1。
  - 工作内容：
    - 在 `pkg/model` 中将运行实例相关类型重命名为 `Sandbox`、`SandboxSummary`、`SandboxEvent`、`SandboxEnvVar`、`SandboxWorkspace`、`SandboxVMInfo`。
    - 将 `NotebookCell.AgentSessionID` 改为 `AgentThreadID`。
    - 将 `AgentResumeInfo.SessionID` 改为 `ThreadID`。
    - JSON 字段统一为 `sandbox_id`、`agent_thread_id` 或 `thread_id`，v1 mapping 层负责旧字段转换。
  - 可并行子任务：
    - [ ] 可并行：模型类型和 JSON tag 重命名。
    - [ ] 可并行：API/proto mapping 调用点迁移。
    - [ ] 可并行：loader/project/run payload 调用点迁移。
  - 测试方案：
    - `go test ./pkg/model ./pkg/agentcompose/api ./pkg/loaders ./pkg/projects ./pkg/runs`
  - 验收标准：
    - 内部 domain 不再把低层运行实例称为 `Session`。
    - v1 request/response 仍保持 `session_id` 和 `agent_session_id` wire shape。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：4.2。

- [ ] 4.2 重命名 runtime driver domain 和实现
  - 依赖：4.1。
  - 工作内容：
    - `SessionRuntime` -> `SandboxRuntime`。
    - `EnsureSession` -> `EnsureSandbox`，`StopSession` -> `StopSandbox`，`IsSessionAlive` -> `IsSandboxAlive`。
    - `SessionVMInfo` -> `SandboxVMInfo`。
    - `ResolveSessionRuntimeDriver` -> `ResolveSandboxRuntimeDriver`，`ResolveSessionGuestImage` -> `ResolveSandboxGuestImage`。
    - 更新 Docker、BoxLite、Microsandbox runtime、runtime mount manifest、stats、guest bootstrap、image/cache references 的内部 JSON 字段和日志键。
  - 可并行子任务：
    - [ ] 可并行：迁移 `pkg/driver` 接口、实现和 tests。
    - [ ] 可并行：迁移 BoxLite/Microsandbox build-tag 文件和 smoke tests。
    - [ ] 可并行：迁移 runtime mount manifest 和 Docker rebase tests。
  - 测试方案：
    - `go test ./pkg/driver`
    - 如环境可用：`task test:runtime-smoke`
  - 验收标准：
    - driver 支持矩阵仍为 `docker`、`boxlite`、`microsandbox`。
    - driver resolve/start/stop/reconcile/stats/exec 测试覆盖 sandbox 命名。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：4.3。

- [ ] 4.3 迁移 app/adapters/service graph 的 sandbox-native 依赖
  - 依赖：4.1、4.2。
  - 工作内容：
    - 更新 `pkg/agentcompose/adapters` 中 session driver、runtime provider、cell executor、agent executor、loader session runner、capability binding。
    - 更新 `pkg/agentcompose/app` service graph，内部依赖使用 sandbox store、sandbox driver、sandbox delegate。
    - v1 registration 继续注册 `SessionService`、`KernelService`、`AgentService` 等旧 Connect handler。
    - 内部事件类型改为 `sandbox.created/resumed/stopped`，日志键改为 `sandbox_id`、`agent_thread_id`。
  - 可并行子任务：
    - [ ] 可并行：迁移 adapters 和相关 fake/test doubles。
    - [ ] 可并行：迁移 app setup/background/reconcile tests。
    - [ ] 可并行：审计事件类型和日志键。
  - 测试方案：
    - `go test ./pkg/agentcompose/adapters ./pkg/agentcompose/app`
    - 集成测试覆盖创建、恢复、停止、删除 sandbox 后状态和事件。
  - 验收标准：
    - app 内部默认依赖 sandbox-native 类型。
    - v1 handler 仍可用 `session_id` 操作同一个 sandbox。
    - 非允许边界不新增 `session_id` 日志键。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：5.1。

## 5. 阶段 5：SQLite config store、loader/event/LLM facade schema 收敛

参考文档：[docs/plan/sandbox-naming-implementation-plan.md](docs/plan/sandbox-naming-implementation-plan.md#阶段-5sqlite-config-storeloadereventllm-facade-schema-收敛)

- [ ] 5.1 迁移 SQLite schema 和相关模型字段
  - 依赖：4.1。
  - 工作内容：
    - `loader.session_policy` -> `loader.sandbox_policy`。
    - `loader_binding.session_id` -> `loader_binding.sandbox_id`。
    - `loader_event.linked_session_id` -> `loader_event.linked_sandbox_id`。
    - `loader_event.linked_agent_session_id` -> `loader_event.linked_agent_thread_id`。
    - `event_session_link` -> `event_sandbox_link`。
    - `llm_facade_token.session_id` -> `llm_facade_token.sandbox_id`。
    - 保持 `project_run.sandbox_id`，将模型中的兼容 `SessionID` 字段收敛为 `SandboxID`。
    - 更新 scan、insert、update、query、index、filter、JSON payload。
  - 可并行子任务：
    - [ ] 可并行：迁移 loader/configstore schema 和 tests。
    - [ ] 可并行：迁移 topic event schema 和 tests。
    - [ ] 可并行：迁移 LLM facade token schema/index 和 tests。
    - [ ] 可并行：迁移 project/run models 和 query filters。
  - 测试方案：
    - `go test ./pkg/storage/configstore ./pkg/projects ./pkg/runs ./pkg/loaders ./pkg/events/... ./pkg/llms/...`
  - 验收标准：
    - 新数据库 schema 不再创建运行实例相关的 `session_id` 列或 `event_session_link` 表。
    - loader/event/LLM/project/run 查询仍能按 sandbox/thread 关联。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：5.2。

- [ ] 5.2 实现旧 SQLite schema 拒绝和 runtimecache 命名收敛
  - 依赖：5.1。
  - 工作内容：
    - 初始化或 migration 检查中检测旧列和旧表：`loader_binding.session_id`、`loader_event.linked_session_id`、`loader_event.linked_agent_session_id`、`event_session_link`、`llm_facade_token.session_id`。
    - 发现旧 schema 时返回可诊断错误，不自动迁移。
    - 将 `pkg/runtimecache` 的 domain/type/filter/id/reference 命名改为 sandbox ephemeral state。
  - 可并行子任务：
    - [ ] 可并行：编写旧 SQLite schema fixture 和拒绝路径 tests。
    - [ ] 可并行：迁移 runtimecache model/id/API tests。
  - 测试方案：
    - `go test ./pkg/storage/configstore ./pkg/runtimecache ./pkg/agentcompose/api`
    - `task test:integration`
  - 验收标准：
    - 旧 schema 错误信息能定位具体旧表或旧列。
    - runtimecache v2 API 不再暴露 session ephemeral state。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：6.1。

## 6. 阶段 6：v2 proto、Go generated code 和 TypeScript client

参考文档：[docs/plan/sandbox-naming-implementation-plan.md](docs/plan/sandbox-naming-implementation-plan.md#阶段-6v2-protogo-generated-code-和-typescript-client)

- [ ] 6.1 清理 v2 proto 并重新生成 Go/TypeScript client
  - 依赖：4.1、5.1。
  - 工作内容：
    - 只修改 `proto/agentcompose/v2/agentcompose.proto`，不修改 v1 proto。
    - `RunSessionCleanupPolicy` -> `RunSandboxCleanupPolicy`，enum value numbers 保持。
    - 删除或重命名 v2 public `session_id` 字段，按 spec 保留 field number/name reserve 策略。
    - `ExecSessionSelector` -> `ExecSandboxSelector`。
    - `CACHE_DOMAIN_SESSION_EPHEMERAL_STATE` -> `CACHE_DOMAIN_SANDBOX_EPHEMERAL_STATE`。
    - 执行 Go pb/connect 生成和 `proto-client` 生成构建。
  - 可并行子任务：
    - [ ] 可并行：编辑 v2 proto 并检查 reserve 策略。
    - [ ] 可并行：更新 generated Go 调用点编译错误。
    - [ ] 可并行：更新 `proto-client` generated TypeScript 和 build 输出。
  - 测试方案：
    - `protoc -I proto --go_out=. --go_opt=paths=source_relative --connect-go_out=. --connect-go_opt=paths=source_relative proto/health/v1/health.proto proto/agentcompose/v1/agentcompose.proto proto/agentcompose/v2/agentcompose.proto`
    - `cd proto-client && npm run gen && npm run build`
    - `go test ./proto/agentcompose/v2 ./proto/agentcompose/v2/agentcomposev2connect`
  - 验收标准：
    - v1 proto 和 v1 generated Go 无 diff。
    - v2 generated Go 和 `proto-client` 与 proto 同步。
    - v2 public `session_id` 字段不存在，删除字段已 reserved。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：6.2。

- [ ] 6.2 更新 v2 server/client mappings 和 v1 compatibility mapper
  - 依赖：6.1。
  - 工作内容：
    - 更新 v2 Run/Exec/Sandbox/Cache handlers、CLI client、tests 和 mappings，只读取和返回 `sandbox_id`。
    - v2 response 不再填充空 `session_id`。
    - v1 handlers 使用 compatibility mapper：v1 `session_id` -> internal `SandboxID`，v1 `agent_session_id` -> internal `AgentThreadID`。
  - 可并行子任务：
    - [ ] 可并行：迁移 `pkg/agentcompose/api` v2 handlers/tests。
    - [ ] 可并行：迁移 `cmd/agent-compose` v2 client 调用点。
    - [ ] 可并行：补充 v1 compatibility mapping tests。
  - 测试方案：
    - `go test ./pkg/agentcompose/api ./cmd/agent-compose`
    - 集成测试覆盖 v2 Run/Exec/Sandbox/Cache sandbox 字段和 v1 response 旧字段。
  - 验收标准：
    - v2 server/client 不再读取 legacy `session_id`。
    - v1 behavior 保持兼容。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：7.1。

## 7. 阶段 7：runtime JS、runtime SDK、agent thread artifact 和 LLM facade

参考文档：[docs/plan/sandbox-naming-implementation-plan.md](docs/plan/sandbox-naming-implementation-plan.md#阶段-7runtime-jsruntime-sdkagent-thread-artifact-和-llm-facade)

- [ ] 7.1 将 runtime JS contract 从 `sessionId` 切换为 `threadId`
  - 依赖：4.1。
  - 工作内容：
    - `runtime/javascript` 中 `AgentResult.sessionId` -> `threadId`。
    - `StoredSession/readStoredSession/writeStoredSession` -> `StoredThread/readStoredThread/writeStoredThread`。
    - Provider state 路径保持 `/data/state/agents/providers/<provider>.json`，payload 改为 `threadId`。
    - Provider adapter 内部继续解析第三方 native `session_id/sessionId/sessionID/--session`，对外统一输出 `threadId`。
    - `__AGENT_RESULT__` payload 改为包含 `threadId`。
  - 可并行子任务：
    - [ ] 可并行：迁移 runtime types、session-state 和 exports。
    - [ ] 可并行：迁移 codex/claude/gemini/opencode runners 和 tests。
    - [ ] 可并行：迁移 CLI/runtime e2e tests 和 fixture payload。
  - 测试方案：
    - `cd runtime/javascript && npm run test:unit`
  - 验收标准：
    - runtime public contract 使用 `threadId`。
    - provider-native session 字段只留在 runner adapter 内部和对应 tests。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：7.2。

- [ ] 7.2 更新 runtime SDK public result type 和包装测试
  - 依赖：7.1。
  - 工作内容：
    - `runtime/agent-compose-runtime-sdk` 中 public result type `sessionId` -> `threadId`。
    - SDK parser 读取 `__AGENT_RESULT__` 的 `threadId`。
    - 更新 SDK README、tests、packaging 验证。
  - 可并行子任务：
    - [ ] 可并行：迁移 SDK source/types/tests。
    - [ ] 可并行：迁移 SDK README 示例。
  - 测试方案：
    - `cd runtime/agent-compose-runtime-sdk && npm test && npm run test:packaging`
  - 验收标准：
    - SDK public API 使用 `threadId`。
    - packaging test 通过。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：7.3。

- [ ] 7.3 迁移 host artifact、guest env 和 runtime LLM facade
  - 依赖：5.1、7.1。
  - 工作内容：
    - host cell artifact 从 `agent-session.json` 改为 `agent-thread.json`。
    - artifact 内容使用 `provider`、`thread_id`、`thread_state_path`、`thread_manifest_path`、`provider_log_paths`、`updated_at`。
    - `execution.LoadStoredAgentSessionID`、`CollectAgentResumeInfo`、`FindAgentSessionJSONLPaths` 收敛为 thread 命名。
    - daemon 注入 guest env 改为 `SANDBOX_ID`，不再注入 `SESSION_ID`。
    - Runtime LLM facade env 改为 `AGENT_COMPOSE_SANDBOX_TOKEN`，不再注入 `AGENT_COMPOSE_SESSION_TOKEN`。
    - Runtime LLM facade path 改为 `/api/runtime/sandboxes/:sandbox_id/llm/...`，token scope 校验使用 sandbox。
  - 可并行子任务：
    - [ ] 可并行：迁移 `pkg/execution` artifact/resume/parser tests。
    - [ ] 可并行：迁移 `pkg/agentcompose/adapters` guest env 和 managed env tests。
    - [ ] 可并行：迁移 `pkg/llms` runtimefacade 和 `pkg/agentcompose/proxy` tests。
  - 测试方案：
    - `go test ./pkg/execution ./pkg/agentcompose/adapters ./pkg/llms/... ./pkg/agentcompose/proxy`
    - 集成测试覆盖 runtime LLM facade 新 path、token scope、StopSandbox revoke。
  - 验收标准：
    - host cell 记录 `AgentThreadID`，v1 mapping 仍返回 `agent_session_id`。
    - `SESSION_ID` 和 `AGENT_COMPOSE_SESSION_TOKEN` 不再由 daemon 写入。
    - facade token 必须属于 path 中的 `sandbox_id`。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：8.1。

## 8. 阶段 8：Run、Exec、Loader、Capability 和 topic workflow 收敛

参考文档：[docs/plan/sandbox-naming-implementation-plan.md](docs/plan/sandbox-naming-implementation-plan.md#阶段-8runexecloadercapability-和-topic-workflow-收敛)

- [ ] 8.1 迁移 RunService 和 ExecService 工作流
  - 依赖：6.2、7.3。
  - 工作内容：
    - `RunService.RunAgent` 使用 v2 `sandbox_id` 复用 sandbox；缺少 `sandbox_id` 时创建新 sandbox。
    - cleanup policy 使用 `RunSandboxCleanupPolicy`。
    - `ExecService` 使用 `sandbox_id`、`run_id`、`ExecSandboxSelector` 定位 running sandbox；不创建 sandbox。
  - 可并行子任务：
    - [ ] 可并行：迁移 Run handler/controller/supervisor tests。
    - [ ] 可并行：迁移 Exec handler/selectors/runtime tests。
  - 测试方案：
    - `go test ./pkg/agentcompose/api ./pkg/agentcompose/app ./pkg/runs ./pkg/projects`
    - 集成测试覆盖 Run 创建/复用 sandbox、ListRuns(sandbox_id)、RunSummary.sandbox_id、Exec 三类 target。
  - 验收标准：
    - Run/Exec 新路径只使用 sandbox 字段。
    - Exec 不创建 sandbox。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：8.2。

- [ ] 8.2 迁移 loader scheduler sandbox API 和 deprecated aliases
  - 依赖：5.1、7.3。
  - 工作内容：
    - 新增/迁移 `scheduler.sandbox.createSandbox/resumeSandbox/stopSandbox/getSandbox/listSandboxes/getSandboxProxy`。
    - 保留 `scheduler.session.*` deprecated compatibility alias，映射到 sandbox API，并写 loader event 或 validation warning。
    - `scheduler.agent/exec/shell` options 使用 `sandboxPolicy`、`sandboxEnv`。
    - 保留 `sessionPolicy/session_env/sessionEnv` alias，解析后内部事件、结果、持久化使用 sandbox/thread。
    - loader sticky policy 绑定 `loader_id -> sandbox_id`。
  - 可并行子任务：
    - [ ] 可并行：迁移 loader engine bindings 和 QJS API tests。
    - [ ] 可并行：迁移 loader run host/result/payload/event tests。
    - [ ] 可并行：迁移 sticky binding store 和 scheduler tests。
  - 测试方案：
    - `go test ./pkg/loaders ./pkg/agentcompose/adapters ./pkg/storage/configstore`
    - `task test:integration`
  - 验收标准：
    - 新 loader API 使用 sandbox/thread。
    - Deprecated aliases 有 warning 和测试，不污染内部持久化字段。
    - 同一 loader run 内 command/shell 复用 run-scoped loader sandbox。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：8.3。

- [ ] 8.3 迁移 capability token 和 topic event link
  - 依赖：5.1、8.2。
  - 工作内容：
    - capability token 索引改为 token -> sandbox/capset。
    - 启动重建、sandbox 创建/停止时增量更新和撤销。
    - topic event link 使用 `event_sandbox_link`，loader 派生 event 和 run 查询按 sandbox 关联。
  - 可并行子任务：
    - [ ] 可并行：迁移 capability provider/gateway/capproxy tests。
    - [ ] 可并行：迁移 topic event query/link tests。
  - 测试方案：
    - `go test ./pkg/capabilities ./pkg/capproxy ./pkg/events/... ./pkg/loaders`
  - 验收标准：
    - StopSandbox 撤销 sandbox-scoped LLM facade token 和 capability token。
    - loader/topic event 可以按 sandbox 关联回溯。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：9.1。

## 9. 阶段 9：CLI 用户界面和 E2E workflow

参考文档：[docs/plan/sandbox-naming-implementation-plan.md](docs/plan/sandbox-naming-implementation-plan.md#阶段-9cli-用户界面和-e2e-workflow)

- [ ] 9.1 迁移 CLI 命令、文本输出和 JSON shape
  - 依赖：6.2、8.1。
  - 工作内容：
    - 更新 `cmd/agent-compose/main.go` 中 v2 client request/response 使用 `sandbox_id`、`RunSandboxCleanupPolicy`、`ExecSandboxSelector`。
    - CLI 命令和 help 使用 `run --sandbox-id`、`ps` 的 `SANDBOX ID`、`exec <sandbox>`、`logs --sandbox`、`inspect sandbox`、`sandbox stop|resume|rm|prune`、`stats <sandbox>`。
    - JSON 输出只包含 `sandbox_id`、`sandbox_short_id`、`agent_thread_id`、`thread_id`、`linked_sandbox_id`、`linked_agent_thread_id`。
    - `inspect session <sandbox>` 保留 deprecated alias，stderr 输出 warning，JSON shape 仍为 sandbox output。
  - 可并行子任务：
    - [ ] 可并行：迁移 CLI command implementation。
    - [ ] 可并行：迁移 CLI help/golden/snapshot tests。
    - [ ] 可并行：迁移 deprecated alias tests。
  - 测试方案：
    - `go test ./cmd/agent-compose`
  - 验收标准：
    - CLI 新输出不再包含 `session_id`，除 deprecated warning 或 v1 compatibility 调试输出。
    - CLI help 第一层用户语义为 sandbox。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：9.2。

- [ ] 9.2 补齐 CLI/E2E 和 compose env 工作流测试
  - 依赖：9.1。
  - 工作内容：
    - 覆盖 `agent-compose run <agent> --sandbox-id <id>`。
    - 覆盖 `agent-compose ps --json` 不包含 `session_id`。
    - 覆盖 `agent-compose exec <sandbox> --command ...`。
    - 覆盖 `agent-compose logs --sandbox <sandbox>`。
    - 覆盖 `agent-compose inspect sandbox <sandbox>` 和 `inspect session` deprecated warning。
    - 覆盖 `agent-compose sandbox stop|resume|rm|prune`。
    - Docker compose env E2E 使用 `SANDBOX_ROOT` / `DOCKER_HOST_SANDBOX_ROOT`。
  - 可并行子任务：
    - [ ] 可并行：补齐 CLI E2E tests。
    - [ ] 可并行：补齐 compose/env E2E 或 integration tests。
  - 测试方案：
    - `task test:e2e`
    - `go test ./cmd/agent-compose`
  - 验收标准：
    - 主要用户工作流有 E2E 证明。
    - compose env 不再依赖旧 session 变量。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：10.1。

## 10. 阶段 10：文档、部署材料和残留审计

参考文档：[docs/plan/sandbox-naming-implementation-plan.md](docs/plan/sandbox-naming-implementation-plan.md#阶段-10文档部署材料和残留审计)

- [ ] 10.1 更新仓库入口、部署和用户文档
  - 依赖：2.2、9.2。
  - 工作内容：
    - 更新 `AGENTS.md` 的 overview、runtime layout、core services、proxy path、runtime defaults、persistence、Docker/Compose 说明。
    - 更新 `README.md`、`docs/zh-CN/README.md`、`.env.example`、`Dockerfile`、`docker-compose.yml`、`docker-compose.override.yml`。
    - 更新 `docs/command-line-manual.md`、`docs/zh-CN/command-line-manual.md`。
    - 更新 runtime SDK README、proto-client README、loader-script README 中的公开字段和示例。
  - 可并行子任务：
    - [ ] 可并行：更新部署和 env 文档。
    - [ ] 可并行：更新 CLI 文档。
    - [ ] 可并行：更新 package README 示例。
  - 测试方案：
    - 文档人工审阅。
    - 若示例含命令或 JSON，用现有 CLI tests 或 focused tests 固定 shape。
  - 验收标准：
    - 用户文档中的当前目标状态与实现一致。
    - `.env.example` 不提供旧变量可复制默认值。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：10.2。

- [ ] 10.2 更新设计文档和中文对应文档
  - 依赖：7.3、8.3、9.2。
  - 工作内容：
    - 更新 `docs/design/agent-compose_design.md`。
    - 更新 `docs/design/agent-compose-runtime_contract.md`。
    - 更新 `docs/design/runtime_environment_variables_design.md`。
    - 更新 `docs/design/runtime_mount_manifest_design.md`。
    - 更新 `docs/design/runtime_mount_manifest_driver_specific_design.md`。
    - 更新 `docs/design/octobus_integration.md`。
    - 更新 `docs/design/webhook_design.md`。
    - 更新对应 `docs/zh-CN/design/*` 文档，包括 runtime LLM facade 中文设计文档。
  - 可并行子任务：
    - [ ] 可并行：更新 runtime/env/mount 设计文档。
    - [ ] 可并行：更新 agent-compose/webhook/octobus 设计文档。
    - [ ] 可并行：更新中文对应文档。
  - 测试方案：
    - 文档人工审阅。
    - `rg -n "\bsession\b|session_id|sessionId|Session" docs README.md .env.example Dockerfile docker-compose.yml docker-compose.override.yml`
  - 验收标准：
    - 文档中的 `session` 残留全部能归入允许类别。
    - 新文档不描述旧 runtime env、旧 SQLite 字段或旧 v2 public fields 为当前行为。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：10.3。

- [ ] 10.3 执行全仓 `session` 残留审计并修复非允许残留
  - 依赖：10.1、10.2。
  - 工作内容：
    - 执行残留审计命令：

      ```bash
      rg -n "\bsession\b|session_id|sessionId|Session" cmd pkg proto runtime docs README.md .env.example Dockerfile docker-compose.yml docker-compose.override.yml
      ```

    - 将每个残留归类为 v1 compatibility、deprecated alias、auth/browser session、provider-native protocol、migration/error 文案。
    - 修复无法归类的残留。
  - 可并行子任务：
    - [ ] 可并行：审计 `cmd pkg proto` 残留。
    - [ ] 可并行：审计 `runtime` 残留。
    - [ ] 可并行：审计 `docs README .env Docker Compose` 残留。
  - 测试方案：
    - 残留审计命令。
    - 对修复涉及代码的模块运行对应 focused tests。
  - 验收标准：
    - 残留清单可解释。
    - 内部 domain、v2 API、runtime env、SQLite、新文档没有非允许 `session` 命名。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：11.1。

## 11. 阶段 11：完整质量门禁和发布停止条件

参考文档：[docs/plan/sandbox-naming-implementation-plan.md](docs/plan/sandbox-naming-implementation-plan.md#阶段-11完整质量门禁和发布停止条件)

- [ ] 11.1 运行完整 harness 和 CI 等价补充门禁
  - 依赖：10.3。
  - 工作内容：
    - 运行主门禁：`task lint`、`task build`、`task test`。
    - 运行 CI 等价补充门禁：`go test ./cmd/... ./pkg/...`、runtime JS、runtime SDK、proto-client。
    - 如环境可用，额外运行 `task test:runtime-smoke`。
    - 检查 coverage 输出包含 unit、integration、E2E 和 total combined，且满足 `TESTING.md` baseline。
  - 可并行子任务：
    - [ ] 可并行：运行 Go focused/all tests。
    - [ ] 可并行：运行 runtime JS 和 runtime SDK gates。
    - [ ] 可并行：运行 proto-client gen/build。
    - [ ] 可并行：运行 lint/build/test 主门禁。
  - 测试方案：
    - `task lint`
    - `task build`
    - `task test`
    - `go test ./cmd/... ./pkg/...`
    - `cd runtime/javascript && npm run test:unit`
    - `cd runtime/agent-compose-runtime-sdk && npm test && npm run test:packaging`
    - `cd proto-client && npm run gen && npm run build`
    - 可选：`task test:runtime-smoke`
  - 验收标准：
    - 必跑门禁通过，或记录明确环境性阻塞和复现信息。
    - 覆盖率 baseline 满足。
    - 生成代码无未预期 diff。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：11.2。

- [ ] 11.2 最终发布审计和停止条件确认
  - 依赖：11.1。
  - 工作内容：
    - 确认 v1 wire contract 未变化。
    - 确认 v2、CLI、runtime、存储、文档使用 sandbox/thread 命名。
    - 确认旧 env、旧目录、旧 SQLite schema 拒绝路径有测试。
    - 抽查 Docker/Compose：远端部署仍只需 `docker-compose.yml` 加用户 `.env`，本地 build 行为仍在 override。
    - 确认所有停止条件均未触发；如触发，停止合入并回到 spec/plan。
  - 可并行子任务：
    - [ ] 可并行：审计 proto/generated diff。
    - [ ] 可并行：审计 deployment diff。
    - [ ] 可并行：审计残留清单和测试覆盖证据。
  - 测试方案：
    - `git diff -- proto/agentcompose/v1 proto/agentcompose/v2 proto-client`
    - 残留审计命令。
    - 复核 11.1 门禁结果。
  - 验收标准：
    - 没有修改 v1 proto/generated。
    - 所有 `session` 残留均有允许类别。
    - 停止条件清单全部通过。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：无。

## 停止条件

- 任何任务发现必须修改 v1 proto 或 v1 generated code 才能继续时，立即停止并回到 spec 重新确认。
- 任何任务重新要求读取旧 `<DATA_ROOT>/sessions` 或旧 SQLite schema 时，立即停止并重新设计 migration；首版不做自动迁移。
- `task test` 缺失 unit、integration、E2E 或 total combined coverage 输出，或 baseline 不满足时，不得用单独 `go test` 代替质量门禁。
- v2 proto field number/name reserve 策略与 generated code 或 client 生成冲突时，先修正 proto 策略并重新生成，不得复用已删除字段编号表达不同语义。
- `rg session` 出现无法归入允许类别的残留时，不得进入最终验收。
- Compose 修改导致远端部署必须依赖 `docker-compose.override.yml`、本地 build tag 或未记录的新默认值时，必须回滚部署文件设计并按 `AGENTS.md` 重新调整。
- provider adapter 保留第三方 native `session_id/sessionId/--session` 时，残留必须限制在 `runtime/javascript/src/runners/*` 或对应 tests，不得泄漏到 agent-compose runtime contract。

## 首版不做事项

- 不修改 `proto/agentcompose/v1/agentcompose.proto` 或 v1 generated Go。
- 不提供旧 `<DATA_ROOT>/sessions` 到 `<DATA_ROOT>/sandboxes` 的自动迁移。
- 不提供旧 SQLite schema 到新 schema 的自动迁移。
- 不保证旧 v2 generated clients 兼容；v2 是破坏式重命名。
- 不重命名 UI/browser auth session、OAuth session、cookie session、`AUTH_SESSION_TTL`。
- 不重命名第三方 provider 原生协议字段；只在 adapter 边界转换为 `threadId`。
- 不新增复杂 Node.js workflow、`scheduler.run`、workflow bridge token 或新的 runtime 子命令。
- 不改变 runtime driver 支持矩阵，仍为 `docker`、`boxlite`、`microsandbox`。
- 不改变 `JUPYTER_PROXY_BASE` 的变量语义。
