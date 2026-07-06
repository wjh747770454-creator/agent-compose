# Runtime output protocol contract spec

## 背景与目标

`agent-compose run <agent> --prompt`、`agent-compose run <agent> --command` 和 `agent-compose exec` 都通过 runtime driver 的 `ExecStream` 执行 sandbox 内命令。当前系统把底层 stdout/stderr、agent 可见 transcript、最终结构化结果、CLI 本机 stdout/stderr 和 run/exec 持久化结果混在同一条数据路径中，依赖 `__AGENT_RESULT__`、`__COMMAND_RESULT__` marker 和 `ExecChunk.IsStderr` 共同区分语义。

本规格定义 runtime 输出协议的当前态和首版完善目标：

- 明确 `docker`、`boxlite`、`microsandbox` driver 只提供 stdio 传输语义，不承载 agent/command 协议语义。
- 明确 `agent-compose-runtime prompt` 和 `agent-compose-runtime exec` 的 stdout/stderr 协议边界。
- 明确 host 侧用显式 marker 识别协议 payload，不能把 stdout 或 `STDIO_STREAM_STDOUT` 重载为“内部 payload”。
- 将内部 `ExecChunk.IsStderr bool` 升级为 `ExecChunk.Stream StdioStream` 枚举，并同步将 v2 stream API 改为 `StdioStream` 枚举。
- 修正并文档化 reviewer 指出的风险：streaming 回调静默丢弃 stdout chunk 会造成未来 runtime 或第三方 runtime 的用户 stdout 输出丢失。
- 为后续实现提供可测试、可验收的协议契约，避免 CLI、RunService、ExecService、notebook cell 和 artifacts 对输出语义产生分歧。

## 现状和 harness 约束

项目 harness 约束：

- `AGENTS.md` 指定 `cmd/agent-compose/main.go` 是 daemon 和 CLI 入口，`pkg/agentcompose/api/` 暴露 Connect handlers，`pkg/agentcompose/adapters/` 承接 daemon-only runtime 和 agent/loader adapters，`pkg/runs/` 管理 project run lifecycle。
- `AGENTS.md` 指定 runtime drivers 为 `docker`、`boxlite`、`microsandbox`，默认 driver 为 `docker`；输出协议必须在这三个 driver 上保持统一 host 语义。
- `TESTING.md` 要求按 unit、integration、E2E 三类测试覆盖变更。输出协议跨 runtime-driver behavior、service boundary 和 user-facing CLI workflow，必须至少补充 unit/integration 覆盖；必要时补充 E2E 或 fake-runtime CLI integration。
- `Taskfile.yml` 的主质量门禁是 `task lint`、`task build`、`task test`。涉及 runtime TypeScript 时还需覆盖 `runtime/javascript` 对应 npm 测试；涉及 proto 时需重新生成 Go/Connect/TypeScript client 产物，且 `proto-client/` 是发布给 `agent-compose-ui` 使用的 v2 TypeScript client。

相关当前实现：

- `proto/agentcompose/v2/agentcompose.proto` 当前定义 `RunAgentStreamResponse.chunk/is_stderr/transcript`、`TranscriptEvent.kind/is_stderr` 和 `ExecStreamResponse.chunk/is_stderr/transcript/result`。v2 不要求保持 wire 兼容，首版应将这些 bool/string 通道字段升级为 `StdioStream` 枚举。
- `pkg/model/model.go` 当前定义 `ExecChunk{Text, IsStderr}` 和 `ExecResult{Stdout, Stderr, Output, ExitCode, Success}`。首版应将 `ExecChunk` 改为 `ExecChunk{Text, Stream}`，这是 driver 到 host 的统一 stdio 抽象。
- `pkg/driver/types.go` 当前也定义 driver-local `ExecChunk{Text, IsStderr}`。首版应在 driver 层同步引入 driver-local `StdioStream` 枚举，由 adapter 映射到 domain enum。
- `pkg/agentcompose/adapters/runtime_provider.go` 将 `docker`、`boxlite`、`microsandbox` driver 的 `ExecStream` 适配为 domain `ExecChunk`，不解释 marker。
- `pkg/execution/parse.go` 定义协议 marker：`__AGENT_RESULT__` 和 `__COMMAND_RESULT__`，并提供 parse/sanitize/strip helper。
- `pkg/agentcompose/adapters/agent_runner.go` 对 `run --prompt` 执行 `agent-compose-runtime prompt --message-file ...`，prompt 不走 stdin。
- `pkg/execution/command_runtime.go` 对 `run --command`、`exec` 和 loader command 执行 `agent-compose-runtime exec --request-file ...`，command 不走 stdin。
- `pkg/agentcompose/adapters/agent_executor.go` 当前只转发 `chunk.IsStderr == true` 的 agent prompt stream chunk；这隐含了“agent 可见 transcript 必须写 stderr、stdout 只放机器结果”的约定。
- `pkg/agentcompose/api/exec.go` 和 `pkg/runs/controller.go` 当前 command/exec 路径使用 `StripCommandResultPayload` 过滤 `__COMMAND_RESULT__`，并保留 stdout/stderr 原始通道。
- `pkg/agentcompose/adapters/loader_command_executor.go` 当前未对 streaming chunk 剥离 `__COMMAND_RESULT__`，最终 cell 会被解析结果覆盖，但 streaming/interim cell output 可能短暂暴露 payload。
- `runtime/javascript/src/transcript.ts` 的 agent transcript writer 当前写 stderr。
- `runtime/javascript/src/command.ts` 当前对 command 子进程 stdout 写 runtime stdout、stderr 写 runtime stderr，并把 stdout/stderr/output 分别写 artifact；这与 `docs/design/agent-compose-runtime_contract.md` 中“用户命令 stdout/stderr 都镜像到 wrapper stderr”的描述不一致。
- `docs/command-line-manual.md` 明确 `run --command` 和 `exec` 使用同一套 command transcript；文本模式实时输出 transcript，`--json` suppress transcript 并只输出最终 result。

## 核心概念或领域模型

### Stdio stream

`Stdio stream` 是 sandbox 内进程的原始 stdout/stderr 通道。首版使用显式枚举表达：

```go
type StdioStream string

const (
    StdioStdout StdioStream = "stdout"
    StdioStderr StdioStream = "stderr"
)

type ExecChunk struct {
    Text   string
    Stream StdioStream
}
```

`ExecChunk.Stream` 只能表示“该 chunk 来自哪个原始 stdio 通道”，不能表示“该 chunk 是否用户可见”，也不能表示“该 chunk 是否协议 payload”。Go 零值 `Stream == ""` 和 v2 proto `STDIO_STREAM_UNSPECIFIED` 均按 stdout 处理，避免零值 chunk 改变历史 stdout 行为。

### Protocol payload

`Protocol payload` 是由 guest `agent-compose-runtime` 输出、供 host 解析的结构化结果行。首版 marker 固定为：

- `__AGENT_RESULT__{...}`：`agent-compose-runtime prompt` 的最终结果。
- `__COMMAND_RESULT__{...}`：`agent-compose-runtime exec` 的最终结果。

payload 是否内部协议只由 marker 判定，不由 stdout/stderr 判定。

### Human transcript

`Human transcript` 是用户在 CLI、RunService stream、ExecService stream、run logs、notebook cell output 中应看到的可读输出。

- agent prompt transcript 当前由 guest runtime 写 stderr，并聚合到 `AgentRunResult.Transcript`。
- command transcript 应保留用户命令 stdout/stderr 的原始通道属性，并在 host 侧剥离 `__COMMAND_RESULT__` payload。

### JS runtime output cases

`runtime/javascript` 对 host 可观察输出有以下协议有效场景：

| 场景 | runtime stdout | runtime stderr | payload/artifacts |
| --- | --- | --- | --- |
| `prompt` 成功 | 一行 `__AGENT_RESULT__{...}` | agent transcript、MPI warning、provider 可读信息 | payload 含 provider/sessionId/stopReason/finalText/transcript/stderr；Codex/Claude/OpenCode 成功后写 provider resume state |
| `prompt` preflight 失败 | 无协议 payload | 顶层 `formatError(error)` | host 走 parse failure/fallback |
| `prompt` provider 运行中失败 | 无协议 payload | 已产生的 partial transcript + 顶层 error | 可能已有 provider native logs，provider state 不保证写入 |
| `exec` 子命令 exit 0 | `$ command...\n`、用户 stdout、最后一行 `__COMMAND_RESULT__{...}` | 用户 stderr | 写 stdout/stderr/output/request/result artifacts，payload `success=true` |
| `exec` 子命令非零退出 | `$ command...\n`、用户 stdout、最后一行 `__COMMAND_RESULT__{...}` | 用户 stderr + `command exited with code N\n` | payload `success=false, exitCode=N` |
| `exec` pre-spawn/preflight 失败 | 通常无 `$ ...`，无 payload | 顶层 error | 无 `command-result.json`，host fallback |
| `exec` spawn/timeout/artifact infra 失败 | 可能已有 `$ ...` 和部分用户 stdout，无 payload | 可能已有用户 stderr + 顶层 error | 可能有部分 artifacts，无 `command-result.json`，host fallback |

当前 `prompt` 正常实现不会主动产生 stdout 非 marker 文本：`runtime/javascript/src/cli.ts` 只在成功完成后写 `__AGENT_RESULT__` 到 stdout，`TranscriptWriter` 写 stderr，Gemini/OpenCode 子进程 stdout 被 pipe 后解析而非透传。host 仍必须防御未来 runtime/provider 回归：stdout 中 marker 之外的文本不得静默丢失。

### Final result

`Final result` 是 host 解析 payload 后写入持久化对象和最终 API response 的结构化结果：

- agent prompt：`RunDetail.output` 来自 agent cell output/transcript，`RunDetail.result_json` 包含 cell id、agent session id、stop reason、success、exitCode。
- command run：`RunDetail.output` 来自 `RuntimeCommandResult.Output`，`RunDetail.result_json` 包含 mode、command、success、exitCode。
- exec：`ExecResult.stdout/stderr/output/exit_code/success/error` 来自解析后的 `RuntimeCommandResult` 或 infra error fallback。

## 架构和组件边界

### Runtime driver 层

`docker`、`boxlite`、`microsandbox` driver 只负责：

- 执行 `ExecSpec{Command, Args, Env, Cwd}`。
- 将底层 stdout/stderr 转成 `ExecChunk{Text, Stream}`。
- 聚合 `ExecResult{Stdout, Stderr, Output, ExitCode, Success}`。
- 在必要时过滤 driver/runtime 自身噪声，例如现有 seccomp warning filter。

driver 层不得识别 `__AGENT_RESULT__`、`__COMMAND_RESULT__`，也不得根据 agent/command 模式改变 `Stream` 语义。

### Guest runtime 层

`runtime/javascript` 是 agent/command payload 的生产者：

- `prompt` 子命令读取 `--message-file`，将 provider stream 转为 human transcript，并在完成时输出 `__AGENT_RESULT__{...}`。
- `exec` 子命令读取 `--request-file`，执行用户 command/script，捕获 stdout/stderr/output artifacts，并在完成时输出 `__COMMAND_RESULT__{...}`。

guest runtime 可以继续把 agent transcript 写 stderr，以保护 prompt stdout 机器结果通道；但该约定必须写入 runtime contract 和测试，不能只存在于 `if chunk.Stream != StdioStderr { return }`。

### Host parsing 层

`pkg/execution` 应作为 marker 解析和 stream payload 过滤的唯一归口：

- parse helper 负责从最终 `ExecResult.Stdout` 或 `ExecResult.Output` 中解析 payload。
- stream filter helper 负责从 chunk 中剥离 payload marker，并返回剩余 human transcript。
- command/exec stream 必须保留 stdout/stderr 原始通道。
- agent prompt stream 首版可以继续把 stderr 作为主要 transcript 通道，但 stdout 上 marker 之外的文本不得静默丢失；应作为 stdout transcript 保留。

### API 和 CLI 层

`RunService.RunAgentStream`、`ExecService.ExecStream` 和 CLI 只消费 host 已过滤后的 transcript：

- v2 stream event 的 `stream` 映射到 CLI stdout/stderr。
- `--json` 继续 suppress 实时 transcript，只输出最终 JSON。
- API 不暴露 `__AGENT_RESULT__` 或 `__COMMAND_RESULT__` payload。

## API、CLI、配置、数据模型或协议变化

首版不新增配置项，不改变 CLI 参数。v2 proto wire shape 允许破坏性变更，首版应把 stream 通道从 bool/string 升级为枚举。

需要固化的协议变化是语义层面的：

- Go domain 内部 `ExecChunk.IsStderr bool` 改为 `ExecChunk.Stream StdioStream`。
- Go driver 层同步从 `driver.ExecChunk.IsStderr bool` 改为 driver-local `driver.StdioStream`。
- `__AGENT_RESULT__` 和 `__COMMAND_RESULT__` 是 host 判断协议 payload 的唯一依据。
- `proto/agentcompose/v2/agentcompose.proto` 新增 `StdioStream` enum：

```proto
enum StdioStream {
  STDIO_STREAM_UNSPECIFIED = 0;
  STDIO_STREAM_STDOUT = 1;
  STDIO_STREAM_STDERR = 2;
}
```

- `RunAgentStreamResponse.is_stderr` 改为 `StdioStream stream = 5`。
- `ExecStreamResponse.is_stderr` 改为 `StdioStream stream = 6`。
- `TranscriptEvent.kind` 和 `TranscriptEvent.is_stderr` 改为 `StdioStream stream = 1`；`text/name/payload_json/created_at` 语义保持不变。
- `proto/agentcompose/v1` 暂不迁移，仍保持历史 `is_stderr` 字段。
- `cmd/agent-compose/main.go` 的 CLI stream writer 改为从 v2 `stream` 选择本机 stdout/stderr；不再从 `TranscriptEvent.kind/is_stderr` 推断通道。
- 重新生成 v2 Go/Connect 和 `proto-client/` TypeScript client；`agent-compose-ui` 需从 `isStderr` 迁移到 `stream`。
- `docs/design/agent-compose-runtime_contract.md` 需要修正 command 部分：当前实现保留用户 command stdout/stderr 原始通道；host 通过 marker stripping 保护 transcript，不要求所有用户可见输出都写 stderr。

建议在 `pkg/execution` 增加明确 helper，名称以实际实现为准：

```go
func FilterAgentStreamChunk(chunk domain.ExecChunk) (domain.ExecChunk, bool)
func FilterCommandStreamChunk(chunk domain.ExecChunk) (domain.ExecChunk, bool)
```

语义要求：

- `FilterCommandStreamChunk` 调用 `StripCommandResultPayload`，保留 payload 前的 stdout/stderr 文本，空文本返回 `visible=false`。
- `FilterAgentStreamChunk` 调用 `StripAgentResultPayload`。stderr transcript 保持可见；stdout payload 被剥离；stdout 上剥离 payload 后仍有文本时，作为 stdout transcript 保留。
- helper 保留原始 `Stream`；任何 host callsite 不再手写 marker stripping 或 `Stream` 可见性判断，避免协议分叉。

## 工作流和失败语义

### `run --prompt`

目标工作流：

1. CLI 构造 `RunAgentRequest.prompt`。
2. host 写 `<session>/state/agents/prompts/<provider>-<unix_nano>.txt`。
3. runtime driver 执行 `agent-compose-runtime prompt --message-file ...`。
4. guest runtime 将 provider human transcript 写 stderr，将最终 `__AGENT_RESULT__{...}` 写 stdout。
5. host streaming filter 剥离 agent payload，转发 human transcript。
6. host 从最终 `ExecResult` 解析 agent payload，sanitize stdout/output，写 cell artifacts、events、run detail。

失败语义：

- 缺失 `__AGENT_RESULT__` 时，host 保持当前 parse failure 行为，并从 stderr/output/stdout 摘要错误。
- stdout 中存在 marker 之外的文本时，首版不得静默吞掉，应作为 stdout transcript 转发并纳入 cell output。
- provider CLI stderr 仍可作为 human transcript，不应因 stdout payload 失败而丢弃。

### `run --command` / `exec`

目标工作流：

1. CLI 或 API 构造 command/script。
2. host 写 `command-request.json`。
3. runtime driver 执行 `agent-compose-runtime exec --request-file ...`。
4. guest runtime 执行用户命令，实时转发用户 stdout/stderr，并写 `stdout.txt`、`stderr.txt`、`output.txt`、`command-result.json`。
5. host streaming filter 只剥离 `__COMMAND_RESULT__` payload，保留 stdout/stderr transcript。
6. host 从最终 `ExecResult` 解析 `RuntimeCommandResult`，生成 `RunDetail` 或 `ExecResult`。

失败语义：

- 用户命令非零退出仍应输出 `__COMMAND_RESULT__`，host 将 run/exec 标记失败但保留 stdout/stderr/output。
- invalid request、spawn error、timeout、artifact write 等 infra error 可能没有 payload；host 使用现有 fallback，把 exit code 置为非零，并从 stderr/stdout/error 生成错误信息。
- command stdout 不得因为 `Stream == StdioStdout` 被过滤，否则视为协议回归。

### `logs` 和 artifacts

- run logs 读取 `logs_path`，应只包含 human transcript，不包含 marker payload。
- agent prompt artifacts 中 `stdout.txt` / `output.txt` 不应包含 `__AGENT_RESULT__`。
- command artifacts 中 `stdout.txt`、`stderr.txt`、`output.txt` 保留用户命令输出，`command-result.json` 保留结构化结果。

## 测试、质量门禁和验收标准

必须通过 harness 主门禁：

```bash
task lint
task build
task test
```

建议补充的测试：

- `pkg/execution` unit tests：
  - `ExecStreamAccumulator` 将空 `Stream` 和 `StdioStdout` 聚合进 stdout，将 `StdioStderr` 聚合进 stderr。
  - `FilterCommandStreamChunk` 保留 stdout chunk。
  - `FilterCommandStreamChunk` 剥离 `__COMMAND_RESULT__`，payload 不进入 transcript。
  - `FilterAgentStreamChunk` 剥离 `__AGENT_RESULT__`。
  - agent stdout 中 marker 前存在可见文本时不静默丢弃。
- `pkg/driver` unit tests：
  - docker、boxlite、microsandbox stream collector 将 stdout/stderr 转成正确 `StdioStream`。
  - seccomp warning filter 保留真实 stderr 的 `StdioStderr`。
- `pkg/agentcompose/adapters` tests：
  - agent prompt stderr transcript 正常进入 stream/cell output。
  - agent stdout payload 不进入 stream/cell output。
  - agent stdout 非 payload 文本作为 stdout transcript 可见。
  - driver enum 到 domain enum 的 adapter 映射正确。
- `pkg/agentcompose/api` tests：
  - `RunAgentStream` / `ExecStream` 中 stdout 用户输出实时转发，`stream=STDIO_STREAM_STDOUT` 保持。
  - `RunAgentStream` / `ExecStream` 中 stderr 用户输出实时转发，`stream=STDIO_STREAM_STDERR` 保持。
  - `__COMMAND_RESULT__` 不进入 transcript。
- `pkg/runs` tests：
  - `run --command` stdout/stderr 都写入 transcript/logs，payload 不进入 `transcript.txt`。
  - 非零 exit code 保留 stdout/stderr/output 并标记 run failed。
- `runtime/javascript` tests：
  - `command.ts` 对 child stdout/stderr 的 capture、artifact 和 final payload 行为与 host contract 一致。
  - `prompt` runner transcript 继续写 stderr，final result 继续写 stdout marker。
- CLI integration tests：
  - 非 JSON `run --command` 显示 stdout 和 stderr transcript。
  - CLI 根据 v2 `stream` 正确写本机 stdout/stderr。
  - `--json` suppress transcript，只输出最终 result。

验收标准：

- domain 和 driver `ExecChunk` 不再包含 `IsStderr bool`。
- v2 `RunAgentStreamResponse`、`ExecStreamResponse`、`TranscriptEvent` 不再包含 `is_stderr`；通道统一使用 `StdioStream stream`。
- 没有任何 command/exec host path 通过 `Stream == StdioStderr` 或同类 guard 丢弃 stdout。
- agent prompt path 对 stdout 的处理有显式 helper、注释和测试，不再是未文档化静默过滤。
- loader command streaming 也通过 `FilterCommandStreamChunk` 剥离 `__COMMAND_RESULT__`。
- runtime contract 文档与 `runtime/javascript/src/command.ts` 当前行为一致。
- v2 Go/Connect/TypeScript 生成产物已更新，CLI 和 `proto-client/` 编译通过。

## 首版不做事项

- 不新增 `chunk_type`、`payload_kind` 或 typed payload event；首版只新增 stdio stream enum，payload 仍通过 marker helper 和文档固化语义。
- 不改变 CLI 参数、默认输出格式或 JSON schema。
- 不改变 runtime driver `BoxRuntime` / `ExecStream` 方法签名；只改变 `ExecChunk` 字段类型。
- 不引入 stdin 转发能力；prompt 和 command 继续通过文件传入。
- 不重写 provider runner 的完整 transcript 格式，只固化 stdout/stderr/payload 边界。
- 不要求 Docker/BoxLite/Microsandbox driver 解析或过滤 agent/command payload。
- 不迁移 `proto/agentcompose/v1` 的历史 `is_stderr` 字段。

## 关键假设和已确认决策

- `__AGENT_RESULT__` 和 `__COMMAND_RESULT__` 是首版稳定协议 marker。
- `ExecChunk.Stream` 只表示 stdio stream，不表示内部/外部、机器/人类、可见/不可见。
- v2 接口不需要保持 wire 兼容，首版可移除 v2 `is_stderr` 并改为 `StdioStream stream`。
- v1 接口暂不迁移。
- Go 零值 `Stream == ""` 和 proto `STDIO_STREAM_UNSPECIFIED` 按 stdout 处理。
- `run --command` 和 `exec` 必须保留用户 stdout/stderr 的原始通道语义。
- `run --prompt` 可以继续采用“human transcript 写 stderr、final payload 写 stdout”的 guest runtime 约定，但 host 必须用 marker helper 和测试固化该约定。
- 当前 reviewer 指出的“stdout chunk 静默过滤”风险在 agent prompt path 已存在；如果待合并变更把同类 guard 加到 command/exec path，应拒绝该实现并改为 marker-based filtering。
- 文档以当前代码为准修正：command 用户输出不是必须统一镜像到 wrapper stderr。
