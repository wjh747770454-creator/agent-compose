# Sandbox 命名收敛技术规格

## 背景与目标

agent-compose 早期以 `session` 表达运行实例：一次运行实例拥有 workspace、home、state、runtime、logs、VM/proxy 状态、notebook cell、agent event 和 runtime driver 生命周期。当前产品语义已经在 CLI 和部分 v2 API 中转向 `sandbox`，但内部模型、v2 wire shape、runtime env、SQLite 表、文档和测试仍混用 `session` 与 `sandbox`。

本规格定义一次命名收敛：控制面内部、v2 API、CLI、runtime、存储和文档统一使用 `sandbox` 表达运行实例；provider 续接语义统一使用 `thread`；`proto/agentcompose/v1` 保持原有 session-centric wire shape，通过兼容层映射到新的内部模型。

目标状态：

- 内部运行实例领域模型命名为 `Sandbox`，不再把低层 runtime 生命周期对象称为 `Session`。
- v2 API、CLI 输出、配置、runtime facade、runtime env 和持久化 schema 使用 `sandbox_id` / `Sandbox*` / `SANDBOX_*`。
- v1 API 完全保留 `SessionService`、`session_id`、`agent_session_id` 等外部字段和行为，不修改 v1 proto 与 v1 generated code。
- provider 续接 ID 使用 `thread_id` / `threadId` / `AgentThreadID`；Claude、OpenCode 等第三方原生 `session_id` 或 `--session` 只留在 provider adapter 内部解析边界。
- 旧 session 持久化目录和 SQLite schema 不做自动兼容读取；daemon 必须显式拒绝旧数据根或旧 schema，避免静默读写错目录。

## 现状和 harness 约束

### 项目约束

- `AGENTS.md` 规定本仓库是 agent-compose session control plane，主要入口包括 `cmd/agent-compose/main.go`、`pkg/agentcompose/app/`、`pkg/agentcompose/api/`、`pkg/agentcompose/adapters/`、`pkg/agentcompose/proxy/`、`pkg/model`、`pkg/storage/sessionstore`、`pkg/sessions`、`proto/agentcompose/v1`、`proto/agentcompose/v2`、`proto-client/`。
- `AGENTS.md` 的当前默认配置仍是 `SESSION_ROOT=<DATA_ROOT>/sessions`，Jupyter proxy 仍有 `/agent-compose/session/<session_id>` 兼容路径，持久化仍由 `SESSION_ROOT` 和 `DATA_ROOT/data.db` 承载。
- Docker/Compose 约束来自 `AGENTS.md`：`docker-compose.yml` 必须可独立用于远端部署；本地 build 行为放在 `docker-compose.override.yml`；新增或变更环境变量必须先判断 deploy-time、image-default、application-default 或 local-development-only；不要把应用默认值硬写进 compose，当应用或 image ENV 可提供默认时优先使用默认和 `.env.example` 注释。
- `TESTING.md` 要求 `task test` 作为质量门禁，并按 unit、integration、E2E 三种 shape 统计覆盖率；跨 API、持久化、runtime driver、用户工作流的变更必须有更宽的 integration/E2E 覆盖。
- `Taskfile.yml` 的主门禁是 `task lint`、`task build`、`task test`；CI 还单独运行 Go tests、runtime SDK、scheduler runtime、proto-client 生成与构建。
- `go.mod` 已声明 `protoc-gen-go` 和 `protoc-gen-connect-go` 为 Go tool；v2 proto 变更必须同步生成 Go pb/connect 代码，并同步 `proto-client` 的 TypeScript client。

### 当前代码事实

- `proto/agentcompose/v1/agentcompose.proto` 仍定义 `SessionService`、`KernelService`、`AgentService`、`AgentDefinitionService.CreateAgentSession`、`LoaderService`，并暴露 `SessionIDRequest.session_id`、`SessionSummary.session_id`、`AgentRun.agent_session_id` 等 v1 session 形态。
- `proto/agentcompose/v2/agentcompose.proto` 已存在 `SandboxService`、`RemoveSandboxRequest.sandbox_id`、`SandboxStats.sandbox_id`，但仍混有：
  - `RunSessionCleanupPolicy`
  - `RunAgentRequest.session_id = 5` 与 `sandbox_id = 15`
  - `ListRunsRequest.session_id = 3` 与 `sandbox_id = 11`
  - `RunSummary.session_id = 11` 与 `sandbox_id = 20`
  - `ExecRequest.session_id = 1`
  - `ExecSessionSelector`
  - `ExecStreamResponse.session_id = 3`
  - `ExecResult.session_id = 2`
  - `CacheDomain.CACHE_DOMAIN_SESSION_EPHEMERAL_STATE`
  - `CacheItem.session_id = 10` 与 `sandbox_id = 11`
- `pkg/model/model.go` 仍以 `Session`、`SessionSummary`、`SessionEvent`、`SessionEnvVar`、`SessionWorkspace`、`NotebookCell.AgentSessionID`、`AgentResumeInfo.SessionID` 表达运行实例和 provider 续接。
- `pkg/storage/sessionstore` 仍是文件存储 owner，创建 `<SESSION_ROOT>/<id>`，但 ID 已通过 `identity.ResourceSandbox` 生成，说明 ID 语义已先行转向 sandbox。
- `pkg/storage/configstore/project_schema.go` 的 `project_run` 已使用 `sandbox_id`，但 `pkg/model.ProjectRunRecord` 仍保留 `SessionID` 作为兼容字段。
- `pkg/storage/configstore/loader_store.go` 仍有 `loader.session_policy`、`loader_binding.session_id`、`loader_event.linked_session_id`、`loader_event.linked_agent_session_id`。
- `pkg/storage/configstore/topic_event_store.go` 仍有 `event_session_link.session_id`。
- `pkg/storage/configstore/llm_config.go` 的 `llm_facade_token` 仍使用 `session_id`。
- `runtime/javascript/src/session-state.ts` 的 provider 续接状态路径是 `/data/state/agents/providers/<provider>.json`，payload 字段为 `sessionId`；这不同于 host 写入 cell artifact 的 `/data/state/cells/<cell_id>/agent-session.json`。
- `docs/design/agent-compose_design.md` 已描述 CLI 面向 sandbox，但低层 runtime 生命周期仍称 `Session`，并记录 `<DATA_ROOT>/sessions/<session_id>` 存储布局。
- `docs/design/agent-compose-runtime_contract.md` 仍定义 runtime 输出 `__AGENT_RESULT__{"sessionId":...}`、provider state `sessionId`、cell artifact `agent-session.json`。
- `docs/design/runtime_environment_variables_design.md` 仍记录 guest env `SESSION_ID`。
- `docs/zh-CN/design/agent-compose-runtime-llm-facade.md` 仍定义 `/api/runtime/sessions/:session_id/...` 和 `AGENT_COMPOSE_SESSION_TOKEN`。

### 约束结论

- 这是跨 proto、Go domain、SQLite、file store、runtime JS/SDK、CLI、Docker/env、文档的破坏式内部重命名，不应作为局部重构处理。
- v1 wire 兼容是硬约束；v1 generated Go 和 v1 proto 不改。
- v2 是主清理面，允许破坏 v2 generated Go/TS client，但 proto field number 必须明确处理，避免未来误复用。
- 旧持久化不兼容是本规格的明确产品决策，但 daemon 必须给出可诊断失败，不能静默创建新 schema 后把旧数据变成不可见脏状态。

## 核心概念

### Sandbox

`Sandbox` 是 agent-compose 的低层运行实例。它拥有：

- host 文件树：workspace、context、home、runtime、state、logs、vm、proxy。
- runtime driver 生命周期：docker、boxlite、microsandbox 的 start/stop/reconcile/stats/exec。
- notebook cell 和 sandbox event 时间线。
- Jupyter proxy 状态和 runtime LLM facade token。
- 与 project run、loader run、capability token、topic event link 的关联。

`Sandbox` 替代内部 `Session`。内部 Go 类型、接口、包名、日志键、JSON 字段、SQLite 新 schema、runtime env 和文档均应使用 sandbox 命名。

### v1 Session

`Session` 在首版收敛后只表示 v1 兼容 wire 概念。它不是内部领域对象。v1 handler 负责把 v1 request/response 映射到 sandbox service/store，并继续返回旧字段：

- `session_id` 映射 sandbox ID。
- `SessionSummary` 映射 `SandboxSummary`。
- `SessionEnvVar` 映射 `SandboxEnvVar`。
- `agent_session_id` 映射 `agent_thread_id`。

v1 proto、generated Go、Connect service 名称和客户端行为不变。

### Provider Thread

`Thread` 是 guest provider 的续接 ID，不等同于 sandbox。一个 sandbox 可以多次调用 provider；每个 provider 可以在 sandbox state 下保存一个 provider-level thread index。

命名规则：

- Host domain 使用 `AgentThreadID`、`AgentResumeInfo.ThreadID`。
- Runtime JS / SDK 输出 `threadId`。
- Host cell artifact 使用 `agent-thread.json`。
- Provider state payload 使用 `threadId`。
- 第三方 provider 原生协议中的 `session_id`、`sessionId`、`--session`、`resume` 字段只在 `runtime/javascript/src/runners/*` adapter 内出现，转换后对 agent-compose runtime contract 暴露 `threadId`。

### Auth Session

登录态、cookie、OAuth、UI server browser session 仍可称为 session。它与 sandbox 命名无关，`AUTH_SESSION_TTL` 等 auth 语义不在本次重命名范围内。

## 架构和组件边界

### Daemon

daemon 是 sandbox 状态权威，负责：

- 创建、恢复、停止、删除、查询、统计 sandbox。
- 持久化 sandbox 文件树和 SQLite 关系。
- 注册 v1 compatibility handlers 和 v2 sandbox-native handlers。
- 启动 background reconciler、loader manager、event dispatcher、capability proxy。
- 生成 runtime env、runtime mount manifest、Jupyter proxy state、runtime LLM facade token。

`pkg/agentcompose/app` 注册服务时应以 sandbox-native 依赖为内部默认；v1 service 注册仍存在，但 handler 不再直接暴露内部 session store 类型。

### Storage

文件存储 owner 从 `pkg/storage/sessionstore` 收敛为 sandbox store。目标布局：

```text
<DATA_ROOT>/
  data.db
  sandboxes/<sandbox_id>/
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

SQLite `DATA_ROOT/data.db` 仍是全局配置、project、loader、event、LLM config 的 authority，但新 schema 字段统一为 `sandbox_id`、`linked_sandbox_id`、`linked_agent_thread_id`、`thread_id`。

首版不读取旧 `<DATA_ROOT>/sessions` 或旧 schema。配置加载或 store 初始化发现旧数据时返回错误，错误信息必须指明：

- 检测到的旧路径或旧列。
- 当前版本期望 `SANDBOX_ROOT` / `<DATA_ROOT>/sandboxes` 和新 schema。
- 首版不支持自动迁移，操作者需要使用全新数据根或手动清空旧数据。

### Runtime Driver

driver domain 从 `SessionRuntime` 收敛为 `SandboxRuntime`：

- `EnsureSession` -> `EnsureSandbox`
- `StopSession` -> `StopSandbox`
- `IsSessionAlive` -> `IsSandboxAlive`
- `SessionVMInfo` -> `SandboxVMInfo`
- `ResolveSessionRuntimeDriver` -> `ResolveSandboxRuntimeDriver`
- `ResolveSessionGuestImage` -> `ResolveSandboxGuestImage`

Docker label、boxlite runtime ref、microsandbox cache references、mount manifest 内部 JSON 字段使用 sandbox 命名。第三方 runtime 本身使用的外部字段不强行改名。

### CLI

CLI 是 daemon client，不直接读写 sandbox 文件或 SQLite。用户可见命令使用 sandbox：

- `run --sandbox-id`
- `ps` 显示 `SANDBOX ID`
- `exec <sandbox>`
- `logs --sandbox`
- `inspect sandbox`
- `sandbox stop|resume|rm|prune`
- `stats <sandbox>`

`inspect session` 保留为 deprecated alias，只调用 `inspect sandbox` 逻辑并输出 deprecation warning。除兼容 alias 外，CLI JSON 输出不再包含 `session_id`。

### Loader Runtime

loader scheduler 运行在 daemon 内部 QJS 环境，仍由 daemon 调用 sandbox 创建、agent、exec、shell、LLM 和 event 发布能力。新 API 面向 sandbox：

- `scheduler.sandbox.createSandbox(request)`
- `scheduler.sandbox.resumeSandbox(request)`
- `scheduler.sandbox.stopSandbox(request)`
- `scheduler.sandbox.getSandbox(request)`
- `scheduler.sandbox.listSandboxes(request)`
- `scheduler.sandbox.getSandboxProxy(request)`

旧 `scheduler.session.*` 作为 deprecated compatibility alias 保留，行为等同 `scheduler.sandbox.*`，并在 loader event 或 validation warning 中标记 deprecated。旧 alias 是允许残留 `session` 命名的兼容边界。

`scheduler.agent`、`scheduler.exec`、`scheduler.shell` 的 options 使用 `sandboxPolicy`、`sandboxEnv`。旧 `sessionPolicy`、`session_env`、`sessionEnv` 作为 deprecated aliases 映射到新字段。

## API、CLI、配置和数据模型

### v1 API

不修改以下文件的 wire contract：

- `proto/agentcompose/v1/agentcompose.proto`
- `proto/agentcompose/v1/*.pb.go`
- `proto/agentcompose/v1/*connect/*.go`

允许修改 v1 handler 实现，但必须保持：

- `SessionService` 方法名、request、response、stream event 类型不变。
- `KernelService.ExecuteCell/ListCells` 仍接收 `session_id`。
- `AgentService.SendAgentMessage/ListSessionEvents` 仍接收 `session_id` 并返回 `agent_session_id`。
- `AgentDefinitionService.CreateAgentSession` 仍返回 `SessionResponse`。
- `LoaderService` v1 shape 中的 `session_policy`、`linked_session_id`、`linked_agent_session_id` 不变。
- v1 dashboard/capability/config 中任何历史 session 字段不改 wire shape。

v1 mapping 规则：

| v1 字段 | 内部字段 |
| --- | --- |
| `session_id` | `sandbox_id` |
| `Session*` message | `Sandbox*` domain |
| `agent_session_id` | `agent_thread_id` |
| `linked_session_id` | `linked_sandbox_id` |
| `linked_agent_session_id` | `linked_agent_thread_id` |

### v2 API

`proto/agentcompose/v2/agentcompose.proto` 是破坏式清理面。目标规则：

- 删除或替换所有 v2 public `session_id` 字段，只保留 `sandbox_id`。
- 类型名、enum 名、selector 名使用 sandbox。
- 对删除字段使用 `reserved <field_number>; reserved "<field_name>";`，除非同一 field number 直接同语义重命名为 `sandbox_id`。

具体 wire shape：

| 当前字段或类型 | 目标字段或类型 | field number 策略 |
| --- | --- | --- |
| `RunSessionCleanupPolicy` | `RunSandboxCleanupPolicy` | enum value numbers 0..3 保持；value 名改为 `RUN_SANDBOX_CLEANUP_POLICY_*` |
| `RunAgentRequest.session_id = 5` | 删除 | reserve 5 和 `"session_id"`；使用现有 `sandbox_id = 15` |
| `RunAgentRequest.cleanup_policy = 7` | 类型改为 `RunSandboxCleanupPolicy` | field number 7 保持 |
| `ListRunsRequest.session_id = 3` | 删除 | reserve 3 和 `"session_id"`；使用现有 `sandbox_id = 11` |
| `RunSummary.session_id = 11` | 删除 | reserve 11 和 `"session_id"`；使用现有 `sandbox_id = 20` |
| `ExecRequest.session_id = 1` | `sandbox_id = 1` | oneof field number 1 保持，reserved name `"session_id"` |
| `ExecSessionSelector` | `ExecSandboxSelector` | message 类型重命名；字段不变 |
| `ExecStreamResponse.session_id = 3` | `sandbox_id = 3` | field number 3 保持，reserved name `"session_id"` |
| `ExecResult.session_id = 2` | `sandbox_id = 2` | field number 2 保持，reserved name `"session_id"` |
| `RemoveProject.stop_running_sessions = 3` | `stop_running_sandboxes = 3` | field number 3 保持，reserved name `"stop_running_sessions"` |
| `CacheDomain.CACHE_DOMAIN_SESSION_EPHEMERAL_STATE = 4` | `CACHE_DOMAIN_SANDBOX_EPHEMERAL_STATE = 4` | enum number 4 保持，reserved old enum value name |
| `CacheItem.session_id = 10` | 删除 | reserve 10 和 `"session_id"`；使用现有 `sandbox_id = 11` |

v2 response 不再为了兼容填充 `session_id` 空字符串。所有 v2 server/client 读取逻辑必须使用 `sandbox_id`。

### CLI 输出

CLI 文本输出维持当前 sandbox 术语。JSON 输出使用：

- `sandbox_id`
- `sandbox_short_id`
- `agent_thread_id`
- `thread_id`
- `linked_sandbox_id`
- `linked_agent_thread_id`

不得输出新的 `session_id`，除非命令是 deprecated `inspect session` 的 stderr warning 或 v1 compatibility 调试输出。

### 配置和环境变量

运行实例根目录配置：

| 旧变量 | 新变量 | 默认 |
| --- | --- | --- |
| `SESSION_ROOT` | `SANDBOX_ROOT` | `<DATA_ROOT>/sandboxes` |
| `DOCKER_HOST_SESSION_ROOT` | `DOCKER_HOST_SANDBOX_ROOT` | 空；Docker bind mount 不 rebase |
| `SESSION_START_TIMEOUT` | `SANDBOX_START_TIMEOUT` | `30m` |
| `SESSION_STOP_TIMEOUT` | `SANDBOX_STOP_TIMEOUT` | `30s` |

配置加载规则：

- `Config.SessionRoot` 改为 `Config.SandboxRoot`。
- `Config.DockerHostSessionRoot` 改为 `Config.DockerHostSandboxRoot`。
- `Config.SessionStartTimeout` / `Config.SessionStopTimeout` 改为 `SandboxStartTimeout` / `SandboxStopTimeout`。
- 旧变量不作为 silent fallback。若检测到旧变量且新变量未设置，配置加载失败并提示新变量名。这避免旧部署无意继续写入 `sessions` 目录。
- `JUPYTER_PROXY_BASE` 保持原名，因为它是 Jupyter proxy base，不是 session 语义。默认仍可为 `/jupyter`；proxy path 中的 path parameter 改为 sandbox ID。
- `AUTH_SESSION_TTL` 或 UI/browser auth session 相关变量不改名。

Docker/Compose：

- `Dockerfile` image ENV 使用 `SANDBOX_ROOT=/data/sandboxes`。
- `docker-compose.yml` 只暴露 deploy-time 变量，使用 `DOCKER_HOST_SANDBOX_ROOT`；不新增本地 build-only 默认。
- `.env.example` 按部署用途分组记录新变量；旧变量只在 migration/breaking-change 注释中说明已废弃，不提供可复制默认值。
- `README.md` 和 `docs/zh-CN/README.md` 同步更新。

### Guest runtime env

daemon 注入 guest 的运行实例标识改为：

```text
SANDBOX_ID=<sandbox_id>
WORKSPACE=/workspace
STATE_ROOT=/data/state
RUNTIME_ROOT=/data/runtime
VERSION=<version>
```

不再注入 `SESSION_ID`。如果第三方工具需要 provider-native session 概念，adapter 自己负责转换，不依赖全局 `SESSION_ID`。

Runtime LLM Facade env 改为 sandbox token 命名：

```text
AGENT_COMPOSE_SANDBOX_TOKEN=<llm_facade_token>
LLM_API_KEY=<llm_facade_token>
LLM_API_ENDPOINT=<guest-reachable-facade-family-base-url>
LLM_API_PROTOCOL=<responses|chat_completions|messages>
```

OpenAI/Codex 兼容：

```text
OPENAI_API_KEY=<llm_facade_token>
OPENAI_BASE_URL=<guest-reachable-agent-compose-url>/api/runtime/sandboxes/<sandbox_id>/llm/openai/v1
```

Anthropic/Claude 兼容：

```text
ANTHROPIC_API_KEY=<llm_facade_token>
ANTHROPIC_BASE_URL=<guest-reachable-agent-compose-url>/api/runtime/sandboxes/<sandbox_id>/llm/anthropic
```

`AGENT_COMPOSE_SESSION_TOKEN` 不再由 daemon 写入。长期 provider key 仍不得进入 runtime。

### Runtime HTTP facade

runtime LLM facade path 改为：

```text
POST /api/runtime/sandboxes/:sandbox_id/llm/openai/v1/responses
POST /api/runtime/sandboxes/:sandbox_id/llm/openai/v1/chat/completions
POST /api/runtime/sandboxes/:sandbox_id/llm/anthropic/v1/messages
```

校验规则同步为：

- token 必须属于 path 中的 `sandbox_id`。
- token 未撤销且未过期。
- sandbox 存在且未停止。
- 请求 body 中的 model 与 token scope 一致。
- token scope 的 provider/wire_api 限制继续生效。

`llm_facade_token` 表字段改为 `sandbox_id`，索引改为 `idx_llm_facade_token_sandbox`。

### 数据模型和 SQLite schema

新 schema 使用以下字段名：

| 当前表/字段 | 目标字段 |
| --- | --- |
| `loader.session_policy` | `loader.sandbox_policy` |
| `loader_binding.session_id` | `loader_binding.sandbox_id` |
| `loader_event.linked_session_id` | `loader_event.linked_sandbox_id` |
| `loader_event.linked_agent_session_id` | `loader_event.linked_agent_thread_id` |
| `event_session_link` | `event_sandbox_link` |
| `event_session_link.session_id` | `event_sandbox_link.sandbox_id` |
| `llm_facade_token.session_id` | `llm_facade_token.sandbox_id` |
| `project_run.sandbox_id` | 保持 |

模型命名：

- `ProjectSessionRelationFilter` -> `ProjectSandboxRelationFilter`
- `ProjectSessionStatus` -> `ProjectSandboxStatus`
- `LoaderSessionPolicy*` -> `LoaderSandboxPolicy*`
- `LoaderBinding.SessionID` -> `SandboxID`
- `LoaderAgentResult.SessionID` -> `SandboxID`
- `LoaderCommandResult.SessionID` -> `SandboxID`
- `TopicEventSessionLink` -> `TopicEventSandboxLink`

首版不提供旧 schema 到新 schema 的自动迁移。检测到旧表或旧列时失败。测试必须覆盖旧 schema 拒绝路径。

### Runtime JS / SDK protocol

`__AGENT_RESULT__` payload 改为：

```text
__AGENT_RESULT__{"provider":"codex","threadId":"...","stopReason":"completed","finalText":"...","transcript":"...","stderr":""}
```

Runtime types：

- `AgentResult.sessionId` -> `threadId`
- `StoredSession` -> `StoredThread`
- `readStoredSession` -> `readStoredThread`
- `writeStoredSession` -> `writeStoredThread`

Provider state 文件路径可保持：

```text
/data/state/agents/providers/<provider>.json
```

但 payload 改为：

```json
{
  "provider": "codex",
  "threadId": "<provider-thread-id>",
  "updatedAt": "2026-01-01T00:00:00.000Z"
}
```

Host cell artifact 改为：

```text
/data/state/cells/<cell_id>/agent-thread.json
```

artifact 内容使用：

- `provider`
- `thread_id`
- `thread_state_path`
- `thread_manifest_path`
- `provider_log_paths`
- `updated_at`

`NotebookCell.AgentSessionID` 改为 `AgentThreadID`。v1 `AgentRunToProto` 和 `CellToProto` 把该字段映射回 `agent_session_id`。

### 允许残留的 session 命名

收敛完成后，`rg -n "\bsession\b|session_id|sessionId|Session"` 的残留必须能归入以下类别：

- `proto/agentcompose/v1`、v1 generated code、v1 compatibility handler、v1 tests。
- Deprecated compatibility aliases：`inspect session`、`scheduler.session.*`、旧 loader option aliases、相关 warning 文案和测试。
- Auth/browser login session、cookie session、`AUTH_SESSION_TTL`。
- 第三方 provider adapter 内部原生协议：Claude `session_id`、OpenCode `--session`、第三方 JSON event 的 `sessionId`。
- 引用历史行为或 breaking change 的 migration/error 文案。

除上述类别外，内部 domain、v2 API、runtime env、SQLite、new docs 不应保留 session 命名。

## 工作流和失败语义

### 创建 sandbox

创建 sandbox 的目标流程：

1. API/loader/run 请求解析 sandbox-native 字段。
2. daemon 合并 global/project/agent/request env，过滤 LLM provider key。
3. sandbox store 在 `SANDBOX_ROOT/<sandbox_id>` 创建目录和 metadata。
4. 写入 VM state、proxy state、cells/events 初始文件。
5. 写 capability guide 和 runtime LLM facade config。
6. runtime driver 根据 mount manifest 启动 sandbox。
7. sandbox 状态变为 `RUNNING`，记录 `sandbox.created` event，发布 topic event。
8. v1 compatibility handler 返回 `SessionResponse`，其中 `session_id` 等于 sandbox ID。

失败语义：

- 目录创建失败：返回 internal，日志键使用 `sandbox_id`。
- driver resolve/start 失败：sandbox 状态标记为 `FAILED`，记录 `sandbox.start.failed`。
- capability guide 渲染失败仍为 best-effort，记录 warning event，不阻塞创建。
- LLM facade config 失败按现有语义区分必需/可选；不可泄露 provider key。

### 恢复、停止、删除和 reconcile

- `ResumeSandbox` 重新准备 workspace 并启动 runtime，成功记录 `sandbox.resumed`。
- `StopSandbox` 停止 runtime，撤销 sandbox-scoped LLM facade token、capability token，状态变为 `STOPPED`，记录 `sandbox.stopped`。
- `RemoveSandbox(force=false)` 遇到 running sandbox 返回 failed precondition；`force=true` 先 stop 再删除文件树。
- 启动时、Get/List/Stop/Stats 前的 runtime reconcile 使用 sandbox 命名；reconcile 发现 runtime 消失时标记 stopped/failed 并撤销 token。

v1 `StopSession`、`ResumeSession`、`GetSession` 调用相同逻辑，但错误 message 可以继续使用 v1 客户端习惯的 “session” 字样。

### Run / Exec / Loader

- `RunService.RunAgent` 新请求只接受 `sandbox_id` 复用既有 sandbox。缺少 `sandbox_id` 时创建新 sandbox。
- cleanup policy 为 sandbox policy：默认 stop-on-completion，`KEEP_RUNNING` 保持运行，`REMOVE_ON_COMPLETION` 成功后删除 sandbox。
- `ExecService` 不创建 sandbox，只能在 running sandbox 中执行命令；target 可以是 `sandbox_id`、`run_id`、project/agent selector。
- loader sticky policy 绑定 `loader_id -> sandbox_id`；同一 loader run 内 command/shell 调用复用 run-scoped loader sandbox。
- 旧 loader `sessionPolicy/sessionEnv` alias 解析成新字段后，内部事件、结果和持久化使用 sandbox/thread 命名。

### 旧数据拒绝

首版启动或 store 初始化必须检测：

- `<DATA_ROOT>/sessions` 存在且非空，同时新 `SANDBOX_ROOT` 未显式指向其他新路径。
- `DATA_ROOT/data.db` 中存在旧列：`loader_binding.session_id`、`loader_event.linked_session_id`、`loader_event.linked_agent_session_id`、`event_session_link`、`llm_facade_token.session_id`。
- 旧 env：`SESSION_ROOT`、`DOCKER_HOST_SESSION_ROOT`、`SESSION_START_TIMEOUT`、`SESSION_STOP_TIMEOUT` 在新 env 未设置时出现。

检测到上述情况时返回可诊断错误，不自动迁移、不自动复制、不自动重命名目录。

## 测试、质量门禁和验收标准

### 必跑门禁

- `task lint`
- `task build`
- `task test`
- `cd runtime/javascript && npm run test:unit`
- `cd runtime/agent-compose-runtime-sdk && npm test`
- `cd proto-client && npm run gen && npm run build`

Go proto 生成必须显式执行或新增 task。若直接使用 protoc，命令形态应覆盖 v1/v2/health：

```bash
protoc -I proto \
  --go_out=. --go_opt=paths=source_relative \
  --connect-go_out=. --connect-go_opt=paths=source_relative \
  proto/health/v1/health.proto \
  proto/agentcompose/v1/agentcompose.proto \
  proto/agentcompose/v2/agentcompose.proto
```

### 单元测试

覆盖：

- v2 proto mapping：所有 v2 response 只填 `sandbox_id`，不填 `session_id`。
- v1 compatibility mapping：v1 request `session_id` 能定位 sandbox，response 保持旧字段。
- config：新 env 正常，旧 env 单独出现时报错，新旧同时出现时新 env 优先并对旧 env 报明确冲突或 warning。
- sandbox store：目录布局、metadata JSON、RemoveSandbox path safety、旧 `sessions` 目录拒绝。
- SQLite schema：新表/列创建；旧 schema 拒绝；`project_run.sandbox_id` 保持。
- runtime parser：`threadId` payload 解析；缺失 payload 和 provider 原生 session 字段解析错误分类。
- loader option aliases：`sandboxPolicy/sandboxEnv` 主路径；`sessionPolicy/sessionEnv` deprecated alias 映射。
- runtimecache：`sandbox-ephemeral-state` domain/type/filter/id/reference。

### 集成测试

覆盖：

- v1 `SessionService` 创建、获取、停止、恢复、watch、proxy 在内部 sandbox store 上工作。
- v1 `KernelService` / `AgentService` 使用 `session_id` 执行 cell/agent，内部 cell 记录 `AgentThreadID`，v1 返回 `agent_session_id`。
- v2 `RunService` 创建 sandbox、复用 `sandbox_id`、`ListRuns(sandbox_id)`、`RunSummary.sandbox_id`。
- v2 `ExecService` 通过 `sandbox_id`、`run_id`、selector 执行命令。
- `SandboxService.RemoveSandbox`、`GetSandboxStats` 只使用 sandbox 字段。
- loader sticky sandbox binding、loader command result、loader event linked sandbox/thread。
- runtime LLM facade 新 path、token scope sandbox 校验、停止 sandbox 后 token revoke。
- capability token 索引从 token -> sandbox/capset 重建和撤销。

### E2E / CLI 测试

覆盖：

- `agent-compose run <agent> --sandbox-id <id>`。
- `agent-compose ps --json` 不包含 `session_id`。
- `agent-compose exec <sandbox> --command ...`。
- `agent-compose logs --sandbox <sandbox>`。
- `agent-compose inspect sandbox <sandbox>`。
- `agent-compose inspect session <sandbox>` 输出 deprecated warning，JSON shape 仍是 sandbox output。
- `agent-compose sandbox stop|resume|rm|prune`。
- Docker compose env 使用 `SANDBOX_ROOT` / `DOCKER_HOST_SANDBOX_ROOT`。

### 文档验收

必须更新：

- `AGENTS.md`
- `README.md`
- `.env.example`
- `Dockerfile`
- `docker-compose.yml`
- `docs/design/agent-compose_design.md`
- `docs/design/agent-compose-runtime_contract.md`
- `docs/design/runtime_environment_variables_design.md`
- `docs/zh-CN/design/*` 对应中文文档
- `docs/command-line-manual.md`
- `docs/zh-CN/command-line-manual.md`

文档中的 `session` 残留必须落入“允许残留的 session 命名”类别。

## 首版不做事项

- 不修改 `proto/agentcompose/v1/agentcompose.proto` 或 v1 generated code。
- 不提供旧 `<DATA_ROOT>/sessions` 到 `<DATA_ROOT>/sandboxes` 的自动迁移。
- 不提供旧 SQLite schema 到新 schema 的自动迁移。
- 不保证旧 v2 generated clients 兼容；v2 是破坏式重命名。
- 不把 UI/browser auth session、OAuth session、cookie session 改名。
- 不重命名第三方 provider 原生协议字段；只在 adapter 边界转换。
- 不新增复杂 Node.js workflow、`scheduler.run`、workflow bridge token 或新的 runtime 子命令。
- 不改变 runtime driver 支持矩阵，仍为 `docker`、`boxlite`、`microsandbox`。
- 不改变 Jupyter proxy base 变量 `JUPYTER_PROXY_BASE` 的语义。

## 关键假设和已确认决策

- v1 对外 API 和行为完全兼容；兼容只承诺 API wire shape，不承诺旧持久化数据可读。
- v2 是破坏式 API，必须清理 legacy `session_id` 字段并重新生成 Go/TS client。
- 内部运行实例统一叫 sandbox；provider 续接统一叫 thread。
- 旧数据目录和旧 SQLite schema 不兼容；首版通过显式检测和报错处理，不做自动迁移。
- 新默认 sandbox 根目录为 `<DATA_ROOT>/sandboxes`。
- `AUTH_SESSION_TTL` 等登录态 session 命名保留。
- Deprecated aliases (`inspect session`、`scheduler.session.*`、`sessionPolicy/sessionEnv`) 可以短期存在，但必须集中在兼容层并有测试覆盖。
- `rg session` 的验收不是零残留，而是所有残留都能解释为 v1 compatibility、deprecated alias、auth session、第三方 provider native protocol 或 migration/error 文案。
