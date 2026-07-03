# agent-compose CLI 改进计划

本文面向研发和评审，记录 agent-compose CLI 从当前代码状态迁移到目标命令体系的实施计划。最终用户文档见 [命令行使用手册](../command-line-manual.md)。

## 当前代码依据

当前 CLI 入口集中在 [cmd/agent-compose/main.go](/data/src/github.com/kingfs/agent-compose/cmd/agent-compose/main.go:405) 的 `newRootCommand`。

已实现全局参数：

- `--host`
- `-f, --file`
- `--project-name`
- `--json`

当前 project 解析逻辑：

- `resolveComposePath` 只在未指定 `-f` 时读取当前目录下的 `agent-compose.yml`。
- `loadNormalizedCompose` 使用 `compose.ParseFile` 解析配置，并用 `--project-name` 覆盖配置中的 project name。
- `runComposeUpCommand` apply project 时会把 `ProjectSource.ComposePath` 设置为配置文件路径，把 `ProjectSource.ProjectDir` 设置为配置文件所在目录。

当前已注册命令：

| 命令 | 当前用法 | 当前实现 |
| --- | --- | --- |
| `daemon` | `agent-compose daemon` | 启动 daemon。 |
| `version` | `agent-compose version` | 输出 build version。 |
| `status` | `agent-compose status` | 请求 daemon version/status。 |
| `config` | `agent-compose config [--quiet]` | 解析并输出 normalized config。 |
| `up` | `agent-compose up` | 调用 v2 `ProjectService.ApplyProject`；无 `-d/--detach`，无前台 attach。 |
| `down` | `agent-compose down` | 调用 v2 `ProjectService.RemoveProject`。 |
| `run` | `agent-compose run <agent> [prompt...]` | 调用 v2 `RunService.RunAgentStream`；positional 剩余参数会拼成 prompt；支持 `--prompt`、`--session-id`、`--keep-running`。 |
| `logs` | `agent-compose logs` | 支持 `--agent`、`--run-id`、`--session-id`、`--follow`。 |
| `ps` | `agent-compose ps` | 通过 `GetProject` 汇总 project agents、latest run 和 running session；当前不是 sandbox 列表。 |
| `exec` | `agent-compose exec [flags] <command> [args...]` | 调用 v2 `ExecService.ExecStream`；通过 `--agent`、`--run-id`、`--session-id` 选择目标；支持 `--cwd`。 |
| `inspect` | `agent-compose inspect <project|agent|run|session> [name-or-id]` | 查看 project、agent、run、session。 |
| `images` | `agent-compose images` | 调用 image list；支持 `--query`、`-a/--all`。 |
| `pull` | `agent-compose pull <image>` | 调用 image pull；支持 `--platform`。 |
| `rmi` | `agent-compose rmi <image>` | 调用 image remove；支持 `--force`、`--prune-children`。 |
| `image` | `agent-compose image <subcommand>` | 旧 image 命令树，包含 `ls`、`pull`、`rm`、`inspect`。 |

现有后端/API 能力：

- v2 `ProjectService` 已有 `ListProjects`、`ApplyProject`、`GetProject`、`RemoveProject`、`WatchProject`。
- v2 `ListProjectsResponse` 返回 `ProjectSummary` 列表，summary 包含 `project_id`、`name`、`source_path`、`current_revision`、`spec_hash`、`agent_count`、`scheduler_count`、`running_run_count`、`latest_run_id`、`created_at`、`updated_at`、`removed_at`。
- 当前 `ProjectService.ListProjects` 调用 `ProjectSummaryToProto(project, nil, nil)`，因此 `agent_count` 和 `scheduler_count` 在 list 场景下会是 0；如果 `ls` 要展示真实 agent/scheduler 数量，需要修复该实现或补充查询。
- v1 `SessionService` 已有 `ResumeSession`、`StopSession`、`GetSession`、`ListSessions`、`WatchSession`，没有删除 session/sandbox 的 RPC。
- runtime driver 已有 stop 能力；资源统计命令没有现成 CLI 和统一 API。

## 目标命令体系

目标用户文档以 sandbox 为统一对象。研发实现时可以继续复用已有 session/run 数据结构，但 CLI 参数、输出字段和错误信息应对外统一使用 sandbox。

最终顶层命令：

```text
daemon
version
status
config
ls
up
down
run
ps
stop
resume
rm
exec
logs
inspect
stats
images
pull
push
rmi
```

本轮不实现 `build`。旧 `image` 命令树不删除，只标记 deprecated，并提示用户迁移到顶层 image 命令和 `inspect image`。

## 实施原则

本轮目标是快速、可靠地优化命令行结构，补全命令行使用逻辑。除非现有 API 或存储能力无法支撑目标行为，否则优先在 CLI 层完成语义调整、输出转换和兼容处理。

具体原则：

- 不扩散改动范围。优先复用当前 v1 SessionService、v2 ProjectService、RunService、ExecService、ImageService 能力。
- 必要时才扩展后端/API。当前明确需要新增或扩展后端能力的范围包括 sandbox 删除、run 新输入模式中的部分能力、image push、stats。
- 需要新增底层功能或 API 时，统一走 v2。v1 会在后续版本逐步删除，因此本轮不向 v1 增加新 RPC 或新数据模型。
- 兼容优先。计划删除命令、删除参数、改变 positional 语义前，必须先提供替代命令，并在旧入口输出 deprecated warning。
- warning 必须写到 stderr，不污染 `--json` stdout。
- deprecated warning 需要明确说明旧入口后续会被删除，并给出替代命令。例如：`agent-compose image inspect` is deprecated and will be removed in a future release; use `agent-compose inspect image` instead.
- 代码中同步增加 `Deprecated:` 注释或显式 deprecation 标记，方便后续定位旧兼容逻辑。
- 本轮不实际删除旧命令和旧参数。删除动作等后续经过几个版本兼容期后再单独评估和执行。
- `stats` 涉及统一资源采集 API 和 runtime driver 指标接入，改动范围较大，作为最后阶段实现。

## 任务拆分和依赖关系

### 前置任务

以下任务会影响后续多个命令，应优先完成：

| 前置任务 | 原因 | 影响命令 |
| --- | --- | --- |
| 配置文件发现统一 | 所有 project 命令都依赖 `resolveComposeProject`；`.yml/.yaml` 和 `-f` 语义必须一致。 | `config`、`up`、`down`、`run`、`ps`、`logs`、`inspect`、`stats`、sandbox 生命周期命令 |
| CLI 输出模型整理 | 多个命令需要把内部 session/run 转换成对外 sandbox；先定义 shared output struct 可减少重复和破坏性变更。 | `ps`、`run`、`exec`、`logs`、`inspect sandbox`、`stop`、`resume`、`rm`、`stats` |
| deprecation warning 机制 | 旧 `image`、`--session-id`、`inspect session`、旧 `exec` 目标选择都需要兼容期 warning，且不能污染 `--json` stdout。 | `run`、`exec`、`logs`、`inspect`、`image` |
| project list 计数字段修复 | `ls` 默认要展示 agent/scheduler 数量；当前 `ListProjects` 传 nil 导致计数为 0。 | `ls` |
| sandbox 删除 API 设计 | `rm` 和 `run --rm` 都依赖删除能力；当前没有删除 session/sandbox RPC。 | `rm`、`run --rm` |

### 顺序链路

必须按顺序推进的链路：

1. 配置文件发现统一 -> project 命令测试基线 -> 后续所有 project 命令。
2. project 列表字段修复 -> `ls` -> project 级自动化输出稳定。
3. `inspect sandbox` -> `ps` sandbox 输出模型 -> `exec <sandbox>` 和 `logs --sandbox` 的用户发现路径。
4. sandbox 删除 API -> `rm --force` -> `run --rm`。
5. `ps` sandbox 化 -> `stop/resume/rm` 批量操作体验。
6. `run` 新输入模式 API 支持 -> `run --trigger/--command/--detach` -> positional prompt deprecated warning。
7. `up -d` 语义固化 -> `up` 前台 attach -> `Ctrl+C` project shutdown。
8. `inspect image` 发布 -> 旧 `image` 命令树 deprecated warning。
9. sandbox 输出模型稳定 -> stats API -> `stats` CLI。

### 可并行任务

在前置任务完成或接口边界明确后，可以并行推进：

| 可并行任务 | 前置条件 | 说明 |
| --- | --- | --- |
| `ls` CLI | 配置文件发现不阻塞；需修复 list 计数 | 只依赖现有 ProjectService，和 sandbox 命令基本正交。 |
| `inspect image` | deprecation warning 机制 | 复用现有 `runComposeImageInspectCommand`，与 sandbox 生命周期无关。 |
| `logs [agent]`、`--tail`、`--timestamp` | 输出/warning 规则 | 可以在 `ps` sandbox 化前完成；`--sandbox` 可后续补。 |
| image `push` | ImageService 扩展方案确定 | 与 project/sandbox 命令正交。 |
| `up -d` flag | `up` 当前行为确认 | 第一阶段只加 flag 和 help，前台 attach 可后续做。 |

### 命令级开发矩阵

| 命令 | 当前状态 | 目标变更 | 后端/API 需求 | 依赖 | 可并行性 |
| --- | --- | --- | --- | --- | --- |
| `config` | 已实现 | 支持 `.yaml` 默认发现 | 无 | 配置文件发现 | 前置基础 |
| `ls` | CLI 未实现，API 已有 | 新增 project 列表命令 | 修复 `ListProjects` 计数字段；确认 services 字段来源 | project list API | 可独立推进 |
| `up` | 已实现 apply | 新增 `-d`，默认前台 attach，Ctrl+C down | 可能复用 `WatchProject`/logs；无需先改 proto | project logs/stop 语义 | `-d` 可先做，attach 顺序推进 |
| `down` | 已实现 | 文案和输出对齐 sandbox | 无 | sandbox 输出术语 | 可随输出模型调整 |
| `run` | 已实现 prompt stream | trigger/command/detach/sandbox/jupyter/rm | 需要扩展 Run API 或定义映射；`--rm` 依赖删除 API | sandbox 删除、run API | 需拆多 PR，不能一次做完 |
| `ps` | 已实现 agent 视图 | 改为 sandbox 视图 | 可组合 v1 ListSessions + v2 ListRuns；必要时补查询 | sandbox 输出模型 | 依赖 inspect/logs 部分模型 |
| `stop` | CLI 未实现，API 有 StopSession | 新增 stop sandbox | 可先复用 v1 StopSession | sandbox id 映射 | 可在 rm 前做 |
| `resume` | CLI 未实现，API 有 ResumeSession | 新增 resume sandbox | 可先复用 v1 ResumeSession | sandbox id 映射 | 可与 stop 同 PR |
| `rm` | CLI/API 都缺 | 删除 sandbox，running 需 `--force` | 需要新增 v2 sandbox 删除 API 和存储清理 | sandbox 删除 API | 必须在 run --rm 前 |
| `exec` | 已实现 flag 选择目标 | 新增 `exec <sandbox>`，旧 target flags deprecated | 现有 ExecRequest 支持 session target；CLI 映射即可 | ps sandbox 发现路径 | ps 后推进 |
| `logs` | 已实现 flags | positional agent、tail、timestamp、sandbox | 可能需要服务端 tail/filter，或 CLI 层截断 | warning/output 规则 | agent/tail 可先做，sandbox 后做 |
| `inspect` | 已实现 project/agent/run/session；image 在旧树 | 新增 sandbox/image，旧入口 warning | 无新增 API；复用 GetSession/InspectImage | warning 机制 | 可早做 |
| `stats` | 缺 CLI/API | running sandbox 当前值和 watch | 新增统一 stats API；driver 接入 | sandbox 输出模型、driver 指标能力 | 最后阶段实现 |
| `images` | 已实现 | 保留 | 无 | 无 | 无需优先改 |
| `pull` | 已实现 | 保留 | 无 | 无 | 无需优先改 |
| `push` | 缺 CLI/API | 新增 push | v2 ImageService 增加 PushImage | image store 能力 | 与 sandbox 正交 |
| `rmi` | 已实现 | 保留 | 无 | 无 | 无需优先改 |

### 建议里程碑

为了控制 PR 尺寸，建议把本分支作为命令行优化主分支，再按以下里程碑拆短分支或连续 PR：

1. **CLI 基线和 project list**：配置文件发现、`ls`、project list 计数字段、相关测试。
2. **命名迁移和兼容层**：deprecation warning、`inspect image`、`inspect sandbox`、`logs [agent]`、`--sandbox` alias。
3. **sandbox 可观测性**：`ps` sandbox 视图、`logs --tail/--timestamp`、JSON 输出模型稳定。
4. **sandbox 生命周期**：`stop`、`resume`、删除 API、`rm --force`。
5. **执行和运行语义**：`exec <sandbox>`、`run --sandbox`、`run --trigger/--command`、`run -d`、`run --rm`。
6. **project 前台运行**：`up -d`、`up` attach、Ctrl+C shutdown。
7. **镜像扩展和旧入口兼容**：`push`、旧 `image` 命令树 deprecated warning。
8. **资源统计**：最后实现 `stats` 和 `stats -w/--watch`。

## 分阶段实施计划

### 1. 配置文件发现

目标：

- 显式 `-f` 支持任意路径，常规后缀为 `.yml` 和 `.yaml`。
- 未指定 `-f` 时，在当前目录查找 `agent-compose.yml` 和 `agent-compose.yaml`。
- 如果两个文件同时存在，返回 usage error，要求用户显式指定 `-f`。

代码入口：

- `resolveComposePath`
- `loadNormalizedCompose`
- `resolveComposeProject`

测试点：

- 默认发现 `.yml`。
- 默认发现 `.yaml`。
- 两个文件同时存在时报错。
- `-f /path/to/project/agent-compose.yaml` 时 project root 为文件所在目录。
- `--project-name` 仍覆盖 normalized project name。

### 2. 新增 `ls`

目标：

- 新增 `agent-compose ls`，列出 daemon 上所有 project。
- 支持 `--verbose`。
- 支持 `--json`。

代码依据：

- v2 `ProjectService.ListProjects` 已存在。
- `ListProjectsRequest` 支持 `query`、`include_removed`、`offset`、`limit`。
- `ProjectSummary` 已提供大部分需要输出的字段。

实现要点：

- 在 `newRootCommand` 注册 `ls`。
- CLI 端处理分页，至少拉取到 `has_more=false`。
- 默认列建议使用 `PROJECT`、`CONFIG FILE`、`REVISION`、`AGENTS`、`SCHEDULERS`、`SERVICES`。
- `CONFIG FILE` 可先使用 `ProjectSummary.source_path`。如果需要严格区分 compose path 和 project dir，需要检查 `ProjectRecord.Source` 的存储和 `ProjectServiceSourcePath`。
- 修复 `ProjectService.ListProjects` 中 agent/scheduler 数量为 0 的问题，或在 CLI 中避免展示不准确字段。
- 当前 API 没有 `services` 字段；若必须展示，需要先扩展 proto/store 或在本期将 services 显示为 `-` 并在 JSON 中清晰表达不可用。

测试点：

- 空 project 列表。
- 多 project 按更新时间排序。
- `--json` 输出包含分页后的完整列表。
- `--verbose` 包含 project id、source path、spec hash、created/updated/removed。

### 3. `inspect` 迁移

目标：

- 新增 `inspect image <image>`。
- 新增 `inspect sandbox <sandbox>`。
- 保留 `inspect project|agent|run`。
- `inspect session <id>` 暂时作为 alias，输出 deprecation warning。
- `image inspect <image>` 暂时作为 alias，输出 deprecation warning。

代码入口：

- `runComposeInspectCommand`
- `runComposeImageInspectCommand`
- `newRootCommand` 中 `imageCmd` 和 `inspectCmd`

测试点：

- `inspect image` 输出与旧 `image inspect` 一致。
- `inspect sandbox` 输出与旧 `inspect session` 等价，但字段命名对外使用 sandbox。
- 旧命令 warning 写到 stderr，不影响 `--json` stdout。
- 旧入口代码旁包含 `Deprecated:` 注释，注释中写明替代命令。

### 4. `logs` 增强

目标：

- 支持 `logs [agent]`。
- 新增 `-n/--tail`。
- 新增 `-t/--timestamp`。
- 新增 `--sandbox <sandbox>`，旧 `--session-id` 作为 alias。

代码入口：

- `composeLogsOptions`
- `runComposeLogsCommand`
- `followOrPrintProjectLogs`
- `writeLogsForRun`

实现要点：

- positional agent 与 `--agent` 同时出现时报 usage error。
- `--sandbox` 先映射到当前 run/session 查询能力。
- 旧 `--session-id` 输出 deprecated warning，说明后续删除，并提示使用 `--sandbox`。
- 旧 `--session-id` flag 或兼容分支旁增加 `Deprecated:` 注释。
- 当前 `--json --follow` 不兼容限制可以保留，直到定义流式 JSON。
- tail 和 timestamp 应在服务端过滤还是 CLI 端过滤需要结合日志来源确定；优先避免读取无限历史后再截断。

测试点：

- `logs reviewer` 等价于 `logs --agent reviewer`。
- `--tail` 对 run detail 和 project logs 都有效。
- `--timestamp` 文本输出包含时间。
- `--sandbox` 与旧 `--session-id` 行为一致。

### 5. `ps` 转为 sandbox 视图

目标：

- `ps` 默认展示 running sandbox。
- `ps -a/--all` 展示全部 sandbox。
- `--status` 过滤 sandbox 状态。
- `--verbose` 展示 driver、image、workspace、Jupyter、错误摘要等。

当前差异：

- 当前 `runComposePSCommand` 基于 project agents 构造 agent 视图。
- 当前输出列是 `AGENT/SCHEDULER/LATEST RUN/RUN STATUS/SESSION/DRIVER/IMAGE`。

实现要点：

- 需要确定 sandbox 数据源：可先由 v1 `ListSessions` + v2 run/project 信息组合得到。
- CLI output adapter 对外字段统一为 sandbox，不暴露 session 作为主列。
- `--all` 需要包含已结束和错误状态；如果现有 session store 已保留历史，可以直接查询，否则需要补 API。

测试点：

- 默认只显示 running。
- `--all` 包含 stopped/exited/error。
- `--status running,error` 正确过滤。
- `--json` 字段稳定。

### 6. `run` 输入模式重构

目标：

- `run <agent> <trigger>` 运行配置中的 trigger。
- `--trigger` 与 positional trigger 等价。
- `--prompt` 是手动 prompt 的唯一入口。
- 新增 `--command`。
- 新增 `-d/--detach`、`-i/--interactive`、`--sandbox`、`--jupyter`、`--jupyter-expose`、`--rm`。
- 旧 `--session-id` 作为 `--sandbox` alias，兼容期后删除。

当前差异：

- 当前 `run <agent> [prompt...]` 会把第二个及后续 positional 参数拼成 prompt。
- 当前 `RunAgentRequest` 有 `Prompt`、`SessionId`、`CleanupPolicy`，没有 trigger/command/detach/jupyter/rm 字段。

实现要点：

- 需要扩展 v2 run API 或定义 trigger/command 到现有 run 模型的映射。
- `--detach` 需要非 streaming 或 stream 早返回语义。
- `--rm` 依赖 sandbox 删除能力。
- `--jupyter`/`--jupyter-expose` 需要 runtime/session 创建参数支持。
- trigger、prompt、command 必须互斥。

兼容策略：

1. 先新增 `--sandbox` alias，保留 `--session-id` warning。
2. 新增 `--trigger`/`--command`，不改变旧 positional prompt。
3. 对旧 positional prompt 输出 warning。
4. 兼容期后将第二 positional 参数解释为 trigger。
5. 旧 positional prompt 和 `--session-id` 兼容逻辑旁增加 `Deprecated:` 注释，标明替代用法。

测试点：

- `run reviewer --prompt "..."` 不受影响。
- `run reviewer trigger-name` 在迁移期 warning，最终作为 trigger。
- `--trigger`、`--prompt`、`--command` 互斥。
- `--rm --keep-running` 报 usage error。

### 7. `exec` 目标重构

目标：

- `exec <sandbox> [command] [args...]`。
- 新增 `--prompt`、`--command`、`-d/--detach`、`-i/--interactive`。
- 保留 `--agent`、`--run-id`、`--session-id` 目标选择方式作为 deprecated 兼容入口。

当前差异：

- 当前第一个 positional 参数是 command。
- 当前通过 `--agent`、`--run-id`、`--session-id` 选择目标。
- 当前支持 `--cwd`。

实现要点：

- 需要调整 Cobra args 解析，避免与旧形式冲突。
- 兼容期内可以通过是否提供旧 target flags 区分旧形式。
- 旧 `--agent`、`--run-id`、`--session-id` target flags 输出 deprecated warning，说明后续删除，并提示使用 `agent-compose exec <sandbox> ...`。
- 旧 target flags 定义或兼容分支旁增加 `Deprecated:` 注释。
- `--cwd` 是否保留为执行上下文参数需要单独决定；如果保留，不应再承担目标选择语义。

测试点：

- `exec sandbox_123 pwd` 目标为 sandbox_123，命令为 pwd。
- 旧 `exec --session-id ... pwd` warning 后仍可用。
- 未传 command 时进入默认交互入口。

### 8. Sandbox 生命周期命令

目标：

- `stop <sandbox...>` 停止 sandbox。
- `resume <sandbox...>` 恢复 sandbox。
- `rm [--force] <sandbox...>` 删除 sandbox。

代码依据：

- v1 `SessionService.StopSession` 和 `ResumeSession` 已存在，可作为 `stop`/`resume` 的第一阶段实现基础。
- 当前没有删除 session/sandbox 的 RPC；`rm` 需要新增 v2 后端能力。

`rm` 行为要求：

- 删除非 running sandbox：删除 sandbox 元数据和相关运行态资源。
- 删除 running sandbox 且未传 `--force`：报错，错误信息明确包含 `is running`。
- 删除 running sandbox 且传 `--force`：先停止 sandbox，再删除资源和元数据。
- 多个 sandbox 批量删除时，应逐项返回结果；任一失败时命令返回非零退出码。

实现要点：

- 新增 v2 sandbox 删除 API；不在 v1 SessionService 中新增删除 RPC。
- 明确 running 状态判断来源。
- `--json` 输出应包含每个 sandbox 的 deleted/stopped/error 状态。

测试点：

- running sandbox 不带 `--force` 删除失败。
- running sandbox 带 `--force` 会先 stop 再 delete。
- stopped sandbox 可直接删除。
- 批量删除部分失败时退出码非零，JSON 包含逐项结果。

### 9. `up` 前台/后台语义

目标：

- `up` 默认前台 attach project 输出。
- `up -d/--detach` apply 后返回。
- 前台 `Ctrl+C` 停止整个 project。

当前差异：

- 当前 `up` 只是 `ApplyProject` 后输出 apply 结果，行为更接近目标 `up -d`。

实现要点：

- 第一阶段新增 `--detach`，并让当前行为成为 detach 语义。
- 第二阶段实现 project 级日志 attach。
- 第三阶段处理 signal，调用 project down/stop 逻辑。

测试点：

- `up -d` 返回 project/revision/change summary。
- `up` attach 输出。
- 中断前台 `up` 后 project 被停止。

### 10. 镜像命令整理

目标：

- 保留顶层 `images`、`pull`、`rmi`。
- 新增 `push`。
- `image inspect` 迁移到 `inspect image`。
- 保留旧 `image` 命令树，但全部标记 deprecated。

当前依据：

- `images` 已支持 `--query`、`-a/--all`。
- `pull` 已支持 `--platform`。
- `rmi` 已支持 `--force`、`--prune-children`。
- 旧 `image` 命令树与顶层命令重复。

兼容策略：

1. 新增 `inspect image`。
2. 旧 `image ls/pull/rm/inspect` 输出 deprecation warning。
3. 本轮不删除旧 `image` 命令树；后续经过几个版本兼容期后，再单独评估删除。
4. 旧 `image` 命令树注册代码旁增加 `Deprecated:` 注释，逐项写明替代命令。

### 11. `stats`

目标：

- `stats` 默认展示当前 running sandbox 的资源消耗，采集一次后返回。
- `stats -w/--watch` 定期刷新。

当前差异：

- 当前没有 `stats` CLI。
- 当前没有统一 sandbox stats API。

实现要点：

- 放到最后阶段实现，避免资源采集 API 和 driver 指标接入影响命令行结构优化主线。
- 先定义统一 stats response。
- Docker runtime 可先接入 Docker stats。
- BoxLite/Microsandbox 指标按可获得性渐进支持，不可用字段显示 `-`。
- watch 模式应支持稳定刷新周期和 Ctrl+C 退出。

测试点：

- 无 running sandbox 时输出空表/空数组。
- running sandbox 有 CPU/memory 字段。
- `--watch` 能定期刷新并响应中断。

## Deprecated 兼容项

本轮只增加 deprecated warning 和替代命令提示，不删除旧命令或旧参数。后续经过几个版本兼容期后，再单独评估是否删除。

| Deprecated 项 | 替代方式 | 本轮处理 |
| --- | --- | --- |
| `agent-compose image` 命令树 | `images`、`pull`、`rmi`、`inspect image` | 保留旧入口，输出 deprecated warning，代码旁增加 `Deprecated:` 注释。 |
| `agent-compose run <agent> [prompt...]` | `agent-compose run <agent> --prompt "..."` | 保留兼容解析，输出 deprecated warning，代码旁增加 `Deprecated:` 注释。 |
| `agent-compose run --session-id <id>` | `agent-compose run --sandbox <sandbox>` | 保留 alias，输出 deprecated warning，代码旁增加 `Deprecated:` 注释。 |
| `agent-compose exec --agent/--run-id/--session-id ...` | `agent-compose exec <sandbox> ...` | 保留旧目标选择方式，输出 deprecated warning，代码旁增加 `Deprecated:` 注释。 |
| `agent-compose logs --session-id <id>` | `agent-compose logs --sandbox <sandbox>` | 保留 alias，输出 deprecated warning，代码旁增加 `Deprecated:` 注释。 |
| `agent-compose inspect session <id>` | `agent-compose inspect sandbox <sandbox>` | 保留 alias，输出 deprecated warning，代码旁增加 `Deprecated:` 注释。 |

## 推荐 PR 顺序

1. 文档：命令行使用手册和本改进计划。
2. 配置文件发现：支持 `.yml/.yaml`。
3. `ls`：基于现有 `ListProjects` 增加 CLI，并修复 list project 的 agent/scheduler count。
4. `inspect image` 和 `inspect sandbox`，旧入口 warning。
5. `logs` 增强：positional agent、tail、timestamp、sandbox alias。
6. `ps` sandbox 视图。
7. `stop` 和 `resume`：先基于 v1 session API 实现 sandbox 包装。
8. v2 sandbox 删除 API 与 `rm --force`。
9. `exec <sandbox>` 目标重构。
10. `run` 输入模式重构。
11. `up -d` 与前台 attach/shutdown。
12. `push` 和旧 `image` 命令树 deprecated warning。
13. `stats` 统一 API 与 CLI。

## 仍需确认

- sandbox id 是否直接等于当前 session id，还是新增独立 alias。无论内部如何实现，CLI 输出和参数都使用 sandbox。
- `run --command` 和 `exec <sandbox> --command` 是否都记录为 run，或 exec 单独记录执行历史。
- `--jupyter` 和 `--jupyter-expose` 需要扩展哪些 session/runtime 创建参数。
- `stats` 的统一采集周期、字段命名和 driver 不支持字段的 JSON 表达方式。
