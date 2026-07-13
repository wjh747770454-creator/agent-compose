# Platform Runtime Build Progress

本文档把平台化 Runtime 构建规格和实施计划拆成可独立执行、独立验收的任务清单。任务按依赖顺序排列；标记为“可并行”的子任务可以在同一父任务内并行推进，subagent 并发度最高不超过 5。

当前位置：项目根目录 PROGRESS.md。

## 当前执行范围

- 当前变更：platform-runtime-build。
- 已确认产物：macOS Docker-only binary、Linux 三 Driver binary、Linux 三 Driver multi-arch Docker image。
- 发布边界：binary 只用于本地和 CI 验证，不进入 GitHub Release。
- 当前进度：0/18 个父任务完成。
- 当前下一目标：1.1 建立 compiled driver 能力模型与 typed error。

## 文档索引

- 技术规格：[docs/spec/platform-runtime-build-spec.md](docs/spec/platform-runtime-build-spec.md)
- 实施计划：[docs/plan/platform-runtime-build-implementation-plan.md](docs/plan/platform-runtime-build-implementation-plan.md)
- Agent harness：[AGENTS.md](AGENTS.md)
- 测试标准：[TESTING.md](TESTING.md)
- 任务入口：[Taskfile.yml](Taskfile.yml)
- 普通 CI：[.github/workflows/ci.yml](.github/workflows/ci.yml)
- 镜像与发布 CI：[.github/workflows/images.yml](.github/workflows/images.yml)
- 发布 Dockerfile：[Dockerfile](Dockerfile)
- 本地镜像 Dockerfile：[Dockerfile.agent-compose-local](Dockerfile.agent-compose-local)
- 部署 Compose：[docker-compose.yml](docker-compose.yml)
- Installer：[deploy/install.sh](deploy/install.sh)
- 英文设计文档：[docs/design/agent-compose_design.md](docs/design/agent-compose_design.md)
- 中文设计文档：[docs/zh-CN/design/agent-compose_design.md](docs/zh-CN/design/agent-compose_design.md)
- Playground 设计：[docs/design/playground_setup.md](docs/design/playground_setup.md)
- 核心 E2E 规格：[docs/spec/core-e2e-test-strategy-spec.md](docs/spec/core-e2e-test-strategy-spec.md)

## 执行规则

- 当前 mass-loop 只选择“Platform Runtime Build Progress”下依赖已完成的第一个未完成父任务。
- 每轮只完成一个父任务；父任务测试和验收未通过前不得勾选完成或进入下一父任务。
- 同一父任务内标记“可并行”的子任务可以并行，subagent 并发上限为 5；并行结果由主 agent 统一集成和验证。
- 不跨阶段提前接入依赖未稳定的生产路径；明确标注并行关系的父任务除外。
- 产品支持的 Driver 名称与当前 binary 的 compiled capability 必须分离；不得让本地 compose 解析随平台变化。
- 普通 CGO 不得隐式启用 Microsandbox；BoxLite/Microsandbox 真实实现必须使用显式 Linux、CGO 和 Driver tag。
- macOS binary 只支持 Docker；Linux binary 和 Docker image 支持三 Driver；任何失败不得静默退化为 Docker-only。
- compiled_drivers 只表示编译能力，不探测 Docker daemon、KVM 或 runtime artifact 健康。
- RUNTIME_DRIVER 默认保持 docker；未编译 Driver 必须在持久化或 runtime 启动前返回 unsupported。
- agent-compose version 文本输出保持兼容；新增信息只通过 JSON version 和 HTTP version additive 字段暴露。
- 基础 docker-compose.yml 不得要求 privileged 或 /dev/kvm；KVM 能力只进入 docker-compose.kvm.yml。
- task test 保持 unit/integration/E2E/combined coverage 输出和 60%/60%/60%/70% baseline；不得放宽 baseline、扩大无关 exclusion 或用 skip 处理失败。
- 真实 BoxLite/Microsandbox smoke 保持显式 opt-in，不进入普通 GitHub-hosted PR KVM 门禁。
- 不修改 v1/v2/health protobuf、SQLite schema、guest protocol或默认 Docker driver；若实施发现必须修改，暂停并回到 spec。
- GitHub Release 不增加 per-arch binary；release assets继续是镜像引用和 installer assets。
- 保留用户已有改动；提交、推送和外部发布不属于本账本默认动作。
- 每个父任务完成后写五段式完成总结，并把当前进度和下一目标同步到“当前执行范围”。

## 1. 显式 Driver 编译能力与 Build Constraints

参考：[实施计划阶段 1](docs/plan/platform-runtime-build-implementation-plan.md#阶段-1建立显式-driver-编译能力与-build-constraints)

- [ ] 1.1 建立 compiled driver 能力模型与 typed error
  - 依赖：无。
  - 工作内容：
    - 在 pkg/driver 实现 CompiledRuntimeDrivers、IsRuntimeDriverCompiled、ValidateCompiledRuntimeDriver。
    - 固定顺序为 docker、boxlite、microsandbox，返回副本并保持名称验证与编译能力验证分离。
    - 增加 ErrRuntimeDriverNotCompiled 和包含 Driver、GOOS、GOARCH、compiled drivers 的具体错误，支持 errors.Is。
    - 使用互补 build-constrained 常量文件声明 boxliteCompiled、microsandboxCompiled；Docker始终为 true。
  - 可并行子任务：
    - [ ] 可并行：能力 API、稳定排序和副本实现。
    - [ ] 可并行：typed error、sentinel 和错误信息测试。
    - [ ] 可并行：Docker-only 与 full-tag能力 fixture。
  - 测试方案：
    - CGO_ENABLED=0 ./scripts/with-go-toolchain.sh go test ./pkg/driver -run 'Test.*(Compiled|NotCompiled|RuntimeDriver)' -count=1
    - CGO_ENABLED=0 ./scripts/with-go-toolchain.sh go build ./cmd/agent-compose
    - git diff --check
  - 验收标准：名称合法但未编译可稳定区分；默认/非法名称语义不变；返回列表不可篡改内部状态；普通关闭CGO构建仅报告Docker。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：1.2。

- [ ] 1.2 收紧 BoxLite、Microsandbox 和共享 CGO build constraints
  - 依赖：1.1。
  - 工作内容：
    - BoxLite真实实现、cache/source及测试统一使用 linux && cgo && boxlitecgo，stub/no-source使用完整互补条件。
    - Microsandbox真实实现、cache/source及测试统一使用 linux && cgo && microsandboxcgo，stub/no-source使用完整互补条件。
    - env_path.go、local_docker_oci.go、runtime_mount_manifest_smoke_test.go等共享helper使用 linux && cgo && (boxlitecgo || microsandboxcgo)。
    - 审计所有 pkg/driver Go build constraints，确保普通CGO不再声称Microsandbox能力。
  - 可并行子任务：
    - [ ] 可并行：BoxLite文件和测试constraints。
    - [ ] 可并行：Microsandbox文件和测试constraints。
    - [ ] 可并行：共享CGO helper、runtime cache source与rg审计。
  - 测试方案：
    - CGO_ENABLED=0 ./scripts/with-go-toolchain.sh go test ./pkg/driver -count=1
    - 准备两套artifact后，CGO_ENABLED=1和tags boxlitecgo,microsandboxcgo运行pkg/driver非KVM unit tests。
    - rg -n '^//go:build' pkg/driver --glob '*.go'
  - 验收标准：Docker-only和Linux full两类build均可编译；runtime与cache source能力一致；full-tag unit构造不访问KVM；无宽泛cgo条件残留在Microsandbox实现。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：1.3。

- [ ] 1.3 更新 runtime smoke tags 并完成阶段 1 门禁
  - 依赖：1.2。
  - 工作内容：
    - 更新 Taskfile.yml 中 test:runtime-smoke、test:boxlite-mount-repro 的显式tags。
    - 确保BoxLite和Microsandbox测试各自只编译所需能力，共享/full验证显式传两个tags。
    - 审计 scripts/test-coverage.sh exclusion；新增能力代码纳入普通coverage，不扩大无关排除。
    - 补齐stub/full-tag矩阵回归并修复coverage-shape编译。
  - 可并行子任务：
    - [ ] 可并行：Task runtime smoke命令更新。
    - [ ] 可并行：coverage exclusion和测试shape审计。
    - [ ] 可并行：full-tag focused test复跑。
  - 测试方案：
    - task lint
    - task test
    - 无KVM环境运行full-tag pkg/driver unit tests；有KVM时追加 task test:runtime-smoke。
  - 验收标准：普通task test不下载artifact、不要求Docker/KVM且满足coverage；显式smoke命令编译正确能力；阶段1所有constraints和测试审计完成。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：2.1。

## 2. 提前校验与可观察 Build 信息

参考：[实施计划阶段 2](docs/plan/platform-runtime-build-implementation-plan.md#阶段-2接入提前校验与可观察-build-信息)

- [ ] 2.1 在 Runtime Provider 和执行路径接入 compiled capability
  - 依赖：1.3。
  - 工作内容：
    - NewRuntimeProvider验证默认RUNTIME_DRIVER已编译；ForDriver区分非法名称、未编译和未配置。
    - 保持BoxLite/Microsandbox wrapper lazy，provider构造不得初始化native runtime或KVM。
    - start/resume/exec/remove通过provider对历史session返回unsupported，不修改原driver、VM state或runtime reference。
    - 在adapter/app边界把ErrRuntimeDriverNotCompiled分类为domain.ErrUnsupported和Connect CodeUnimplemented。
  - 可并行子任务：
    - [ ] 可并行：provider实现和constructor tests。
    - [ ] 可并行：历史session runtime操作与不修改状态测试。
    - [ ] 可并行：domain/Connect/CLI错误映射测试。
  - 测试方案：
    - CGO_ENABLED=0 ./scripts/with-go-toolchain.sh go test ./pkg/driver ./pkg/agentcompose/adapters ./pkg/agentcompose/api -run 'Test.*(RuntimeProvider|Compiled|Unsupported|SessionDriver)' -count=1
    - task lint
  - 验收标准：默认未编译driver在service graph构造时失败；历史对象可读取；需要runtime的操作返回typed unsupported；Docker-only启动不触碰KVM。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：2.2与2.3可并行。

- [ ] 2.2 在所有持久化入口提前拒绝未编译 Driver
  - 依赖：2.1。
  - 可并行关系：可与2.3并行，避免同时修改cmd/agent-compose/main.go。
  - 工作内容：
    - session_rpc_bridge在CreateSandbox前验证。
    - agent_definition_handler在create/update/batch写入前验证。
    - projects.Controller在revision/agent reconciliation前验证。
    - loader/scheduler创建sandbox前再次验证。
    - 保持pkg/compose纯normalize跨平台接受三种产品driver。
  - 可并行子任务：
    - [ ] 可并行：session和agent definition入口。
    - [ ] 可并行：project apply/validate入口。
    - [ ] 可并行：loader/scheduler入口和持久化副作用审计。
  - 测试方案：
    - ./scripts/with-go-toolchain.sh go test ./pkg/agentcompose/adapters ./pkg/agentcompose/api ./pkg/projects ./pkg/compose -run 'Test.*(Driver|Sandbox|AgentDefinition|Project|Loader)' -count=1
    - 断言失败前后store内容、revision和session数量不变。
  - 验收标准：macOS/Docker-only daemon不能持久化BoxLite/Microsandbox新配置；纯config解析仍跨平台；所有错误使用unsupported语义且无部分写入。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：2.3（若未完成）或3.1。

- [ ] 2.3 暴露兼容的 CLI 与 HTTP Build 信息
  - 依赖：2.1。
  - 可并行关系：可与2.2并行。
  - 工作内容：
    - 定义version、os、arch、compiled_drivers稳定shape。
    - 保持agent-compose version文本只输出版本；agent-compose --json version输出四字段JSON。
    - GET /api/version additive增加os、arch、compiled_drivers。
    - status文本列保持不变；--json status继续透传新增字段。
    - 不修改任何proto。
  - 可并行子任务：
    - [ ] 可并行：本地version JSON实现和测试。
    - [ ] 可并行：HTTP version/status解析和兼容测试。
    - [ ] 可并行：文本输出快照和proto零差异审计。
  - 测试方案：
    - CGO_ENABLED=0 ./scripts/with-go-toolchain.sh go test ./cmd/agent-compose -run 'Test.*(Version|Status)' -count=1
    - ./scripts/with-go-toolchain.sh go test ./pkg/agentcompose/app ./pkg/health -run 'Test.*(Version|Health)' -count=1
  - 验收标准：文本兼容；JSON/HTTP字段稳定且drivers排序正确；旧客户端可忽略新增字段；proto目录无生成差异。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：3.1（等待2.2）。

## 3. 统一 Binary Build Helper 与 Task 合同

参考：[实施计划阶段 3](docs/plan/platform-runtime-build-implementation-plan.md#阶段-3统一-binary-build-helper-与-task-合同)

- [ ] 3.1 实现唯一 Binary Build Helper 与确定性脚本测试
  - 依赖：2.2、2.3。
  - 工作内容：
    - 新增scripts/build-agent-compose-binary.sh，支持auto、darwin-docker、linux-full及goarch/output/version参数。
    - 固化两个profile的GOOS、CGO、tags、drivers和BuildVersion ldflags。
    - linux-full逐项preflight两套artifact；darwin-docker不访问artifact。
    - 仅BUILD_VERBOSE=1启用-x；提供print-config测试模式。
    - 拒绝未知profile/arch、空output和含换行version；错误不泄露secret。
  - 可并行子任务：
    - [ ] 可并行：参数/profile/build命令实现。
    - [ ] 可并行：artifact preflight与错误诊断。
    - [ ] 可并行：fake Go/toolchain shell测试和bash语法检查。
  - 测试方案：
    - bash -n scripts/build-agent-compose-binary.sh
    - ./scripts/test-build-agent-compose-binary.sh
    - 用helper分别构建darwin/amd64和darwin/arm64。
  - 验收标准：profile参数只有一个owner；shell测试不访问网络；print-config与binary metadata一致；linux-full缺artifact时go build前失败。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：3.2。

- [ ] 3.2 重构 Taskfile 平台分发、Proto任务与兼容 Alias
  - 依赖：3.1。
  - 工作内容：
    - 增加GOHOSTOS/GOHOSTARCH。
    - build:agent-compose按host分发；增加darwin、linux显式任务。
    - Linux任务依赖prepare:boxlite-dev和prepare:microsandbox-dev；Darwin任务无native artifact依赖。
    - 新增build:proto并移除两个binary任务的重复proto build。
    - 顶层build依赖host binary、proto和runtime SDK。
    - build:agent-compose:boxlite保留deprecated alias并指向Linux full。
    - 更新prepare task sources/generates，避免helper或版本变化错误命中cache。
  - 可并行子任务：
    - [ ] 可并行：Task平台分发与alias。
    - [ ] 可并行：build:proto和顶层依赖。
    - [ ] 可并行：prepare cache输入/产物审计。
  - 测试方案：
    - task --list-all
    - task build:proto
    - 当前host运行task build:agent-compose和task build。
    - Linux运行task build:agent-compose:linux；Darwin运行task build:agent-compose:darwin。
  - 验收标准：输出仍为build/agent-compose；Taskfile不拼装CGO/tags/ldflags；Linux默认full、Darwin默认Docker-only；alias提示准确。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：3.3。

- [ ] 3.3 完成平台 Binary Build 阶段门禁
  - 依赖：3.2。
  - 工作内容：
    - 修复Task重构引起的E2E binary路径、coverage-shape和脚本调用回归。
    - 验证Darwin双arch构建信息仅Docker。
    - 验证Linux host full binary信息为三driver，且两套artifact完整。
    - 审计Taskfile、helper和文档示例，不保留第二套profile参数。
  - 可并行子任务：
    - [ ] 可并行：Darwin双arch构建审计。
    - [ ] 可并行：Linux full构建和artifact审计。
    - [ ] 可并行：Task/脚本静态重复参数审计。
  - 测试方案：
    - task lint
    - task build
    - task test
    - ./build/agent-compose --json version
    - rg审计Taskfile中的CGO_ENABLED和boxlitecgo/microsandboxcgo。
  - 验收标准：权威门禁通过；host产物能力正确；重复构建参数清除；不存在静默降级。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：4.1。

## 4. 完整 Docker Image

参考：[实施计划阶段 4](docs/plan/platform-runtime-build-implementation-plan.md#阶段-4让本地与发布-dockerfile-使用同一-linux-full-profile)

- [ ] 4.1 统一两个 Dockerfile 的 Linux Full Build
  - 依赖：3.3。
  - 工作内容：
    - Dockerfile的go-build stage复制统一helper/wrapper及BoxLite、Microsandbox preflight目录。
    - Dockerfile.agent-compose-local从两个本地build context复制对应artifact。
    - 两者用linux-full helper替换内联go build。
    - 保持agent-compose-artifact target、/out/agent-compose、最终runtime路径和RUNTIME_DRIVER=docker。
    - 增加镜像artifact存在性和权限构建断言。
  - 可并行子任务：
    - [ ] 可并行：发布Dockerfile改造。
    - [ ] 可并行：本地Dockerfile改造。
    - [ ] 可并行：artifact target/path兼容审计。
  - 测试方案：
    - docker build --target agent-compose-artifact -f Dockerfile .
    - task image:agent-compose
    - docker run --rm agent-compose:latest --json version
    - 镜像内test检查BoxLite/Microsandbox binaries和libraries。
  - 验收标准：两个Dockerfile无内联profile参数；镜像报告Linux/目标arch/三driver；artifact target和最终路径兼容。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：4.2。

- [ ] 4.2 建立无 KVM 启动与 Docker Sandbox Image Smoke
  - 依赖：4.1。
  - 工作内容：
    - 新增无/dev/kvm的daemon startup和/api/version smoke。
    - 新增容器化daemon挂载Docker socket后通过公开API完成Docker sandbox create/exec/stop/resume/remove。
    - 复用test/e2e公共断言，不复制产品实现。
    - 注册task test:e2e:image-docker，明确guest image和Docker前置检查。
  - 可并行子任务：
    - [ ] 可并行：无KVM daemon/version smoke。
    - [ ] 可并行：Docker sandbox lifecycle E2E。
    - [ ] 可并行：Task入口、清理和泄漏审计。
  - 测试方案：
    - task image:agent-compose
    - task test:e2e:image-docker
    - docker ps/volume/network审计确认无遗留。
    - 有KVM时追加task test:runtime-smoke。
  - 验收标准：full image不映射KVM即可启动和使用Docker；未调用BoxLite/Microsandbox初始化；E2E失败保留诊断且清理资源。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：5.1。

## 5. Compose 与 Installer KVM 部署能力

参考：[实施计划阶段 5](docs/plan/platform-runtime-build-implementation-plan.md#阶段-5拆分基础-compose-与-kvm-部署能力)

- [ ] 5.1 拆分基础 Compose 与 KVM Overlay
  - 依赖：4.2。
  - 工作内容：
    - 从docker-compose.yml删除privileged和/dev/kvm，保留Docker socket/data/env/port。
    - 新增docker-compose.kvm.yml，只增加privileged和/dev/kvm。
    - 保持docker-compose.override.yml只承载本地build行为。
    - 保持playground/docker-compose.yml现有链接/来源，不创建漂移副本。
  - 可并行子任务：
    - [ ] 可并行：基础Compose最小权限改造。
    - [ ] 可并行：KVM overlay与合并配置断言。
    - [ ] 可并行：playground链接和路径审计。
  - 测试方案：
    - docker compose -f docker-compose.yml config
    - docker compose -f docker-compose.yml -f docker-compose.kvm.yml config
    - 基础输出断言无privileged/KVM；合并输出断言存在。
    - 使用基础Compose运行task test:e2e:image-docker。
  - 验收标准：基础Compose跨macOS/Linux Docker-only独立部署；overlay只含增量KVM配置；默认driver仍docker。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：5.2。

- [ ] 5.2 实现 Installer 的 KVM 检测与 Compose 选择持久化
  - 依赖：5.1。
  - 工作内容：
    - installer复制KVM overlay到安装目录。
    - 新安装有KVM时持久化COMPOSE_FILE双文件，无KVM时使用基础Compose并提示仅Docker可用。
    - 已有显式COMPOSE_FILE保持不变；upgrade不因临时KVM状态反复改写。
    - --no-start、pull/up和最终提示都使用持久化文件集合。
    - 抽取或注入KVM检测点，允许测试模拟而不修改真实/dev/kvm。
  - 可并行子任务：
    - [ ] 可并行：install/upgrade选择逻辑。
    - [ ] 可并行：KVM检测注入和fake Docker fixture。
    - [ ] 可并行：--no-start与用户.env保留测试。
  - 测试方案：
    - bash -n deploy/install.sh
    - 临时bundle覆盖有/无KVM、新装/upgrade/显式COMPOSE_FILE。
    - 断言真实用户env中secret和image override不被覆盖。
  - 验收标准：后续普通docker compose命令使用同一文件集合；KVM选择可重复；用户显式配置优先；失败不破坏既有安装。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：5.3。

- [ ] 5.3 接入部署测试、Release Payload 与阶段门禁
  - 依赖：5.2。
  - 工作内容：
    - 新增稳定task test:deploy并纳入task test前置门禁。
    - .github/workflows/images.yml的installer payload复制docker-compose.kvm.yml。
    - .env.example记录RUNTIME_DRIVER=docker、可选COMPOSE_FILE和KVM边界。
    - 验证release tar包含两个Compose文件且仍无binary。
  - 可并行子任务：
    - [ ] 可并行：部署shell测试和Task接入。
    - [ ] 可并行：release payload及tar内容测试。
    - [ ] 可并行：.env.example部署变量审计。
  - 测试方案：
    - task test:deploy
    - task test
    - 本地构造installer tar并列出内容。
    - docker compose两种组合config。
  - 验收标准：部署测试失败阻断task test但不计入Go/JS coverage；installer payload完整；coverage baseline不变；Release范围不变。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：6.1。

## 6. 平台 Binary 与完整 Image CI

参考：[实施计划阶段 6](docs/plan/platform-runtime-build-implementation-plan.md#阶段-6建立平台-binary-与完整-image-ci-矩阵)

- [ ] 6.1 增加 Darwin 与 Linux Binary CI 矩阵
  - 依赖：5.3。
  - 工作内容：
    - CI构建darwin/amd64、darwin/arm64并断言仅Docker。
    - 至少一个macOS runner原生执行--json version和daemon startup/version smoke。
    - CI构建linux/amd64、linux/arm64 full binary并断言三driver和artifact preflight。
    - PR至少覆盖Darwin双arch、一个native macOS、Linux amd64；main/tag覆盖Linux双arch。
    - binary只留job workspace或短期传递artifact，不进入Release。
  - 可并行子任务：
    - [ ] 可并行：Darwin build/native smoke jobs。
    - [ ] 可并行：Linux amd64/arm64 full jobs。
    - [ ] 可并行：matrix触发、cache和artifact retention审计。
  - 测试方案：
    - 本地执行各job核心helper/Task命令。
    - YAML/workflow lint。
    - PR dry run确认matrix、runner、权限和cache key。
  - 验收标准：四种binary target有build证据，至少一个Darwin target有原生执行证据；binary不出现在release job。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：6.2。

- [ ] 6.2 增加完整 Image CI Smoke 并保持发布合同
  - 依赖：6.1。
  - 工作内容：
    - images workflow path filter加入docker-compose.kvm.yml。
    - 保持native amd64/arm64 build和digest merge。
    - 增加image metadata/artifact断言和可加载amd64 Docker smoke job。
    - 无KVM挂载Docker socket运行公开API sandbox lifecycle。
    - main/tag inspect manifest双arch；release只上传installer assets。
  - 可并行子任务：
    - [ ] 可并行：image smoke job。
    - [ ] 可并行：manifest/digest merge非回归。
    - [ ] 可并行：release assets和installer payload审计。
  - 测试方案：
    - task image:agent-compose
    - task test:e2e:image-docker
    - docker buildx imagetools inspect目标镜像。
    - CI PR与main/tag workflow结果审计。
  - 验收标准：PR证明无KVM Docker路径；main/tag发布双arch full image；镜像metadata为三driver；Release无binary。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：7.1。

## 7. 文档、Harness 与最终验收

参考：[实施计划阶段 7](docs/plan/platform-runtime-build-implementation-plan.md#阶段-7同步文档harness-与最终验收)

- [ ] 7.1 同步英文/中文文档与 Harness
  - 依赖：6.2。
  - 工作内容：
    - 更新AGENTS.md、CONTRIBUTING.md、README.md、docs/zh-CN/README.md。
    - 更新deploy/README.md、.env.example及英文/中文agent-compose设计和playground设计。
    - 仅在新增image/deploy smoke需要说明时更新TESTING.md，不改变coverage baseline和KVM opt-in。
    - 删除“普通开发不需要Docker”和“可选BoxLite binary”旧叙述，记录deprecated alias。
  - 可并行子任务：
    - [ ] 可并行：英文harness/README/deploy文档。
    - [ ] 可并行：中文README/设计文档。
    - [ ] 可并行：命令、环境变量和旧术语rg审计。
  - 测试方案：
    - rg审计build:agent-compose:boxlite、Docker is not required、旧内联tags和/dev/kvm叙述。
    - task --list-all与文档命令逐项对照。
    - docker compose两种组合config。
  - 验收标准：文档准确表达三类产物、compiled与available区别、基础/KVM Compose及Release边界；中英文关键合同一致。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：7.2。

- [ ] 7.2 执行全量门禁、实机验证与最终审计
  - 依赖：7.1。
  - 工作内容：
    - 顺序执行lint、build、coverage test、image build和Docker image E2E。
    - 有KVM环境运行runtime smoke；无KVM时记录未运行原因，不伪造通过。
    - 在macOS Docker Desktop验证native Darwin binary和基础Compose full image Docker-only路径。
    - 审计工作区diff、生成物、Release assets、用户改动和残余风险。
    - 更新本文件当前进度、所有完成总结和下一目标。
  - 可并行子任务：
    - [ ] 可并行：静态diff/生成物/Release资产审计。
    - [ ] 可并行：Linux full image和Docker E2E。
    - [ ] 可并行：macOS native/Compose实机验证（独立环境）。
  - 测试方案：
    - task lint
    - task build
    - task test
    - task image:agent-compose
    - task test:e2e:image-docker
    - 有KVM时task test:runtime-smoke。
    - macOS运行task build:agent-compose、--json version、基础docker compose up和image smoke。
  - 验收标准：所有可用权威门禁通过且coverage为60%/60%/60%/70%以上；三类产物能力与spec一致；基础Compose无KVM可用；Release范围不变；无法执行的环境验证明确记录。
  - 完成总结：
    - 状态：待完成。
    - 变更：待完成。
    - 验证：待完成。
    - 审计与例外：待完成。
    - 下一目标：无。
