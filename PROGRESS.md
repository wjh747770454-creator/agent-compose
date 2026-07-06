# agent-compose sandbox prune Progress

本文档把 `agent-compose sandbox` 命令组和 `sandbox prune` 实现拆成可独立执行、独立验收的任务清单。任务按依赖顺序排列；标记为“可并行”的子任务可以在同一父任务内用 subagent 并行推进，但 subagent 并发度最高不超过 5。

## 文档索引

- 技术方案：[docs/spec/sandbox-cli-prune-spec.md](docs/spec/sandbox-cli-prune-spec.md)
- 实施计划：[docs/plan/sandbox-cli-prune-implementation-plan.md](docs/plan/sandbox-cli-prune-implementation-plan.md)
- Harness：[AGENTS.md](AGENTS.md)
- 测试标准：[TESTING.md](TESTING.md)
- 任务命令：[Taskfile.yml](Taskfile.yml)
- CLI 设计：[docs/zh-CN/design/agent-compose-cli-improvement-plan.md](docs/zh-CN/design/agent-compose-cli-improvement-plan.md)
- 英文 CLI 手册：[docs/command-line-manual.md](docs/command-line-manual.md)
- 中文 CLI 手册：[docs/zh-CN/command-line-manual.md](docs/zh-CN/command-line-manual.md)

## 执行规则

- [ ] 每个任务完成时必须同时完成对应测试方案和验收标准。
- [ ] 不跨阶段提前合并依赖未满足的功能。
- [ ] 不修改 proto、generated Connect 文件、runtime driver、部署 compose 或 image build 行为，除非先更新 spec。
- [ ] `sandbox prune` 删除路径不得对 `RemoveSandbox` 传 `force=true`。
- [ ] 涉及 CLI 用户可见行为时必须补充或更新 `cmd/agent-compose/main_test.go` integration tests。
- [ ] 阶段性收口前至少运行 focused Go 测试；最终收口运行 `task lint`、`task test`、`task build`，或记录无法运行的具体原因。
- [ ] 每个任务完成后必须把完成总结写成多行 Markdown 结构，使用 `状态`、`变更`、`验证`、`审计与例外`、`下一目标`。

## 阶段 1：建立 `sandbox` 命令组并复用现有行为

参考文档：[docs/plan/sandbox-cli-prune-implementation-plan.md](docs/plan/sandbox-cli-prune-implementation-plan.md) 阶段 1。

- [x] 1.1 注册 `agent-compose sandbox` 命令组
  - 依赖：无。
  - 工作内容：在 `cmd/agent-compose/main.go` 新增 `sandboxCmd`，默认执行 help；注册 `sandbox ls`、`sandbox stop`、`sandbox resume`、`sandbox rm`；复用现有 `runComposePSCommand`、`runComposeSandboxActionCommand`、`runComposeSandboxRemoveCommand`；保留顶层 `ps/stop/resume/rm` 不变。
  - 可并行子任务：
    - [x] 可并行：梳理命令 help 输出与现有 `ps/stop/resume/rm` flag 是否一致。
    - [x] 可并行：检查 root command 注册顺序是否影响既有 help 输出和测试断言。
  - 测试方案：新增或扩展 CLI integration test，覆盖 `sandbox ls --json` 与 `ps --json` 等价、`sandbox rm --force <id>` 传递 `force=true`。
  - 验收标准：`agent-compose sandbox --help` 展示 `ls/stop/resume/rm/prune` 入口；顶层 `ps/stop/resume/rm` 现有测试仍通过；未修改 proto 或 daemon API。
  - 完成总结：
    - 状态：已完成。
    - 变更：
      - 在 `cmd/agent-compose/main.go` 注册 `sandbox` 命令组。
      - 新增 `sandbox ls`，使用独立 `composePSOptions` 并复用 `runComposePSCommand`。
      - 新增 `sandbox stop`、`sandbox resume`，复用 `runComposeSandboxActionCommand`。
      - 新增 `sandbox rm`，使用独立 `composeSandboxRemoveOptions` 并复用 `runComposeSandboxRemoveCommand`。
      - 新增 `sandbox prune` help 入口，行为暂为 unsupported 占位，等待阶段 2 实现 flags 和 dry-run 模型。
      - 扩展 `cmd/agent-compose/main_test.go`，覆盖 `sandbox ls --json` 与 `ps --json` 等价、`sandbox rm --force` 传递 `force=true`、`sandbox --help` 展示 `ls/stop/resume/rm/prune`。
    - 验证：
      - `go test ./cmd/agent-compose -run 'TestIntegrationCLI(PSTableAndJSON|RemoveSandboxes|Sandbox)' -count=1`
      - `go test ./cmd/agent-compose -run 'TestIntegrationCLI(StopSandbox|ResumeSandboxesJSON|RemoveSandboxes|PSTableAndJSON|Sandbox)|TestCLI(StopRequiresSandboxUsageError|ResumeRejectsEmptySandboxUsageError|RemoveSandbox)' -count=1`
    - 审计与例外：
      - 未修改 proto、generated Connect 文件、runtime driver、部署 compose 或 image build 行为。
      - `sandbox prune` 仅作为 help 入口占位；未提前实现候选选择、删除路径或新后端 API。
      - 顶层 `ps/stop/resume/rm` 保留原有命令和 option 实例，新增 sandbox 子命令使用独立 option 实例，避免 flag 状态共享。
    - 下一目标：2.1 增加 prune 数据结构、flags 和通用 duration 解析。

## 阶段 2：实现 `sandbox prune` 过滤和 dry-run 模型

参考文档：[docs/plan/sandbox-cli-prune-implementation-plan.md](docs/plan/sandbox-cli-prune-implementation-plan.md) 阶段 2。

- [x] 2.1 增加 prune 数据结构、flags 和通用 duration 解析
  - 依赖：1.1。
  - 工作内容：新增 `composeSandboxPruneOptions`、`composeSandboxPruneOutput`、`composeSandboxPruneSkipped`；在 `sandbox prune` 挂载 `--status`、`--agent`、`--driver`、`--older-than`、`--force`；复用或重命名 `parseCacheOlderThanSeconds` 为通用 duration helper，并保持 `cache prune` 行为不变。
  - 可并行子任务：
    - [x] 可并行：审计 `parseCacheOlderThanSeconds` 的错误消息和测试覆盖，确保复用后不破坏 cache prune。
    - [x] 可并行：设计 `composeSandboxPruneOutput` JSON 字段与 spec 一致性检查。
  - 测试方案：新增 duration helper 相关 CLI 测试或复用 cache prune 现有测试，确保 `7d`、`168h`、非法值、0、负数、亚秒行为不回归。
  - 验收标准：`cache prune --older-than` 现有测试通过；`sandbox prune --older-than` 使用同一解析规则；JSON struct tag 与 spec 字段一致。
  - 完成总结：
    - 状态：已完成。
    - 变更：
      - 新增 `composeSandboxPruneOptions`，包含 `Status`、`Agent`、`Driver`、`OlderThan`、`Force`。
      - 新增 `composeSandboxPruneOutput` 和 `composeSandboxPruneSkipped`，JSON 字段与 spec 的 `dry_run`、`matched`、`removed`、`skipped`、`warnings` 一致。
      - 为 `sandbox prune` 挂载 `--status`、`--agent`、`--driver`、`--older-than`、`--force`。
      - 将 `parseCacheOlderThanSeconds` 重命名为通用 `parseOlderThanSeconds`，并让 `cache prune` 与 `sandbox prune` 共享该解析规则。
      - 新增 parser edge case 测试和 prune JSON shape 测试，覆盖 `7d`、`168h`、非法值、0、负数、亚秒行为。
    - 验证：
      - `go test ./cmd/agent-compose -run 'TestIntegrationCLI(PSTableAndJSON|RemoveSandboxes|Sandbox|CachePrune)|TestParseOlderThanSeconds|TestComposeSandboxPruneOutputJSONShape' -count=1`
      - `go test ./cmd/agent-compose -run 'TestIntegrationCLI(PSTableAndJSON|RemoveSandboxes|Sandbox)' -count=1`
    - 审计与例外：
      - `sandbox prune` 当前只完成 flags、数据结构和 `--older-than` 解析；候选选择、dry-run 输出和删除路径留给 2.2、3.1、4.1。
      - `sandbox prune --older-than 7d` 会通过共享 duration parser 后返回 unsupported 占位错误，避免提前实现依赖未满足的行为。
      - 未修改 proto、generated Connect 文件、runtime driver、部署 compose 或 image build 行为。
    - 下一目标：2.2 实现 prune 候选选择和安全过滤。

- [x] 2.2 实现 prune 候选选择和安全过滤
  - 依赖：2.1。
  - 工作内容：实现 `runComposeSandboxPruneCommand` 的 dry-run 路径；通过 `composePSOutputFromProject(..., composePSOptions{All: true})` 获取当前 project 全部 sandbox；默认匹配 `stopped,failed`；支持 `--status`、`--agent`、`--driver`、`--older-than`；禁止 `running/pending` 状态进入 prune。
  - 可并行子任务：
    - [x] 可并行：构造测试 fixtures，覆盖 running/stopped/failed/error/foreign project/不同 agent/不同 driver。
    - [x] 可并行：审计 `composePSSessionBelongsToProject` 的归属判断，确认 prune 复用时不会扩大清理范围。
  - 测试方案：新增 CLI integration tests，覆盖默认 dry-run、`--status error`、`--agent worker`、`--driver microsandbox`、`--older-than 24h`、`--status running` 和 `--status pending`。
  - 验收标准：dry-run 不调用 `RemoveSandbox`；foreign project 不进入 matched；时间无法解析的项进入 warnings 而不是 matched；usage error 使用 `exitCodeUsage`。
  - 完成总结：
    - 状态：已完成。
    - 变更：
      - `runComposeSandboxPruneCommand` 现在解析当前 compose project，读取 daemon project，并通过 `composePSOutputFromProject(..., composePSOptions{All: true})` 构建当前 project 下候选 sandbox。
      - 默认 dry-run 匹配 `stopped,failed`，并支持 `--status`、`--agent`、`--driver`、`--older-than` 过滤。
      - `--status running` 和 `--status pending` 返回 `exitCodeUsage`，提示使用 `agent-compose sandbox rm --force <sandbox>` 处理运行中 sandbox。
      - `--older-than` 使用 `updated_at`，缺失时回退 `created_at`；时间缺失或无法解析的 sandbox 会跳过并进入 `warnings`。
      - dry-run JSON 输出 `dry_run=true`、`matched`、空 `removed`、空 `skipped`、`warnings`，且不会调用 `RemoveSandbox`。
    - 验证：
      - `go test ./cmd/agent-compose -run 'TestIntegrationCLI(PSTableAndJSON|RemoveSandboxes|Sandbox)|TestParseOlderThanSeconds|TestComposeSandboxPruneOutputJSONShape' -count=1`
    - 审计与例外：
      - prune 复用 `composePSOutputFromProject`，因此继续沿用 `composePSSessionBelongsToProject` 的 project 归属判断，没有扩大到 daemon 全局 sandbox。
      - `--force` 仍返回 unsupported，占位等待 3.1 实现删除路径；本任务未添加任何删除调用。
      - 文本输出仍是临时 dry-run 摘要；正式 text/JSON 输出整理留给 4.1。
      - 未修改 proto、generated Connect 文件、runtime driver、部署 compose 或 image build 行为。
    - 下一目标：3.1 实现 `sandbox prune --force` 删除路径。

## 阶段 3：实现 forced prune 删除和失败语义

参考文档：[docs/plan/sandbox-cli-prune-implementation-plan.md](docs/plan/sandbox-cli-prune-implementation-plan.md) 阶段 3。

- [ ] 3.1 实现 `sandbox prune --force` 删除路径
  - 依赖：2.2。
  - 工作内容：当 `--force` 为 true 时遍历 matched sandbox；逐个调用 `removeSandbox(ctx, clients.sandbox, id, false)`；成功项加入 `Removed`；失败项加入 `Skipped` 并继续后续删除；存在 skipped 时输出后返回非零。
  - 可并行子任务：
    - [ ] 可并行：补充 sandbox remove stub 断言，确保 prune 删除永远传 `force=false`。
    - [ ] 可并行：梳理 partial failure 输出和退出码应与现有 `cache rm/prune` 风格保持接近。
  - 测试方案：新增 CLI integration tests，覆盖全部删除成功、一个删除失败后继续、存在 skipped 返回非零、未匹配项不删除。
  - 验收标准：删除顺序与 matched 顺序一致；partial failure 不吞错误；running/pending 不会通过 prune 删除。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：待完成。

## 阶段 4：文本输出、JSON 输出和 CLI 手册更新

参考文档：[docs/plan/sandbox-cli-prune-implementation-plan.md](docs/plan/sandbox-cli-prune-implementation-plan.md) 阶段 4。

- [ ] 4.1 实现 prune 输出格式
  - 依赖：3.1。
  - 工作内容：新增 `writeSandboxPruneOutput`；支持 text 和 JSON；文本 dry-run 输出 matched/skipped/would remove 并提示 `--force`；文本 forced 输出 removed/matched/skipped；表格展示 `SANDBOX`、`AGENT`、`STATUS`、`DRIVER`、`UPDATED`、`REASON`。
  - 可并行子任务：
    - [ ] 可并行：对比 `writeCacheOperationOutput` 的输出风格，保持提示语和表格密度一致。
    - [ ] 可并行：检查 `--json` 路径不向 stderr 写普通提示。
  - 测试方案：新增文本输出测试和 JSON 解码测试，验证 dry-run、forced、skipped、warnings 字段。
  - 验收标准：`--json` stdout 是合法 JSON；文本输出能明确区分 dry-run 与实际删除；字段名与 spec 保持一致。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：待完成。

- [ ] 4.2 更新中英文 CLI 手册
  - 依赖：4.1。
  - 工作内容：更新 `docs/command-line-manual.md` 和 `docs/zh-CN/command-line-manual.md`；新增 `sandbox` 命令组说明、`sandbox prune` 参数、dry-run/force 示例；明确 `sandbox prune` 不清理 runtime cache，cache 文件仍由 `cache prune` 管理。
  - 可并行子任务：
    - [ ] 可并行：英文手册更新。
    - [ ] 可并行：中文手册更新。
    - [ ] 可并行：检查 README 中是否已有命令索引需要同步提示；如无必要，记录不修改原因。
  - 测试方案：文档检查；运行 focused CLI tests 确认文档更新未伴随行为回归。
  - 验收标准：中英文手册语义一致；示例命令与实际 flags 一致；不暗示 `sandbox prune` 会删除 runtime cache。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：待完成。

## 阶段 5：完整验证和收口

参考文档：[docs/plan/sandbox-cli-prune-implementation-plan.md](docs/plan/sandbox-cli-prune-implementation-plan.md) 阶段 5。

- [ ] 5.1 运行 focused 测试并审计范围
  - 依赖：4.2。
  - 工作内容：检查 `git diff`，确认未修改 proto、generated Connect 文件、runtime driver、compose deployment 配置；运行 focused CLI tests 和相关 package tests。
  - 可并行子任务：
    - [ ] 可并行：代码范围审计。
    - [ ] 可并行：文档范围审计。
    - [ ] 可并行：focused tests 运行和结果整理。
  - 测试方案：

```bash
go test ./cmd/agent-compose -run 'TestIntegrationCLI(PSTableAndJSON|RemoveSandboxes|Sandbox)' -count=1
go test ./cmd/agent-compose ./pkg/agentcompose/api ./pkg/storage/sessionstore -count=1
```

  - 验收标准：focused tests 通过；diff 范围符合 spec；如果有失败，完成总结记录命令、错误和下一步。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：待完成。

- [ ] 5.2 运行 harness 门禁
  - 依赖：5.1。
  - 工作内容：运行项目级质量门禁；如因环境依赖失败，记录具体失败命令、失败阶段和可复现错误。
  - 可并行子任务：无，质量门禁按顺序执行，避免缓存和资源竞争造成误判。
  - 测试方案：

```bash
task lint
task test
task build
```

  - 验收标准：门禁通过；或失败原因被清晰记录且不是实现本身导致时，标出残余风险。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：无。

## 停止条件

- 发现首版必须修改 proto/RPC 才能满足 spec 时停止，并回到 `docs/spec/sandbox-cli-prune-spec.md` 更新设计。
- 发现 `sandbox prune` 可能删除 running/pending sandbox 时停止，不允许用 `force=true` 绕过。
- 发现当前 project 归属判断会误包含 foreign project 时停止，先修复候选选择或收缩 prune 范围。
- 发现实现需要直接删除 `SESSION_ROOT`、`DATA_ROOT` 或 runtime driver 私有目录时停止，回到 spec 重新评审。
