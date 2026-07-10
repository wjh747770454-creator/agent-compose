# Sandbox 命名收敛实施计划

本计划对应 `docs/spec/sandbox-naming-spec.md`。执行目标是在保持 v1 wire contract 不变的前提下，将内部运行实例、v2 API、CLI、runtime、存储和文档统一收敛到 `sandbox` 命名，并将 provider 续接语义统一为 `thread`。

权威 harness 和质量门禁来自 `AGENTS.md`、`TESTING.md`、`Taskfile.yml`、`.github/workflows/ci.yml`：

- 主门禁：`task lint`、`task build`、`task test`。
- CI 补充门禁：`go test ./cmd/... ./pkg/...`、`cd runtime/javascript && npm run test:unit`、`cd runtime/agent-compose-runtime-sdk && npm test && npm run test:packaging`、`cd proto-client && npm run gen && npm run build`。
- v2 proto 变更必须重新生成 Go pb/connect 和 `proto-client` TypeScript client。
- 覆盖率要求沿用 `TESTING.md`：unit、integration、E2E 三类覆盖率必须由 `task test` 输出并满足 baseline。

## 阶段 1：建立命名边界和安全基线

目标：在不改变外部行为的前提下，明确 sandbox-native 与 v1 compatibility 的代码边界，为后续大规模重命名提供可验证锚点。

依赖：已确认的 `docs/spec/sandbox-naming-spec.md`、当前 `AGENTS.md`、`TESTING.md`、`Taskfile.yml` 和 CI 配置。

实施步骤：

1. 新增或更新一个面向本次重命名的残留审计说明，记录允许保留 `session` 命名的类别：v1 proto/generated/handler/tests、deprecated aliases、auth/browser session、第三方 provider native protocol、migration/error 文案。
2. 在当前测试集中增加最小 characterization tests，锁定现有 v1 `SessionService`、v2 `SandboxService`、CLI `inspect session` deprecated alias、loader `sessionPolicy/sessionEnv` alias、runtime `sessionId` payload 解析的当前行为。
3. 在 `pkg/agentcompose/api` 和 `pkg/agentcompose/app` 中标注 v1 compatibility handler 与 sandbox-native handler 的职责边界，避免后续把 v1 字段泄漏回内部模型。
4. 明确本次不修改 `proto/agentcompose/v1/agentcompose.proto`、v1 generated Go、v1 Connect service 名称。
5. 建立一个本地审计命令，用于阶段末检查非允许类别的 `session` 残留，例如 `rg -n "\bsession\b|session_id|sessionId|Session"` 后人工分类。

测试和验证：

- `go test ./pkg/agentcompose/api ./pkg/agentcompose/app ./cmd/agent-compose`
- `cd runtime/javascript && npm run test:unit`
- `cd runtime/agent-compose-runtime-sdk && npm test`

验收标准：

- 当前行为被 characterization tests 覆盖，后续重命名失败能被测试捕获。
- v1 compatibility 与 sandbox-native 的边界有明确代码位置和测试名称。
- 没有修改 v1 proto 和 v1 generated code。

适用 harness 约束或命令：

- 遵守 `TESTING.md` 的三类测试形态划分。
- 每个阶段结束时至少运行本阶段 focused tests；阶段 11 再运行完整门禁。

## 阶段 2：配置、部署默认值和旧 env 拒绝

目标：将运行实例根目录和 runtime timeout 配置切换为 sandbox 命名，并确保旧 env 不会 silent fallback。

依赖：阶段 1 的 characterization tests。

实施步骤：

1. 在 `pkg/config` 中将 `Config.SessionRoot`、`DockerHostSessionRoot`、`SessionStartTimeout`、`SessionStopTimeout` 改为 `SandboxRoot`、`DockerHostSandboxRoot`、`SandboxStartTimeout`、`SandboxStopTimeout`。
2. 默认目录从 `<DATA_ROOT>/sessions` 改为 `<DATA_ROOT>/sandboxes`。
3. 支持新 env：`SANDBOX_ROOT`、`DOCKER_HOST_SANDBOX_ROOT`、`SANDBOX_START_TIMEOUT`、`SANDBOX_STOP_TIMEOUT`。
4. 检测旧 env：`SESSION_ROOT`、`DOCKER_HOST_SESSION_ROOT`、`SESSION_START_TIMEOUT`、`SESSION_STOP_TIMEOUT`。当旧 env 出现且对应新 env 未设置时配置加载失败，错误信息必须指出旧变量、新变量和不支持 silent fallback。
5. 当新旧 env 同时出现时，以新 env 为准，并按 spec 决策实现明确冲突错误或 warning；该行为必须有测试固定。
6. 更新所有依赖配置字段的 Go 调用点，包括 driver、adapter、store、runtime mount manifest、Docker path rebase、测试 helper。
7. 更新 `Dockerfile` image ENV 为 `SANDBOX_ROOT=/data/sandboxes`。
8. 更新 `docker-compose.yml` 只暴露 deploy-time `DOCKER_HOST_SANDBOX_ROOT`，保持远端部署可只用 compose 加用户 `.env`。本地 build 或本地镜像 tag 只放 `docker-compose.override.yml`。
9. 更新 `.env.example`，按部署用途分组记录新变量；旧变量只放 breaking-change 注释，不给可复制默认值。

测试和验证：

- `go test ./pkg/config ./pkg/driver ./pkg/agentcompose/adapters`
- 覆盖新 env 正常、旧 env 单独出现报错、新旧同时出现行为、Windows/UNC `DOCKER_HOST_SANDBOX_ROOT` 保留、path traversal 拒绝。
- 覆盖 Docker runtime mount source 使用 `DOCKER_HOST_SANDBOX_ROOT` rebase，且拒绝越界路径。

验收标准：

- 代码中非兼容边界不再读取旧 env。
- 默认数据根为 `<DATA_ROOT>/sandboxes`。
- Docker/Compose 变更符合 `AGENTS.md`：应用默认不硬写入 compose，本地 build 行为不进入远端部署 compose。

适用 harness 约束或命令：

- `task lint`
- `task build`

## 阶段 3：文件存储和旧数据目录拒绝

目标：将 session 文件存储 owner 收敛为 sandbox store，并在发现旧 `<DATA_ROOT>/sessions` 时明确失败。

依赖：阶段 2 的 `Config.SandboxRoot`。

实施步骤：

1. 将 `pkg/storage/sessionstore` 迁移为 sandbox store。可选择先保留目录包名但导出 sandbox 命名类型作为过渡，最终应收敛为 `pkg/storage/sandboxstore` 或同等清晰边界。
2. 将内部领域对象从 `Session` 文件树布局切换为 `Sandbox` 文件树布局：

   ```text
   <DATA_ROOT>/sandboxes/<sandbox_id>/
     metadata.json
     workspace/
     context/
     home/
     runtime/
     state/
       cells.json
       events.json
       cells/<cell_id>/agent-thread.json
     logs/
     vm/runtime.json
     proxy/jupyter.json
   ```

3. 保持 ID 生成使用 `identity.ResourceSandbox`。
4. 更新 path safety、metadata load/save、workspace/home/runtime/state/logs/proxy/vm 目录 helper 为 sandbox 命名。
5. 在 store 初始化时检测 `<DATA_ROOT>/sessions` 存在且非空，同时 `SANDBOX_ROOT` 未显式指向其他新路径时返回可诊断错误。
6. 错误信息必须包含检测到的旧路径、期望的新路径、首版不支持自动迁移的说明。
7. 更新所有 Go 调用点和测试 fixture 中的临时目录命名。

测试和验证：

- `go test ./pkg/storage/... ./pkg/sessions ./pkg/agentcompose/adapters`
- 单元测试覆盖 sandbox 目录创建、metadata JSON、RemoveSandbox path safety、旧 `sessions` 目录拒绝。
- 集成测试覆盖创建 sandbox 后文件树位于 `sandboxes/<sandbox_id>`。

验收标准：

- 新实例不创建 `<DATA_ROOT>/sessions`。
- 旧目录检测不会静默创建新 schema 或隐藏旧数据。
- 每次 remove 操作仍具备 path safety，不能删除 sandbox root 外的路径。

适用 harness 约束或命令：

- `task test:unit`
- `task build`

## 阶段 4：核心 domain、runtime driver 和 app service graph 重命名

目标：将内部低层运行实例从 `Session` 收敛为 `Sandbox`，并让 app service graph 默认依赖 sandbox-native 类型。

依赖：阶段 2 和阶段 3 的配置及 store。

实施步骤：

1. 在 `pkg/model` 中将运行实例相关类型重命名为 sandbox 语义：`Sandbox`、`SandboxSummary`、`SandboxEvent`、`SandboxEnvVar`、`SandboxWorkspace`、`SandboxVMInfo`。
2. 将 provider 续接字段改名为 thread 语义：`NotebookCell.AgentSessionID` -> `AgentThreadID`，`AgentResumeInfo.SessionID` -> `ThreadID`，相关 JSON 字段改为 `agent_thread_id` 或 `thread_id`。
3. 将 `pkg/driver` 中的 runtime domain 重命名：`SessionRuntime` -> `SandboxRuntime`、`EnsureSession` -> `EnsureSandbox`、`StopSession` -> `StopSandbox`、`IsSessionAlive` -> `IsSandboxAlive`、`SessionVMInfo` -> `SandboxVMInfo`、`ResolveSessionRuntimeDriver` -> `ResolveSandboxRuntimeDriver`、`ResolveSessionGuestImage` -> `ResolveSandboxGuestImage`。
4. 更新 Docker、BoxLite、Microsandbox runtime 实现、runtime mount manifest、stats、guest bootstrap、image/cache references 的内部 JSON 字段和日志键。
5. 更新 `pkg/agentcompose/adapters` 中的 session driver、runtime provider、cell executor、agent executor、loader session runner、capability binding 为 sandbox 命名。
6. 更新 `pkg/agentcompose/app` service graph，内部依赖使用 sandbox store、sandbox driver、sandbox delegate；v1 registration 仍注册 `SessionService` 等 Connect handler。
7. 事件类型从内部 `session.created/resumed/stopped` 改为 `sandbox.created/resumed/stopped`，v1 返回或错误文案可保留用户习惯的 session 字样。
8. 更新日志键为 `sandbox_id`、`agent_thread_id`。除 v1/alias/provider-native 边界外，不再新增 `session_id` 日志键。

测试和验证：

- `go test ./pkg/model ./pkg/driver ./pkg/agentcompose/adapters ./pkg/agentcompose/app`
- 单元测试覆盖 driver resolve、runtime mount manifest、start/stop/reconcile/stats/exec。
- 集成测试覆盖创建、恢复、停止、删除 sandbox 后状态和事件正确。

验收标准：

- 内部低层 runtime 生命周期对象不再称为 `Session`。
- v1 handler 仍可通过 `session_id` 操作同一个 sandbox。
- Docker、BoxLite、Microsandbox 支持矩阵不变。

适用 harness 约束或命令：

- `task lint`
- `task build`

## 阶段 5：SQLite config store、loader/event/LLM facade schema 收敛

目标：将全局 SQLite schema 中运行实例相关字段切换为 sandbox/thread 命名，并显式拒绝旧 schema。

依赖：阶段 4 的 domain 类型命名。

实施步骤：

1. 更新 `pkg/storage/configstore` schema：
   - `loader.session_policy` -> `loader.sandbox_policy`
   - `loader_binding.session_id` -> `loader_binding.sandbox_id`
   - `loader_event.linked_session_id` -> `loader_event.linked_sandbox_id`
   - `loader_event.linked_agent_session_id` -> `loader_event.linked_agent_thread_id`
   - `event_session_link` -> `event_sandbox_link`
   - `event_session_link.session_id` -> `event_sandbox_link.sandbox_id`
   - `llm_facade_token.session_id` -> `llm_facade_token.sandbox_id`
   - `idx_llm_facade_token_session` -> `idx_llm_facade_token_sandbox`
2. 保持 `project_run.sandbox_id` 不变；将 `pkg/model.ProjectRunRecord.SessionID` 等兼容字段收敛为 `SandboxID`，只在必要 v1 mapping 层处理旧名。
3. 重命名模型：`ProjectSessionRelationFilter` -> `ProjectSandboxRelationFilter`、`ProjectSessionStatus` -> `ProjectSandboxStatus`、`LoaderSessionPolicy*` -> `LoaderSandboxPolicy*`、`LoaderBinding.SessionID` -> `SandboxID`、`LoaderAgentResult.SessionID` -> `SandboxID`、`LoaderCommandResult.SessionID` -> `SandboxID`、`TopicEventSessionLink` -> `TopicEventSandboxLink`。
4. 在 SQLite 初始化或 migration 检查中检测旧列和旧表：`loader_binding.session_id`、`loader_event.linked_session_id`、`loader_event.linked_agent_session_id`、`event_session_link`、`llm_facade_token.session_id`。发现时返回可诊断错误，不自动迁移。
5. 更新 scan、insert、update、query、index、filter 和 JSON payload 生成代码。
6. 更新 `pkg/runtimecache` 的 domain/type/filter/id/reference 命名为 sandbox ephemeral state。

测试和验证：

- `go test ./pkg/storage/configstore ./pkg/projects ./pkg/runs ./pkg/loaders ./pkg/events/... ./pkg/llms/... ./pkg/runtimecache`
- 单元测试覆盖新表/列创建、旧 schema 拒绝、project_run `sandbox_id` 查询、LLM token sandbox index。
- 集成测试覆盖 loader sticky sandbox binding、loader command result、loader event linked sandbox/thread、topic event sandbox link。

验收标准：

- 新数据库 schema 不再创建运行实例相关的 `session_id` 列或 `event_session_link` 表。
- 旧 schema 启动失败信息可定位具体旧表或旧列。
- loader 和 event 查询仍可按 sandbox/run 关联回溯。

适用 harness 约束或命令：

- `task test:unit`
- `task test:integration`

## 阶段 6：v2 proto、Go generated code 和 TypeScript client

目标：将 v2 public wire shape 清理为 sandbox-native，保留 v1 wire shape 完全不变。

依赖：阶段 4 和阶段 5 的内部模型已能表达 sandbox/thread。

实施步骤：

1. 只修改 `proto/agentcompose/v2/agentcompose.proto`，不修改 `proto/agentcompose/v1/agentcompose.proto`。
2. 按 spec 清理 v2 字段和类型：
   - `RunSessionCleanupPolicy` -> `RunSandboxCleanupPolicy`，enum number 0..3 保持。
   - 删除 `RunAgentRequest.session_id = 5`，reserve 5 和 `"session_id"`，使用现有 `sandbox_id = 15`。
   - `RunAgentRequest.cleanup_policy = 7` 类型改为 `RunSandboxCleanupPolicy`。
   - 删除 `ListRunsRequest.session_id = 3`，reserve 3 和 `"session_id"`，使用现有 `sandbox_id = 11`。
   - 删除 `RunSummary.session_id = 11`，reserve 11 和 `"session_id"`，使用现有 `sandbox_id = 20`。
   - `ExecRequest.session_id = 1` -> `sandbox_id = 1`，保留 field number，reserve name `"session_id"`。
   - `ExecSessionSelector` -> `ExecSandboxSelector`。
   - `ExecStreamResponse.session_id = 3` -> `sandbox_id = 3`，保留 field number，reserve name `"session_id"`。
   - `ExecResult.session_id = 2` -> `sandbox_id = 2`，保留 field number，reserve name `"session_id"`。
   - `RemoveProject.stop_running_sessions = 3` -> `stop_running_sandboxes = 3`，保留 field number，reserve name `"stop_running_sessions"`。
   - `CACHE_DOMAIN_SESSION_EPHEMERAL_STATE = 4` -> `CACHE_DOMAIN_SANDBOX_EPHEMERAL_STATE = 4`，保留 enum number。
   - 删除 `CacheItem.session_id = 10`，reserve 10 和 `"session_id"`，使用现有 `sandbox_id = 11`。
3. 重新生成 Go pb/connect：

   ```bash
   protoc -I proto \
     --go_out=. --go_opt=paths=source_relative \
     --connect-go_out=. --connect-go_opt=paths=source_relative \
     proto/health/v1/health.proto \
     proto/agentcompose/v1/agentcompose.proto \
     proto/agentcompose/v2/agentcompose.proto
   ```

4. 重新生成并构建 `proto-client`：

   ```bash
   cd proto-client && npm run gen && npm run build
   ```

5. 更新 v2 server handlers、CLI client、tests 和 mappings，只读取和返回 `sandbox_id`。
6. v2 response 不再为了兼容填充空 `session_id`。
7. v1 handlers 使用 compatibility mapper：v1 `session_id` 映射内部 `SandboxID`，v1 `agent_session_id` 映射内部 `AgentThreadID`。

测试和验证：

- `go test ./proto/agentcompose/v2 ./proto/agentcompose/v2/agentcomposev2connect ./pkg/agentcompose/api ./cmd/agent-compose`
- `cd proto-client && npm run gen && npm run build`
- 单元测试覆盖 v2 response 只填 `sandbox_id`，v1 response 保持旧字段。
- 集成测试覆盖 v2 `RunService`、`ExecService`、`SandboxService`、`CacheService` 的 sandbox 字段。

验收标准：

- v1 proto 和 v1 generated code diff 为空。
- v2 generated Go 和 `proto-client` 与 proto 同步。
- v2 public `session_id` 字段不再存在，保留的旧 field number/name 已按策略 reserved。

适用 harness 约束或命令：

- `task build`
- `cd proto-client && npm run gen && npm run build`

## 阶段 7：runtime JS、runtime SDK、agent thread artifact 和 LLM facade

目标：将 guest runtime contract 从 provider session 改为 thread，并将 runtime LLM facade path/env/token scope 改为 sandbox。

依赖：阶段 4 的 `AgentThreadID` 和阶段 5 的 `llm_facade_token.sandbox_id`。

实施步骤：

1. 在 `runtime/javascript` 中将 `AgentResult.sessionId` 改为 `threadId`。
2. 将 `StoredSession`、`readStoredSession`、`writeStoredSession` 改为 `StoredThread`、`readStoredThread`、`writeStoredThread`。provider state 路径可保持 `/data/state/agents/providers/<provider>.json`，payload 字段改为 `threadId`。
3. Provider adapter 内部继续允许解析第三方原生字段：Claude `session_id`、Gemini 或 OpenCode `sessionId/session_id/sessionID`、OpenCode `--session`。adapter 对外统一输出 `threadId`。
4. `__AGENT_RESULT__` payload 改为 `{"provider":"codex","threadId":"...","stopReason":"completed",...}`。
5. 在 host execution 中将 cell artifact 从 `agent-session.json` 改为 `agent-thread.json`，内容使用 `provider`、`thread_id`、`thread_state_path`、`thread_manifest_path`、`provider_log_paths`、`updated_at`。
6. 将 `execution.LoadStoredAgentSessionID`、`CollectAgentResumeInfo`、`FindAgentSessionJSONLPaths`、相关 tests 收敛为 thread 命名。
7. 更新 `runtime/agent-compose-runtime-sdk` 的 public result type：`sessionId` -> `threadId`，并同步 README 和 tests。
8. daemon 注入 guest env 改为 `SANDBOX_ID`，不再注入 `SESSION_ID`。
9. Runtime LLM facade env 改为 `AGENT_COMPOSE_SANDBOX_TOKEN`，保留 `LLM_API_KEY`、`OPENAI_API_KEY`、`ANTHROPIC_API_KEY` 指向同一 token；不再注入 `AGENT_COMPOSE_SESSION_TOKEN`。
10. Runtime LLM facade path 改为：
    - `/api/runtime/sandboxes/:sandbox_id/llm/openai/v1/responses`
    - `/api/runtime/sandboxes/:sandbox_id/llm/openai/v1/chat/completions`
    - `/api/runtime/sandboxes/:sandbox_id/llm/anthropic/v1/messages`
11. LLM facade 校验使用 path `sandbox_id` 与 token `SandboxID` 匹配，并保留 token 未撤销、未过期、sandbox 存在且未停止、model/provider/wire_api scope 校验。

测试和验证：

- `cd runtime/javascript && npm run test:unit`
- `cd runtime/agent-compose-runtime-sdk && npm test && npm run test:packaging`
- `go test ./pkg/execution ./pkg/agentcompose/adapters ./pkg/llms/... ./pkg/agentcompose/proxy`
- 单元测试覆盖 `threadId` payload 解析、provider-native session 字段只在 adapter 内部解析、缺失 payload 错误分类、`SANDBOX_ID` env、`AGENT_COMPOSE_SANDBOX_TOKEN` env。
- 集成测试覆盖 runtime LLM facade 新 path、token scope sandbox 校验、停止 sandbox 后 token revoke。

验收标准：

- runtime contract 和 SDK public API 使用 `threadId`。
- host cell 记录 `AgentThreadID`，v1 `CellToProto` 和 `AgentRunToProto` 仍映射回 `agent_session_id`。
- `SESSION_ID` 和 `AGENT_COMPOSE_SESSION_TOKEN` 不再由 daemon 写入。

适用 harness 约束或命令：

- `cd runtime/javascript && npm run test:unit`
- `cd runtime/agent-compose-runtime-sdk && npm test`
- `task build`

## 阶段 8：Run、Exec、Loader、Capability 和 topic workflow 收敛

目标：将 daemon 内部用户工作流从 session 选项和结果字段切换到 sandbox/thread，同时保留指定 deprecated aliases。

依赖：阶段 5、阶段 6、阶段 7。

实施步骤：

1. `RunService.RunAgent` 只接受 v2 `sandbox_id` 复用既有 sandbox；缺少 `sandbox_id` 时创建新 sandbox。
2. cleanup policy 使用 `RunSandboxCleanupPolicy`：默认 stop-on-completion，`KEEP_RUNNING` 保持运行，`REMOVE_ON_COMPLETION` 成功后删除 sandbox。
3. `ExecService` 使用 `sandbox_id`、`run_id`、`ExecSandboxSelector` 定位 running sandbox；不创建 sandbox。
4. loader scheduler 新 API 面向 sandbox：
   - `scheduler.sandbox.createSandbox`
   - `scheduler.sandbox.resumeSandbox`
   - `scheduler.sandbox.stopSandbox`
   - `scheduler.sandbox.getSandbox`
   - `scheduler.sandbox.listSandboxes`
   - `scheduler.sandbox.getSandboxProxy`
5. 保留 `scheduler.session.*` deprecated compatibility alias，映射到 sandbox API，并在 loader event 或 validation warning 中标记 deprecated。
6. `scheduler.agent`、`scheduler.exec`、`scheduler.shell` options 使用 `sandboxPolicy`、`sandboxEnv`。
7. 保留旧 `sessionPolicy`、`session_env`、`sessionEnv` deprecated aliases，解析后内部事件、结果、持久化统一写 sandbox/thread。
8. loader sticky policy 绑定 `loader_id -> sandbox_id`；同一 loader run 内 command/shell 复用 run-scoped loader sandbox。
9. capability token 索引和撤销逻辑改为 token -> sandbox/capset；启动重建、sandbox 创建/停止时增量更新。
10. topic event link 使用 `event_sandbox_link`，loader 派生 event 和 run 查询按 sandbox 关联。

测试和验证：

- `go test ./pkg/agentcompose/api ./pkg/agentcompose/app ./pkg/loaders ./pkg/events/... ./pkg/capabilities ./pkg/capproxy ./pkg/runs ./pkg/projects`
- 集成测试覆盖 v2 Run 创建 sandbox、复用 `sandbox_id`、`ListRuns(sandbox_id)`、`RunSummary.sandbox_id`。
- 集成测试覆盖 Exec 通过 `sandbox_id`、`run_id`、selector 执行命令。
- 集成测试覆盖 loader sticky sandbox binding、deprecated aliases warning、loader command result、loader event linked sandbox/thread。
- 集成测试覆盖 capability token 重建和 StopSandbox 撤销。

验收标准：

- 新 loader API、run、exec、capability、topic event 全部使用 sandbox/thread 命名。
- Deprecated aliases 集中在 compatibility 层，有测试和 warning，不污染内部持久化字段。
- v1 loader wire shape 中的 `session_policy`、`linked_session_id`、`linked_agent_session_id` 仍保持原样并映射到内部 sandbox/thread。

适用 harness 约束或命令：

- `task test:integration`
- `task build`

## 阶段 9：CLI 用户界面和 E2E workflow

目标：CLI 文本和 JSON 输出使用 sandbox/thread 命名，`inspect session` 仅作为 deprecated alias 保留。

依赖：阶段 6 的 v2 generated client 和阶段 8 的 Run/Exec/Sandbox 行为。

实施步骤：

1. 更新 `cmd/agent-compose/main.go` 中所有 v2 client request/response 使用 `sandbox_id`、`RunSandboxCleanupPolicy`、`ExecSandboxSelector`。
2. CLI 命令保持或调整为：
   - `run --sandbox-id`
   - `ps` 显示 `SANDBOX ID`
   - `exec <sandbox>`
   - `logs --sandbox`
   - `inspect sandbox`
   - `sandbox stop|resume|rm|prune`
   - `stats <sandbox>`
3. JSON 输出只包含 `sandbox_id`、`sandbox_short_id`、`agent_thread_id`、`thread_id`、`linked_sandbox_id`、`linked_agent_thread_id`。
4. `inspect session <sandbox>` 保留 deprecated alias，调用 `inspect sandbox` 逻辑并向 stderr 输出 deprecation warning；JSON shape 仍为 sandbox output。
5. 更新 CLI help、usage、test golden 或 snapshot，避免新文案出现非允许的 session 命名。
6. Docker compose env E2E 使用 `SANDBOX_ROOT` / `DOCKER_HOST_SANDBOX_ROOT`。

测试和验证：

- `go test ./cmd/agent-compose`
- E2E/CLI 测试覆盖：
  - `agent-compose run <agent> --sandbox-id <id>`
  - `agent-compose ps --json` 不包含 `session_id`
  - `agent-compose exec <sandbox> --command ...`
  - `agent-compose logs --sandbox <sandbox>`
  - `agent-compose inspect sandbox <sandbox>`
  - `agent-compose inspect session <sandbox>` 输出 deprecated warning
  - `agent-compose sandbox stop|resume|rm|prune`

验收标准：

- CLI 新输出不再包含 `session_id`，除 deprecated warning 或 v1 compatibility 调试输出。
- CLI help 第一层用户语义为 sandbox。
- E2E 测试证明主要用户工作流完整。

适用 harness 约束或命令：

- `task test:e2e`
- `task build`

## 阶段 10：文档、部署材料和残留审计

目标：同步所有用户和设计文档，并确认 `session` 残留全部属于允许类别。

依赖：阶段 2 至阶段 9 的实现已落地。

实施步骤：

1. 更新 `AGENTS.md`，将仓库 overview、runtime layout、core services、proxy path、runtime defaults、persistence、Docker/Compose 说明切换为 sandbox 命名，同时明确 v1 compatibility 的 session 语义。
2. 更新 `README.md`、`docs/zh-CN/README.md`、`.env.example`、`Dockerfile`、`docker-compose.yml`、`docker-compose.override.yml`。
3. 更新设计文档：
   - `docs/design/agent-compose_design.md`
   - `docs/design/agent-compose-runtime_contract.md`
   - `docs/design/runtime_environment_variables_design.md`
   - `docs/design/runtime_mount_manifest_design.md`
   - `docs/design/runtime_mount_manifest_driver_specific_design.md`
   - `docs/design/octobus_integration.md`
   - `docs/design/webhook_design.md`
   - 对应 `docs/zh-CN/design/*`
4. 更新命令行文档：
   - `docs/command-line-manual.md`
   - `docs/zh-CN/command-line-manual.md`
5. 更新 runtime SDK README、proto-client README、loader-script README 中的公开字段和示例。
6. 执行残留审计：

   ```bash
   rg -n "\bsession\b|session_id|sessionId|Session" cmd pkg proto runtime docs README.md .env.example Dockerfile docker-compose.yml docker-compose.override.yml
   ```

7. 对每个残留标注归类：v1 compatibility、deprecated alias、auth/browser session、provider-native protocol、migration/error 文案。无法归类的残留必须修复。

测试和验证：

- 文档检查以人工审阅和 `rg` 残留审计为准。
- 若文档示例包含命令或 JSON，优先用现有 CLI tests 或 focused tests 固定示例 shape。

验收标准：

- 文档中的当前目标状态与实现一致。
- `.env.example` 不提供旧变量可复制默认值。
- `session` 残留清单可解释，无内部 domain、v2 API、runtime env、SQLite、新文档的非允许残留。

适用 harness 约束或命令：

- `task lint`
- 文档和部署文件不应破坏 `task build`。

## 阶段 11：完整质量门禁和发布停止条件

目标：用完整 harness 证明重命名收敛完成，并定义不能继续合入的停止条件。

依赖：阶段 1 至阶段 10 全部完成。

实施步骤：

1. 运行主门禁：

   ```bash
   task lint
   task build
   task test
   ```

2. 运行 CI 等价补充门禁：

   ```bash
   go test ./cmd/... ./pkg/...
   cd runtime/javascript && npm run test:unit
   cd runtime/agent-compose-runtime-sdk && npm test && npm run test:packaging
   cd proto-client && npm run gen && npm run build
   ```

3. 如 proto 生成有 diff，确认只涉及 v2 和预期 generated client；v1 proto/generated 不应变化。
4. 运行残留审计并保留结果摘要。
5. 抽查 Docker/Compose 部署文件，确认远端部署仍只需要 `docker-compose.yml` 加用户 `.env`，本地 build 行为仍在 override。
6. 检查覆盖率输出是否包含 unit、integration、E2E 和 total combined，且满足 `TESTING.md` baseline。

测试和验证：

- 所有命令必须通过，或记录明确的环境性阻塞和复现信息。
- 对 Docker/BoxLite/Microsandbox 真实 runtime smoke 如环境可用，可额外运行：

  ```bash
  task test:runtime-smoke
  ```

验收标准：

- `task lint`、`task build`、`task test` 通过。
- CI 等价补充门禁通过。
- 覆盖率 baseline 满足。
- v1 wire contract 未变化。
- 旧 env、旧目录、旧 SQLite schema 的拒绝路径有测试。
- v2、CLI、runtime、存储、文档使用 sandbox/thread 命名。

适用 harness 约束或命令：

- `AGENTS.md` 的主门禁和 CI 配置全部纳入本阶段。

## 风险和停止条件

- 如果任何阶段发现必须修改 v1 proto 或 v1 generated code 才能继续，停止实施并回到 spec 重新确认；本计划不允许破坏 v1 wire contract。
- 如果旧数据读取兼容被重新要求，停止实施并重新设计 migration；本计划明确不做旧 `<DATA_ROOT>/sessions` 或旧 SQLite schema 自动迁移。
- 如果 `task test` 的三类覆盖率输出缺失或 baseline 不满足，不能以单独 `go test` 代替质量门禁。
- 如果 v2 proto field number/name reserve 策略与 generated code 或 client 生成冲突，先修正 proto 策略并重新生成，不得复用已删除字段编号表达不同语义。
- 如果 `rg session` 出现无法归入允许类别的残留，不能进入最终验收。
- 如果 Compose 修改导致远端部署必须依赖 `docker-compose.override.yml`、本地 build tag 或未记录的新默认值，必须回滚部署文件设计并重新按 `AGENTS.md` 约束调整。
- 如果 runtime provider adapter 需要保留第三方 native `session_id/sessionId/--session`，残留必须限制在 `runtime/javascript/src/runners/*` 或对应 tests，不得泄漏到 agent-compose runtime contract。

## 首版不做的事项

- 不修改 `proto/agentcompose/v1/agentcompose.proto` 或 v1 generated Go。
- 不提供旧 `<DATA_ROOT>/sessions` 到 `<DATA_ROOT>/sandboxes` 的自动迁移。
- 不提供旧 SQLite schema 到新 schema 的自动迁移。
- 不保证旧 v2 generated clients 兼容；v2 是破坏式重命名。
- 不重命名 UI/browser auth session、OAuth session、cookie session、`AUTH_SESSION_TTL`。
- 不重命名第三方 provider 原生协议字段；只在 adapter 边界转换为 `threadId`。
- 不新增复杂 Node.js workflow、`scheduler.run`、workflow bridge token 或新的 runtime 子命令。
- 不改变 runtime driver 支持矩阵，仍为 `docker`、`boxlite`、`microsandbox`。
- 不改变 `JUPYTER_PROXY_BASE` 的变量语义。
