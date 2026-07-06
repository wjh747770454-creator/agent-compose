# Runtime output protocol contract Progress

本文档把 runtime output protocol contract 的规格和实施计划拆成可独立执行、独立验收的任务清单。任务按依赖顺序排列；标记为“可并行”的子任务可以在同一父任务内用 subagent 并行推进，但 subagent 并发度最高不超过 5。

位置：项目根目录 `PROGRESS.md`。`mass-merge-docs` 完成后默认删除本文件。

## 文档索引

- 技术规格：[docs/spec/output-protocol-contract-spec.md](docs/spec/output-protocol-contract-spec.md)
- 实施计划：[docs/plan/output-protocol-contract-implementation-plan.md](docs/plan/output-protocol-contract-implementation-plan.md)
- Harness：[AGENTS.md](AGENTS.md)
- 测试标准：[TESTING.md](TESTING.md)
- 任务入口：[Taskfile.yml](Taskfile.yml)
- CI：[.github/workflows/ci.yml](.github/workflows/ci.yml)
- Runtime contract：[docs/design/agent-compose-runtime_contract.md](docs/design/agent-compose-runtime_contract.md)
- CLI manual：[docs/command-line-manual.md](docs/command-line-manual.md)
- Runtime JS package：[runtime/javascript/package.json](runtime/javascript/package.json)
- Proto client package：[proto-client/package.json](proto-client/package.json)

## 执行规则

- [ ] 每个任务完成时必须同时完成对应测试方案和验收标准。
- [ ] 不跨阶段提前合并依赖未满足的功能；父任务依赖未满足时只能做只读调研。
- [ ] 涉及生成代码、脚本枚举、质量门禁或覆盖率范围时，必须同步更新相关脚本、文档和生成产物。
- [ ] 每个行为变更任务至少运行该任务要求的 focused tests；阶段性收口运行 harness 定义的完整门禁。
- [ ] 完成总结必须使用 `状态`、`变更`、`验证`、`审计与例外`、`下一目标` 五组，多项证据用列表或表格。
- [ ] 内部 stdio stream 只能表示 stdout/stderr 原始通道；host payload 判断只能依赖 `__AGENT_RESULT__` 和 `__COMMAND_RESULT__` marker。
- [ ] `proto/agentcompose/v1` 保持历史 `is_stderr` 字段；v2 可以破坏性迁移到 `StdioStream stream`。
- [ ] 主合并门禁是 `task lint`、`task build`、`task test`；涉及 runtime/proto client 时同步运行对应 npm/package 命令。

## 1. 阶段一：建立 stdio stream 领域模型和协议过滤入口

参考文档：[docs/spec/output-protocol-contract-spec.md](docs/spec/output-protocol-contract-spec.md)、[docs/plan/output-protocol-contract-implementation-plan.md](docs/plan/output-protocol-contract-implementation-plan.md)

- [x] 1.1 定义 domain `StdioStream` 并迁移 `ExecStreamAccumulator`
  - 依赖：无。
  - 工作内容：
    - 在 `pkg/model/model.go` 增加 `StdioStream` 类型和 `StdioStdout`、`StdioStderr` 常量。
    - 将 domain `ExecChunk{Text, IsStderr}` 迁移为 `ExecChunk{Text, Stream}`。
    - 提供零值 stream 按 stdout 处理的 helper 或等价方法。
    - 更新 `pkg/execution/exec_result.go` 的 stdout/stderr/output 聚合逻辑。
  - 可并行子任务：
    - [x] 可并行：审计 `pkg/model`、`pkg/execution` 中 domain `ExecChunk` 构造和断言。
    - [x] 可并行：补充 `ExecStreamAccumulator` 单元测试用例。
  - 测试方案：
    - `./scripts/with-go-toolchain.sh go test ./pkg/execution`
  - 验收标准：
    - domain `ExecChunk` 不再包含 `IsStderr` 字段。
    - 空 stream 和 stdout stream 都进入 stdout；stderr stream 进入 stderr；output 保持合并输出。
    - focused test 通过。
  - 完成总结：
    - 状态：已完成。
    - 变更：
      - `pkg/model/model.go` 新增 domain `StdioStream`、`StdioStdout`、`StdioStderr` 和 `NormalizeStdioStream`。
      - domain `ExecChunk` 已从 `Text/IsStderr` 迁移为 `Text/Stream`。
      - `pkg/execution/exec_result.go` 的 `ExecStreamAccumulator` 改为按 normalized stream 聚合 stdout/stderr，并继续维护合并 output。
      - `pkg/execution/exec_result_test.go` 增加 accumulator 单元测试，覆盖空 stream、stdout、stderr-first 交错顺序、未知 stream 和空文本 chunk。
    - 验证：
      - `rg -n "IsStderr|ExecChunk" pkg/model pkg/execution`：`pkg/model`、`pkg/execution` 范围内无 `IsStderr` 命中，剩余命中为 `ExecChunk` 类型/回调/测试构造。
      - `./scripts/with-go-toolchain.sh go test ./pkg/execution`：通过。
    - 审计与例外：
      - 本任务按 1.1 范围只迁移 domain model 与 `pkg/execution` accumulator；driver、adapter、API、runs、session 和 proto 调用点保留到后续已列任务迁移。
      - 只运行 1.1 要求的 focused test；完整 `task lint`、`task build`、`task test` 按阶段收口任务执行。
    - 下一目标：1.2 建立 marker stream filter helper。

- [ ] 1.2 建立 `pkg/execution` marker stream filter helper
  - 依赖：1.1。
  - 工作内容：
    - 在 `pkg/execution/parse.go` 增加 `FilterCommandStreamChunk` 和 `FilterAgentStreamChunk` 或等价命名 helper。
    - `FilterCommandStreamChunk` 调用 `StripCommandResultPayload`，保留原始 stream，空文本返回不可见。
    - `FilterAgentStreamChunk` 调用 `StripAgentResultPayload`，stderr transcript 可见，stdout payload 被剥离，stdout marker 外文本必须保留为 stdout transcript。
    - 保持 final result parser/sanitizer 只基于 marker，不基于 stream。
  - 可并行子任务：
    - [ ] 可并行：补充 command stream filter 单元测试。
    - [ ] 可并行：补充 agent stream filter 单元测试。
    - [ ] 可并行：审计现有 strip helper 的 marker 查找语义是否满足流式 chunk 场景。
  - 测试方案：
    - `./scripts/with-go-toolchain.sh go test ./pkg/execution`
  - 验收标准：
    - `__COMMAND_RESULT__` 不进入 command transcript。
    - `__AGENT_RESULT__` 不进入 agent transcript。
    - agent stdout 中 marker 前的可见文本不被静默丢弃。
    - `pkg/execution` 成为 host streaming marker filtering 的唯一归口。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：2.1 迁移 driver-local stream enum。

## 2. 阶段二：迁移 runtime driver 层为 driver-local `StdioStream`

参考文档：[docs/spec/output-protocol-contract-spec.md](docs/spec/output-protocol-contract-spec.md)、[AGENTS.md](AGENTS.md)

- [ ] 2.1 迁移 driver `ExecChunk` 和 collector stream 聚合
  - 依赖：1.1。
  - 工作内容：
    - 在 `pkg/driver/types.go` 增加 driver-local `StdioStream` 类型和 stdout/stderr 常量。
    - 将 driver `ExecChunk{Text, IsStderr}` 迁移为 `ExecChunk{Text, Stream}`。
    - 迁移 docker、boxlite、microsandbox collector/writer，使真实 stdout/stderr 映射到 driver stream enum。
    - 保持 `BoxRuntime.ExecStream` 方法签名不变，只改变 chunk 字段类型。
  - 可并行子任务：
    - [ ] 可并行：迁移 docker collector 和对应测试。
    - [ ] 可并行：迁移 boxlite collector 和对应测试。
    - [ ] 可并行：迁移 microsandbox collector 和对应测试。
  - 测试方案：
    - `./scripts/with-go-toolchain.sh go test ./pkg/driver`
  - 验收标准：
    - `pkg/driver` 不再暴露 `ExecChunk.IsStderr`。
    - 三个 runtime driver 只表达 stdio stream，不解析 agent/command marker。
    - focused driver tests 通过。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：2.2 迁移 driver output filter 和噪声过滤测试。

- [ ] 2.2 迁移 seccomp warning filter 和 driver stream 测试
  - 依赖：2.1。
  - 工作内容：
    - 更新 `pkg/driver/exec_output_filter.go`，只按 stderr stream 过滤 driver/runtime 噪声。
    - 更新 `pkg/driver/exec_output_filter_test.go`，断言真实 stderr 保留 `StdioStderr`。
    - 增加或更新 driver collector tests，覆盖 stdout/stderr stream 映射。
  - 可并行子任务：
    - [ ] 可并行：补充 seccomp warning filter 测试。
    - [ ] 可并行：补充 stdout/stderr stream collector 测试。
  - 测试方案：
    - `./scripts/with-go-toolchain.sh go test ./pkg/driver`
  - 验收标准：
    - seccomp warning 被过滤时不会改变真实 stderr stream 语义。
    - driver 层没有 `__AGENT_RESULT__` 或 `__COMMAND_RESULT__` 解析逻辑。
    - focused driver tests 通过。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：3.1 迁移 adapter stream 映射。

## 3. 阶段三：迁移 adapter、agent、command、loader 和 session stream 路径

参考文档：[docs/spec/output-protocol-contract-spec.md](docs/spec/output-protocol-contract-spec.md)、[docs/plan/output-protocol-contract-implementation-plan.md](docs/plan/output-protocol-contract-implementation-plan.md)

- [ ] 3.1 迁移 runtime adapter 和历史 bool 兼容边界
  - 依赖：1.1、2.1。
  - 工作内容：
    - 在 `pkg/agentcompose/adapters/runtime_provider.go` 增加 driver stream 到 domain stream 的显式映射。
    - 迁移 `pkg/agentcompose/adapters/cell_executor.go`、`pkg/agentcompose/api/kernel.go`、`pkg/agentcompose/api/agent_handler.go`、`pkg/sessions/stream.go`、`pkg/agentcompose/api/session_model.go` 等内部 chunk 使用。
    - 仅在 v1 或历史 session stream 输出边界把 domain stream 转回 bool。
  - 可并行子任务：
    - [ ] 可并行：审计 adapter/domain stream 映射调用点。
    - [ ] 可并行：审计 v1/session 兼容 bool 保留点。
    - [ ] 可并行：更新相关单元测试中的 chunk 构造。
  - 测试方案：
    - `./scripts/with-go-toolchain.sh go test ./pkg/agentcompose/adapters ./pkg/agentcompose/api ./pkg/sessions`
  - 验收标准：
    - 内部 host path 不再回流 `IsStderr` 语义。
    - 空或未知 driver stream 映射为 domain stdout。
    - v1/session 兼容输出行为保持不变。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：3.2 修复 agent prompt streaming。

- [ ] 3.2 修复 agent prompt streaming stdout 静默丢弃
  - 依赖：1.2、3.1。
  - 工作内容：
    - 迁移 `pkg/agentcompose/adapters/agent_executor.go`，删除 `if !chunk.IsStderr { return }` 类 stdout 静默过滤。
    - 使用 `execution.FilterAgentStreamChunk` 过滤 agent payload。
    - filtered stdout 写入 cell stdout/output；filtered stderr 写入 cell stderr/output。
    - stream broker 和 `stream.OnChunk` 只发送 filtered chunk。
  - 可并行子任务：
    - [ ] 可并行：补充 agent stdout payload 不进入 stream/cell output 的测试。
    - [ ] 可并行：补充 agent stdout 非 payload 文本可见的测试。
    - [ ] 可并行：补充 agent stderr transcript 仍正常进入 stream/cell output 的测试。
  - 测试方案：
    - `./scripts/with-go-toolchain.sh go test ./pkg/agentcompose/adapters`
  - 验收标准：
    - agent prompt stdout marker 外文本不再被静默吞掉。
    - `__AGENT_RESULT__` 不进入 stream/cell output/artifacts。
    - agent stderr transcript 行为保持兼容。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：3.3 迁移 command/exec/loader streaming。

- [ ] 3.3 迁移 command、exec、run 和 loader streaming filter
  - 依赖：1.2、3.1。
  - 工作内容：
    - 迁移 `pkg/agentcompose/api/exec.go`，用 `FilterCommandStreamChunk` 替换手写 strip。
    - 迁移 `pkg/runs/controller.go` 中 command run streaming 和 run log append 路径。
    - 迁移 `pkg/agentcompose/adapters/loader_command_executor.go`，避免 streaming/interim cell output 暴露 `__COMMAND_RESULT__`。
    - 保留 command stdout/stderr 原始 stream 属性。
  - 可并行子任务：
    - [ ] 可并行：迁移 ExecService stream path 和测试。
    - [ ] 可并行：迁移 project run command path 和测试。
    - [ ] 可并行：迁移 loader command stream path 和测试。
  - 测试方案：
    - `./scripts/with-go-toolchain.sh go test ./pkg/agentcompose/api ./pkg/runs ./pkg/agentcompose/adapters`
  - 验收标准：
    - command/exec stdout 不会因 stdout stream 被过滤。
    - `__COMMAND_RESULT__` 不进入 transcript、run logs、notebook cell output。
    - 非零 exit code 保留 stdout/stderr/output 并标记失败。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：3.4 阶段三 focused 集成验收。

- [ ] 3.4 阶段三 focused 集成验收
  - 依赖：3.1、3.2、3.3。
  - 工作内容：
    - 汇总更新 `pkg/agentcompose/adapters`、`pkg/agentcompose/api`、`pkg/runs`、`pkg/sessions` 测试。
    - 确保跨 service boundary、persistence/log path 的变更至少有 unit 或 integration 覆盖。
    - 按 `scripts/run-go-test-shape.sh` 规则调整 integration/E2E 测试命名。
  - 可并行子任务：
    - [ ] 可并行：审计 adapter test coverage。
    - [ ] 可并行：审计 API stream test coverage。
    - [ ] 可并行：审计 runs/logs persistence test coverage。
  - 测试方案：
    - `./scripts/with-go-toolchain.sh go test ./pkg/agentcompose/adapters ./pkg/agentcompose/api ./pkg/runs ./pkg/sessions`
  - 验收标准：
    - 阶段三所有 focused tests 通过。
    - 没有 host command/exec path 通过 `Stream == StdioStderr` 或等价 guard 丢弃 stdout。
    - loader command streaming 使用统一 helper。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：4.1 升级 v2 proto schema。

## 4. 阶段四：升级 v2 proto stream API、生成代码和 CLI stream writer

参考文档：[proto/agentcompose/v2/agentcompose.proto](proto/agentcompose/v2/agentcompose.proto)、[proto-client/package.json](proto-client/package.json)

- [ ] 4.1 升级 v2 proto schema 并生成 Go/Connect 代码
  - 依赖：1.1、3.1。
  - 工作内容：
    - 在 `proto/agentcompose/v2/agentcompose.proto` 新增 `StdioStream` enum。
    - 将 v2 `RunAgentStreamResponse.is_stderr`、`ExecStreamResponse.is_stderr`、`TranscriptEvent.kind/is_stderr` 迁移为 `StdioStream stream`。
    - 不修改 `proto/agentcompose/v1/agentcompose.proto`。
    - 使用 `protoc` 重新生成 Go proto 和 Connect 代码。
  - 可并行子任务：
    - [ ] 可并行：审计 v2 proto 字段号和生成代码影响面。
    - [ ] 可并行：确认 generator 版本和 `protoc` 可用性。
  - 测试方案：
    - `go tool protoc-gen-go --version`
    - `go tool protoc-gen-connect-go --version`
    - `protoc -I proto --go_out=. --go_opt=paths=source_relative --connect-go_out=. --connect-go_opt=paths=source_relative proto/health/v1/health.proto proto/agentcompose/v1/agentcompose.proto proto/agentcompose/v2/agentcompose.proto`
    - `./scripts/with-go-toolchain.sh go test ./proto/agentcompose/v2 ./proto/agentcompose/v2/agentcomposev2connect`
  - 验收标准：
    - v2 stream response 和 transcript event 不再包含 `is_stderr` 或 stdout/stderr string `kind`。
    - v1 proto 和 v1 generated code 保持历史兼容。
    - Go generated code 可编译。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：4.2 迁移 API/app/CLI v2 stream 使用。

- [ ] 4.2 迁移 API、app 和 CLI 到 v2 `stream`
  - 依赖：4.1、3.4。
  - 工作内容：
    - 更新 `pkg/agentcompose/api/run.go` 的 `TranscriptEventFromExecChunk`，返回 v2 `StdioStream`。
    - 更新 `pkg/agentcompose/app/run_controller.go` 和 `pkg/agentcompose/api/exec.go`，发送 `Stream` 字段。
    - 更新 `cmd/agent-compose/main.go` 的 `writeTranscriptOrChunk`，从 event/transcript `Stream` 选择本机 stdout/stderr。
    - `STDIO_STREAM_UNSPECIFIED` 按 stdout。
    - `--json` 继续 suppress 实时 transcript。
  - 可并行子任务：
    - [ ] 可并行：迁移 RunService stream API tests。
    - [ ] 可并行：迁移 ExecService stream API tests。
    - [ ] 可并行：迁移 CLI stream writer tests。
  - 测试方案：
    - `./scripts/with-go-toolchain.sh go test ./cmd/agent-compose ./pkg/agentcompose/app ./pkg/agentcompose/api`
  - 验收标准：
    - CLI 不再调用 v2 `GetIsStderr()` 或从 `TranscriptEvent.Kind` 推断通道。
    - CLI 根据 v2 `stream` 正确写本机 stdout/stderr。
    - `--json` 行为保持兼容。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：4.3 生成并验证 TypeScript proto client。

- [ ] 4.3 生成并验证 `proto-client` TypeScript 产物
  - 依赖：4.1。
  - 工作内容：
    - 在 `proto-client` 安装依赖并运行生成。
    - 确认 generated client 暴露 `StdioStream` enum，且不再暴露 v2 `isStderr`。
    - 构建 `@chaitin-ai/agent-compose-client`。
  - 可并行子任务：
    - [ ] 可并行：审计 `proto-client/src` 或 `dist` 中 v2 stream 字段。
    - [ ] 可并行：审计外部 `agent-compose-ui` 迁移风险并记录，不在本仓库内修改。
  - 测试方案：
    - `cd proto-client && npm ci`
    - `cd proto-client && npm run gen`
    - `cd proto-client && npm run build`
  - 验收标准：
    - `proto-client` 生成和构建通过。
    - v2 TypeScript client 反映 `StdioStream` enum。
    - 若发现外部 UI 需要迁移，完成总结中记录发布切分风险。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：4.4 阶段四 CLI/API/proto 收口。

- [ ] 4.4 阶段四 CLI/API/proto 收口
  - 依赖：4.2、4.3。
  - 工作内容：
    - 更新 CLI tests 中的 fake v2 stream response。
    - 覆盖非 JSON `run --command` stdout/stderr transcript、`exec` stream、`--json` suppress transcript、unspecified stream as stdout。
    - 审计 `cmd pkg proto proto-client` 中剩余 v2 `is_stderr` 使用。
  - 可并行子任务：
    - [ ] 可并行：CLI run stream 测试。
    - [ ] 可并行：CLI exec stream 测试。
    - [ ] 可并行：v2 legacy field 搜索审计。
  - 测试方案：
    - `./scripts/with-go-toolchain.sh go test ./cmd/agent-compose ./pkg/agentcompose/app ./pkg/agentcompose/api`
    - `cd proto-client && npm run gen && npm run build`
  - 验收标准：
    - 阶段四 focused tests 全部通过。
    - v2 `RunAgentStreamResponse`、`ExecStreamResponse`、`TranscriptEvent` 不再包含 `is_stderr`。
    - v1 legacy `is_stderr` 保留且被审计记录。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：5.1 固化 runtime/javascript 行为。

## 5. 阶段五：固化 runtime/javascript 行为和 runtime contract 文档

参考文档：[runtime/javascript/src/command.ts](runtime/javascript/src/command.ts)、[runtime/javascript/src/transcript.ts](runtime/javascript/src/transcript.ts)、[docs/design/agent-compose-runtime_contract.md](docs/design/agent-compose-runtime_contract.md)

- [ ] 5.1 补充 runtime/javascript stdout/stderr contract 测试
  - 依赖：1.2、3.3。
  - 工作内容：
    - 保持 prompt transcript writer 写 stderr，final prompt result 写 stdout marker。
    - 保持 command child stdout 镜像 runtime stdout，child stderr 镜像 runtime stderr。
    - 覆盖 command stdout/stderr/output artifacts 和 `command-result.json` payload 行为。
    - 覆盖用户命令非零退出仍输出 `__COMMAND_RESULT__`。
  - 可并行子任务：
    - [ ] 可并行：补充 `command.ts` stdout/stderr/artifact 测试。
    - [ ] 可并行：补充 `cli.ts` prompt/exec marker 输出测试。
    - [ ] 可并行：补充 `transcript.ts` stderr transcript 测试。
  - 测试方案：
    - `cd runtime/javascript && npm ci`
    - `cd runtime/javascript && npm run typecheck`
    - `cd runtime/javascript && TEST_SHAPE=unit npm run test:unit`
  - 验收标准：
    - runtime tests 证明 prompt/result marker 与 human transcript stream 分离。
    - command stdout/stderr 保持原始通道镜像语义。
    - 未新增 runtime 配置项或 CLI 参数。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：5.2 更新 runtime contract 和 CLI 文档。

- [ ] 5.2 更新 runtime contract、CLI manual 和相关文档
  - 依赖：5.1。
  - 工作内容：
    - 修正 `docs/design/agent-compose-runtime_contract.md` 中 command 输出“都镜像到 wrapper stderr”的过期描述。
    - 文档化 host marker stripping helper 已保护 streaming transcript。
    - 若 `README.md` 或 `docs/command-line-manual.md` 提到 v2 bool stream 或 command transcript 旧语义，按新语义同步。
    - 保持首版不做事项明确：不新增 typed payload event、不改 CLI 参数、不迁移 v1。
  - 可并行子任务：
    - [ ] 可并行：审计 runtime contract 中 stdout/stderr/payload 相关段落。
    - [ ] 可并行：审计 CLI manual 的 `run --command`、`exec`、`--json` 描述。
    - [ ] 可并行：审计 README 中 proto-client 和 command transcript 相关描述。
  - 测试方案：
    - `task docs` 当前为 placeholder，可运行但不作为强门禁。
    - `rg -n "wrapper stderr|streaming output has no dedicated protocol payload filter|is_stderr|isStderr" docs README.md`
  - 验收标准：
    - runtime contract 与 `runtime/javascript/src/command.ts` 当前行为一致。
    - 文档不再声称 command 用户 stdout/stderr 必须统一写 stderr。
    - 文档明确 payload 由 marker 判定，不由 stdout/stderr stream 判定。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：6.1 全仓旧字段和 marker 审计。

## 6. 阶段六：全仓清理、兼容审计和质量门禁

参考文档：[TESTING.md](TESTING.md)、[Taskfile.yml](Taskfile.yml)、[.github/workflows/ci.yml](.github/workflows/ci.yml)

- [ ] 6.1 全仓旧字段、marker callsite 和首版范围审计
  - 依赖：4.4、5.2。
  - 工作内容：
    - 搜索并分类剩余 `IsStderr`、`is_stderr`、`isStderr`、`GetIsStderr`、`TranscriptEvent{Kind` 命中。
    - 搜索 marker helper callsite，确认 host streaming path 使用 `FilterAgentStreamChunk` 或 `FilterCommandStreamChunk`。
    - 确认未误实现 `chunk_type`、`payload_kind`、typed payload event、stdin 转发、CLI 参数或 JSON schema 变更。
    - 确认 `BoxRuntime.ExecStream` 方法签名未改变。
  - 可并行子任务：
    - [ ] 可并行：旧字段搜索审计。
    - [ ] 可并行：marker helper callsite 审计。
    - [ ] 可并行：首版不做事项审计。
    - [ ] 可并行：coverage shape 测试命名审计。
  - 测试方案：
    - `rg -n "IsStderr|is_stderr|isStderr|GetIsStderr|TranscriptEvent\\{Kind" cmd pkg proto runtime proto-client docs -g '!**/node_modules/**'`
    - `rg -n "__AGENT_RESULT__|__COMMAND_RESULT__|StripAgentResultPayload|StripCommandResultPayload|FilterAgentStreamChunk|FilterCommandStreamChunk" cmd pkg runtime docs`
    - `rg -n "chunk_type|payload_kind|stdin passthrough|stdin 转发" cmd pkg proto runtime docs`
  - 验收标准：
    - 合法旧字段剩余项只包括 v1 proto/generated code、历史 session stream 兼容字段或文档化兼容面。
    - host streaming path 不再手写 strip 或 stream 可见性判断。
    - 首版范围未漂移。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：6.2 运行完整 harness 和 CI 对齐门禁。

- [ ] 6.2 运行完整 harness 和 CI 对齐门禁
  - 依赖：6.1。
  - 工作内容：
    - 运行主质量门禁并修复失败。
    - 运行 CI 对齐补充命令。
    - 记录 coverage summary、不可运行命令和环境缺失。
    - 确认无需构建 Docker images，除非实现过程改动 Dockerfile、guest image 构建脚本或部署配置。
  - 可并行子任务：
    - [ ] 可并行：Go focused/full tests 结果整理。
    - [ ] 可并行：runtime/javascript npm 测试结果整理。
    - [ ] 可并行：runtime SDK package 测试结果整理。
    - [ ] 可并行：proto-client 生成构建结果整理。
    - [ ] 可并行：lint/build/test 门禁失败 triage。
  - 测试方案：
    - `task lint`
    - `task build`
    - `task test`
    - `./scripts/with-go-toolchain.sh go test ./cmd/... ./pkg/...`
    - `cd runtime/javascript && npm run test:unit`
    - `cd runtime/agent-compose-runtime-sdk && npm test && npm run test:packaging`
    - `cd proto-client && npm run gen && npm run build`
  - 验收标准：
    - `task lint`、`task build`、`task test` 全部通过。
    - `task test` 打印 unit、integration、E2E、combined coverage，且满足 baseline：unit/integration/E2E 至少 60%，combined 至少 70%。
    - CI 对齐补充命令全部通过，或完成总结清楚记录无法运行原因。
    - v2 Go/Connect/TypeScript 生成产物已更新并可编译。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：无。
