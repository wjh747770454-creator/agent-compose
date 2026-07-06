# Runtime output protocol contract implementation plan

## 计划前提

本计划对应 `docs/spec/output-protocol-contract-spec.md`，目标是在不改 CLI 参数、不迁移 `proto/agentcompose/v1`、不新增 typed payload event 的前提下，统一 runtime stdio stream 语义，修复 stdout chunk 被协议路径误丢弃的风险，并同步 v2 API、CLI、runtime contract、测试和生成产物。

已确认的 harness 约束：

- 主质量门禁是 `task lint`、`task build`、`task test`。
- `task test` 必须输出并满足 unit、integration、E2E 和 combined coverage baseline：unit/integration/E2E 至少 60%，combined 至少 70%。
- CI 还直接运行 `go test ./cmd/... ./pkg/...`、`./scripts/test-coverage.sh`、`runtime/javascript` 的 `npm run test:unit`、`runtime/agent-compose-runtime-sdk` 的 `npm test` 和 `npm run test:packaging`、`proto-client` 的 `npm run gen` 和 `npm run build`。
- 涉及 v2 proto 时必须更新 Go/Connect 生成代码和 `proto-client` TypeScript 生成代码。`proto/agentcompose/v1` 的历史 `is_stderr` 字段保持不变。

风险和停止条件：

- 如果执行中发现外部 `agent-compose-ui` 必须在同一仓库内同步迁移才能保持发布可用，应停止并要求确认发布切分；本仓库内只需保证 `proto-client` 生成和构建通过。
- 如果实际 generator 版本或本机 `protoc` 不可用，先不要手写生成文件；应安装/启用 `go tool protoc-gen-go`、`go tool protoc-gen-connect-go` 和 `proto-client` npm 依赖后再生成。
- 如果某个 runtime driver 的真实 stdout/stderr API 无法区分 stream，必须停止并记录该 driver 的 fallback 语义；不能用 payload marker 或 agent/command 模式反推 stdio stream。

## 阶段 1：建立 stdio stream 领域模型和协议过滤入口

目标：先在 Go 内部建立 `StdioStream` enum、零值 stdout 语义和唯一 marker 过滤 helper，使后续迁移有稳定落点。

依赖：无。

实施工作：

1. 在 `pkg/model/model.go` 增加 domain `StdioStream` 类型和常量：`StdioStdout = "stdout"`、`StdioStderr = "stderr"`。
2. 将 domain `ExecChunk{Text, IsStderr}` 改为 `ExecChunk{Text, Stream}`，并提供小型 helper，例如 `NormalizeStdioStream(stream StdioStream) StdioStream`、`ExecChunk.IsStdout()` 或同等私有函数，确保 `Stream == ""` 按 stdout 处理。
3. 在 `pkg/execution/exec_result.go` 迁移 `ExecStreamAccumulator.WriteChunk`，按 normalized stream 聚合 stdout/stderr/output。
4. 在 `pkg/execution/parse.go` 增加统一 stream filter helper，名称可按实现微调，但语义必须等价：
   - `FilterCommandStreamChunk(chunk domain.ExecChunk) (domain.ExecChunk, bool)` 调用 `StripCommandResultPayload`，保留原始 stream，空文本返回 `false`。
   - `FilterAgentStreamChunk(chunk domain.ExecChunk) (domain.ExecChunk, bool)` 调用 `StripAgentResultPayload`，stderr 可见，stdout payload 被剥离，stdout 上 marker 之外的文本必须作为 stdout transcript 保留。
5. 保留现有 final result parse/sanitize 行为：`ParseAgentExecResult`、`ParseCommandExecResult`、`SanitizeAgentExecResult` 仍只通过 marker 判断 payload。
6. 迁移当前最小编译面中的 domain `ExecChunk` 构造和断言，优先把默认 stdout chunk 写成 `domain.ExecChunk{Text: ...}`，stderr chunk 写成 `Stream: domain.StdioStderr`。

测试和验证：

- 增加或更新 `pkg/execution` unit tests：
  - `ExecStreamAccumulator` 将空 stream 和 stdout stream 聚合进 stdout。
  - stderr stream 聚合进 stderr。
  - `FilterCommandStreamChunk` 保留 stdout chunk。
  - `FilterCommandStreamChunk` 剥离 `__COMMAND_RESULT__` 且 payload 不进入 transcript。
  - `FilterAgentStreamChunk` 剥离 `__AGENT_RESULT__`。
  - agent stdout 中 marker 前存在可见文本时不静默丢弃。
- 运行 focused gate：
  - `./scripts/with-go-toolchain.sh go test ./pkg/execution`

验收标准：

- `pkg/model` 不再暴露 domain `ExecChunk.IsStderr`。
- `pkg/execution` 是 host 侧 marker stream filtering 的唯一归口。
- Go 零值 stream 行为保持历史 stdout 兼容。
- 本阶段后 `pkg/execution` tests 通过，项目可继续编译迁移。

适用 harness 约束或命令：

- `task lint` 最终必须通过；本阶段代码应保持 gofmt/golangci-lint 可接受。

## 阶段 2：迁移 runtime driver 层为 driver-local StdioStream

目标：让 `docker`、`boxlite`、`microsandbox` driver 只表达原始 stdout/stderr，不承载 agent/command 协议语义。

依赖：阶段 1。

实施工作：

1. 在 `pkg/driver/types.go` 增加 driver-local `StdioStream` 类型和常量，字段值与 domain enum 对齐：`stdout`、`stderr`。
2. 将 driver `ExecChunk{Text, IsStderr}` 改为 `ExecChunk{Text, Stream}`，零值 stream 按 stdout 处理。
3. 迁移 docker collector/writer：
   - `dockerExecWriter` 内部可继续保存 bool 或 stream，但写出的 `ExecChunk` 必须设置 `Stream`。
   - `dockerExecCollector.appendChunk` 按 normalized stream 聚合 stdout/stderr。
4. 迁移 boxlite 和 microsandbox stream collector，保持真实 stdout/stderr 到 `StdioStream` 的映射。
5. 迁移 `pkg/driver/exec_output_filter.go`，只按 `chunk.Stream == StdioStderr` 判断是否过滤 seccomp warning；过滤后 emit 的真实 stderr 保持 `StdioStderr`。
6. 确认 driver 层没有识别 `__AGENT_RESULT__` 或 `__COMMAND_RESULT__`。

测试和验证：

- 更新 `pkg/driver/exec_output_filter_test.go`，断言真实 stderr 仍保留 `StdioStderr`。
- 增加或更新 driver collector tests，覆盖 docker、boxlite、microsandbox 的 stdout/stderr stream 映射；无法启动真实 runtime 的路径使用已有 fake/collector 单元测试。
- 运行 focused gate：
  - `./scripts/with-go-toolchain.sh go test ./pkg/driver`

验收标准：

- `pkg/driver` 不再暴露 `ExecChunk.IsStderr`。
- 三个 runtime driver 的 `ExecStream` 方法签名不变，只改变 chunk 字段类型。
- driver 层不包含任何 marker 解析或 agent/command 模式分支。

适用 harness 约束或命令：

- `task test` 的 Go coverage scope 包含 `./pkg/...`；新增 driver 代码必须有对应 unit 或 integration 覆盖。

## 阶段 3：迁移 adapter、agent、command、loader 和 session stream 路径

目标：所有 host callsite 使用 `ExecChunk.Stream`，并通过 `pkg/execution` 的 filter helper 决定可见 transcript，修复 agent stdout 静默丢弃和 loader command payload 暴露风险。

依赖：阶段 1、阶段 2。

实施工作：

1. 在 `pkg/agentcompose/adapters/runtime_provider.go` 增加 driver enum 到 domain enum 的显式映射函数；未知或空 driver stream 映射为 domain stdout。
2. 迁移 `pkg/agentcompose/adapters/agent_executor.go`：
   - 删除 `if !chunk.IsStderr { return }` 这类静默 stdout guard。
   - 使用 `execution.FilterAgentStreamChunk`。
   - filtered stdout 写入 cell stdout/output，filtered stderr 写入 cell stderr/output。
   - publish stream 和 `stream.OnChunk` 使用 filtered chunk。
3. 迁移 `pkg/agentcompose/adapters/loader_command_executor.go`：
   - 使用 `execution.FilterCommandStreamChunk`。
   - streaming/interim cell output、session stream broker 和最终 stream callback 都不能暴露 `__COMMAND_RESULT__`。
4. 迁移 `pkg/agentcompose/api/exec.go` 的 `ExecStream` writer，替换手写 `StripCommandResultPayload` 为 `FilterCommandStreamChunk`。
5. 迁移 `pkg/runs/controller.go` 中 command run streaming/log append 路径，确保 run logs 只写 human transcript，stdout/stderr stream 属性保留。
6. 迁移 `pkg/agentcompose/adapters/cell_executor.go`、`pkg/agentcompose/api/kernel.go`、`pkg/agentcompose/api/agent_handler.go`、`pkg/sessions/stream.go`、`pkg/agentcompose/api/session_model.go` 等仍需要 v1/session stream bool 的边界：
   - 内部使用 domain stream。
   - 仅在 v1 或历史 session event 输出处转换为 `is_stderr` bool。
7. 用 `rg -n "IsStderr|isStderr|GetIsStderr"` 检查 Go domain/driver/host paths，保留项只能是 v1 proto、生成代码迁移前的 v2、或明确的外部兼容映射。

测试和验证：

- 更新 `pkg/agentcompose/adapters` tests：
  - agent prompt stderr transcript 正常进入 stream/cell output。
  - agent stdout payload 不进入 stream/cell output。
  - agent stdout 非 payload 文本作为 stdout transcript 可见。
  - driver enum 到 domain enum 的 adapter 映射正确。
  - loader command streaming 不暴露 `__COMMAND_RESULT__`。
- 更新 `pkg/agentcompose/api` tests：
  - `ExecStream` stdout 用户输出实时转发且 stream 为 stdout。
  - `ExecStream` stderr 用户输出实时转发且 stream 为 stderr。
  - `__COMMAND_RESULT__` 不进入 transcript 或 transcript file。
- 更新 `pkg/runs` tests：
  - `run --command` stdout/stderr 都写入 transcript/logs。
  - payload 不进入 `transcript.txt` 或 run logs。
  - 非零 exit code 保留 stdout/stderr/output 并标记 run failed。
- 运行 focused gates：
  - `./scripts/with-go-toolchain.sh go test ./pkg/agentcompose/adapters ./pkg/agentcompose/api ./pkg/runs ./pkg/sessions`

验收标准：

- command/exec host path 不再通过 `Stream == StdioStderr` 或同类 guard 丢弃 stdout。
- agent prompt stdout marker 外文本可见，stdout payload 不可见。
- loader command streaming 通过统一 helper 剥离 payload。
- v1/session 兼容 bool 只在协议边界转换，内部不再回流 bool 语义。

适用 harness 约束或命令：

- 本阶段跨 service boundary 和 persistence/log path，必须有 unit 和 integration 形态覆盖；测试命名按仓库现有规则包含 `Integration` 时会进入 integration shape。

## 阶段 4：升级 v2 proto stream API、生成代码和 CLI stream writer

目标：将 v2 stream API 从 `is_stderr`/`kind` 迁移到 `StdioStream stream` enum，并让 CLI 从 v2 stream enum 选择本机 stdout/stderr。

依赖：阶段 1、阶段 3。

实施工作：

1. 在 `proto/agentcompose/v2/agentcompose.proto` 增加：
   - `STDIO_STREAM_UNSPECIFIED = 0`
   - `STDIO_STREAM_STDOUT = 1`
   - `STDIO_STREAM_STDERR = 2`
2. 修改 v2 message：
   - `RunAgentStreamResponse.is_stderr` 改为 `StdioStream stream = 5`。
   - `ExecStreamResponse.is_stderr` 改为 `StdioStream stream = 6`。
   - `TranscriptEvent.kind` 和 `TranscriptEvent.is_stderr` 改为 `StdioStream stream = 1`，`text/name/payload_json/created_at` 语义保持不变，字段号按 spec 允许破坏性变更处理。
3. 不修改 `proto/agentcompose/v1/agentcompose.proto`。
4. 重新生成 Go proto/Connect 代码。建议命令：
   - `go tool protoc-gen-go --version`
   - `go tool protoc-gen-connect-go --version`
   - `protoc -I proto --go_out=. --go_opt=paths=source_relative --connect-go_out=. --connect-go_opt=paths=source_relative proto/health/v1/health.proto proto/agentcompose/v1/agentcompose.proto proto/agentcompose/v2/agentcompose.proto`
5. 更新 `pkg/agentcompose/api/run.go` 中 `TranscriptEventFromExecChunk`，返回 `agentcomposev2.StdioStream`。
6. 更新 `pkg/agentcompose/app/run_controller.go` 和 `pkg/agentcompose/api/exec.go`，发送 `Stream` 字段，不再发送 v2 `IsStderr`。
7. 更新 `cmd/agent-compose/main.go`：
   - `writeTranscriptOrChunk` 改为从 `TranscriptEvent.Stream` 或 event `Stream` 选择 stdout/stderr。
   - `STDIO_STREAM_UNSPECIFIED` 按 stdout。
   - `--json` 仍 suppress 实时 transcript，只输出最终 JSON。
8. 更新 CLI tests 中的 v2 fake stream response，从 `IsStderr` 或 `TranscriptEvent.Kind/IsStderr` 迁移到 `Stream`。
9. 更新 `proto-client` 生成产物：
   - `cd proto-client && npm ci`
   - `npm run gen`
   - `npm run build`

测试和验证：

- 更新 `cmd/agent-compose` CLI integration/unit tests：
  - 非 JSON `run --command` 显示 stdout 和 stderr transcript。
  - CLI 根据 v2 `stream` 正确写本机 stdout/stderr。
  - `--json` suppress transcript，只输出最终 result。
  - `STDIO_STREAM_UNSPECIFIED` 作为 stdout。
- 运行 focused gates：
  - `./scripts/with-go-toolchain.sh go test ./cmd/agent-compose ./pkg/agentcompose/app ./pkg/agentcompose/api`
  - `cd proto-client && npm run gen && npm run build`

验收标准：

- v2 `RunAgentStreamResponse`、`ExecStreamResponse`、`TranscriptEvent` 不再包含 `is_stderr`。
- v2 `TranscriptEvent` 不再使用 string `kind` 表示 stdout/stderr。
- `proto-client` dist/src 生成产物反映 `StdioStream` enum。
- CLI 不再调用 v2 `GetIsStderr()` 或从 `TranscriptEvent.Kind` 推断通道。

适用 harness 约束或命令：

- `task build` 会构建 v2 Go proto 包，必须通过。
- CI 的 `proto-client` job 必须通过 `npm run gen` 和 `npm run build`。

## 阶段 5：固化 runtime/javascript 行为和 runtime contract 文档

目标：让 guest runtime 当前 stdout/stderr 行为有测试和文档证明，避免 host contract 与 JS runtime contract 分歧。

依赖：阶段 1、阶段 3。

实施工作：

1. 保持 `runtime/javascript/src/transcript.ts` 的 agent transcript writer 写 stderr。
2. 保持 `runtime/javascript/src/cli.ts`：
   - `prompt` 成功只在 stdout 写 `__AGENT_RESULT__{...}`。
   - `exec` 成功或用户命令非零退出时在 stdout 最后一行写 `__COMMAND_RESULT__{...}`。
   - top-level error 继续写 stderr 并非零退出。
3. 保持 `runtime/javascript/src/command.ts` 当前语义：用户 command stdout 镜像到 runtime stdout，用户 command stderr 镜像到 runtime stderr，stdout/stderr/output artifacts 保留用户输出，`command-result.json` 保留结构化结果。
4. 修正 `docs/design/agent-compose-runtime_contract.md`：
   - command 部分描述为保留用户 stdout/stderr 原始通道。
   - host 通过 marker stripping 保护 transcript。
   - 删除或改写“用户可见输出都镜像到 wrapper stderr”这类与当前实现不一致的描述。
   - 明确 streaming payload filter 已集中在 host helper，不再是缺口。
5. 如 README 或 `docs/command-line-manual.md` 中提到 v2 stream bool 或 command transcript 行为，按新语义同步。

测试和验证：

- 更新 `runtime/javascript` tests：
  - `command.ts` 捕获 child stdout/stderr、artifact 和 final payload 行为与 host contract 一致。
  - child stdout/stderr 分别镜像到 runtime stdout/stderr。
  - 非零 exit code 仍输出 command result payload，且 stderr 包含 exit code 说明。
  - `prompt` transcript 继续写 stderr，final result 继续写 stdout marker。
- 运行 focused gates：
  - `cd runtime/javascript && npm ci && npm run typecheck && TEST_SHAPE=unit npm run test:unit`

验收标准：

- runtime contract 文档与 `runtime/javascript/src/command.ts` 当前行为一致。
- runtime tests 证明 prompt/result marker 与 transcript stream 分离。
- 没有新增 runtime 配置项或 CLI 参数。

适用 harness 约束或命令：

- `task test` 会运行 `runtime/javascript` unit/integration/e2e coverage shapes。
- CI 的 `scheduler-runtime` job 会运行 `runtime/javascript` 的 `npm run test:unit`。

## 阶段 6：全仓清理、兼容审计和质量门禁

目标：完成跨仓库一致性检查，确保首版验收标准全部满足并通过 harness。

依赖：阶段 1 至阶段 5。

实施工作：

1. 全仓搜索并分类剩余旧字段：
   - `rg -n "IsStderr|is_stderr|isStderr|GetIsStderr|TranscriptEvent\\{Kind" cmd pkg proto runtime proto-client docs -g '!**/node_modules/**'`
   - 合法剩余项只能是 `proto/agentcompose/v1`、v1 生成代码、历史 session stream 兼容字段或文档中明确说明 v1 未迁移的文字。
2. 搜索 marker callsite：
   - `rg -n "__AGENT_RESULT__|__COMMAND_RESULT__|StripAgentResultPayload|StripCommandResultPayload|FilterAgentStreamChunk|FilterCommandStreamChunk" cmd pkg runtime docs`
   - host streaming callsite 必须使用 filter helper，不应手写 strip 或 stream 可见性判断。
3. 确认 `docs/spec/output-protocol-contract-spec.md` 的首版不做事项未被误实现：
   - 未新增 `chunk_type`、`payload_kind` 或 typed payload event。
   - 未改变 CLI 参数或 JSON schema。
   - 未改变 `BoxRuntime.ExecStream` 方法签名。
   - 未引入 stdin 转发。
4. 若 coverage shape 因测试命名未被归类，按 `scripts/run-go-test-shape.sh` 规则调整测试名：integration 测试名包含 `Integration`，E2E 测试名包含 `E2E`。
5. 运行主质量门禁并修复失败：
   - `task lint`
   - `task build`
   - `task test`
6. 运行 CI 对齐补充命令：
   - `./scripts/with-go-toolchain.sh go test ./cmd/... ./pkg/...`
   - `cd runtime/javascript && npm run test:unit`
   - `cd runtime/agent-compose-runtime-sdk && npm test && npm run test:packaging`
   - `cd proto-client && npm run gen && npm run build`

测试和验证：

- 主门禁：`task lint`、`task build`、`task test` 全部通过。
- `task test` 正常打印 unit、integration、E2E、combined coverage，且不低于 harness baseline。
- CI 对齐补充命令全部通过，或记录不可运行原因和环境缺失。

验收标准：

- domain 和 driver `ExecChunk` 不再包含 `IsStderr bool`。
- v2 stream API 统一使用 `StdioStream stream`，v1 保持历史兼容。
- host 不再根据 stdout/stderr stream 判断 payload，只根据 marker 判断 payload。
- command/exec stdout 不会被 host streaming path 丢弃。
- agent prompt stdout marker 外文本不会被静默吞掉。
- loader command streaming 不暴露 `__COMMAND_RESULT__`。
- run logs、exec transcript、notebook cell output 和 CLI stream 不包含协议 payload。
- v2 Go/Connect/TypeScript 生成产物已更新并可编译。

适用 harness 约束或命令：

- 以 `task lint`、`task build`、`task test` 作为最终合并门禁。
- 不要求在本计划内构建 Docker images，除非实现过程改动 Dockerfile、guest image 构建脚本或部署配置。

## 首版不做的事项

- 不新增 `chunk_type`、`payload_kind` 或 typed payload event。
- 不改变 CLI 参数、默认输出格式或 JSON schema。
- 不改变 runtime driver `BoxRuntime` / `ExecStream` 方法签名。
- 不引入 stdin 转发能力；prompt 和 command 继续通过文件传入。
- 不迁移 `proto/agentcompose/v1` 的历史 `is_stderr` 字段。
- 不在 driver 层识别 `__AGENT_RESULT__` 或 `__COMMAND_RESULT__`。
- 不修改 `docker-compose.yml`、`.env.example` 或部署默认值，除非后续实现发现需要新增部署配置；当前 spec 不需要。

