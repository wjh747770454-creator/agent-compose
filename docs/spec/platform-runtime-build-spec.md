# agent-compose 平台化 Runtime 构建矩阵技术规格

## 背景与目标

agent-compose 同时支持 `docker`、`boxlite`、`microsandbox` 三种 runtime driver，但三者的平台条件不同：Docker driver 可以由 macOS 原生进程连接 Docker Desktop 或远程 Docker daemon；BoxLite 与 Microsandbox 的真实实现依赖 Linux、CGO、平台原生产物，并在实际运行时依赖 KVM。仓库当前用 `build:agent-compose` 构建关闭 CGO 的通用二进制，用 `build:agent-compose:boxlite` 构建启用 BoxLite 的 Linux 二进制，而正式 daemon 镜像又在 Dockerfile 中维护第三套构建命令。三个入口的命名、能力和参数没有形成统一合同。

当前行为还存在以下问题：

- `build:agent-compose` 的名称没有说明产物缺少 BoxLite，并因 `CGO_ENABLED=0` 同时使用 Microsandbox stub。
- `build:agent-compose:boxlite` 开启 `CGO_ENABLED=1` 后会顺带编入当前由通用 `cgo` build constraint 控制的 Microsandbox 实现，任务名称与实际能力不一致。
- `Taskfile.yml`、`Dockerfile`、`Dockerfile.agent-compose-local` 分别维护 CGO、build tags、版本 ldflags 和输出参数，存在发布镜像与本地二进制能力漂移风险。
- macOS、Linux 原生二进制和 Linux 容器镜像缺少明确的产物矩阵与可观察能力声明。
- `docker-compose.yml` 无条件请求 `/dev/kvm` 和 privileged 模式，使只需要 Docker driver 的 macOS Docker Desktop 部署也可能在容器启动前失败。
- `.github/workflows/ci.yml` 不构建平台二进制；`.github/workflows/images.yml` 构建完整镜像，但没有验证镜像内编译能力声明和 Docker-only 运行路径。

本规格定义以下目标状态：

- macOS 原生二进制支持 Docker driver，不编译 BoxLite 或 Microsandbox 真实实现。
- Linux 原生二进制支持 Docker、BoxLite、Microsandbox 三种 driver。
- 发布的 Linux Docker image 支持三种 driver，并继续提供 `linux/amd64`、`linux/arm64` multi-arch manifest。
- 完整 Linux image 可以在 macOS Docker Desktop 中以 Docker driver 启动和工作；缺少 KVM 只影响 BoxLite 与 Microsandbox。
- 本地 Task、CI binary build 和 Docker image build 共享一份构建 profile 定义，不再独立维护关键 Go 构建参数。
- daemon 和 CLI 可以报告编译进当前产物的 driver，并在状态写入或 runtime 启动前拒绝未编译的 driver。
- 平台 binary 只用于本地开发和 CI 构建验证，首版不作为 GitHub Release 下载产物发布；正式发布载体仍是 multi-arch Docker image 和现有 installer bundle。

## 现状和 harness 约束

### 项目 harness

- `AGENTS.md` 规定支持 `docker`、`boxlite`、`microsandbox` 三种 runtime driver，默认 driver 是 `docker`；构建矩阵不得改变默认 driver。
- `AGENTS.md` 规定 `task lint`、`task build`、`task test` 是主质量门禁，并要求 `docker-compose.yml` 使用已发布镜像即可独立部署。平台化构建后这些命令仍是稳定入口，基础 Compose 在不附加 KVM 配置时必须能够以 Docker driver 部署。
- `AGENTS.md` 规定本地构建行为属于 `docker-compose.override.yml`，远程部署只应依赖发布文件和用户创建的 `.env`。KVM 配置是部署能力而非本地 build override，因此使用独立、随 installer 发布的 KVM Compose overlay，不把它混入本地开发 override。
- `TESTING.md` 要求 unit、integration、E2E 三种测试形态，并要求 `task test` 输出各形态及 combined coverage；最低门禁为 unit、integration、E2E 各 60%，combined 70%。本规格不得通过平台构建调整绕过这些门禁。
- `TESTING.md` 将真实 Docker Jupyter lifecycle E2E 定义为 opt-in；`docs/spec/core-e2e-test-strategy-spec.md` 进一步规定三 driver 真实 E2E 不进入普通 pull request 阻塞门禁。平台构建 CI 证明产物组成和 Docker 路径，KVM driver 的真实 E2E 继续由具备条件的显式环境负责。
- `Taskfile.yml` 当前用 `prepare:boxlite-dev` 和 `prepare:microsandbox-dev` 从 Dockerfile 导出开发产物；Linux full binary 必须复用这些 owner，不建立第二套下载和版本解析逻辑。
- `Taskfile.yml` 的 `task all` 已包括 lint、test、build、guest image 和 daemon image。Docker 作为标准开发依赖后，轻量 lint/unit 命令仍无需为了形式而启动 Docker；需要原生产物或镜像的任务必须执行明确的 Docker 前置检查。
- `.github/workflows/images.yml` 当前在原生 amd64/arm64 runner 上分别构建镜像并按 digest 合并 manifest，避免 QEMU 执行原生依赖构建。该发布拓扑继续保留。
- `.github/workflows/images.yml` 当前 GitHub Release 只发布 `install.sh`、`agent-compose-installer.tar.gz` 和校验和，并明确不发布 per-arch binary。本规格保持该发布合同。

### 当前实现事实

- `pkg/driver/boxlite_cgo.go` 使用 `//go:build boxlitecgo`，通过 `build/boxlite/include/boxlite.h` 和 `build/boxlite/lib/libboxlite.a` 编译 BoxLite 真实实现。
- `pkg/driver/boxlite_stub.go` 使用 `//go:build !boxlitecgo`，直到调用 runtime 操作时才返回未编译 BoxLite 的错误。
- `pkg/driver/microsandbox_runtime.go` 使用 `//go:build cgo`；`pkg/driver/microsandbox_runtime_stub.go` 使用 `//go:build !cgo`。因此当前任何 CGO 构建都隐式声称具备 Microsandbox 实现。
- `pkg/agentcompose/adapters/runtime_provider.go` 会构造并注册三个 runtime。BoxLite 和 Microsandbox 构造函数只创建包装对象，真实 native runtime 初始化发生在相应 driver 首次执行时；完整 image 在 `RUNTIME_DRIVER=docker` 下不应因缺少 KVM 而启动失败。
- `pkg/driver/runtime_driver.go` 当前只验证 driver 名称是否属于三个已知值，不区分“产品识别该 driver”和“当前 binary 编译了该 driver”。
- `cmd/agent-compose/main.go` 的 `agent-compose version` 只输出 `config.BuildVersion`；`GET /api/version` 返回 version、timestamp 和 timezone，尚未暴露 OS、architecture 或 compiled drivers。
- `Dockerfile` 和 `Dockerfile.agent-compose-local` 都以 `CGO_ENABLED=1`、`GOOS=linux`、`boxlitecgo` 构建 daemon；Microsandbox 因 `cgo` constraint 被隐式编入。两个镜像都复制 BoxLite 与 Microsandbox runtime artifact，并默认 `RUNTIME_DRIVER=docker`。
- Linux full binary 的完整运行能力不仅由 ELF 文件决定，还依赖 BoxLite runtime、Microsandbox binaries 和 shared libraries。现有本地开发产物位于 `build/boxlite` 与 `build/microsandbox`。
- `docker-compose.yml` 当前无条件设置 `privileged: true`、映射 `/dev/kvm`，同时映射 Docker socket。`deploy/README.md` 却声明只有 BoxLite/Microsandbox 需要 `/dev/kvm`，Docker driver 不需要，二者需要对齐。

### 约束结论

- “支持某 driver”表示当前产物包含该 driver 的真实实现及其已定义的运行时依赖合同，不以 stub 能接受名称作为支持。
- macOS binary 和 Linux full binary 是不同目标产物；Linux ELF 不提供 macOS 原生兼容性。
- Docker image 始终是 Linux 产物。它在 macOS 上由 Docker Desktop 的 Linux VM 执行，因此能够复用同一份完整镜像，但只能使用部署环境实际提供的 runtime 能力。
- Docker 是标准开发前置依赖；这不改变 unit test 必须可隔离、确定且不依赖外部服务的 `TESTING.md` 要求。
- 首版不定义 Linux binary 的独立分发 bundle；不能把 CI 构建成功表述为一个脱离 runtime artifact 即可获得三 driver 完整能力的单文件发行版。

## 核心概念

### 构建 Profile

构建 profile 是 OS、architecture、CGO 状态、build tags、compiled drivers 和原生产物前置条件的不可分割定义。profile 是构建合同的唯一 owner，Task 和 Dockerfile 只能选择 profile、版本、architecture 与输出路径，不能重新拼装 tags。

首版只有两个 profile：

| Profile | 目标平台 | CGO | Build tags | Compiled drivers |
| --- | --- | --- | --- | --- |
| `darwin-docker` | `darwin/amd64`、`darwin/arm64` | `0` | `netgo,osusergo` | `docker` |
| `linux-full` | `linux/amd64`、`linux/arm64` | `1` | `netgo,osusergo,boxlitecgo,microsandboxcgo` | `docker,boxlite,microsandbox` |

Profile 不由用户通过任意 tags 组合动态生成。新增 driver 或新的平台组合必须先扩展 profile 和测试矩阵。

### 编译能力与运行可用性

编译能力（compiled capability）表示真实 driver 实现存在于 binary。运行可用性（runtime availability）还取决于环境：

- Docker 需要可连接的 Docker daemon；容器化 daemon 通常通过 `/var/run/docker.sock` 连接。
- BoxLite 需要 Linux、BoxLite runtime artifact 和 KVM。
- Microsandbox 需要 Linux、`msb`、`agentd`、shared libraries 和 KVM。

`compiled_drivers` 只报告编译能力，不探测 Docker daemon、KVM、文件权限或 runtime 健康状态。运行环境问题继续由对应 driver 返回带上下文的诊断错误。

### Host Build 与 Target Build

Host build 是 `task build:agent-compose` 针对当前开发宿主机生成 `build/agent-compose` 的工作流。默认选择依据是 Go 的 host OS，而不是允许任意 CGO 交叉编译的 `GOOS`：

- Darwin host 选择 `darwin-docker`。
- Linux host 选择 `linux-full`。
- 其他 host 首版返回明确的不支持错误。

Target build 是 CI 或 Docker Buildx 为显式 OS/architecture 生成产物的工作流。Darwin profile因关闭 CGO可以交叉编译，但仍需至少一个 macOS runner执行原生启动验证；Linux full profile使用对应 architecture 的原生 Linux runner或 Docker build stage，不要求开发者在 macOS 配置 Linux CGO 交叉工具链。

### Full Binary 与 Full Image

Linux full binary 是编译进三 driver 实现的开发/验证二进制。它在本地运行时使用 `build/boxlite` 和 `build/microsandbox` 下的配套产物。

Linux full image 是可发布的完整运行载体，包含 full binary、BoxLite runtime 和 Microsandbox runtime。正式发布、installer 和跨宿主部署以 full image 为准。

## 架构和组件边界

### 平台能力边界

`pkg/driver` 负责声明编译能力并提供稳定查询函数。能力集合由 build-constrained 源文件产生：

- Docker 始终存在。
- BoxLite 只有在 `linux && cgo && boxlitecgo` 下存在。
- Microsandbox 只有在 `linux && cgo && microsandboxcgo` 下存在。

对应 stub 的 constraints 必须覆盖真实实现之外的所有组合，保证 Darwin、关闭 CGO、缺少显式 tag 时都能编译。与 driver 相关的 runtime cache source 使用相同能力 constraints，不得继续用泛化 `cgo` 推断 Microsandbox 能力。

编译能力列表使用稳定的小写 driver 名称，去重并按固定顺序输出：`docker`、`boxlite`、`microsandbox`。

### Runtime Provider 边界

`pkg/agentcompose/adapters/runtime_provider.go` 继续作为 driver 到 runtime adapter 的选择边界，但只把当前 binary 编译支持的 driver 视为可用。产品仍识别三种 driver 名称，以便配置、schema 和跨平台项目文件保持兼容；当前 binary 未编译的 driver 必须返回 typed/可分类的 unsupported 错误，而不是“未配置”或 native library 失败。

以下入口必须在产生不可逆或持久化副作用前检查编译能力：

- daemon 启动时的 `RUNTIME_DRIVER` 默认值验证。
- API/CLI 创建或应用显式指定 driver 的 sandbox/agent 配置。
- 从已有 session 恢复 runtime 时的 driver 选择。

已有数据引用当前 binary 未编译的 driver 时不得被重写、删除或迁移；相关 start/resume/exec 操作返回 unsupported，读取和检查历史数据仍可用。

### 构建脚本边界

统一 binary build helper 是 profile 到 Go 命令的唯一转换层，负责：

- 校验 profile、target OS/architecture 与输出路径。
- 设置 `CGO_ENABLED`、`GOOS`、`GOARCH` 和完整 build tags。
- 生成一致的 `BuildVersion` ldflags。
- 在 `linux-full` 下校验 BoxLite headers/static library/runtime 与 Microsandbox binaries/shared libraries。
- 使用仓库 `scripts/with-go-toolchain.sh` 约束本地 Go toolchain；在 Docker build stage 中使用镜像内已验证工具链。
- 默认输出简洁构建日志，仅在显式 debug 开关下启用 `go build -x`。

Taskfile、`Dockerfile` 和 `Dockerfile.agent-compose-local` 不再直接维护 profile 内部参数。Dockerfile仍分别拥有发布 artifact 下载和本地 artifact build context 的差异。

Proto 包的独立编译验证属于 `build:proto`，不重复附着在每个 agent-compose binary profile 后。

### Task 编排边界

对外任务合同如下：

| Task | 合同 |
| --- | --- |
| `task build:agent-compose` | 根据当前 host OS 选择 Darwin Docker-only 或 Linux full profile，输出 `build/agent-compose` |
| `task build:agent-compose:darwin` | 显式构建 Darwin Docker-only binary；支持明确的 amd64/arm64 target |
| `task build:agent-compose:linux` | 显式构建 Linux full binary，依赖 BoxLite 和 Microsandbox 开发产物 |
| `task build:proto` | 独立验证需要维护的 protobuf Go packages 可编译 |
| `task build` | 保持项目主构建入口，至少包括 host binary、proto 验证和 runtime SDK build |
| `task image:agent-compose` | 构建包含三 driver 的本地 Linux daemon image |
| `task all` | 保持 lint、test、build、guest image、daemon image 的完整开发门禁 |

`build:agent-compose:boxlite` 在首版保留为 deprecated alias，指向 `build:agent-compose:linux` 并打印迁移提示。该别名不再表示只包含 BoxLite，也不在新文档中作为推荐入口；后续删除不属于首版。

`prepare:boxlite-dev` 与 `prepare:microsandbox-dev` 继续是原生产物 owner。Linux full build 对二者都有依赖，不能只准备 BoxLite 后声称 full。

### Docker Image 边界

`Dockerfile` 是发布镜像 owner，继续获取固定版本的 BoxLite/Microsandbox artifact，生成 `linux-full` binary，并把运行依赖复制到最终镜像。`Dockerfile.agent-compose-local` 复用本地导出的 artifact，但调用相同 profile。

最终镜像保持：

- 默认 `RUNTIME_DRIVER=docker`。
- 同时包含三种 compiled driver。
- 包含 `linux/amd64` 与 `linux/arm64` 变体。
- 缺少 `/dev/kvm` 时仍能启动 daemon 并使用 Docker driver。

### Compose 与部署边界

基础 `docker-compose.yml` 是跨 Linux/macOS 的 Docker-only 最小部署合同：

- 使用发布的 daemon image。
- 映射 Docker socket、data 和只读 `.env`。
- 不无条件设置 privileged，也不无条件映射 `/dev/kvm`。
- 单独使用该文件即可按 `AGENTS.md` 约束完成默认 Docker driver部署。

独立的部署 overlay `docker-compose.kvm.yml` 增加 BoxLite/Microsandbox 所需的 privileged 与 `/dev/kvm` 映射。它随 installer payload 发布，但不是本地 build override。

安装脚本在宿主存在 `/dev/kvm` 时选择 KVM overlay，并把该选择持久化到安装目录的 Compose 调用配置，使后续普通 `docker compose up/down/logs` 使用相同文件集合；没有 KVM 时只使用基础 Compose，并明确提示只有 Docker driver可用。手动部署文档同时给出基础模式和 KVM 模式命令。

镜像能力与 Compose 环境能力分离：同一个 full image 在 macOS Docker Desktop 上报告三个 compiled drivers，但 BoxLite/Microsandbox 因缺少 KVM 不可运行；Docker driver必须可用。

## API、CLI、配置和数据模型

### Build 信息模型

构建时信息至少包含：

| 字段 | 类型 | 语义 |
| --- | --- | --- |
| `version` | string | 现有 `BuildVersion` |
| `os` | string | Go target OS，例如 `darwin`、`linux` |
| `arch` | string | Go target architecture，例如 `amd64`、`arm64` |
| `compiled_drivers` | string[] | 当前 binary 编译的真实 driver，稳定排序 |

OS 和 architecture 以 Go build target 为准，不使用运行时可修改环境变量。`compiled_drivers` 由 build constraints 产生，不接受环境变量覆盖。

### CLI

现有文本命令保持兼容：

```bash
agent-compose version
```

仍只输出版本字符串，避免破坏脚本。

全局 `--json` 对 version 命令生效：

```bash
agent-compose --json version
```

输出稳定 JSON：

```json
{
  "version": "v1.2.3",
  "os": "darwin",
  "arch": "arm64",
  "compiled_drivers": ["docker"]
}
```

Linux full binary 对应 `compiled_drivers` 为 `docker`、`boxlite`、`microsandbox`。

`agent-compose status` 的现有文本列保持不变；`agent-compose --json status` 因透传 `/api/version` 响应而获得新增 build 信息。

### HTTP API

`GET /api/version` 的 `data` 增加以下 additive 字段：

```json
{
  "version": "v1.2.3",
  "os": "linux",
  "arch": "amd64",
  "compiled_drivers": ["docker", "boxlite", "microsandbox"],
  "timestamp": 0,
  "timezone": "UTC",
  "timezone_offset": 0
}
```

现有字段和 envelope 不变。该变化不要求修改 v1/v2 protobuf 或 health protobuf；旧客户端忽略新增 JSON 字段，现有 status 文本保持兼容。

### 配置

不新增 daemon 环境变量。`RUNTIME_DRIVER` 仍默认 `docker`，但启动配置必须同时满足：

- 名称是产品识别的 driver。
- driver 位于当前 binary 的 `compiled_drivers`。

macOS binary 设置 `RUNTIME_DRIVER=boxlite` 或 `microsandbox` 时启动失败并返回明确错误；不得静默回退 Docker。Linux full binary接受三种值。

`.env.example` 继续把 `RUNTIME_DRIVER=docker` 作为安全跨平台默认值，并说明 KVM overlay 与非 Docker driver的部署条件。

### 持久化与协议

本规格不修改：

- SQLite schema。
- sandbox、VM state、proxy state 或 cache 文件布局。
- `agent-compose.yml` driver one-of schema。
- v1/v2 Connect wire contract。
- guest runtime protocol。

编译能力是 binary metadata，不写入 session 或数据库。已有项目配置可以在 macOS 和 Linux 间共享；能力检查发生在执行该配置的 daemon。

## 工作流和失败语义

### macOS Host Build

1. `task build:agent-compose` 识别 Darwin host 并选择 `darwin-docker`。
2. 构建过程强制 `CGO_ENABLED=0`，不准备 BoxLite/Microsandbox artifact。
3. 产物写入 `build/agent-compose`。
4. `agent-compose --json version` 报告 Darwin target 和仅 `docker`。
5. daemon 可以连接 Docker Desktop 或显式配置的 Docker daemon；请求其他 driver时返回 unsupported。

Docker 是标准开发依赖，但 Darwin binary 的纯编译步骤不为了形式而调用 Docker。需要验证 Docker driver或构建镜像时才要求 daemon 可连接。

### Linux Host Build

1. `task build:agent-compose` 识别 Linux host 并选择 `linux-full`。
2. Task 通过现有 export helper 准备或复用 BoxLite 与 Microsandbox artifact；Docker 不可用、下载失败或 architecture 不支持时构建失败。
3. build helper校验完整前置产物，以显式 tags 和 CGO 构建 binary。
4. 产物写入 `build/agent-compose`，运行时默认从现有配置路径使用 `build/boxlite`、`build/microsandbox` 产物。
5. version metadata 报告三种 compiled driver。

Linux full build 不因 KVM 缺失而编译失败；KVM 是真实启动 BoxLite/Microsandbox 的运行条件。

### Image Build 与 macOS 运行

1. CI 或本地镜像任务为目标 architecture 获取原生产物。
2. Docker build stage选择 `linux-full`，最终镜像包含 binary 与两套 runtime artifact。
3. multi-arch manifest使 Intel Mac 选择 `linux/amd64`、Apple Silicon 选择 `linux/arm64`。
4. macOS 使用基础 Compose 启动 full image，不映射 `/dev/kvm`，默认选择 Docker driver。
5. daemon 构造所有已编译 runtime wrapper，但不在启动阶段初始化 BoxLite/Microsandbox native runtime；Docker 工作流不受缺少 KVM 影响。

### Unsupported Driver

未编译 driver的错误必须包含：

- 请求的规范化 driver 名称。
- 当前 OS/architecture。
- 当前 `compiled_drivers`。
- 这是 build capability 不支持，而不是 runtime 暂时故障。

错误不得建议 macOS 用户通过添加 Linux CGO tag 原地重建。对于 Darwin，应引导使用 Docker driver或在 Linux/full image 环境运行对应 driver。

运行环境缺失与编译能力缺失必须区分：

- 未编译：unsupported。
- 已编译但 Docker daemon不可达：Docker connection/runtime error。
- 已编译但缺少 KVM：BoxLite/Microsandbox environment error。
- 已编译但 artifact 文件缺失或不可加载：runtime artifact/configuration error。

### Build Failure

构建失败必须在执行 `go build` 前尽可能报告 profile 与缺失前置条件。错误中不得输出代理凭证、registry token 或其他 secret。已存在但不完整的 artifact 不得被当作 cache hit。

不支持的 host OS、target architecture 或 profile 必须显式失败，不得隐式退化为 Docker-only 或关闭 CGO。

## 测试、质量门禁和验收标准

### Unit Tests

unit tests 覆盖：

- 每个 build constraint 组合的 compiled driver集合。
- driver 名称合法但未编译时的 unsupported 分类和错误内容。
- `RUNTIME_DRIVER` 对 compiled capability 的启动校验。
- version 文本兼容与 `--json version` shape。
- `/api/version` additive fields 与稳定排序。
- Compose/installer 的 KVM 检测和文件集合选择逻辑。
- build helper 的 profile、OS、architecture 和 artifact preflight。

普通 unit tests 继续满足 `TESTING.md`：不依赖 Docker、网络、KVM 或共享持久状态。需要测试脚本行为时使用临时目录和 fake executable。

### Build Matrix CI

`.github/workflows/ci.yml` 增加 binary build 验证：

| CI 产物 | 必须验证 |
| --- | --- |
| `darwin/amd64` | `darwin-docker` 可编译，metadata 仅含 Docker |
| `darwin/arm64` | `darwin-docker` 可编译，metadata 仅含 Docker |
| `linux/amd64` | `linux-full` 可编译，metadata 含三 driver，原生产物 preflight 通过 |
| `linux/arm64` | `linux-full` 可编译，metadata 含三 driver，原生产物 preflight 通过 |

Darwin 交叉编译可以覆盖两个 architecture，但至少一个 macOS runner必须原生执行 version 和 daemon startup smoke。Linux full build优先复用 native architecture runner或 Docker build stage，避免未定义的 CGO 交叉工具链。

binary 只作为 job 内验证产物，不上传到 GitHub Release；CI 是否短期保存 workflow artifact属于实现细节，不构成发布合同。

### Image CI

`.github/workflows/images.yml` 继续执行：

- pull request 构建 `linux/amd64` 验证镜像。
- main/tag 构建 `linux/amd64`、`linux/arm64` 并合并 manifest。
- 发布 `agent-compose` 与 `agent-compose-guest` 镜像。
- tag release 只发布 installer assets，不发布 binary。

daemon image 还必须验证：

- image 内 `agent-compose --json version` 报告 `linux`、目标 architecture 和三 driver。
- 不提供 `/dev/kvm`、选择 Docker driver时 daemon 可启动。
- 挂载 Docker socket后能够完成至少一个真实 Docker sandbox lifecycle smoke。
- BoxLite/Microsandbox artifact 存在于镜像配置声明的路径。

真实 BoxLite/Microsandbox smoke继续通过 `task test:runtime-smoke` 或 `docs/spec/core-e2e-test-strategy-spec.md` 定义的具备 KVM 环境执行，不要求普通 GitHub-hosted PR runner提供 KVM。

### Compose 验证

必须覆盖：

- `docker compose -f docker-compose.yml config` 在不具有 `/dev/kvm` 的环境成功。
- 基础 Compose 启动 full image 后 Docker driver可用。
- Linux KVM 环境叠加 `docker-compose.kvm.yml` 后 `/dev/kvm` 和 privileged 配置存在。
- installer 在有/无 `/dev/kvm` 两种情况下生成可重复的 Compose 选择，并保持 upgrade 后选择稳定。
- 发布 installer archive 包含基础 Compose；KVM overlay 作为选择性部署文件一并包含。

### Harness 门禁

完成变更必须通过：

```bash
task lint
task build
task test
task image:agent-compose
```

其中：

- Darwin 执行 `task build` 时 host binary 为 Docker-only。
- Linux 执行 `task build` 时 host binary 为 full，并允许通过 Docker准备原生产物。
- `task test` 继续输出 unit、integration、E2E 和 combined coverage，并满足 `TESTING.md` 的 60%/60%/60%/70% 最低门禁。
- runtime 或部署边界变化必须增加相应 integration/E2E；不能只以编译成功作为 Docker driver兼容性证明。

### 验收标准

- macOS `build/agent-compose` 是可原生执行的 Mach-O binary，`compiled_drivers` 精确为 `docker`。
- Linux `build/agent-compose` 是启用 CGO 的 Linux binary，`compiled_drivers` 精确为 `docker`、`boxlite`、`microsandbox`。
- Linux full build同时准备并校验 BoxLite 和 Microsandbox runtime artifact。
- 发布 daemon image 在 amd64/arm64 上使用同一个 `linux-full` profile，并包含两套 runtime artifact。
- 同一 full image 在 macOS Docker Desktop 上不映射 KVM即可启动并完成 Docker sandbox smoke。
- 基础 Compose 不要求 privileged 或 `/dev/kvm`；KVM overlay只在显式选择或 installer检测到 KVM 时生效。
- Darwin 对 BoxLite/Microsandbox、Linux full 对缺失运行环境分别返回可区分的错误。
- `agent-compose version` 文本输出保持兼容；JSON version 与 HTTP version准确暴露 build 信息。
- Taskfile与两个 Dockerfile不再分别拼装 CGO、tags 和 ldflags。
- GitHub Release 保持 image/installer-first，不增加 per-arch binary 下载项。

## 首版不做事项

- 不在 macOS 原生 binary 中支持 BoxLite 或 Microsandbox。
- 不支持 Windows 原生 binary；Windows Docker-only profile需要独立规格。
- 不提供任意 driver tag 组合或用户自定义 profile。
- 不支持在 macOS 上直接用本机工具链交叉构建 Linux full CGO binary。
- 不把 macOS Docker Desktop 视为 BoxLite/Microsandbox 支持环境，也不承诺 nested virtualization。
- 不发布 macOS/Linux binary 到 GitHub Release。
- 不定义 Linux full binary 的独立 tar bundle、安装目录或动态库重定位合同。
- 不把 KVM runtime 真实 E2E 加入普通 pull request 阻塞 CI。
- 不修改 v1/v2 Connect protobuf、SQLite schema、guest protocol 或默认 runtime driver。
- 不在 `compiled_drivers` 中混入运行时健康探测结果；未来若需要 `available_drivers`，应单独定义探测、缓存和权限语义。
- 不在首版删除 deprecated `build:agent-compose:boxlite` alias。

## 关键假设和已确认决策

- 已确认保留三类构建产物：macOS Docker-only binary、Linux 三 driver binary、Linux 三 driver Docker image。
- 已确认 Docker 可以作为标准开发依赖，不再承诺普通开发循环完全不需要 Docker。
- 已确认 macOS 使用完整 Linux image 时只要求 Docker driver可用；BoxLite/Microsandbox 缺少 KVM 不得阻止 daemon 启动。
- 已确认平台 binary用于本地和 CI 构建验证，不作为 GitHub Release 产物；现有 image/installer 发布模型保持不变。
- 已确认 Compose/KVM 拆分与 compiled driver能力声明属于本次范围。
- 默认 driver继续是 `docker`，这是 macOS binary、Linux binary和 Docker image 的共同安全默认值。
- host 默认 profile依据 Go host OS 选择；显式 target matrix由 CI/Docker build负责。
- Linux full binary 的“full”指编译能力完整，并以现有 `build/boxlite`、`build/microsandbox` runtime artifact 为本地运行前提；首版不承诺单文件可移植发行。
- 新规格名称采用 `platform-runtime-build`，供后续 `mass-plan` 读取 `docs/spec/platform-runtime-build-spec.md`。
