# agent-compose 平台化 Runtime 构建矩阵实施计划

对应规格：docs/spec/platform-runtime-build-spec.md

## 实施总则

本计划按“能力模型与 build constraints → 提前校验与 build 信息 → 统一 binary 构建入口 → 完整镜像 → Compose/installer → CI → 文档与最终门禁”的依赖顺序实施。每个阶段完成后仓库必须保持可编译、可测试，不允许以临时跳过测试、放宽覆盖率或保留两套构建参数作为过渡状态。

全程遵守以下 harness 约束：

- AGENTS.md 的权威质量门禁是 task lint、task build、task test；最终还必须通过 task image:agent-compose。
- TESTING.md 要求 unit、integration、E2E coverage 分别不低于 60%，combined 不低于 70%；不得修改 scripts/test-coverage.sh 的 baseline 来换取通过。
- task test 的普通覆盖率执行保持 CGO_ENABLED=0。真实 BoxLite/Microsandbox 测试继续走显式 full tags 和 opt-in runtime smoke，不把 KVM 依赖引入普通 unit test。
- docker-compose.yml 必须使用发布镜像独立部署默认 Docker driver；本地 build 行为仍属于 docker-compose.override.yml，KVM 部署能力使用独立 docker-compose.kvm.yml。
- .github/workflows/images.yml 保持原生 amd64/arm64 runner、按 digest 合并 multi-arch manifest 的发布拓扑。
- GitHub Release 继续只发布 installer assets，不新增 macOS/Linux binary 下载项。
- 不修改 v1/v2 protobuf、health protobuf、SQLite schema 或 guest runtime protocol，因此不运行 proto 生成命令；只保留已有 proto package 编译验证。

实施前记录基线：

    git status --short
    task lint
    task test
    task build

如果基线因环境、现有用户改动或外部依赖失败，先保存失败命令和日志，不修改无关代码规避。执行期间保留现有工作区中与本计划无关的 Taskfile.yml、playground/docker-compose.yml 或其他用户改动。

## 阶段 1：建立显式 Driver 编译能力与 build constraints

目标：使 docker、boxlite、microsandbox 的“名称合法性”和“当前 binary 已编译能力”成为两个独立概念；任何 CGO 构建不再隐式启用 Microsandbox。

依赖：无。该阶段先完成，后续 CLI、构建脚本和 CI 都依赖这里提供的稳定能力集合。

实施步骤：

1. 在 pkg/driver 增加编译能力 owner：
   - CompiledRuntimeDrivers() []string 返回防止调用方修改内部状态的副本。
   - IsRuntimeDriverCompiled(string) bool 先规范化 driver 名称，再判断能力。
   - ValidateCompiledRuntimeDriver(string) error 保留名称验证和编译能力验证的区别。
   - 能力顺序固定为 docker、boxlite、microsandbox，不得依赖 map 迭代。
   - 用互补 build-constrained 文件定义 boxliteCompiled 和 microsandboxCompiled，避免环境变量改变结果。
2. 定义 ErrRuntimeDriverNotCompiled sentinel 和携带 driver、runtime.GOOS、runtime.GOARCH、compiled drivers 的具体错误类型；errors.Is(err, ErrRuntimeDriverNotCompiled) 必须成立。错误文本明确这是 build capability，不复用“runtime not configured”或 native library 错误。
3. 保持 ValidateRuntimeDriver、ResolveRuntimeDriver、ResolveSandboxRuntimeDriver 只负责产品支持的名称和默认值，不把本地 agent-compose config 的跨平台 compose 解析变成平台相关行为。
4. 将 BoxLite 真实实现及其专属 cache/source/test 文件统一改为 linux && cgo && boxlitecgo；stub 使用完整互补表达式 !linux || !cgo || !boxlitecgo。至少审计：
   - pkg/driver/boxlite_cgo.go
   - pkg/driver/boxlite_image_materialize_cgo.go
   - pkg/driver/boxlite_cache_gc.go
   - pkg/driver/runtime_cache_sources_boxlite.go
   - 对应 stub、no-source 和测试文件。
5. 将 Microsandbox 真实实现及其专属 cache/source/test 文件统一改为 linux && cgo && microsandboxcgo；stub 使用 !linux || !cgo || !microsandboxcgo。至少审计：
   - pkg/driver/microsandbox_runtime.go
   - pkg/driver/microsandbox_cache.go
   - pkg/driver/runtime_cache_sources_microsandbox.go
   - 对应 stub、no-source 和测试文件。
6. 对 BoxLite 与 Microsandbox 共享的 CGO helper 使用 linux && cgo && (boxlitecgo || microsandboxcgo)，并同步测试 constraints；至少审计 env_path.go、local_docker_oci.go、runtime_mount_manifest_smoke_test.go。docker_interaction_smoke_test.go 当前复用 full-runtime smoke helper，首版使用同一共享 constraint，不做无关 helper 拆分。
7. 更新 Taskfile.yml 中 test:runtime-smoke、test:boxlite-mount-repro 的 tags：
   - BoxLite 测试显式传 boxlitecgo。
   - Microsandbox 测试显式传 microsandboxcgo。
   - 同时编译三 driver 的验证显式传 boxlitecgo,microsandboxcgo。
8. 审计 scripts/test-coverage.sh 的 CGO source exclusion regex。文件名未变化时不扩大 exclusion；新增 build capability 文件属于维护代码，应进入普通 coverage。

测试和验证：

- 为 driver 名称合法但未编译、非法名称、默认 Docker、稳定排序、返回副本增加 unit tests。
- 在关闭 CGO 下验证 stub 组合：

    CGO_ENABLED=0 ./scripts/with-go-toolchain.sh go test ./pkg/driver
    CGO_ENABLED=0 ./scripts/with-go-toolchain.sh go build ./cmd/agent-compose

- 在 Linux 准备 artifact 后验证 full tags 和不需要真实 KVM 的 unit tests：

    ./scripts/export-boxlite-dev-artifact.sh ./build/boxlite
    ./scripts/export-microsandbox-dev-artifact.sh ./build/microsandbox
    LD_LIBRARY_PATH="$PWD/build/boxlite/lib:$PWD/build/microsandbox/lib:$LD_LIBRARY_PATH" \
      CGO_ENABLED=1 ./scripts/with-go-toolchain.sh go test \
      -tags 'boxlitecgo,microsandboxcgo' ./pkg/driver

- 运行阶段门禁：

    task lint
    task test

验收标准：

- 普通 CGO 不再等价于 Microsandbox compiled capability。
- Darwin/CGO-off 组合只报告 Docker；Linux full tags 报告三 driver。
- runtime cache source 与 runtime 实现使用相同能力边界。
- 未编译错误可以用 sentinel 判断，且包含平台和 compiled drivers。
- 普通 task test 不下载 runtime artifact、不要求 Docker/KVM，并保持覆盖率 baseline。

风险和停止条件：

- 如果共享 CGO 文件实际只被一个 driver 使用，按真实依赖收窄 constraint，不能把整个 pkg/driver 放入 full tags。
- 如果 full-tag unit test 在构造阶段访问 KVM，停止并修复 eager initialization；不能无条件 skip，因为完整 image 的 Docker-only 启动依赖 lazy initialization。

## 阶段 2：接入提前校验与可观察 Build 信息

目标：daemon 在持久化或启动 runtime 前拒绝未编译 driver，并通过兼容的 CLI/HTTP 接口报告 target OS、architecture 和 compiled drivers。

依赖：阶段 1 的能力查询和 typed error。

实施步骤：

1. 在 pkg/agentcompose/adapters/runtime_provider.go：
   - NewRuntimeProvider 先验证 config.RuntimeDriver 已编译，保证不支持的默认 driver 在 service graph 构造时失败。
   - ForDriver 先执行名称验证，再执行 compiled capability 验证；未编译时不返回通用“not configured”。
   - 保持三个 wrapper 构造函数 lazy，不在 provider 构造时初始化 BoxLite、Microsandbox 或 KVM。
2. 在会持久化 driver 选择的 daemon 边界增加 compiled validation：
   - pkg/agentcompose/adapters/session_rpc_bridge.go 在 store.CreateSandbox 前验证。
   - pkg/agentcompose/api/agent_definition_handler.go 在 create/update/batch 写入前验证。
   - pkg/projects/controller.go 在 project/revision/agent reconciliation 前验证。
   - loader/scheduler 真正创建 sandbox 前再次验证，覆盖历史数据和内部调用。
3. 不修改 pkg/compose 的纯解析/规范化规则；agent-compose config 仍可在 macOS 读取包含 BoxLite/Microsandbox 的跨平台项目文件。能力拒绝发生在连接 daemon 的 apply/create/run 边界。
4. 把 driver 层 ErrRuntimeDriverNotCompiled 在 API/app 边界分类为 domain.ErrUnsupported，复用 pkg/agentcompose/api/errors.go 到 Connect CodeUnimplemented 的现有映射。CLI 沿用 exitCodeUnsupported，不映射成 invalid argument 或 internal。
5. 对历史 session：
   - list、inspect 不做数据迁移或删除。
   - start/resume/exec/remove 通过 provider 返回 unsupported。
   - 错误不得覆盖 session driver、VM state 或 runtime reference。
6. 在 cmd/agent-compose/main.go 定义稳定 build info shape：version、os、arch、compiled_drivers。OS/arch 使用 Go target runtime 常量，drivers 使用阶段 1 owner。
7. 保持 agent-compose version 文本只输出版本；使全局 --json 对 version 生效并输出稳定 JSON。
8. 在 GET /api/version 的 data 中 additive 增加 os、arch、compiled_drivers；保留 version、timestamp、timezone 和 envelope。
9. 扩展 daemonStatusResponse 解析新增字段，但保持 agent-compose status 文本列不变；--json status 继续透传完整 HTTP body。
10. 不修改任何 proto 或持久化 schema。

测试和验证：

- pkg/driver：compiled validation error、sentinel、stable list。
- pkg/agentcompose/adapters：默认 driver 未编译时 provider 构造失败；Docker-only build 可创建 Docker session；未编译 driver 在 store 写入前失败；历史 session 读取不受影响。
- pkg/projects 与 agent definition handler：未编译 driver 产生 unsupported/validation issue 且无持久化副作用；纯 compose normalize 仍接受三个产品 driver。
- cmd/agent-compose：
  - 原有 version 文本测试保持通过。
  - --json version 精确断言四个字段和稳定 drivers。
  - /api/version 断言 additive 字段。
  - status 文本不增加列，status JSON 包含新增字段。

Focused commands：

    CGO_ENABLED=0 ./scripts/with-go-toolchain.sh go test \
      ./pkg/driver ./pkg/agentcompose/adapters ./pkg/agentcompose/api \
      ./pkg/projects ./cmd/agent-compose
    task lint
    task test

验收标准：

- macOS/Docker-only build 无法把 BoxLite/Microsandbox 新配置写入 daemon 状态。
- 已有不支持 session 仍可读取，但 runtime 操作稳定返回 unsupported。
- CLI unsupported exit code 和 Connect CodeUnimplemented 与现有语义一致。
- version 文本兼容；JSON/HTTP build info 与 compiled constraints 一致。
- 没有 proto 或数据库迁移。

风险和停止条件：

- 如果 compiled validation 被加入 ResolveSandboxRuntimeDriver 导致本地 compose parse 平台相关，停止并退回边界校验方案。
- 如果 provider 构造会加载 native runtime 或检查 KVM，停止并恢复 lazy initialization，先补回归测试。

## 阶段 3：统一 Binary Build Helper 与 Task 合同

目标：建立 darwin-docker、linux-full 两个唯一 profile，让本地 Task 和后续 Dockerfile 只选择 profile，不重复拼装 Go 参数。

依赖：阶段 1 的显式 tags 和阶段 2 的 build metadata。

实施步骤：

1. 新增 scripts/build-agent-compose-binary.sh，参数固定为：
   - --profile auto|darwin-docker|linux-full
   - --goarch amd64|arm64
   - --output PATH
   - --version VALUE；未传时使用与 Taskfile 当前 VERSION 等价的 git fallback。
2. helper 固定 profile：
   - darwin-docker：GOOS=darwin、CGO_ENABLED=0、tags 为 netgo,osusergo。
   - linux-full：GOOS=linux、CGO_ENABLED=1、tags 为 netgo,osusergo,boxlitecgo,microsandboxcgo。
   - auto 通过 go env GOHOSTOS/GOHOSTARCH 选择，只接受 Darwin/Linux host。
3. helper 统一注入 BuildVersion ldflags，创建输出父目录，并通过 scripts/with-go-toolchain.sh go build 调用 Go。Docker stage 也使用同一 wrapper/helper。
4. linux-full 在 go build 前逐项校验 BoxLite/Microsandbox headers、static library、runtime binaries 和 shared libraries；缺失项逐项列出。darwin-docker 不读取这些目录。
5. helper 默认不加 -x，仅 BUILD_VERBOSE=1 时启用。增加 --print-config 或等价内部模式，输出 profile、OS、arch、CGO、tags、drivers，供 tests/CI 断言。
6. helper 拒绝未知 profile、OS、architecture、空输出路径和非法版本换行；错误不得回显代理凭证。
7. 在 Taskfile.yml 增加 GOHOSTOS、GOHOSTARCH 和任务：
   - build:agent-compose：host dispatch，输出 build/agent-compose。
   - build:agent-compose:darwin：darwin-docker，不依赖 native artifact。
   - build:agent-compose:linux：依赖 prepare:boxlite-dev、prepare:microsandbox-dev，调用 linux-full。
   - build:proto：承接两个 binary task 末尾重复的 v2 proto package build。
8. 顶层 build 依赖 build:agent-compose、build:proto、build:runtime-sdk，保持 build/agent-compose 兼容 README 和 E2E。
9. build:agent-compose:boxlite 改为 deprecated alias，打印迁移提示并执行 Linux full task；不再生成“只含 BoxLite”产物。
10. 保留两个 prepare task 为 artifact owner，并将 helper、Dockerfile或版本来源加入正确的 Task sources，避免版本变化时错误命中 cache。
11. 增加 shell focused test，使用 fake Go/toolchain 覆盖 profile解析、host dispatch、preflight失败、verbose和 print-config，不访问网络。

测试和验证：

    bash -n scripts/build-agent-compose-binary.sh
    ./scripts/test-build-agent-compose-binary.sh
    ./scripts/build-agent-compose-binary.sh \
      --profile darwin-docker --goarch amd64 \
      --output ./build/agent-compose-darwin-amd64
    ./scripts/build-agent-compose-binary.sh \
      --profile darwin-docker --goarch arm64 \
      --output ./build/agent-compose-darwin-arm64

Linux host 追加：

    task build:agent-compose:linux
    ./build/agent-compose --json version
    task build:proto

阶段门禁：

    task lint
    task build
    task test

验收标准：

- Taskfile 不再直接写 profile 的 CGO/tags/ldflags。
- Darwin host 默认输出 Docker-only Mach-O；Linux host 默认输出 full binary。
- Linux full 缺少任一 runtime artifact 时在 go build 前失败。
- proto 编译验证只存在于 build:proto。
- 旧 BoxLite task 有迁移提示且行为等价 Linux full。
- task build 保持稳定公共入口。

风险和停止条件：

- 如果 helper 为 Docker 复制第二套 profile 常量，停止并改为同一脚本入口。
- 如果 Linux full 在 macOS 上走宿主 CGO 交叉编译，停止；跨平台 full 必须交给 Docker 或 native Linux runner。
- 如果 task build 因网络失败，保留 artifact/download 诊断，不静默退化 Docker-only。

## 阶段 4：让本地与发布 Dockerfile 使用同一 Linux Full Profile

目标：镜像 binary 与 Linux host binary 共享 helper/profile，最终镜像包含三 driver 及其 runtime artifact。

依赖：阶段 3 的 helper 和 Linux full preflight。

实施步骤：

1. 修改 Dockerfile 的 go-build stage：
   - 复制 build helper 和 with-go-toolchain wrapper。
   - 除 BoxLite /out 外，将 microsandbox-fetch /out 复制到 build/microsandbox，使 full preflight 与本地一致。
   - 用 linux-full helper替换内联 go build。
2. 修改 Dockerfile.agent-compose-local：
   - 同时把 boxlite-local、microsandbox-local 复制到 helper预期目录。
   - 调用相同 linux-full helper。
3. 保持 agent-compose-artifact target 和 /out/agent-compose 路径兼容现有 export/CI。
4. 保持最终镜像 RUNTIME_DRIVER=docker、runtime路径和 LD_LIBRARY_PATH；不得将 compiled drivers 等同默认 driver。
5. 增加镜像 artifact 构建断言，校验 BoxLite runtime、msb、agentd、libmicrosandbox_go_ffi.so、libkrunfw.so 存在且权限正确。
6. 增加无 KVM startup smoke：不映射 /dev/kvm，执行 --json version，启动 daemon并等待 /api/version。
7. 在可连接 Docker socket环境增加容器化 daemon Docker sandbox lifecycle smoke，复用 test/e2e 公共 API 断言，注册 task test:e2e:image-docker。

测试和验证：

    task image:agent-compose
    docker run --rm agent-compose:latest --json version
    docker run --rm --entrypoint sh agent-compose:latest -c \
      'test -x /app/agent-compose && test -x /app/boxlite/runtime/boxlite-guest && test -x /app/microsandbox/bin/msb && test -s /app/microsandbox/lib/libmicrosandbox_go_ffi.so'
    task test:e2e:image-docker

具备 KVM 时追加：

    task test:runtime-smoke

阶段门禁：

    task lint
    task build
    task test
    task image:agent-compose

验收标准：

- 两个 Dockerfile 不再包含内联 CGO/tags/BuildVersion 组装。
- 镜像 metadata 报告 Linux、目标 arch 和三 driver。
- 镜像不提供 KVM 时可启动 Docker模式。
- 两个 Dockerfile 均通过同一 helper 的 full artifact preflight。
- agent-compose-artifact target 和最终路径兼容。

风险和停止条件：

- binary 在 main 前因 shared library 加载失败时，停止并检查链接方式、复制路径和 LD_LIBRARY_PATH，不能伪装成 driver unavailable。
- Docker-only smoke触发 native runtime初始化时，停止并修复 lazy边界。

## 阶段 5：拆分基础 Compose 与 KVM 部署能力

目标：full image 在 macOS/无 KVM 环境通过基础 Compose 使用 Docker driver，在 Linux KVM 环境通过 overlay 启用三 driver。

依赖：阶段 4 的无 KVM 可启动 full image。

实施步骤：

1. 从根 docker-compose.yml 删除 privileged 和 /dev/kvm；保留发布镜像、Docker socket、data、只读 .env、network和端口。
2. 新增 docker-compose.kvm.yml，只为 agent-compose 增加 privileged 和 /dev/kvm，不复制其他基础配置。
3. 保持 docker-compose.override.yml 只用于本地 build行为，不把 KVM overlay 实现为本地 override。
4. 更新 deploy/install.sh：
   - bundle存在KVM overlay时复制到安装目录。
   - 新安装检测 /dev/kvm：存在时持久化 COMPOSE_FILE=docker-compose.yml:docker-compose.kvm.yml；不存在时只用基础文件并提示仅Docker可用。
   - 已有安装显式设置 COMPOSE_FILE 时保留；未设置时首次补全，--upgrade 不因临时 KVM状态反复改写。
   - --no-start、pull/up和最终提示使用同一持久化文件集合。
5. 更新 .github/workflows/images.yml installer payload，复制 docker-compose.kvm.yml；Release仍不加入binary。
6. 更新 .env.example：RUNTIME_DRIVER=docker不变；COMPOSE_FILE作为部署选择说明，不作为应用默认。
7. 保持 playground/docker-compose.yml 的现有链接/来源关系，不创建漂移副本。
8. 增加确定性部署测试：
   - 基础 compose config 不含 privileged/KVM。
   - overlay 合并后包含 privileged/KVM。
   - 临时 bundle、fake docker和 --no-start --yes 覆盖有/无KVM、新装/升级/显式COMPOSE_FILE。
   - 通过可注入检测路径模拟 KVM，不修改真实 /dev/kvm。
9. 注册 task test:deploy，并纳入 task test 的前置命令；shell测试不计入Go/JS coverage，但失败必须阻断。

测试和验证：

    bash -n deploy/install.sh
    docker compose -f docker-compose.yml config
    docker compose -f docker-compose.yml -f docker-compose.kvm.yml config
    task test:deploy
    task test

验收标准：

- docker-compose.yml 单独在无KVM环境可解析并启动Docker模式。
- KVM overlay只包含增量能力。
- installer新装和升级后普通 docker compose命令使用持久化的文件集合。
- installer tar包含基础Compose和KVM overlay。
- 用户已有 .env 和显式Compose选择不被升级覆盖。

风险和停止条件：

- COMPOSE_FILE 无法由目标 Compose 版本从 .env 稳定读取时，停止并选择可持久化且有测试证明的配置；不能只在首次 up 临时传 -f。
- 基础Compose移除 privileged 后 Docker driver失败时，修复最小权限路径，不把 privileged放回基础配置。

## 阶段 6：建立平台 Binary 与完整 Image CI 矩阵

目标：CI分别证明 Darwin Docker-only binary、Linux full binary和multi-arch full image合同，且不改变Release资产范围。

依赖：阶段 3 至 5 的稳定 Task、镜像和部署测试入口。

实施步骤：

1. 在 .github/workflows/ci.yml 增加 Darwin binary矩阵：
   - 构建 darwin/amd64、darwin/arm64。
   - 断言 compiled drivers仅Docker。
   - 至少一个 macOS runner原生执行 --json version和不连接runtime的daemon startup/version smoke。
2. 增加 Linux full binary矩阵：
   - linux/amd64 使用 ubuntu-latest。
   - linux/arm64 使用与image workflow一致的 ubuntu-24.04-arm。
   - 通过native Task或Docker agent-compose-artifact target构建，执行artifact preflight和metadata断言。
3. PR至少运行Darwin双arch编译、一个native macOS smoke和Linux amd64 full build；main/tag运行完整amd64/arm64 Linux矩阵。
4. binary只保留在job workspace；不得加入release job、installer manifest或Release上传。临时 workflow artifact仅用于job传递并设置短retention。
5. 在 .github/workflows/images.yml：
   - 将 docker-compose.kvm.yml 加入path filter和installer payload。
   - 保持native architecture build和digest merge。
   - 增加daemon image metadata/artifact检查。
6. 增加独立amd64 image Docker smoke job或为单平台daemon build提供可加载输出：
   - 无 /dev/kvm 启动 full image。
   - 挂载 Docker socket。
   - 通过公开API执行Docker sandbox lifecycle。
   - 不在普通GitHub-hosted runner运行真实KVM smoke。
7. 为macOS Docker Desktop提供手动/本地稳定验证命令，复用基础Compose和同一image smoke。GitHub macOS runner无Docker时只运行native binary smoke。
8. 使用可用的workflow/YAML lint工具验证；仓库没有actionlint时至少解析YAML，并以PR dry run确认matrix表达式和权限。

测试和验证：

    task lint
    task build
    task test
    task image:agent-compose
    task test:e2e:image-docker
    docker buildx imagetools inspect <image>:<tag>

验收标准：

- CI对四种binary target有build结果，对至少一个Darwin target有原生执行结果。
- Linux full和image metadata均报告三driver。
- PR image smoke证明无KVM Docker-only路径。
- main/tag继续发布双archmanifest。
- release job没有binary上传。

风险和停止条件：

- CI若使用QEMU运行native runtime构建导致不稳定，停止并复用现有native runner矩阵。
- image smoke只能通过privileged/KVM启动时，视为阶段4/5回归，不能在CI加权限绕过。

## 阶段 7：同步文档、Harness 与最终验收

目标：把新构建合同写入当前文档和agent harness，移除旧的“不需要Docker”和“BoxLite可选binary”叙述，完成全量门禁。

依赖：阶段 1 至 6 完成，命令和 CI 名称稳定。

实施步骤：

1. 更新 AGENTS.md：平台矩阵、Docker标准依赖、基础Compose/KVM overlay、新Task和权威命令。
2. 更新 CONTRIBUTING.md：删除普通开发循环不需要Docker的承诺；明确unit仍不调用Docker、Linux full build需要Docker、KVM只用于真实runtime。
3. 更新 README.md、docs/zh-CN/README.md：task build的host行为、macOS仅Docker、Linux三driver、deprecated alias、full image在macOS的边界。
4. 更新 deploy/README.md、.env.example：基础Compose/KVM overlay、installer自动选择、手动命令、KVM缺失语义。
5. 更新 docs/design/agent-compose_design.md、中文对应文档和 docs/design/playground_setup.md：compiled capability、lazy runtime、build profile、Compose topology。
6. 仅在新增稳定image/deploy smoke入口需要说明时更新 TESTING.md；不改变coverage baseline和KVM E2E opt-in原则。
7. 搜索并清理过时文本和构建命令：

    rg -n 'build:agent-compose:boxlite|without optional BoxLite|Docker is not required|CGO_ENABLED=1.*boxlitecgo|/dev/kvm' \
      AGENTS.md CONTRIBUTING.md README.md docs Taskfile.yml Dockerfile Dockerfile.agent-compose-local deploy .github scripts

8. 对新脚本执行 bash -n，对两个Compose组合执行 docker compose config，确认文档命令与Task列表一致。

最终验证：

    task lint
    task build
    task test
    task image:agent-compose
    task test:e2e:image-docker

具备KVM时追加：

    task test:runtime-smoke

macOS Docker Desktop实机验证：

    task build:agent-compose
    ./build/agent-compose --json version
    docker compose -f docker-compose.yml config
    docker compose up -d agent-compose
    task test:e2e:image-docker

验收标准：

- 所有权威门禁通过，coverage仍满足60%/60%/60%/70%。
- 文档不把Linux full binary描述为macOS原生可执行，不把compiled capability描述为runtime health，不把bare Linux binary描述为独立发行bundle。
- Task、Dockerfile、CI、Compose、installer和文档使用同一profile/driver术语。
- 三类产物能力由 --json version观察且与矩阵一致。
- 基础Compose默认Docker模式独立部署，KVM overlay选择性启用。
- GitHub Release资产范围不变。

风险和停止条件：

- 最终门禁任一失败都阻止完成，不得只报告focused tests。
- registry或runtime artifact下载失败时记录外部依赖阻塞，不修改版本校验或关闭preflight。
- macOS Docker Desktop验证不可获得时，Linux CI smoke不能替代实机验收；交付记录必须明确待补验证。

## 首版不做的事项

- 不实现macOS原生BoxLite或Microsandbox。
- 不实现Windows binary/profile。
- 不开放任意build tags组合或用户自定义profile。
- 不支持macOS宿主工具链直接交叉构建Linux full CGO binary。
- 不承诺macOS Docker Desktop运行BoxLite/Microsandbox或nested virtualization。
- 不发布macOS/Linux binary到GitHub Release。
- 不定义Linux full binary tar bundle、安装布局或单文件可移植合同。
- 不把真实KVM E2E加入普通PR阻塞CI。
- 不修改v1/v2/health protobuf、SQLite schema、guest protocol或默认Docker driver。
- 不实现available_drivers运行时健康探测；compiled_drivers只表示编译能力。
- 不在首版删除deprecated build:agent-compose:boxlite alias。
- 不重构与构建矩阵无关的runtime、测试shape、Docker网络、项目模型或installer交互流程。

