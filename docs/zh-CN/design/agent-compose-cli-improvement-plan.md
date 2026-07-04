# agent-compose CLI 改进计划

本文面向研发和评审，记录 agent-compose CLI 从当前代码状态迁移到目标命令体系的实施计划。最终用户文档见 [命令行使用手册](../command-line-manual.md)。

> 状态说明：本文是历史实施设计，部分“暂缓”或“后续设计”条目已被 `cli-runtime-capabilities` 阶段实现覆盖。以根目录 `PROGRESS.md` 和 [命令行使用手册](../command-line-manual.md) 为当前权威：`stats`、`run -d/--detach`、`run -i/--interactive`、`--jupyter`、`--jupyter-expose`、command transcript、日志 follow 和 `run --rm` service 端清理已经落地；`build`、`push`、`up` 前台 attach/detach、TTY/PTY/WebSocket TTY、terminal resize、Connect bidi stdin、运行中 stdin 透传和 `ExecInteractive` 仍不在本轮范围内。

## 当前代码依据

当前 CLI 入口集中在 [cmd/agent-compose/main.go](/data/src/github.com/kingfs/agent-compose/cmd/agent-compose/main.go:405) 的 `newRootCommand`。

已实现全局参数：

- `--host`
- `-f, --file`
- `--project-name`
- `--json`

远程 daemon 认证：

- 使用 `--host` 或 `AGENT_COMPOSE_HOST` 连接 HTTP(S) daemon 时，CLI 从 `AUTH_USERNAME` 和 `AUTH_PASSWORD` 读取 Basic Auth 凭据并注入请求。
- daemon 侧 AuthManager 使用同一组 `AUTH_USERNAME` / `AUTH_PASSWORD` 校验 CLI Basic Auth，并覆盖 v1/v2 Connect API、`/api/` 和 Jupyter proxy 等远程访问路径；Web cookie 登录仍保留。
- 使用 Unix socket 本地连接时不注入 Basic Auth。
- 兼容历史部署的全局 `HTTP_BASIC_AUTH` 外层 Basic Auth；如果同时启用，远程请求需要同时满足外层认证和 AuthManager 认证。

当前 project 解析逻辑：

- `resolveComposePath` 在未指定 `-f` 时读取当前目录下的 `agent-compose.yml` 或 `agent-compose.yaml`。
- 如果当前目录同时存在 `agent-compose.yml` 和 `agent-compose.yaml`，返回 usage error，要求用户通过 `-f/--file` 显式指定。
- `loadNormalizedCompose` 使用 `compose.ParseFile` 解析配置，并用 `--project-name` 覆盖配置中的 project name。
- `runComposeUpCommand` apply project 时会把 `ProjectSource.ComposePath` 设置为配置文件路径，把 `ProjectSource.ProjectDir` 设置为配置文件所在目录。

当前已注册命令：

| 命令 | 当前用法 | 当前实现 |
| --- | --- | --- |
| `daemon` | `agent-compose daemon` | 启动 daemon。 |
| `version` | `agent-compose version` | 输出 build version。 |
| `status` | `agent-compose status` | 请求 daemon version/status。 |
| `config` | `agent-compose config [--quiet]` | 解析并输出 normalized config。 |
| `up` | `agent-compose up` | 调用 v2 `ProjectService.ApplyProject`；当前行为是 apply 后返回，由 daemon 管理 project；无 `-d/--detach`，无前台 attach。 |
| `down` | `agent-compose down` | 调用 v2 `ProjectService.RemoveProject`。 |
| `run` | `agent-compose run <agent> [prompt...]` | 调用 v2 `RunService.RunAgentStream`；支持 `--prompt`、`--trigger`、`--command`、`--sandbox`、`--session-id` deprecated alias、`--keep-running`、`--rm`；旧 positional prompt 保留并输出 deprecated warning。 |
| `logs` | `agent-compose logs [agent]` | 支持 `--agent`、`--run-id`、`--sandbox`、`--session-id` deprecated alias、`--follow`。 |
| `ps` | `agent-compose ps [-a] [--status ...] [--verbose]` | 已转为 sandbox 列表视图，默认展示 running sandbox。 |
| `stop` | `agent-compose stop <sandbox...>` | 基于 v1 `StopSession` 停止 sandbox。 |
| `resume` | `agent-compose resume <sandbox...>` | 基于 v1 `ResumeSession` 恢复 sandbox。 |
| `rm` | `agent-compose rm [--force] <sandbox...>` | 调用 v2 `SandboxService.RemoveSandbox` 删除 sandbox；running sandbox 无 `--force` 会报 `is running`。 |
| `exec` | `agent-compose exec <sandbox> [command] [args...]` | 调用 v2 `ExecService.ExecStream`；旧 `--agent`、`--run-id`、`--session-id` 目标选择方式保留并输出 deprecated warning；支持 `--cwd` 和 `--command`。 |
| `inspect` | `agent-compose inspect <project|agent|run|sandbox|session|image> [name-or-id]` | 查看 project、agent、run、sandbox/session、image；`inspect session` 保留并输出 deprecated warning。 |
| `images` | `agent-compose images` | 调用 image list；支持 `--query`、`-a/--all`。 |
| `pull` | `agent-compose pull [image]` | 指定 image 时拉取单个镜像；无参数时读取当前 project 下所有 agent image 并去重拉取；支持 `--platform`。 |
| `rmi` | `agent-compose rmi <image>` | 调用 image remove；支持 `--force`、`--prune-children`。 |
| `image` | `agent-compose image <subcommand>` | 旧 image 命令树，包含 `ls`、`pull`、`rm`、`inspect`，全部保留并输出 deprecated warning。 |

现有后端/API 能力：

- v2 `ProjectService` 已有 `ListProjects`、`ApplyProject`、`GetProject`、`RemoveProject`、`WatchProject`。
- v2 `ListProjectsResponse` 返回 `ProjectSummary` 列表，summary 包含 `project_id`、`name`、`source_path`、`current_revision`、`spec_hash`、`agent_count`、`scheduler_count`、`running_run_count`、`latest_run_id`、`created_at`、`updated_at`、`removed_at`。
- `ProjectService.ListProjects` 已可返回 list 场景下的 agent/scheduler 数量。
- v1 `SessionService` 已有 `ResumeSession`、`StopSession`、`GetSession`、`ListSessions`、`WatchSession`。
- v2 `SandboxService.RemoveSandbox` 已新增，用于删除 sandbox；running sandbox 需要 `force=true`。
- runtime driver 已有 stop 能力；资源统计命令没有现成 CLI 和统一 API。

## 当前完成进度

截至 `feature/cli-optimization` 当前主线，已完成：

- 文档：命令行使用手册和本改进计划。
- 配置文件发现：支持 `agent-compose.yml` / `agent-compose.yaml`，`-f/--file` 可指向任意路径，并以配置文件所在目录作为 project root。
- Project 列表：新增 `ls`，支持 `--verbose` 和 `--json`，并修复 list project 的 agent/scheduler 计数。
- 命名迁移和兼容层：新增 `inspect image`、`inspect sandbox`；旧 `image` 命令树、`inspect session`、`--session-id` 等兼容入口输出 deprecated warning 到 stderr。
- Sandbox 可观测性：`ps` 已转为 sandbox 视图，支持 `-a/--all`、`--status`、`--verbose`、`--json`。
- Sandbox 生命周期：新增 `stop`、`resume`、`rm --force`；新增 v2 `SandboxService.RemoveSandbox` 和底层 session 删除能力。
- 执行目标迁移：`exec <sandbox> [command] [args...]` 已落地；`exec <sandbox> --command "..."` 已支持；旧 target flags 保留并输出 deprecated warning。
- Run 增强：新增 `--sandbox`、`--trigger`、`--command`、`--rm`；`--command` 通过 v2 `RunAgentRequest.command` 启动或复用 agent sandbox 后执行 `bash -lc`，并把 stdout/stderr/output、exit code 和 artifacts 归档到该次 run；旧 `--session-id` 和 positional prompt 保留并输出 deprecated warning。
- 镜像命令：旧 `image` 命令树已 deprecated；`pull [image]` 支持无参数时拉取当前 project 下所有 agent image。

## Project Service 概念调查

当前代码中没有发现 project spec 层面的 `services` 定义，也没有发现 project service 生命周期、store 记录、v2 `ProjectSummary` 字段或 CLI/API 的真实使用路径。现有 `agent-compose.yml` 的主体仍是 `agents`、`workspace`、`variables`、`network` 等配置。

因此，`ls` 中的 `SERVICES` 列应视为早期 CLI 设计中借鉴 compose 语义留下的占位概念，而不是当前可用的功能。当前实现只在文本表格中显示 `-`，JSON 输出不提供虚假的 service count。后续如果确实需要 service 概念，应先补齐以下设计，再扩展 CLI：

- project spec 中 service 的定义、字段和生命周期。
- daemon store/API 中 service 的持久化和 `ProjectSummary` 计数字段。
- `up/down/ls/inspect/logs/stats` 如何展示和管理 service。
- service 与 agent/sandbox/runtime driver 的边界关系。

暂不做的命令和能力：

- `build`：本轮不实现。
- `push`：本轮不实现；如未来实现，需要扩展 v2 ImageService。
- `stats` / `stats -w`：本轮不实现；该能力需要统一 sandbox stats API 和 runtime driver 指标接入。
- `up` 前台 attach / Ctrl+C 停止整个 project：本轮不实现。当前 `up` 的语义就是将 project 注册/应用到 daemon 后返回，不新增 `-d/--detach`，也不再规划 docker compose 风格的默认前台 attach。
- `run -d/--detach`、`run -i/--interactive`、`--jupyter`、`--jupyter-expose`：需要明确后台运行、交互和 runtime/session 创建参数。
- `run -d/--detach` 当前不能只在 CLI 或现有 `RunAgentStream` 上包装实现。现有执行生命周期绑定 RPC request context，`StopRun` 也只更新 DB 状态，不取消内存执行；真正实现需要 v2 后台 run 提交 API、daemon 级 run supervisor、cancel map，并明确 daemon 重启后的语义。
- `--jupyter`、`--jupyter-expose` 当前不能只在 CLI 加 flag。Jupyter proxy state 和 runtime port mapping 在 session 创建和 driver 启动时决定，guest port 来自全局配置，host bind 当前固定为内部随机地址；真正实现需要 per-session Jupyter/network options、store/proxy state 扩展和 driver 映射支持。

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
images
pull
rmi
```

本轮不实现 `build`、`push`、`stats`。旧 `image` 命令树不删除，只标记 deprecated，并提示用户迁移到顶层 image 命令和 `inspect image`。

## 实施原则

本轮目标是快速、可靠地优化命令行结构，补全命令行使用逻辑。除非现有 API 或存储能力无法支撑目标行为，否则优先在 CLI 层完成语义调整、输出转换和兼容处理。

具体原则：

- 不扩散改动范围。优先复用当前 v1 SessionService、v2 ProjectService、RunService、ExecService、ImageService 能力。
- 必要时才扩展后端/API。当前已完成 sandbox 删除和 `run --command` 所需 v2 能力；`push`、`stats` 等扩展暂缓。
- 需要新增底层功能或 API 时，统一走 v2。v1 会在后续版本逐步删除，因此本轮不向 v1 增加新 RPC 或新数据模型。
- 兼容优先。计划删除命令、删除参数、改变 positional 语义前，必须先提供替代命令，并在旧入口输出 deprecated warning。
- warning 必须写到 stderr，不污染 `--json` stdout。
- deprecated warning 需要明确说明旧入口后续会被删除，并给出替代命令。例如：`agent-compose image inspect` is deprecated and will be removed in a future release; use `agent-compose inspect image` instead.
- 代码中同步增加 `Deprecated:` 注释或显式 deprecation 标记，方便后续定位旧兼容逻辑。
- 本轮不实际删除旧命令和旧参数。删除动作等后续经过几个版本兼容期后再单独评估和执行。
- `stats` 涉及统一资源采集 API 和 runtime driver 指标接入，改动范围较大，本轮暂缓。

## 任务拆分和依赖关系

### 前置任务

以下任务会影响后续多个命令，应优先完成：

| 前置任务 | 状态 | 原因 | 影响命令 |
| --- | --- | --- | --- |
| 配置文件发现统一 | 已完成 | 所有 project 命令都依赖 `resolveComposeProject`；`.yml/.yaml` 和 `-f` 语义必须一致。 | `config`、`up`、`down`、`run`、`ps`、`logs`、`inspect`、`stats`、sandbox 生命周期命令 |
| CLI 输出模型整理 | 主体已完成 | 多个命令需要把内部 session/run 转换成对外 sandbox；先定义 shared output struct 可减少重复和破坏性变更。 | `ps`、`run`、`exec`、`logs`、`inspect sandbox`、`stop`、`resume`、`rm`、`stats` |
| deprecation warning 机制 | 已完成 | 旧 `image`、`--session-id`、`inspect session`、旧 `exec` 目标选择都需要兼容期 warning，且不能污染 `--json` stdout。 | `run`、`exec`、`logs`、`inspect`、`image` |
| project list 计数字段修复 | 已完成 | `ls` 默认要展示 agent/scheduler 数量。 | `ls` |
| sandbox 删除 API 设计 | 已完成 | `rm` 和 `run --rm` 都依赖删除能力。 | `rm`、`run --rm` |

### 顺序链路

必须按顺序推进的链路：

1. 配置文件发现统一 -> project 命令测试基线 -> 后续所有 project 命令。
2. project 列表字段修复 -> `ls` -> project 级自动化输出稳定。
3. `inspect sandbox` -> `ps` sandbox 输出模型 -> `exec <sandbox>` 和 `logs --sandbox` 的用户发现路径。
4. sandbox 删除 API -> `rm --force` -> `run --rm`。
5. `ps` sandbox 化 -> `stop/resume/rm` 批量操作体验。
6. `run` 新输入模式 API 支持 -> `run --trigger/--command` -> positional prompt deprecated warning；`run --detach` 仍需单独设计。
7. 保持当前 `up` apply 后返回语义；不实现前台 attach / Ctrl+C project shutdown。
8. `inspect image` 发布 -> 旧 `image` 命令树 deprecated warning。
9. sandbox 输出模型稳定；`stats` API/CLI 本轮暂缓。

### 可并行任务

在前置任务完成或接口边界明确后，可以并行推进：

| 可并行任务 | 前置条件 | 说明 |
| --- | --- | --- |
| `logs --tail`、`--timestamp` | 已完成 | 当前基于 v2 RunService 的 run output/artifacts；CLI 端对 `RunDetail.output` 按行 tail，文本 timestamp 使用 run 级时间。 |
| image `push` | 暂不做 | 与 project/sandbox 命令正交；未来如需实现再扩展 v2 ImageService。 |

### 命令级开发矩阵

| 命令 | 当前状态 | 目标变更 | 后端/API 需求 | 依赖 | 可并行性 |
| --- | --- | --- | --- | --- | --- |
| `config` | 已实现，已支持 `.yml/.yaml` | 保持现状 | 无 | 无 | 已完成 |
| `ls` | 已实现 | 后续如需展示 services，需要扩展 API/store | services 字段来源未定义 | project list API | 主体已完成 |
| `up` | 已实现 apply 后返回 | 保持现状，不实现前台 attach，不新增 `-d/--detach` | 无 | 无 | 已完成 |
| `down` | 已实现 | 文案和输出对齐 sandbox | 无 | sandbox 输出术语 | 可随输出模型调整 |
| `run` | 已支持 prompt stream、`--sandbox`、`--trigger`、`--command`、`--rm` | `-d/--detach`、`-i/--interactive`、jupyter 参数 | command 模式已扩展 v2 Run API；后台/交互/jupyter 仍需定义映射 | run API | command 已完成，其余需拆设计 |
| `ps` | 已实现 sandbox 视图 | 后续按需要补更多 verbose 字段 | 可能需要补查询 | sandbox 输出模型 | 主体已完成 |
| `stop` | 已实现 | 保持现状 | 复用 v1 StopSession | sandbox id 映射 | 已完成 |
| `resume` | 已实现 | 保持现状 | 复用 v1 ResumeSession | sandbox id 映射 | 已完成 |
| `rm` | 已实现 | 保持现状，后续可优化批量部分失败 JSON | 已新增 v2 SandboxService 和 store 删除能力 | sandbox 删除 API | 已完成 |
| `exec` | 已实现 `exec <sandbox>`、`--command`，旧 flags deprecated | 后续评估 `--prompt`、`-d`、`-i` | `--command` 复用现有 ExecRequest；交互/后台仍需扩展 | ps sandbox 发现路径 | 主体已完成 |
| `logs` | 已支持 positional agent、`--sandbox`、旧 `--session-id` warning、`--tail`、`--timestamp` | 后续如需 provider 原生日志需单独设计 | 当前 CLI 层截断 `RunDetail.output`，不读取 provider 私有日志 | 日志语义 | 主体完成 |
| `inspect` | 已支持 sandbox/image，旧入口 warning | 保持现状 | 无新增 API；复用 GetSession/InspectImage | warning 机制 | 已完成 |
| `stats` | 缺 CLI/API | 暂不做 | 如未来实现需新增统一 stats API；driver 接入 | sandbox 输出模型、driver 指标能力 | 暂缓 |
| `images` | 已实现 | 保留 | 无 | 无 | 无需优先改 |
| `pull` | 已支持 `pull [image]` | 保持现状 | 无 | compose 解析 | 已完成 |
| `push` | 缺 CLI/API | 暂不做 | 如未来实现需 v2 ImageService 增加 PushImage | image store 能力 | 暂缓 |
| `rmi` | 已实现 | 保留 | 无 | 无 | 无需优先改 |

### 建议里程碑

为了控制 PR 尺寸，建议把本分支作为命令行优化主分支，再按以下里程碑拆短分支或连续 PR：

1. **CLI 基线和 project list**：配置文件发现、`ls`、project list 计数字段、相关测试。
2. **命名迁移和兼容层**：deprecation warning、`inspect image`、`inspect sandbox`、`logs [agent]`、`--sandbox` alias。
3. **sandbox 可观测性**：`ps` sandbox 视图、`logs --tail/--timestamp`、JSON 输出模型稳定。
4. **sandbox 生命周期**：`stop`、`resume`、删除 API、`rm --force`。
5. **执行和运行语义**：`exec <sandbox>`、`run --sandbox`、`run --trigger/--command`、`run --rm` 已完成；`run -d` 单独设计。
6. **暂缓能力**：`build`、`push`、`stats`、`up` 前台 attach / Ctrl+C project shutdown 本轮不做。

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
- 支持 `--limit` 和 `--offset` 分页。
- 支持 `--json`。

代码依据：

- v2 `ProjectService.ListProjects` 已存在。
- `ListProjectsRequest` 支持 `query`、`include_removed`、`offset`、`limit`。
- `ProjectSummary` 已提供大部分需要输出的字段。

实现要点：

- 在 `newRootCommand` 注册 `ls`。
- 未指定 `--limit/--offset` 时，CLI 端自动翻页，至少拉取到 `has_more=false`。
- 指定 `--limit` 或 `--offset` 时，只请求对应页，并保留 `total_count`、`has_more`、`next_offset` 方便自动化继续翻页。
- 默认列建议使用 `PROJECT`、`CONFIG FILE`、`REVISION`、`AGENTS`、`SCHEDULERS`、`SERVICES`。
- `CONFIG FILE` 可先使用 `ProjectSummary.source_path`。如果需要严格区分 compose path 和 project dir，需要检查 `ProjectRecord.Source` 的存储和 `ProjectServiceSourcePath`。
- 修复 `ProjectService.ListProjects` 中 agent/scheduler 数量为 0 的问题，或在 CLI 中避免展示不准确字段。
- 当前 project spec 和 v2 ProjectSummary 均没有真实 service 模型或 `services` 字段；该概念应视为早期 CLI 设想，当前 `ls` 文本列显示 `-`，JSON 中不输出虚假的 service count。

测试点：

- 空 project 列表。
- 多 project 按更新时间排序。
- `--json` 输出包含分页后的完整列表。
- `--limit/--offset --json` 只返回一页，并保留 `has_more` 和 `next_offset`。
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
- 当前日志来源限定为 agent-compose v2 RunService 的 run output/artifacts，即 `RunDetail.output`；不默认读取 Codex、Claude、Gemini 等 provider 私有日志文件。

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
- tail 当前在 CLI 端对 `RunDetail.output` 按行截取，文本和 JSON 输出保持一致。
- timestamp 当前只影响文本输出；由于 run output 没有逐 chunk 时间戳，每行使用 run 级代表时间，优先 `completed_at`，否则 `updated_at`，否则 `started_at`。

测试点：

- `logs reviewer` 等价于 `logs --agent reviewer`。
- `--tail` 对 run detail 和 project logs 都有效。
- `--timestamp` 文本输出包含 run 级时间。
- 多个 agent/run 和 follow 增量输出每行都有 agent 名前缀。
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

- `--trigger` 运行配置中的 trigger。
- `--prompt` 是手动 prompt 的唯一入口。
- `--command` 启动或复用 agent sandbox 后执行 bash command，而不是把 command 当 provider prompt。
- 新增 `--sandbox`、`--rm`；`-d/--detach`、`-i/--interactive`、`--jupyter`、`--jupyter-expose` 后续单独设计。
- 旧 `--session-id` 作为 `--sandbox` alias，兼容期后删除。

当前差异：

- 当前 `run <agent> [prompt...]` 会把第二个及后续 positional 参数拼成 prompt。
- 当前 `RunAgentRequest` 有 `Prompt`、`Command`、`SessionId`、`CleanupPolicy`、`TriggerId`，没有 detach/jupyter 字段。

实现要点：

- `--trigger` 已映射到现有 `RunAgentRequest.TriggerId`。
- `--command` 已映射到 v2 `RunAgentRequest.Command`；服务端复用 project run session 准备、runtime `ExecStream`、run 持久化和 artifacts/output 字段，统一用 `bash -lc <command>` 执行。
- `--detach` 需要非 streaming 或 stream 早返回语义。
- `--rm` 已依赖 v2 `SandboxService.RemoveSandbox` 实现；成功 run 后按 sandbox id 强制删除。
- `--jupyter`/`--jupyter-expose` 需要 runtime/session 创建参数支持。
- trigger、prompt、command 必须互斥。

`run -d/--detach` 后续设计边界：

- 不要用 CLI 后台 goroutine 包装 `RunAgentStream`，客户端退出会取消 RPC，无法形成 daemon 后台任务。
- 不要在现有 `RunAgent` 上简单增加 `detach=true` 后继续使用 request context；客户端断开仍可能取消执行。
- 建议新增 v2 `StartRun` 或同等后台提交 API，返回 run id 或 run summary；CLI `run -d` 调用该 API 后返回。
- 服务端需要新增 daemon 生命周期内的 run supervisor，持有 root context、运行中的 cancel map 和并发控制。
- `StopRun` 需要接入 supervisor cancel，再落 DB 状态；当前仅标记 canceled 不足以停止正在执行的 runtime command/agent。
- detached run 的 stdout/stderr 需要持久化到 artifacts/logs 或 run event 表，不能只依赖 RPC stream。
- 如果短期只实现 daemon 内存后台任务，文档必须明确 daemon 重启后 pending/running run 会按现有 reconcile 语义标记失败，不承诺 durable 恢复。

`--jupyter` / `--jupyter-expose` 后续设计边界：

- 不能在 CLI 层做本地端口转发来伪装能力；daemon、store、driver 和 `GetSessionProxy` 不知道该状态，会导致重启、复用、停止和远程访问失真。
- v2 run API 和 session 创建 API 需要承载 Jupyter options，例如 enabled、guest port、host bind address、host port、expose policy。
- `ProxyState` / driver proxy state 需要记录实际 host address、host port、guest port 和是否暴露。
- session 创建需要支持指定端口并处理端口冲突；复用已有 sandbox 时，如果 Jupyter options 不兼容，应报错而不是静默复用。
- Docker、BoxLite、Microsandbox driver 需要分别确认是否支持 bind address；不支持时 API/CLI 应明确拒绝 `[addr:]port` 中的 addr 部分。
- Jupyter launch command、proxy readiness 和 `GetSessionProxy` 应使用 session 级端口配置，而不是只读取全局 `JUPYTER_GUEST_PORT`。

兼容策略：

1. 先新增 `--sandbox` alias，保留 `--session-id` warning。
2. 新增 `--trigger`，不改变旧 positional prompt。
3. 对旧 positional prompt 输出 warning。
4. 新增 `--command`，并与 `--prompt`、`--trigger`、旧 positional prompt 互斥。
5. 兼容期后将第二 positional 参数解释为 trigger。
6. 旧 positional prompt 和 `--session-id` 兼容逻辑旁增加 `Deprecated:` 注释，标明替代用法。

测试点：

- `run reviewer --prompt "..."` 不受影响。
- `run reviewer legacy prompt` 在迁移期 warning，最终 positional 参数将作为 trigger。
- `--trigger`、`--prompt`、`--command` 互斥。
- `--command` 成功和失败都持久化 run，stream 输出 stdout/stderr，且不调用 provider prompt 执行路径。
- `--rm --keep-running` 成功 run 后仍会强制删除 sandbox。

### 7. `exec` 目标重构

目标：

- `exec <sandbox> [command] [args...]`。
- 新增 `--command`；后续再评估 `--prompt`、`-d/--detach`、`-i/--interactive`。
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
- `--command "..."` 映射为 `bash -lc "..."`，只作为命令输入形式，不改变 exec 的目标选择和历史记录语义。

测试点：

- `exec sandbox_123 pwd` 目标为 sandbox_123，命令为 pwd。
- `exec sandbox_123 --command "pwd"` 目标为 sandbox_123，命令为 `bash -lc "pwd"`。
- `--command` 和 positional command 同时出现时报 usage error。
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

### 9. `up` 语义

目标：

- 保持当前 `up` apply 后返回语义。
- 不实现 project 前台 attach 输出和 `Ctrl+C` 停止整个 project 的行为。

当前差异：

- 当前 `up` 只是 `ApplyProject` 后输出 apply 结果，这已经是本轮保留的真实语义。

实现要点：

- 本轮不新增 `-d/--detach`，避免出现没有实际差异的参数。
- 不规划 docker compose 风格的默认前台 attach。

测试点：

- `up` 返回 project/revision/change summary。

### 10. 镜像命令整理

目标：

- 保留顶层 `images`、`pull`、`rmi`。
- `push` 本轮暂不实现。
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

- 本轮暂不实现。
- 如未来实现，`stats` 默认展示当前 running sandbox 的资源消耗，采集一次后返回。
- 如未来实现，`stats -w/--watch` 定期刷新。

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

## 推荐后续 PR 顺序

已完成的基础迁移不再重复拆分。后续建议按以下顺序继续：

1. 如需支持 provider 原生日志或逐 chunk 时间戳，需要新增独立日志来源/API 设计；当前 `logs` 保持基于 agent-compose run output/artifacts。
2. `run -d/--detach`：先实现 v2 后台 run 提交 API 和 daemon run supervisor，不能复用当前同步 `RunAgent` 或 streaming API 伪装。
3. `--jupyter`、`--jupyter-expose`：先实现 per-session Jupyter/network options、proxy state 和 driver port mapping，再开放 CLI flag。
4. `run -i/--interactive`：需要 runtime/session 的双向 stdin/TTY 流式能力，单独设计。
5. 如确需 project 前台模式：先实现 project 级日志 attach 和中断处理，再开放对应命令/参数。
6. `build`、`push`、`stats`：本轮暂不做，后续需要时重新评估范围。

## 仍需确认

- sandbox id 是否直接等于当前 session id，还是新增独立 alias。无论内部如何实现，CLI 输出和参数都使用 sandbox。
- `exec <sandbox> --command` 是否需要单独执行历史；当前 `run --command` 已记录为 run。
- `--jupyter` 和 `--jupyter-expose` 需要扩展哪些 session/runtime 创建参数。
- `stats` 的统一采集周期、字段命名和 driver 不支持字段的 JSON 表达方式。
