# KubeLoop V2 Roadmap

> 状态：草案
> 目标版本：v2.0.0
> 规划基线：v1.11.0
> 核心方向：Helm 安装集群内 Gateway；所有 Kubernetes 操作由 Gateway 执行；桌面客户端只配置服务地址，并通过 OIDC 或 Active Directory 登录。

配套架构与关键流程图见 [`diagrams/kubeloop-v2-architecture.drawio`](diagrams/kubeloop-v2-architecture.drawio)。该文件包含整体架构、OIDC/AD 登录、Session/RelayTicket、核心数据路径、Traffic Task 生命周期和断线恢复六个可编辑页面。

## 1. V2 目标

V2 将 KubeLoop 从“持有 kubeconfig 并直接访问 Kubernetes API 的桌面工具”改造为“集群内 Gateway 服务 + 轻量桌面客户端”。

用户的标准使用流程应缩短为：

1. 管理员通过 Helm 安装并配置 KubeLoop Gateway。
2. 用户在桌面客户端填写 `https://kubeloop.example.com`。
3. 客户端读取服务发现信息，并按 Gateway 配置发起 OIDC 浏览器登录或 AD 登录。
4. 身份认证成功后，客户端建立 HTTPS/WSS Session。
5. 用户选择授权范围内的 Namespace，并使用网络连接、Port Forward、Pod SSH、文件传输、Exchange、Mirror 和 Preview。

V2 完成时应满足：

- 桌面客户端不读取 kubeconfig，不直接访问 Kubernetes API Server。
- Kubernetes 凭证、client-go 和资源恢复逻辑位于 Gateway。
- OIDC/AD 配置由 Gateway 管理；普通客户端只填写服务地址。
- 每个 HTTP 请求和 WSS 逻辑流都经过身份认证、权限校验和审计。
- 本地 TUN、路由、DNS、sing-box、Helper 和本地进程访问仍留在客户端。
- Gateway 可通过 Helm 安装、升级、回滚和卸载。
- v1 和 v2 的控制协议明确隔离，不依赖隐式兼容。

## 2. 范围边界

### 2.1 迁移到 Gateway

- Namespace、Pod、Service、Container 和端口查询。
- Kubernetes 版本、RBAC capability 和网络信息探测。
- Pod exec、日志、SFTP/tar 文件操作。
- Pod/Service Port Forward。
- Exchange、Mirror、Preview 的资源创建、快照、恢复和清理。
- Endpoint、EndpointSlice、Service、Deployment 等 Kubernetes 资源管理。
- Gateway Session、用户 Session、反向监听和数据转发。
- 操作审计、配额、并发限制和服务端诊断。

### 2.2 保留在客户端

- Wails/React UI。
- 身份认证发起、Token 安全存储和刷新。
- 本地 TUN、路由、split DNS、sing-box 和特权 Helper。
- 本地开发进程和本地监听端口。
- 本地文件选择、读写和终端窗口。
- 面向本地 Agent 的 MCP 入口；MCP 后端改为调用 Gateway API。

### 2.3 V2.0 非目标

- 暴露任意 Kubernetes REST Proxy。
- 替代 kubectl 或完整 Kubernetes IDE。
- 多集群同时透明 TUN 路由。
- 跨 Gateway Pod 无损迁移活动 TCP/WSS Stream。
- 云端账号、计费和商业租户系统。
- 第三方代码在 Gateway 内运行的插件系统。

## 3. 目标架构

```text
Desktop Client
  UI / Authentication / Local MCP
  Local Helper / sing-box / TUN / DNS
  Local files / terminal / development processes
                    │
          https://kubeloop.example.com
                    │
          ┌─────────┴──────────┐
          │                    │
          ▼                    ▼
  Control Plane            Data Plane
  /well-known              /tunnel
  /auth /api               authenticated WSS
  Auth / Authorization     multiplex / relay
  Session / Task           TCP / UDP / DNS
  Kubernetes Provider      reverse streams
          │                    │
          ▼                    ▼
    Kubernetes API       Pods / Services / CoreDNS
```

对客户端和管理员而言，二者仍属于一个 KubeLoop Gateway，通过同一个外部 Origin 和同一个 Helm Chart 交付；运行时必须拆成两个独立 Deployment：

| 组件 | 建议名称 | 权限与职责 |
| --- | --- | --- |
| Control Plane | `kubeloop-controller` | OIDC/AD、Token、Authorizer、审计、Storage、Kubernetes API、Session/Task 和资源恢复；持有受限 ServiceAccount 和必要 Secret |
| Data Plane | `kubeloop-gateway` | WSS 多路复用、TCP/UDP/DNS、反向流量、流控和连接指标；不读取业务数据库、OAuth/AD Secret，也不持有 Kubernetes 管理凭证 |

客户端与 Gateway 之间分为三类协议：

- **HTTPS 控制面：** 登录、资源查询、Session 和 Task 生命周期、配置、诊断。
- **Controller WSS 操作流：** exec 与 Pod 文件传输；由持有 Kubernetes 权限的 Controller 终止连接并创建 Kubernetes stream。
- **Data Plane WSS：** TUN TCP/UDP/DNS、Port Forward 和反向流量；只接受 RelayTicket 授权的网络目标。

不允许通过 WSS 建立未经控制面授权的目标连接。每个逻辑 Stream 必须包含 `sessionID`、`streamID`、操作类型和目标描述，并由 Gateway 再次执行授权。

Control Plane 创建 Cluster Session 后签发短期、一次性或有限重用的 `RelayTicket`。Ticket 至少绑定 principal、device、session、目标 Data Plane、允许的操作、NetworkSpec 摘要、过期时间和唯一 ID。Data Plane 使用 Control Plane 的公开签名密钥离线验证 Ticket，不访问 OIDC/AD Provider，也不依赖每个 Stream 回调 Control Plane。

Pod exec、日志、文件和 Kubernetes API port-forward 仍由持有 Kubernetes 权限的 Control Plane 创建；Data Plane 不为了传输性能获得宽泛的 `pods/exec` 或 `pods/portforward` 权限。后续如果性能数据证明必要，再单独设计权限极窄的 Operation Worker，不纳入 V2.0。

### 3.1 后端存储

V2.0 默认使用内置 SQLite，并允许配置外部 PostgreSQL：

```yaml
database:
  driver: sqlite # sqlite | postgres
  sqlite:
    path: /var/lib/kubeloop/kubeloop.db
  postgres:
    existingSecret: ""
    secretKey: dsn
```

存储边界如下：

- SQLite/PostgreSQL 保存 Principal、身份映射、Refresh Token 哈希、Device/Cluster Session 元数据、Task、幂等键、资源快照和审计事件。
- OIDC Client Secret、AD Bind Secret、数据库 DSN 和签名密钥保存在 Kubernetes Secret 或外部 Secret 系统，不写数据库。
- 活动 WSS/TCP/UDP Stream、socket、流控窗口和实时数据只存在 Data Plane 内存中。
- Controller leader election 使用 Kubernetes Lease，不写业务表模拟分布式锁。
- 文件内容、命令输出和流量内容默认不落服务端存储。

SQLite 模式的部署约束：

- 只允许一个 Controller 副本，并使用 `Recreate` 更新策略。
- 数据库目录必须挂载持久卷；未配置持久卷时只允许显式的开发模式。
- 数据库文件、journal/WAL 和临时文件必须位于同一个本地或具备可靠 POSIX 文件锁语义的块存储文件系统。
- 不支持把 SQLite 文件直接放到 NFS、SMB 或其他共享网络文件系统，也不支持多个 Pod 同时打开同一个数据库文件。
- SQLite 模式不提供 Controller HA；需要多个 Controller 副本时必须使用 PostgreSQL。

外部数据源在 V2.0 只正式支持 PostgreSQL，不承诺任意 SQL 数据库兼容。SQLite 和 PostgreSQL 必须共享同一套逻辑 schema version 和 repository contract，但允许各自使用不同、经过测试的 SQL dialect。切换数据库需要显式 export/import，不通过修改 DSN 自动迁移数据。

## 4. 必须先冻结的架构决策

以下任务是后续开发的入口条件。

| ID | 任务 | 产物 | 完成标准 |
| --- | --- | --- | --- |
| V2-001 | 编写客户端/Gateway 信任边界 ADR | `docs/adr/` 架构决策 | 明确凭证、Token、Kubernetes 权限和本地特权边界 |
| V2-002 | 选择认证模型 | Authentication ADR | 确认 OIDC Broker、Entra ID/AD FS 和原生 AD/LDAPS 的边界，客户端统一使用 Gateway Session Token |
| V2-003 | 选择授权模型 | Authorization ADR | 明确 Gateway Policy、Kubernetes Impersonation 或两者的优先顺序 |
| V2-004 | 定义协议版本策略 | API version ADR | 明确 `/api/v2`、WSS protocol version、最低客户端版本和错误码 |
| V2-005 | 定义 Session 所有权 | Session ADR | 明确用户、设备、Session、Task、Stream、Kubernetes 资源之间的归属 |
| V2-006 | 定义 Gateway HA 边界 | HA ADR | 明确 v2.0 使用 sticky session，活动 Stream 不跨 Pod 迁移 |
| V2-007 | 完成威胁建模 | Threat model | 覆盖 Token 盗用、callback 劫持、越权、SSRF、跨 Session 和资源残留 |
| V2-008 | 冻结存储模型 | Storage ADR | 确认默认 SQLite、外部 PostgreSQL、单副本限制、Secret 边界和迁移方式 |
| V2-009 | 冻结 Control/Data Plane 拆分 | Component ADR | 明确进程、Deployment、ServiceAccount、Secret、网络、协议、扩缩容和故障边界 |

已完成：**V2-008** 的存储决策已冻结在 `docs/adr/0001-controller-storage.md`，确认默认 SQLite、外部 PostgreSQL、单副本/HA 限制、Secret 边界、双后端 migration 和显式 export/import 策略。

2026-08-10：ORM 选型冻结在 `docs/adr/0007-controller-orm.md`。Controller 使用 Bun v1.2.18 作为 Repository 内部的 SQL-first ORM，保留现有 `database/sql` 驱动、显式版本化 migration 和业务层 Repository 接口；从 Cluster Session/NetworkSpec 开始逐仓储迁移，禁止 AutoMigrate。

已完成：**V2-002** 的统一认证模型已冻结在 `docs/adr/0002-authentication.md`，确认 OIDC Broker 优先、原生 AD 仅通过 LDAPS/严格 StartTLS 兼容、桌面 PKCE/exchange code，以及所有 Provider 最终统一为 Gateway Token 和稳定 Principal。

## 5. Roadmap 总览

按 2–3 名核心开发者估算，V2.0 需要约 14–18 周。各阶段以验收门槛推进，不以日期强制切换。

| 阶段 | 建议周期 | 结果 |
| --- | --- | --- |
| M0 架构冻结 | 1–2 周 | ADR、威胁模型、API 草案和迁移边界确定 |
| M1 协议与双组件骨架 | 2 周 | 独立 Controller/Data Plane、发现接口、内部协议和类型化客户端可用 |
| M2 Helm 与部署基线 | 2 周 | 可在测试集群安装、独立升级、访问和卸载两个组件 |
| M3 OIDC/AD、授权与审计 | 2–3 周 | 客户端只凭服务地址完成登录并受到逐操作授权 |
| M4 Kubernetes 控制面迁移 | 2–3 周 | 客户端资源查询和 Session 建立不再使用 kubeconfig |
| M5 数据面迁移 | 2–3 周 | TUN/SOCKS 和多路复用流量完全经过远程 Gateway |
| M6 功能迁移 | 3–4 周 | 现有核心功能全部改用远程 Gateway |
| M7 客户端瘦身与迁移 | 1–2 周 | 移除 kubeconfig UI，形成服务地址优先的完整体验 |
| M8 RC 与 GA | 2–3 周 | 安全、跨平台、升级、恢复和发布门槛全部通过 |

## 6. 具体任务

### M0：架构冻结

- [x] **V2-010：盘点所有 Kubernetes 调用点。**
  - 输出桌面端直接调用 client-go 的 package、接口和功能清单。
  - 标记控制面调用、长连接调用和可流式传输调用。
  - 验收：Namespace、Inventory、Gateway、Port Forward、Intercept、exec、文件操作均有明确迁移归属。
  - 2026-08-10（完成）：新增 `docs/v2-kubernetes-call-sites.zh-CN.md`，按非测试生产源码逐包盘点全部 19 个 `k8s.io/*` 直接依赖包，区分隔离的 V1 桌面遗留、V2 Controller、共享 Kubernetes 基础设施与仅类型/校验依赖，并逐项列出 kubeconfig/client-go、REST、informer、SSAR、SPDY exec/port-forward、Endpoint/EndpointSlice 接管和资源写入用途。Namespace、Inventory、ServerVersion/Capability、网络发现、Gateway 安装、Service/Pod Port Forward、Exchange/Mirror/Preview、Pod exec/TTY、文件传输和文件管理均已映射到明确的 V2 控制面及流所有者；Gateway 安装归 Helm，网络数据流归无 Kubernetes 凭据的 Data Plane，exec/file SPDY 与资源补偿归 Controller。文档同时固定用户 impersonating client 与 system compensation client 的边界。新增架构守卫 `TestKubernetesDirectImportInventoryIsExhaustive`，源码直接依赖集合变化时要求同步评审清单；既有依赖图测试证明桌面组合根、`clientv2`、MCP、Data Plane 和 Helper 不持有 Kubernetes SDK 或 kubeconfig 能力。`go test ./internal/architecture` 与 `git diff --check` 通过。

- [x] **V2-011：定义领域模型。**
  - 定义 `ServerProfile`、`Principal`、`DeviceSession`、`ClusterSession`、`Task`、`Stream` 和 `AuditEvent`。
  - 所有持久化对象包含 schema version；所有运行对象使用不可猜测 ID。
  - 依赖：V2-005。
  - 2026-08-10（完成）：ADR 0016 在 ADR 0005 的 Session 所有权基础上冻结 `ServerProfile -> Principal -> DeviceSession -> ClusterSession -> Task -> Stream/ResourceSnapshot` 聚合关系，并明确 RelayTicket/Access Token 只是有界认证能力而不是资源所有者。领域表逐项定义身份、字段、Owner、生命周期和持久化边界；`profile.ServerProfile`、`storage.DeviceSession`、`storage.ClusterSession` 作为规范领域名称，以兼容 alias 保留现有 `Profile`、`TokenFamily`、`Session` Repository/API。所有服务端持久化聚合继续使用正数对象 schema version；ServerProfile、系统钥匙串 credential metadata 和本地文件传输 Task 新增对象级 `schemaVersion`，显式迁移旧版缺失字段、拒绝未知未来版本，并同步重新生成 Wails TypeScript 模型。所有可跨信任边界引用的 Principal、DeviceSession、ClusterSession、Task、Snapshot、AuditEvent、Ticket 和认证事务均由 UUID v4 或 `crypto/rand` 生成；连接内数字 channel 仅是已认证 Task-bound 流的 framing handle，不能单独作为资源或授权 ID。测试覆盖三类新增对象版本的旧数据升级与未来版本 fail-closed；`go test ./...`、`go vet ./...`、相关五包 race、前端生产构建、Wails 模型生成和 `git diff --check` 均通过。

- [x] **V2-012：定义错误模型。**
  - 至少包含 `UNAUTHENTICATED`、`FORBIDDEN`、`NOT_FOUND`、`CONFLICT`、`INVALID_ARGUMENT`、`UNAVAILABLE`、`VERSION_MISMATCH` 和 `RATE_LIMITED`。
  - 错误响应包含稳定 code、用户消息、可选 field、request ID，不向客户端泄露敏感内部错误。
  - 2026-08-09：已在 Controller API 框架中定义稳定错误码、HTTP 映射与 JSON envelope；panic 只返回通用 `INTERNAL`，不向客户端返回内部详情。

- [x] **V2-013：定义迁移期双栈策略。**
  - 为 cluster、exec、file、traffic 定义本地和远程两套 backend interface。
  - 迁移期用显式 feature flag 选择实现，不在同一 Session 混用本地和远程 Kubernetes 控制面。
  - 验收：每迁移一个功能，都可以复用原 manager 测试验证两种 backend。
  - 2026-08-10（完成，切换策略更新）：ADR 0017 固化“源码隔离双实现、生产 V2 远程唯一”的切换策略。`internal/cluster/session/filemanager` 与 V1 intercept/port-forward adapter 继续作为本地行为参考和独立测试目标；`internal/clientv2` 的 discovery/inventory、ClusterSession/Data Plane、exec、file、Port Forward、Exchange、Mirror、Preview、Pod SSH 与 MCP 均通过窄 Client/Session 接口注入并以 fake 复用 manager 测试。由于当前生产组合根已经完成切换，原计划的运行时 local/remote feature flag 被明确废止：重新引入该开关会让客户端再次持有 kubeconfig/client-go 并允许同一 UI 状态混合两个授权域。`NewApp` 只装配远程 manager，新增只读 `BootstrapData.backendMode=remote`，setup 与 active-profile 测试均断言该值；ServerProfile 不保存 backend 类型，也没有环境变量或隐藏 fallback。同一 V2 ClusterSession 因而只能使用远程 Gateway。架构测试持续禁止桌面、`clientv2`、MCP 和 Data Plane 回流 V1/Kubernetes 依赖；回退必须通过应用版本回退而不是活动 Session 内切换。Wails 模型已重新生成，`go test ./internal/app ./internal/architecture`、前端生产构建与 `git diff --check` 通过。

### M1：协议与 Gateway 骨架

- [x] **V2-100：建立 Control Plane 可执行入口。**
  - 建立独立的 Controller command、配置加载、结构化日志和优雅关闭。
  - Controller 不加载桌面 Store、Wails runtime、本地 Helper 或 Data Plane socket runtime。
  - 提供 build version、commit、protocol min/max version。
  - 2026-08-09：已新增 `cmd/kubeloop-controller`，支持 flag/env 配置、JSON 结构化日志、信号优雅关闭和构建/协议元数据；架构测试持续阻止其依赖 V1 桌面与数据面运行时。

- [x] **V2-111：建立 Data Plane 可执行入口。**
  - Data Plane 只装配 WSS、multiplexer、dialer、reverse relay、流控、指标和优雅排空。
  - 构建检查确保其不依赖 Storage Repository、OIDC/LDAP SDK、Kubernetes Provider 或 Controller Secret 配置。
  - 提供独立镜像或同镜像不同 command；无论采用哪种打包方式，运行时必须是独立进程和 Pod。
  - 2026-08-09：`kubeloop-gateway` 已具备独立镜像和 Helm Deployment，支持 WSS/multiplexer、反向 relay、连接限制、live/ready/metrics、拒绝新连接、活动逻辑流排空和超时强制关闭；架构测试禁止其依赖 Control Plane、Kubernetes、OAuth、桌面 Store 和 Wails。迁移期暂留未暴露到 V2 Service 的 V1 raw TCP listener。

- [x] **V2-112：定义 Controller/Data Plane 内部协议。**
  - 定义 Relay 注册、健康、容量、Session 分配、Ticket 公钥轮换、吊销摘要和排空状态。
  - 内部协议版本独立于客户端 API/WSS version，并有双版本滚动升级 contract test。
  - 禁止通过内部协议传递 OIDC/AD Secret、Refresh Token 或用户密码。
  - 2026-08-10（完成）：ADR 0018 与 `internal/protocol/relaycontrol` 定义独立的 `relay.kubeloop.io/v1` 严格 JSON 协议，覆盖 `RelayRegistration/Result`、`RelayHeartbeat/Result`、`SessionAllocation/Assignment` 六类消息；全部消息限制 64 KiB、拒绝未知字段/版本/kind、多 JSON 文档和非法容量/时间/ID。注册 body 刻意没有 `relayId`；mTLS/SPIFFE（或等价受限工作负载证书）认证层生成 trust domain、namespace、ServiceAccount、Pod UID 的 `PeerIdentity`，Controller 以 SHA-256 派生稳定 Relay ID。协议上报物理连接/逻辑流容量、ready/draining 与已应用 key/revocation generation，返回不可猜测 lease、心跳期限、desired state、Ed25519 公钥集和吊销摘要。钥匙轮换要求先分发并获 ready Relay 确认，再切换签名，旧公钥保留至旧 Ticket 全过期；私钥永不进入 Data Plane。吊销摘要使用排序的 ClusterSession ID SHA-256、最大撤销 generation、expiry 与 canonical digest，支持本地 generation-bound 拒绝且不暴露 Principal。注册通过 ordered supported-version 列表协商最高共同版本；contract test 覆盖 old/new Controller 与 Data Plane 双向滚动升级回退到 v1、双方支持 v2 后才升级以及无共同版本拒绝。严格 unknown-field 测试同时证明 `refreshToken` 等 Secret 字段无法进入协议。`go test ./...`、`go vet ./...`、相关 race 与 `git diff --check` 通过。

- [x] **V2-113：实现 Data Plane Registry。**
  - Controller 根据 ready、draining、active streams、容量和拓扑选择 Data Plane。
  - 注册使用 Pod identity 或双向认证，不信任客户端提交的 relay ID。
  - Data Plane 离线和租约过期后停止分配新 Session，但不伪造活动 Stream 已迁移。
  - 2026-08-10（完成）：新增并发安全的 `controller/relayregistry`，注册时只从经认证的 trust domain、namespace、ServiceAccount、Pod UID 派生 `relay-<sha256>`，生成不可猜测 lease；心跳续租并上报 ready/draining、物理连接/逻辑流使用量、最大容量及已应用 key/revocation generation。分配在同一锁内完成容量预留，先匹配 Controller Pod 的 region/zone/hostname，再比较逻辑/物理负载并稳定按 Relay ID 决胜；同一 Session/generation/NetworkSpec 保持原 assignment，旧 generation、摘要变化和 lease 替换均 fail closed。离线、过期、draining 或控制状态落后的 Relay 立即停止新分配，既有 assignment 返回 unavailable 而不会被静默改写到新 Pod；显式 Release 只接受当前 generation。Controller 增加独立 TLS 1.3 内部 listener 和 ClusterIP Service，不进入公共 Ingress；支持 mTLS/SPIFFE，也支持默认的 Kubernetes TokenReview Pod identity。默认模式保持 Data Plane `automountServiceAccountToken: false`，只投影十分钟、`kubeloop-relay` audience、Pod-bound token；Controller 校验 TokenReview 的 ServiceAccount/Pod UID，再从 Kubernetes Pod/Node 取得可信拓扑。Gateway `relayagent` 启动时注册、立即应用并确认 Ed25519 公钥集/吊销摘要，此后按 lease 心跳报告真实容量；控制状态原子替换且保留 replay/generation high-water mark，公钥有效期、吊销摘要过期和 generation 回退均 fail closed，SIGTERM 会先发送 draining 心跳。RelayTicket API 通过 Registry 动态分配并以派生 Relay ID 为 audience，响应返回 `relayId`/WSS `endpoint`；桌面按该 assignment 建立连接池并拒绝池建立期间端点漂移，静态同源路径仅保留为未启用 Registry 时的兼容模式。Helm 默认启用内部 Registry、专用 NetworkPolicy、server CA 和投影 Pod token；多 Data Plane 必须使用 `{podName}`/`{podUID}` 可路由端点，当前进程内 Registry 明确限制单 Controller 副本，避免 HA split lease。并发容量、拓扑/负载、排空、租约过期、旧 lease、无静默迁移、mTLS、TokenReview、端点 allowlist、动态 Ticket audience/endpoint、客户端端点选择、key window/revocation、Helm 安全拓扑均有测试；`go test ./...`、`go vet ./...`、相关 race 与 Helm client dry-run 通过。

- [x] **V2-101：实现服务发现接口。**
  - `GET /.well-known/kubeloop` 返回服务 ID、API version、认证方式、功能、服务端版本和最低客户端版本。
  - 该接口不返回 Secret、内部地址或完整集群信息。
  - 增加缓存策略和 contract test。
  - 2026-08-09：已实现 discovery、`/health/live`、`/health/ready` 与 contract test；默认不声明尚未配置的认证方式和功能。

- [x] **V2-102：建立 `/api/v2` HTTP 框架。**
  - 统一 request ID、JSON 编解码、错误映射、超时、body 大小限制和 panic recovery。
  - 所有 handler 依赖窄接口，禁止直接访问全局 clientset。
  - 2026-08-09：已实现 request ID、严格 JSON 解码、类型化错误、请求 context 超时、body 限制和 panic recovery；API 默认拒绝未认证请求，并通过 `Authenticator`/`APIHandler` 窄接口注入后续实现。

- [x] **V2-103：定义 WSS v2 握手。**
  - Controller 在签发 RelayTicket 前校验 Access Token；Data Plane 握手只接收最小权限的一次性 RelayTicket，并校验 protocol version、client version 和签名绑定的 device ID。
  - 定义 control frame、stream open/accept/reject、data、half-close、cancel、ping/pong。
  - 设置单 frame、单 stream、单连接和单用户限制。
  - 2026-08-10（完成）：新增严格的二进制 JSON `ClientHello`/`ServerHello`/`Reject` 握手，必须在 `kubeloop-mux-v2` HTTP Upgrade（响应明确携带 `KubeLoop-WSS-Version: 2.0`）和 RelayTicket 验证后、任何 smux 字节前完成；文档限制 8 KiB、默认十秒超时，拒绝文本帧、未知/缺失字段、重复值、尾随 JSON、协议不兼容、低于最低版本的客户端、Ticket/Hello 设备不一致及用户容量耗尽。Gateway 仅实现并选择 WSS protocol `2.0`，`VERSION_MISMATCH` 返回服务端支持版本；客户端返回带稳定 code 的类型化 `HandshakeError`，拒绝后不创建 Forwarder 或部分可用 smux Session。RelayTicket API 明确返回签名 claim 对应的 `deviceId`，连接池内后续 Ticket 必须保持 Relay/endpoint/device assignment 不变；桌面版本由组合根传入握手，Data Plane 使用与 discovery 相同的 `controller.minClientVersion`。`ServerHello` 发布 WebSocket frame、64 KiB 逻辑 data frame、每连接 stream、全局物理连接、跨设备按 Principal 计数的单用户连接和毫秒级 stream idle 限制；客户端按返回值收紧连接池与每连接 stream。ADR 0019 固化 control/open/status/data/half-close/cancel/Ping-Pong 到 smux、KCG2 tunnel header、KubeLoop data/FIN 与 WebSocket 的唯一映射。严格编码、版本/客户端/设备/容量拒绝、精确 limit 协商、无部分 Session、并发复用、half-close、畸形 stream 隔离和 idle timeout 均有测试；全量 Go test/vet、E2E 编译、关键 race、Helm lint/template 和 Kubernetes client dry-run 通过。

- [x] **V2-104：生成或维护类型化客户端 SDK。**
  - 客户端封装 discovery、Token、HTTP API、WSS 和错误类型。
  - UI/Wails binding 不直接拼接 endpoint 或解析裸 JSON。
  - 2026-08-10（完成）：`internal/clientv2` 已作为纯客户端 SDK 边界，分别封装同源 discovery、OIDC/AD 登录与 Token 刷新/撤销、安全凭据存储、带并发单飞刷新和一次 401 重试的 `/api/v2` HTTP 客户端、RelayTicket/WSS v2 连接池、Cluster Session/Data Plane 以及 Port Forward、exec、文件、Pod SSH、Exchange、Mirror、Preview 等类型化远程 Task；所有请求/响应都有有界 body、超时、目标/字段/分页/Session 绑定校验，SDK 依赖图禁止 Controller、Data Plane 服务端、本地 Kubernetes、V1 Session/Store 和 Wails runtime。HTTP `APIError` 暴露 status、稳定 code、field、request ID；认证端点新增类型化 `auth.APIError`，discovery 新增 `CompatibilityError` 和 `VERSION_MISMATCH`/`CLIENT_VERSION_UNSUPPORTED` 常量，WSS 提供带支持版本的 `HandshakeError`，UI 无需解析错误字符串。桌面组合根只注入 SDK/Manager，V2 Wails bindings 只接收/返回领域 struct；新增架构测试扫描生产 bindings 与 `components/server`，禁止 `net/http`、裸 WebSocket、`fetch`、`JSON.parse`、`/api/v2` 和 `/auth/` 路径回流到 UI/Wails。Go 全量 test/vet 和前端 TypeScript/Vite production build 通过。

- [x] **V2-105：建立协议 Contract Test。**
  - 覆盖旧客户端、新 Gateway；新客户端、旧 Gateway；未知字段；缺失字段；版本不兼容。
  - 验收：不兼容组合收到明确 `VERSION_MISMATCH`，不会建立部分可用 Session。
  - 2026-08-10（完成）：建立 discovery/HTTP/WSS 三层兼容契约。Discovery 矩阵验证旧 `2.0` 客户端可连接声明 `2.0–2.1` 的新 Gateway，而 `2.1` 新客户端面对只支持 `2.0` 的旧 Gateway 获得类型化 `VERSION_MISMATCH`；HTTP 类型化 SDK 接受响应中的增量未知字段以支持向前演进，但继续拒绝缺失必填字段。WSS 新 Gateway 在 Upgrade 响应声明 `KubeLoop-WSS-Version: 2.0`，识别旧客户端首发的完整 smux v1/v2 header 并在创建 smux 前返回带支持版本的 `VERSION_MISMATCH`；新客户端在发送 `ClientHello` 前即可从缺失版本头识别旧 Gateway，同样返回类型化 `VERSION_MISMATCH`，不会把一般握手网络错误误分类。双向未知字段/缺失字段均返回 `INVALID_HANDSHAKE`；所有拒绝用例断言 Forwarder 为 nil、Gateway 物理计数归零且逻辑 handler 未被调用。全量 Go test/vet、关键 contract race 和全部 E2E 编译通过。

- [x] **V2-106：定义 Storage Repository。**
  - 为 Principal、Token Family、Session、Task、Resource Snapshot、Idempotency Key 和 Audit Event 定义窄接口。
  - 业务代码不得依赖 SQLite/PostgreSQL 专属类型或拼接数据库方言。
  - 明确每个写操作的事务、唯一约束、并发和过期清理语义。
  - 2026-08-09：已定义带 schema version 的领域对象，以及 Principal、Token Family、Session、Task、Resource Snapshot、Idempotency、Audit 和 TransactionManager 窄接口；接口注释固定身份唯一性、乐观并发、幂等冲突、有限批量清理和 append-only 审计语义。

- [x] **V2-107：实现默认 SQLite Backend。**
  - 启动时创建目录、设置安全文件权限、执行 migration，并校验数据库完整性。
  - 配置 busy timeout、foreign keys、journal/synchronous 策略和连接数上限。
  - 启动时拒绝明显不安全的多副本配置；健康接口报告数据库状态但不暴露路径。
  - 覆盖进程崩溃、写入中断、磁盘已满、只读卷和 migration 失败测试。
  - 2026-08-09：Controller 已实际打开 SQLite，安全创建目录和 `0600` 数据库文件，拒绝符号链接及不安全多副本，启用 busy timeout、foreign keys、WAL、`synchronous=NORMAL` 和单连接，执行事务 migration/quick check，并将数据库状态接入 readiness。全部 Repository、共享事务、乐观并发、幂等冲突、有限批量清理和 append-only 审计均已实现；测试覆盖持久化、并发写、损坏数据库、高版本 schema、migration 回滚、只读卷、磁盘满和未提交事务进程退出恢复。

- [x] **V2-108：实现外部 PostgreSQL Backend。**
  - DSN 只从 Secret、环境变量或文件引用加载，日志中必须脱敏。
  - 支持 TLS、连接池、连接/查询超时、事务重试和健康检查。
  - 通过数据库约束实现 ID、幂等键和身份唯一性，不只依赖进程内检查。
  - 2026-08-10（完成）：Controller 通过 pgx stdlib + Bun PostgreSQL dialect 使用外部 PostgreSQL，TLS 默认 fail-closed；DSN 仅从 Controller 环境、SecretKeyRef 或文件加载，不进入 Data Plane、ConfigMap、参数或错误文本。连接池、连接寿命、连接超时、服务端 `statement_timeout`、readiness 与关闭均可配置；驱动 cause 仅供内部 SQLSTATE 分类，公开/日志错误保持脱敏。migration 将 `schema_migrations` 创建也纳入 advisory-lock 事务，真实 PostgreSQL 验证 6 个 Controller 并发首次启动。共享事务使用 `SERIALIZABLE`，仅对 `40001`/`40P01` 做至多十次可配置的有界指数退避重试。数据库 PK/UNIQUE/FK 约束覆盖 ID、身份、Refresh Token、Task 幂等键和通用幂等记录。Helm 只向 Controller 注入 DSN Secret 和池/超时/重试配置，PostgreSQL HA 使用 RollingUpdate 且不创建 PVC。真实 PostgreSQL 17 测试通过全部共享 Repository conformance、约束、回滚、查询超时、并发 migration、serialization retry 及 race；全量 Go test/vet、前端生产构建、Helm lint/SQLite 与 PostgreSQL HA template 和 `git diff --check` 均通过。

- [x] **V2-109：建立跨数据库 Conformance Test。**
  - 同一组 repository、migration、事务回滚、并发写、过期清理和恢复测试同时运行在 SQLite 与 PostgreSQL。
  - 两种 Backend 产生相同领域结果和稳定错误，不要求底层 SQL 完全相同。
  - 2026-08-10（完成）：`testRepositoryConformance` 将 Token Family/Refresh Token、Session/Task/Snapshot、Idempotency、Audit、认证事务和共享事务的同一套用例运行于 SQLite 与真实 PostgreSQL；共同验证数据库约束、稳定 `ErrNotFound`/`ErrConflict`/`ErrIdempotencyMismatch`、提交/错误回滚/恐慌回滚、单次消费、乐观并发、16 路身份与幂等写竞争及有界过期清理。`testTaskStateMigrationPreservesLegacyTasks` 在两种后端从历史 schema v5 重放 v6 并产生相同领域状态；两者都验证 migration 失败不记录版本、高版本拒绝，PostgreSQL 另验证 6 路并发首次 migration。`testRestartRecovery` 对两种后端关闭再打开后检查已提交数据恢复、未提交数据不复活和 readiness；PostgreSQL JSONB 读取会重新规范化 NetworkSpec，保证领域结果与 SQLite 字节稳定。新增 `scripts/test-postgresql.sh`：可使用外部 DSN，或自动创建并清理独立 PostgreSQL 17 Podman/Docker 容器，以 race 模式运行整个 Storage suite。SQLite 默认测试、真实 PostgreSQL 全套 race、全量 Go test/vet 与 `git diff --check` 通过。

- [x] **V2-110：实现数据库导出、导入和备份工具。**
  - 支持 SQLite 一致性备份、逻辑导出、空 PostgreSQL 导入和导入前校验。
  - 导出文件带 schema version、校验和和创建版本，不包含 OIDC/AD/数据库 Secret。
  - 导入是显式、可审计、失败可回滚的离线管理操作。
  - 2026-08-10（完成）：新增 `kubeloop-controller storage export|import|backup` 离线命令；数据库配置仍只从环境或 Secret 文件读取，不接受 DSN 参数。逻辑导出在 SQLite/PostgreSQL 单一快照内按稳定主键顺序生成固定格式，记录格式/schema version、UTC 时间、Controller 创建版本、源后端和规范化内容 SHA-256；精确校验表列、UUID、对象版本、时间、状态、NetworkSpec/哈希和 JSON，并明确排除 OIDC/AD 配置、数据库 Secret 及 state/nonce/PKCE verifier 等短期认证事务。SQLite 备份使用 `VACUUM INTO`，发布前执行 `quick_check`、schema 校验、SHA-256、`0600` 权限和原子发布，拒绝覆盖与符号链接。导入只允许显式 `--actor --confirm-empty` 的空 PostgreSQL：连接前验证完整文件，事务内对全部业务表加排他锁、复核空库、按外键顺序写入并追加 `storage.import` 审计，任何失败整体回滚。默认测试覆盖确定性、篡改/截断/错误版本、Secret 排除和 SQLite 一致性；真实 PostgreSQL 17 race 测试覆盖 SQLite→PostgreSQL、再导出、非空拒绝、成功审计和外键失败后全表为空。运维流程见 `docs/v2-storage-management.zh-CN.md`；全量 Go test/vet、CLI/Storage race、前端生产构建、Helm lint 与 SQLite/PostgreSQL HA template、`git diff --check` 均通过。

### M2：Helm 与部署基线

- [x] **V2-200：创建 Helm Chart。**
  - 创建 Controller 与 Data Plane 两套 Deployment、Service、ServiceAccount、ConfigMap、Secret 引用和 NOTES。
  - Values 提供 image、replicas、resources、log level、public URL、认证、授权和数据库配置。
  - SQLite 默认创建持久卷并强制 Controller 单副本/Recreate；PostgreSQL 模式允许多副本/RollingUpdate。
  - Data Plane 始终独立扩缩容，不挂载 Controller 数据库卷或认证 Secret。
  - 2026-08-10（完成）：Chart 已提供 Controller/Data Plane 独立 Deployment、Service、ServiceAccount、ConfigMap、existing Secret 引用、镜像、资源、扩缩容、同源 public URL、NOTES 和组件级运维配置。Values 已接入 OIDC/私有 CA、AD/LDAPS、Token/Relay 密钥、默认拒绝授权策略、可选 Kubernetes impersonation 映射、SQLite/PostgreSQL，以及两组件独立 `debug|info|warn|error` 日志级别；二进制启动时再次校验日志级别，Controller 与 Data Plane 均输出结构化 JSON，Data Plane 的旧 `log.Logger` 接口通过固定 info/error 级别适配且致命错误不会被过滤。SQLite 强制单 Controller/Recreate/PVC，PostgreSQL 在关闭当前进程内 Relay Registry 后允许多副本/RollingUpdate 且不创建 PVC；Data Plane 始终独立 RollingUpdate/扩缩容，不接收数据库环境变量、PVC、OIDC/AD/Token Secret 或通用 ServiceAccount Token。Helm contract tests 覆盖资源/Secret 边界、两种存储策略、认证授权配置和非法 log level fail-fast；全量 Go test/vet、相关 race、前端构建、Helm lint、SQLite 与 PostgreSQL HA template、`git diff --check` 通过。

- [x] **V2-201：设计最小 RBAC。**
  - 按只读 Inventory、exec/file、流量工作流拆分权限说明。
  - 默认不授予 secrets 读取、nodes/proxy、任意 impersonate 或通配写权限。
  - 提供 namespace-scoped 与 cluster-scoped 两套安装模式。
  - Data Plane 使用独立 ServiceAccount，默认 `automountServiceAccountToken: false`。
  - 2026-08-10（完成）：Controller RBAC 已按 platform、release namespace 内的 Relay Registry、只读 Inventory、exec/file 和 traffic 五个权限组拆分，各工作流组可独立关闭。默认 `cluster` 模式为 Inventory、exec/file、traffic 分别创建最小 ClusterRole/Binding；`namespace` 模式只在显式 namespace allowlist 中创建对应 Role/Binding，工作流写权限不会泄漏为集群级授权，同时保留 Namespace/Node/ServiceCIDR 只读发现、SelfSubjectAccessReview 和按需 TokenReview 所需的窄 ClusterRole。删除未使用的 Pod logs、apps workload、watch 等权限，默认明确禁止 Secret、`nodes/proxy`、`impersonate`、通配资源/API group/verb；Relay Pod 读取固定限制在 Helm release namespace。Data Plane 继续使用独立 ServiceAccount、`automountServiceAccountToken: false`，且不会成为任何 RBAC Binding subject。Values、权限矩阵、namespace 模式残余集群只读权限、DNS best-effort 行为和外部 impersonation 责任已写入 Chart 文档；Helm contract tests 覆盖分组、开关、作用域、非法/重复 namespace fail-fast、危险权限缺失和 ServiceAccount 边界。`go test ./...`、`go vet ./...`、`go test -race ./...`、前端构建、Helm lint、cluster/namespace/SQLite/PostgreSQL HA template 与 `git diff --check` 通过。

- [x] **V2-202：配置外部入口。**
  - 支持 Ingress 和 Gateway API HTTPRoute，允许二选一。
  - 验证 WebSocket upgrade、长连接超时、请求体限制和 TLS。
  - `publicURL` 必须与 OAuth callback 和服务发现地址一致。
  - 同一 Origin 下将 `/.well-known`、`/auth`、`/api` 路由到 Controller，将 `/tunnel` 路由到 Data Plane。
  - 2026-08-10（完成）：Helm 现支持互斥的 `networking.k8s.io/v1` Ingress 与 Gateway API v1.2+ `HTTPRoute`；HTTPRoute 可附着平台已有 Gateway，或由 Chart 创建带 HTTPS listener、TLS Terminate 和既有证书 Secret 的专用 Gateway。两种入口都以同一 hostname 将 `/.well-known`、`/auth`、`/api` 路由至 Controller，将精确 `/tunnel` 路由至 Data Plane；Service `appProtocol` 明确标识 HTTP 与 `kubernetes.io/ws`。Helm 对非 Origin `publicURL`、路由 hostname 不一致、Ingress 未启用 TLS、Ingress/HTTPRoute 同时启用、缺失 Gateway parent/HTTPS section、缺失 GatewayClass/证书和非零 tunnel timeout 全部 fail-fast。Controller 与客户端也只接受无 path/query/fragment 的单一 Origin；Discovery 原样发布该 Origin，OIDC redirect 固定由它派生为 `/auth/callback/<providerID>`。HTTPRoute Controller 请求保持 30 秒边界，WebSocket request/backend timeout 使用规范定义的 `0s`，由 Data Plane 自身 idle timeout 接管。新增 TLS 1.3 同源反向代理集成测试，真实验证 discovery/Controller/Data Plane 分流、请求体上限、WSS Upgrade，以及连接跨过普通 HTTP write timeout 后仍能双向传输；Helm contract tests 覆盖 Ingress、外部 Gateway、Chart-owned Gateway、TLS/Origin/互斥/timeout 校验和 OAuth callback 一致性。Ingress controller 专属的 WebSocket/超时/body-size annotations 与 Gateway API extended-conformance 要求已写入 Chart 运维文档。

- [x] **V2-203：加入运行安全基线。**
  - 非 root、只读 root filesystem、drop capabilities、seccomp、禁止 privilege escalation。
  - 添加 NetworkPolicy、ServiceMonitor 可选项和拓扑分散选项。
  - PodDisruptionBudget 默认只用于 PostgreSQL 多副本模式；SQLite 单副本模式不创建会阻止维护驱逐的 PDB。
  - NetworkPolicy 禁止 Data Plane 访问 Controller 数据库和 Secret 相关服务；Controller 不提供任意目标拨号能力。
  - 2026-08-10（完成）：Controller、Data Plane、Operator 三个 Deployment 统一默认 `runAsNonRoot`、只读 root filesystem、`drop: ALL`、`RuntimeDefault` seccomp 和禁止 privilege escalation；Data Plane 继续 `automountServiceAccountToken: false`，仅 Registry 模式显式投影受 audience/Pod 绑定的短期 token。默认入口 NetworkPolicy 已覆盖 Controller/Data Plane；新增可选 restricted egress 模式，启用后必须分别提供 Controller、Data Plane、Operator 的显式 allow rules，否则 Helm fail-fast。DNS peer 可配置，Data Plane 只自动允许同 release Controller Relay Registry 端口；业务 namespace/Pod 或 CIDR 需管理员显式列入，文档要求排除 Kubernetes API、cloud metadata、Controller 数据库和身份服务，从网络层避免 Data Plane 获得 Secret/API/数据库通路，同时应用层继续以 Gateway Policy 与 Session NetworkSpec 限制目标。三组件均新增独立 `topologySpreadConstraints`。可选 Prometheus Operator `ServiceMonitor` 只抓取 Data Plane 无用户/目标高基数标签的聚合 `/metrics`。`policy/v1` Controller PDB 仅在 PostgreSQL 且副本数大于一时默认创建，并拒绝 `minAvailable < 1` 或 `minAvailable >= replicas`；SQLite 永不创建阻止维护驱逐的 PDB。Helm contract tests 覆盖三组件安全上下文、默认/受限 NetworkPolicy、显式规则 fail-fast、ServiceMonitor、拓扑分散、PostgreSQL HA PDB 和 SQLite 无 PDB。

- [x] **V2-204：实现健康检查。**
  - `/health/live` 只表示进程存活。
  - `/health/ready` 验证配置和必要依赖，但不执行昂贵 Kubernetes 全量查询。
  - 指标不得包含 Token、用户邮箱、目标地址等高基数敏感标签。
  - 2026-08-10（完成）：Controller、Data Plane 与 Operator 的 Helm Deployment 均具有独立 liveness/readiness 探针，并由 Helm contract test 固定路径契约。Controller `/health/live` 只报告进程存活；`/health/ready` 在统一超时边界内验证 State Store、已配置身份 Provider 与 Kubernetes `/version`，失败仅返回 `unavailable`，不泄露数据库 DSN 或依赖错误，也不执行 Inventory 全量查询。Data Plane `/health/live` 不受外部依赖影响；`/health/ready` 同时验证本地 tunnel runtime、WebSocket runtime，以及 Registry 模式下已确认且未过期的 Relay 注册租约，heartbeat 失败、租约过期或未注册会进入 `unavailable`，主动优雅退出则独立报告 `draining`。Operator 使用 controller-runtime `/healthz` 与 `/readyz`。Data Plane `/metrics` 新增无标签的 readiness gauge，并仅保留 readiness、排空状态、逻辑连接和物理 WebSocket Session 数等聚合指标；契约测试明确拒绝 Token、email、Principal、Session ID、target/endpoint 等敏感或高基数字段。Relay Agent、operations handler、命令装配与 Helm 探针均已有覆盖。

- [x] **V2-205：建立 Helm E2E。**
  - CI 在临时集群执行 install、upgrade、rollback、uninstall。
  - 默认 SQLite 与外部 PostgreSQL 两种模式分别执行安装、升级和数据保留测试。
  - 验证 CRD 为零依赖，或在未来引入 CRD 时单独验证兼容策略。
  - 卸载后不得残留 KubeLoop 创建的临时业务资源。
  - 验证 Controller 与 Data Plane 可以独立升级、扩缩容和失败重启。
  - 2026-08-10（完成）：新增受 context 与显式 opt-in 双重保护的 `e2e/helm/run.sh`、根 Makefile `helm-test-e2e`/镜像构建/Kind 清理入口，以及 CI `Helm Lifecycle E2E (Kind)` job。套件在两个隔离 namespace 真实安装完整 Chart：SQLite 模式启用动态 Relay Registry，以 PVC UID、非空数据库文件和持久标记验证三组件逐个升级、回滚及 Pod 重建后数据不丢失；Data Plane 与 Operator 分别扩缩容并断言其余组件 Pod UID 不变，Controller/Data Plane/Operator 均执行删除 Pod 后 readiness 恢复。PostgreSQL fixture 强制 TLS，Controller 从一副本独立扩到两副本、回滚并重启，以真实 SQL 表记录验证外部数据库数据保留，同时断言 Data Plane/Operator 不重启。CRD 测试固定首次安装、第二 release、升级、回滚和卸载期间 UID 不变，验证 Helm `crds/` 的 install-only/retain 语义，确认不存在 TrafficBinding 后才显式删除；卸载断言按本次 release instance 等待 namespaced 与 cluster RBAC 全部清空，不会误删集群已有 KubeLoop 测试资源。生产 Controller/Data Plane Dockerfile 已改为复制当前 `internal` 包布局，Operator 构建层也与根模块统一；CI race 路径同步迁移至 `internal/protocol/...`。完整套件已在 Minikube Kubernetes v1.35.1 实跑通过并确认测试 namespace/CRD 清理完成，原有 Deployment 未受影响。

### M3：OIDC/AD、授权与审计

Gateway 使用统一的 `AuthProvider` 抽象，首期支持：

- `oidc`：标准 OIDC Provider，包括 Keycloak、Dex、Microsoft Entra ID 和支持 OIDC 的 AD FS。
- `ldap`：面向没有 OIDC Provider 的本地 Active Directory，只允许 LDAPS 或经过严格校验的 StartTLS。
- `static-token`：仅用于开发或受控环境。
- `anonymous`：仅用于显式解锁的本地开发环境。

如果企业 AD 已接入 Entra ID、AD FS、Keycloak 或 Dex，应优先使用 OIDC。原生 LDAP 登录缺少通用 MFA、条件访问和浏览器 SSO，只作为兼容方案。V2.0 不直接实现跨平台 Kerberos/IWA 桌面单点登录。

- [x] **V2-299：定义统一 AuthProvider 接口。**
  - Provider 负责返回登录方式、验证身份并生成标准化 Principal，不直接签发 Gateway Access Token。
  - Token Service、Authorizer 和 Session Registry 不依赖具体 OIDC/LDAP SDK。
  - discovery 只返回可公开的 Provider ID、类型、显示名称和登录交互类型。
  - 2026-08-10：已实现 Provider/BrowserProvider/PasswordProvider/TokenProvider/AnonymousProvider 能力接口、标准 Identity、稳定 OIDC/AD/开发身份键和 fail-closed Registry；discovery 明确返回 `browser`/`password`/`token`/`none` 交互类型并拒绝重复 Provider ID 或类型不匹配。

- [x] **V2-300：实现 OIDC Broker 配置。**
  - Helm 配置 issuer、client ID、Secret 引用、scope、claim mapping 和固定 callback URL。
  - 启动时验证 discovery document、issuer、算法和必要 claims。
  - 禁止通过客户端请求动态覆盖 issuer。
  - 2026-08-09：已接入标准 OIDC discovery/JWKS verifier，实现 issuer、HTTPS endpoint、PKCE S256、签名算法白名单和 required claims 启动校验；Authorization Code exchange 校验 PKCE、签名、audience、expiry、nonce 并映射标准 Identity，完整模拟 IdP 测试覆盖私有 CA HTTP client 复用。Helm 支持 issuer、client ID、scope、claim mapping、算法、超时，以及现有 Secret 中的 client secret/私有 CA 只读投影；公开配置与 Secret 分离且不进入 Data Plane。

- [x] **V2-309：实现 Active Directory/LDAPS Provider。**
  - Helm 配置 AD 地址、Base DN、用户/组搜索规则、CA Secret 引用和可选只读 Bind Account Secret。
  - 禁止默认明文 LDAP；连接必须验证服务端证书和主机名。
  - 用户密码只用于当次 bind，不写入 Store、Token、日志、指标或审计事件。
  - 验证禁用、锁定、过期账号，并为搜索和 bind 设置严格超时与限流。
  - 支持将 AD group 映射为标准化 Principal groups；嵌套组行为必须显式配置和设置深度上限。
  - 验收：错误密码、禁用账号、证书错误、目录超时、LDAP 注入和暴力尝试均安全失败。
  - 2026-08-09：已实现 LDAPS/严格 StartTLS Provider，启动时验证目录连接和可选只读 Bind Account；用户搜索对输入执行 RFC 4515 转义且最多返回一个账号，身份主键使用 directory ID + objectGUID/SID。登录检查 userAccountControl、lockoutTime、accountExpires 和 pwdLastSet，使用独立 TLS 连接执行用户 bind，并清零密码字节缓冲；支持直接/有限深度嵌套组与数量上限。HTTP 密码入口按账号哈希和来源限流，Helm 将 Bind Password/CA 只读投影到 Controller，测试覆盖错误密码、账号状态、注入、目录失败、暴力尝试和 Data Plane Secret 隔离。

- [x] **V2-301：实现桌面登录流程。**
  - 客户端生成 state、nonce、PKCE verifier/challenge 和本地 loopback callback。
  - Gateway 完成 IdP callback 后只签发短时、单次、绑定 PKCE 的 exchange code。
  - 严格限制 callback 为允许的 loopback 或注册的应用 scheme。
  - 验收：重放 code、篡改 state、错误 verifier、过期 code 全部失败。
  - 2026-08-09：Controller 使用共享数据库保存短时登录事务和一次性 exchange code，state/code 仅持久化 SHA-256；桌面端已接入随机 `127.0.0.1` 端口的 loopback listener、系统浏览器调起、state/nonce/PKCE 和 exchange。桌面 PKCE 与 Gateway→IdP PKCE 分层独立，测试覆盖篡改 state、错误 verifier、重放与超时。

- [x] **V2-310：实现客户端认证方式发现。**
  - OIDC Provider 显示“使用浏览器登录”，LDAP/AD Provider 显示 Gateway 返回的组织名称和账号登录表单。
  - 多 Provider 场景先选择登录方式；只有一个 Provider 时直接进入对应流程。
  - AD 密码只提交到当前 Server Profile 的 HTTPS Origin，并在请求完成后从 UI 状态清除。
  - TLS 错误或非 HTTPS 远程地址下禁止提交 AD 密码。
  - 2026-08-09：类型化客户端会先读取并校验 discovery，同源校验通过后才允许认证。首次使用页支持单/多 Provider，OIDC 显示浏览器登录，AD 显示组织名称和账号表单；AD 密码请求完成后立即从前端状态与后端字节缓冲清除，远程 HTTP 地址在发请求前即被拒绝。

- [x] **V2-302：实现 Token 生命周期。**
  - Access Token 短期有效；Refresh Token 支持轮换、撤销和复用检测。
  - 客户端 Token 保存在系统安全存储，不写入普通 JSON Store 或日志。
  - 退出登录立即关闭相关 WSS 和 Cluster Session。
  - 2026-08-10：Controller 已实现 Ed25519 签名短期 Access Token、按 Device 的 Refresh Token Family、每次刷新轮换、历史哈希、并发消费保护、旧 Token 复用整族撤销、显式撤销和每次 API 认证时的 Family 状态校验；复用检测与整族撤销在同一数据库事务中提交，并验证 SQLite 上 Controller 重启前后的验证、轮换与撤销状态连续性。Helm 从只读 Secret 加载 PKCS#8 签名密钥。客户端使用版本化系统 Keychain/Credential Manager/Secret Service 条目保存 Access/Refresh Token 和 device ID，JSON Store 与 Wails 返回值不含 Token；刷新会原子切换凭据版本，退出会依次停止文件、Exec、SSH、端口转发、Exchange、Mirror、Preview，关闭 Data Plane WSS 和远端 Cluster Session，再撤销 Token 并删除本地凭据。单元测试覆盖并发 refresh 只有一个成功且整族撤销、显式注销断连幂等性、凭据部分写入回滚；Data Plane E2E 覆盖 Token Family 撤销后活动操作终止。

- [x] **V2-303：提供开发认证模式。**
  - `static-token` 仅用于开发/受控环境，并在 discovery 中明确标记。
  - `anonymous` 默认关闭，开启时打印高可见度安全警告。
  - 2026-08-10：新增显式 `developmentMode` 门控，配置和 Helm 默认均为关闭；未解锁时 `static-token`/`anonymous` Provider 启动或渲染直接失败。static token 必须来自 Controller-only Secret 且至少 32 字符，Provider 只保留 SHA-256 摘要并常量时间比较，服务端和桌面端均在请求后清空可变凭据缓冲；Secret 不进入 ConfigMap、Data Plane、日志或 discovery。anonymous 每次 Controller 启动输出包含 `SECURITY WARNING` 与 `production_safe=false` 的醒目告警。两种登录方式在 discovery 中分别标记为 `static-token/token` 与 `anonymous/none`，桌面 UI 明确显示开发/危险提示；登录后统一签发标准短期 Access/Refresh Token Family，继续经过同一 Policy、审计、Session、RelayTicket 和 WSS 链路。测试覆盖默认拒绝、配置缺失、错误 Token、输入清零、标准 Token 刷新、客户端同源提交、Helm Secret 隔离及 anonymous 启动告警。

- [x] **V2-304：实现 Principal 和身份映射。**
  - 标准化 subject、issuer、display name、email 和 groups。
  - OIDC 用户主键使用 `issuer + subject`；AD 用户主键使用目录 ID + objectGUID/SID，禁止使用可变 email 或登录名作为唯一身份。
  - 2026-08-09：OIDC callback 以 `issuer + subject`、AD 登录以 `directory ID + objectGUID/SID` Upsert 稳定 Principal；display name、email、groups 仅作为可刷新属性，不参与身份唯一性。两个 Provider 均经过同一个 Principal Repository、Token Service 和后续授权入口。

- [x] **V2-305：实现 Gateway Policy。**
  - Policy 至少按 group、namespace、operation 和 resource kind 限制。
  - 默认拒绝；拒绝结果不得暴露未授权资源是否存在。
  - 配置更新必须校验并原子生效。
  - 2026-08-10：`docs/adr/0003-gateway-authorization.md` 已冻结 allow-only/默认拒绝模型；Rule 对 subject/group、namespace、operation 和 resource kind 做合取匹配，广泛授权必须显式使用 `*`，集群级作用域使用 `$cluster`。Policy 严格解码并限长，只有完整编译校验通过后才原子替换；失败更新保留上一版本。Helm 默认生成空规则 deny-all，Policy 只投影到 Controller。

- [x] **V2-306：实现 Kubernetes Impersonation（可配置）。**
  - Claims 映射只能来自可信 issuer 和显式允许的 claim。
  - Chart 不默认授予宽泛 impersonate 权限。
  - 验证 API Server audit 中可以看到最终用户和 Gateway 身份。
  - 2026-08-10：Kubernetes Provider 已支持默认关闭的 impersonation，用户名由固定前缀加稳定 principal ID 生成，组只来自显式 identity-group → Kubernetes-group 映射；未映射 claim 不会透传。派生 client 保留 Gateway ServiceAccount 凭据并发送最终 `Impersonate-User`/`Impersonate-Group`，System client 始终清空 impersonation。Chart contract test 固定默认及启用映射时都不生成任何 `impersonate` RBAC。Helm Minikube E2E 新增 API Server metadata audit policy：通过真实 Controller anonymous 开发登录取得标准 Token，临时创建精确用户/组的外部 RBAC，调用 `/api/v2/version`，随后从 audit event 同时断言 authenticated `user` 为 Controller ServiceAccount、`impersonatedUser` 为前缀化稳定 Principal、映射组存在且未映射 claim 不存在；临时 RBAC 在验证后删除，生产 Chart 保持最小权限。

- [x] **V2-307：实现统一授权中间层。**
  - HTTP handler、Task 创建和 WSS stream open 必须调用同一个 Authorizer。
  - 任何 feature manager 不允许绕过 Authorizer 直接创建流量。
  - 2026-08-10（完成）：`/api/v2` 在任何业务 handler 前只调用同一个 `authorization.Authorizer`，并将规范化授权请求与命中 Rule 作为授权证明写入 context；拒绝发生在资源查询前且统一返回 `FORBIDDEN`。`APIRouter` 对 exact route、feature prefix 与 fallback 统一要求该允许证明，因此直接调用 Router 也无法绕过 Policy 进入 Task manager 或 WSS upgrade。契约测试覆盖 RelayTicket、Port Forward、Pod exec 创建，以及 Pod exec/file/exchange/mirror/preview stream 均恰好经过一次共享 Authorizer，并验证缺少证明时 feature handler 不会执行。RelayTicket 创建映射为独立的 `relay-tickets/create` 资源；Data Plane 不复制 Gateway Policy，而是只接受该授权结果签发且含 `tunnel` operation 的短期 Ticket，并将每个协议流绑定到 Ticket Session 与 NetworkSpec。

- [x] **V2-308：实现审计日志。**
  - 记录 request ID、principal ID、session ID、operation、namespace、resource、result 和 latency。
  - Token、命令输出、文件内容、OIDC claims 原文不进入审计日志。
  - 2026-08-10（完成）：所有 `/api/v2` 请求记录 request ID、principal ID、可信 session ID、operation、namespace、resource kind/name、授权 Rule、outcome、HTTP status 和 latency 到 append-only Audit Repository；拒绝也在资源查询前留下统一证据。真实 Task Repository 统一包装 `UpdateState/ClaimStale`，使 Port Forward、Pod exec、文件传输/管理、Exchange、Mirror、Preview 及其恢复 worker 的每次成功状态变化，都与 `task.transition` AuditEvent 在同一 SQLite/PostgreSQL 事务提交；audit append 失败会回滚状态 CAS。API transition 沿用框架 request ID，后台 transition 生成 `background-` correlation ID；事件只含 Principal/Task/Session ID、namespace、from/to state、source、outcome 与时间，不复制 Task spec/result、Token、OIDC claims、命令/输出、文件名/内容或网络 payload。SQLite/PostgreSQL 共用 conformance test 固定三个 lifecycle event 的字段与排序，Pod exec HTTP/WSS 测试固定 API/background correlation 及敏感字段白名单，故障注入测试证明 audit 表写失败时 Task 仍保持原状态。

- [x] **V2-311：实现 RelayTicket。**
  - Control Plane 签发短期 Ticket，绑定 principal、device、session、relay、operation、NetworkSpec hash、expiry 和 jti。
  - Data Plane 离线验证签名、audience、expiry、jti 和 stream scope，不查询业务数据库。
  - 支持签名密钥轮换、短期双公钥窗口和紧急吊销；私钥只存在 Controller Secret。
  - 验收：Ticket 重放、跨 Relay、跨 Session、扩大 operation、篡改 NetworkSpec 和过期使用全部失败。
  - 2026-08-10：已实现 Ed25519 紧凑签名 RelayTicket 与严格 JSON/base64url 解码；Ticket 绑定 issuer、relay audience、principal、device、Session、namespace、operation、可选 NetworkSpec SHA-256、iat/nbf/exp 和 jti，最大有效期两分钟。Controller 通过 `POST /api/v2/sessions/{id}/tickets` 在 Session 所有权/活动状态校验后签发；Data Plane 离线验签、原子消费有界 jti cache 防重放，并把 WebSocket 内所有协议租户键绑定到 Ticket Session。公钥文件支持最多八个 kid 的滚动双钥窗口，私钥只投影到 Controller；Helm 已移除静态共享 token 和默认 raw TCP 入口。篡改、过期、提前使用、未知 kid、跨 issuer/relay、scope 扩大、重复 jti 与跨 Session 协议键均有测试。

### M4：Kubernetes 控制面迁移

- [x] **V2-400：建立 Gateway Kubernetes Provider。**
  - Gateway 使用 ServiceAccount 或 impersonated rest.Config。
  - 为 client、informer 和 transport 设置超时、QPS/Burst、User-Agent 和 context cancellation。
  - 2026-08-10：Controller 使用独立 in-cluster ServiceAccount Config，统一设置 timeout、QPS/Burst、User-Agent 和 JSON transport；所有派生 Config 均为副本并清除上游遗留 impersonation。Provider 支持显式组映射的可选 impersonation，API 调用与 `/version` readiness 使用有界 context；竞态和模拟 API Server 测试覆盖取消、配置隔离及身份映射。

- [x] **V2-401：迁移 ServerVersion 和 Capability Probe。**
  - Gateway 返回经过授权的能力集合，而不是让客户端推测 RBAC。
  - 能力结果与 principal、namespace 和 Gateway 版本绑定。
  - 2026-08-10（完成）：`/api/v2/version` 同时返回 Kubernetes 与 Gateway 版本；namespace 级 `/api/v2/capabilities` 是带 schema version 的授权快照，显式绑定 principal、namespace 与 Gateway build。能力由完整 Gateway Policy 工作流与 Kubernetes `SelfSubjectAccessReview` 取交集，覆盖 inventory、Tunnel（Session + RelayTicket）、Port Forward、exec、file、exchange、mirror 与 preview。客户端仅保留默认 30 秒、128 项的有界内存缓存，键包含 Server Profile/地址、当前设备与 refresh credential 的单向摘要、principal、namespace 和 Gateway version；登录切换、Token 轮换、namespace 变化、Gateway 升级或 TTL 到期都会重新探测，且每个实际请求仍独立授权。单元测试覆盖缺失任一工作流策略即不发布能力、响应绑定校验、缓存副本隔离、TTL 失效、Gateway 版本变化、principal/credential 变化和 namespace 隔离。

- [x] **V2-402：迁移 Namespace/Pod/Service Inventory。**
  - 提供分页、过滤和 watch/resync 机制。
  - 不向用户返回无权限 Namespace 的名称或数量。
  - 慢客户端不会阻塞 shared informer。
  - 2026-08-10（完成）：Namespace、Pod、Service 的 list/get API 使用稳定最小 DTO，列表默认 200、最大 500，并透传受限 continue token/resourceVersion；新增长度与语法均受限的 Kubernetes label/field selector。Namespace cluster list 仅作为候选集合，返回前逐项要求同一 principal 具备该 namespace 的 capability probe Policy，避免泄漏无权限 namespace 的名称或数量。Pod/Service `watch=true` 使用认证 WebSocket 下发版本化完整快照，并按 principal/groups、namespace、resource 共享 informer；informer callback 只写入非阻塞 dirty signal，每个客户端只有单槽 latest-snapshot mailbox，慢客户端丢弃中间态但不会阻塞 shared informer，30 秒 resync 会修复状态。Watch 最晚在 Access Token 到期时关闭，桌面以刷新后的凭据重连；客户端 SDK 校验 schema/resource/namespace/sequence 和跨 namespace 对象，Wails 事件桥只把当前 Profile/namespace 快照增量应用到 Server 页面。单元、WebSocket HTTP 集成及 race 测试覆盖策略过滤、selector、共享 feed、慢订阅者、认证快照与客户端绑定校验。

- [x] **V2-403：迁移集群网络发现。**
  - Gateway 发现 Pod CIDR、Service CIDR、Service IP、CoreDNS 和 cluster domain。
  - 返回客户端安装本地 route/DNS 所需的最小、已校验 NetworkSpec。
  - 客户端仍负责检测该 NetworkSpec 与本地网络的冲突。
  - 2026-08-10（完成）：Controller 使用 principal client 读取授权 namespace 的 Pod/Service，并使用 Controller system client 读取 Node PodCIDR、`ServiceCIDR` API，以及 `kube-system` 中固定名称的 kube-dns/CoreDNS Service 与 ConfigMap。Helm 仅授予 `kube-dns`/`coredns` 两个对象的 Service/ConfigMap `get` 权限；Corefile 在 256 KiB 边界内解析，只提取最多 16 个通过 DNS-1123 校验的 `kubernetes` plugin zone，内容永不返回客户端，缺失、过大、格式异常或无权限时安全降级为 `cluster.local`。发现结果经过特殊地址、重叠、数量、排序和去重校验，生成版本化 canonical JSON 与 SHA-256。桌面只消费 Session 返回值并检查本机路由冲突，不读取 kubeconfig；Data Plane 在实际拨号前以该快照限制 Pod CIDR、精确 Service IP、DNS 端口和 cluster domain，并拒绝 metadata、API Server、Node/公网及仅落入 Service CIDR 的地址。单元、race、Helm RBAC 契约和全量非 E2E 回归通过。

- [x] **V2-404：实现 Cluster Session API。**
  - `POST /api/v2/sessions` 创建 Session，返回 `sessionID`、能力、NetworkSpec 和过期时间。
  - 支持 get、heartbeat、disconnect 和幂等 create。
  - Session 与 principal/device 绑定，其他用户不能查询或停止。
  - 2026-08-10（完成）：已实现 namespace query 预授权后的 create/get/heartbeat/disconnect；创建使用 `Idempotency-Key`，状态更新使用 generation/`If-Match` 乐观锁。Session 绑定 principal、device、cluster 和 namespace，TTL heartbeat 续期受绝对最大生命周期限制，过期会话不可复活；所有权不匹配统一返回 404。创建时由服务端发现 NetworkSpec，canonical JSON/hash 经 migration v5 持久化并随全部生命周期响应返回；RelayTicket 只绑定该持久化摘要，客户端自报摘要会被拒绝。创建响应同时携带由 `/api/v2/capabilities` 同一发现路径生成的完整 capability snapshot，使用共享 `internal/protocol/capability` 契约校验 schema、principal、namespace、Gateway version、条目边界与去重；客户端验证后直接填充按认证身份隔离的短期能力缓存，heartbeat/disconnect 只保留本地副本且每个实际操作仍重新授权。单元、客户端契约、关键 race、E2E build-only 编译和全量非 E2E 回归通过。

- [x] **V2-405：实现服务端 Session Registry。**
  - 管理 Session、Task、Stream、反向监听和 Kubernetes 资源所有权。
  - 使用 context tree 和逆序清理；重复 disconnect 必须幂等。
  - 进程退出时执行有界清理，并保留可恢复的资源归属标记。
  - 2026-08-10（完成）：Session Repository 已迁移 namespace/last-heartbeat 字段并支持并发安全续期；桌面 Remote Session Manager 复用失败创建的幂等键，定时 heartbeat，切换 namespace 时先断开旧 Session，并在 logout/profile delete/app shutdown 时执行有界 disconnect。Controller 新增并发安全的 `sessionregistry`，以进程→Session→Task→Stream 建立 context tree；exec、file、exchange、mirror、preview 的共享授权 lease 全部挂载到树上，disconnect 按逆注册顺序取消子节点，并等待 handler 关闭 WebSocket、Pod exec、反向 listener、socket 以及完成 Kubernetes 补偿和终态落库，重复 disconnect 幂等。Controller shutdown 使用同一根 context 和总超时等待释放。exec/file 活动 Task 持久化 owner heartbeat；通用恢复 worker 以 state+updated_at CAS 收敛失主 Task，Port Forward 只在 activation 失主或所属 Session 非活动时终止，随后由 TrafficBinding orphan reconciler 删除 CR；Exchange/Mirror/Preview 继续使用带资源快照的专用恢复器。过期 Session 的 maintenance pass 与数据库级联仍回收无资源快照的 Task，资源型 Task 先补偿再删除。ADR 0019 固化边界；并发、断开任务状态、失主/存活 owner、Port Forward Session 绑定、关键 race 与全量非 E2E test/vet 通过。

- [x] **V2-406：客户端接入 RemoteClusterBackend。**
  - Clusters 页面替换为 Server/Namespace 页面。
  - v2 模式下不调用 kubeconfig inventory、probe 或 Kubernetes client。
  - 加入测试，确保只配置 URL 的干净用户目录可以完成资源浏览。
  - 2026-08-10（完成）：桌面组合根统一使用 RemoteClusterBackend 所需的 Profile、Discovery、认证、Remote Session 和 Data Plane 组件，React 入口仅展示 Server/Namespace 远程资源流程。Inventory 按服务端 capability 获取 Pod/Service，只有 `cluster.tunnel` 可用时才建立 Data Plane，能力被移除时会主动断开旧连接。新增干净临时用户目录的全组合测试：仅保存 Gateway URL，经 anonymous discovery/login 后完成 Session 建立和 Pod 浏览，并验证不读取或生成 kubeconfig；依赖架构测试、全量非 E2E test/vet 与前端生产构建通过。

### M5：远程数据面迁移

- [x] **V2-500：将 WebSocket bearer token 替换为认证 Session。**
  - WSS 使用短期 RelayTicket，服务端验证 audience、issuer、expiry 和 session ownership。
  - Token 只允许出现在 Authorization header，不允许放入 URL query 或日志。
  - 2026-08-10：Data Plane 的静态 bearer 比较已替换为 RelayTicket RequestVerifier；每个物理 WebSocket 需要独立一次性 Ticket，客户端 `TokenSource` 可在连接池扩容/重连时即时获取。Data Plane 不记录 Ticket/claims，失败统一返回 401；Helm 默认仅暴露 WSS/HTTP 端口，legacy raw TCP 仅能通过显式进程配置开启。

- [x] **V2-501：扩展多路复用协议。**
  - 增加逻辑 stream 类型、流控、最大并发、idle timeout、half-close 和取消传播。
  - 一个异常 stream 不得导致其他 stream 或物理连接崩溃。
  - 2026-08-10：WSS 子协议升级为 `kubeloop-mux-v2`，每条 smux stream 内增加有界 64 KiB data frame 与显式 FIN frame，以独立表达应用层 half-close。该设计规避 smux 在双向原生 FIN 相遇时立即回收未读缓冲区、截断“请求 EOF 后返回响应”的问题；客户端、Gateway、SOCKS Bridge、Port Forward 和 intercept relay 均等待双向结束并传播 `CloseWrite`，Forwarder 关闭时会主动回收全部本地连接。服务端为每个逻辑 stream 配置独立的活动超时，默认 30 分钟并可通过 Gateway 参数或 Helm 调整；畸形 frame 或超时只关闭所属 stream。单元、完整 WSS/SOCKS 集成和真实 Minikube Gateway 重启前后请求均验证 half-close；同一物理 WSS 上分别验证 16 MiB 慢 stream、畸形 stream 和超时 stream 不影响健康兄弟 stream。

- [x] **V2-502：实现 Cluster Dial Stream。**
  - Gateway 校验目标属于授权 NetworkSpec 后才能拨号 Pod IP、Service IP 或集群 DNS。
  - 防止借 Gateway 访问 metadata、API Server、Node 管理端口或任意公网地址。
  - 2026-08-10：认证控制流在 Data Plane 注册 RelayTicket 绑定的 canonical NetworkSpec；后续 TCP/UDP stream 必须匹配同一 Session token 和摘要。域名只允许 cluster domain，解析后逐个过滤 IP；ServiceCIDR 仅作路由元数据，不作为拨号授权。测试覆盖任意公网、metadata、API Service、摘要不一致、混合 DNS 答案和未注册 Session 拒绝。

- [x] **V2-503：接入本地 SOCKS Bridge。**
  - 客户端的 SOCKS Bridge 将 cluster outbound 映射到认证 WSS stream。
  - TCP、UDP、DNS、取消和 backpressure 都有集成测试。
  - 2026-08-10：服务发现新增同源 `tunnelPath`，Helm 保证 Controller 下发路径与 Data Plane Ingress 路由一致。桌面 Data Plane Manager 使用每条物理 WSS 独立 RelayTicket 建立 smux 池，先确认授权控制流，再开放 loopback SOCKS5；Session/namespace 变化时替换运行时，logout、Profile 删除和应用退出时先关闭流量。客户端 WSS 传输已从 Gateway 服务端包提取到中立 transport 包，架构测试阻止 V2 SDK 依赖服务端或本地 Kubernetes runtime。真实 WSS→smux→Gateway→SOCKS 集成测试覆盖 TCP、UDP、指定 DNS Server:53、1 MiB 级背压和活动流取消传播。

- [x] **V2-504：接入 TUN/sing-box。**
  - Helper 继续只接收经过校验的本地 NetworkSpec。
  - Gateway 断线时停止或隔离 TUN，不允许静默回退到错误路径。
  - 恢复成功后不重复安装 route、DNS 或本地监听。
  - 2026-08-10：远程工作区提供显式 Enable/Stop TUN，复用现有字段受限的 `SessionSpec` 与受限 Helper，不向 Helper 传入 kubeconfig、路径或命令。启动前再次检查本机路由冲突，并把服务端 NetworkSpec 与已认证 loopback SOCKS endpoint 传给 sing-box；控制流丢失时把稳定 SOCKS Bridge 切到不可达目标以隔离 TUN，namespace 切换、logout、恢复终态失败和应用退出均关闭 TUN。修复启动调用返回时错误取消 sing-box 生命周期 context、导致真实 TUN 立即退出的问题；调用 context 现在只约束启动，成功后的 TUN 由 Data Plane Runtime 持有。真实 Helper/sing-box/系统 TUN E2E 通过直连 Kubernetes ServiceIP 验证网络中断期间不静默回退、generation 恢复与 Gateway Pod 更换后恢复访问，并确认全过程复用同一 Helper Session、不重复安装 route/DNS，显式 Stop 后 Helper 无残留 Session。

- [x] **V2-505：实现断线恢复。**
  - 区分物理 WSS 重连、用户 Session 过期、Gateway Pod 更换和认证 Token 过期。
  - 使用 generation 拒绝过期恢复结果。
  - 达到重试上限后清理本地网络并给出可操作错误。
  - 2026-08-10：控制流断开后先把稳定 SOCKS Bridge 切到不可用目标，关闭旧 WSS/smux，再通过 Controller 立即 heartbeat 获取权威 Session generation，并使用新 RelayTicket 原位替换 transport；成功恢复不更换 SOCKS 地址、不重启 TUN，也不重装 route/DNS。Session ID、NetworkSpec hash 或 generation 回退会拒绝恢复，401/403/404 不做无意义重试；有界指数退避耗尽后关闭 TUN、SOCKS 和本地网络。真实 Minikube E2E 先把稳定 loopback 入口切换到另一条 TCP 路径并强制关闭全部旧连接，再删除 Gateway Pod，两个阶段都验证 Session generation、Ticket 更新、原 SOCKS 地址恢复和集群访问。该测试发现并修复 `Runtime.Reconnect` 成功后错误取消 transport context、导致立即重连循环的问题。终态状态事件区分 `authentication_required`、`access_denied`、`session_expired`、`session_changed` 和 `network_unavailable`，并声明是否可重试；桌面在重连时明确显示流量暂停，失败后按原因提供“重试/新建 Session/重新登录/切换账号”，同时禁止错误状态下启动 TUN 或 Port Forward。race 回归验证失败后稳定 SOCKS 与 TUN 都已清理。

- [x] **V2-507：实现 Data Plane 排空和重选。**
  - 排空中的实例不接收新 Session，已有 Stream 在期限内继续运行。
  - 活动 TCP/WSS Stream 不宣称无损迁移；超时后明确断开并由客户端按 generation 重建。
  - Controller 重选 Data Plane 后签发新的 RelayTicket，旧 generation 不能重新发布为活动状态。
  - 2026-08-10：Data Plane 收到 SIGTERM 后先把 readiness 置为 draining，同时拒绝新物理 WSS 与新逻辑连接；已有 stream 在 `drainTimeout` 内继续，期限到达后明确关闭，`terminationGracePeriodSeconds` 默认留出额外清理时间。实例由稳定同源 Ingress/Kubernetes Service 在 ready Pod 间重选，Controller 继续作为 Session generation 与 RelayTicket 的唯一授权方，客户端无需感知 Pod 地址。RelayTicket schema v2、WSS Identity 和协议 tenant key 全部绑定 Session generation；Data Plane 使用有界、按 Ticket 过期清理的 high-water mark 拒绝旧 Ticket，较旧物理 WSS 在新 generation 出现后只能完成已接受 stream、不能再开新 stream。客户端恢复使用 CAS 防止并发旧结果重新发布 connected，并原子切换 SOCKS 的地址与 generation token。真实 Helper/TUN/Minikube E2E 在 Gateway 优雅删除期间验证慢 stream 于 5 秒窗口内完成，长驻控制流在期限后断开，随后 heartbeat、fresh Ticket、Pod 重建及原 TUN 直连 ServiceIP 恢复；Helper Session 全程不重装。ADR 0008 明确活动 stream 不宣称无损迁移。

- [x] **V2-506：数据面 E2E。**
  - 覆盖 TCP、UDP、DNS、大流量、慢消费者、半关闭、网络切换和 Gateway 重启。
  - 验证两个用户之间不存在 stream、指标或目标泄露。
  - 2026-08-10：本地真实 WSS/TCP/UDP/DNS 集成覆盖 1 MiB 大分块背压、16 MiB 慢消费者、half-close 与取消；同一物理 WSS 上慢 stream 不得阻塞兄弟 stream。Minikube Gateway Deployment/NodePort/RelayTicket E2E 使用 Python sidecar 在真实 ServiceIP 上停止消费 2 秒，期间独立健康 stream 必须在 1.5 秒内成功；随后把稳定 loopback 入口切换到另一条 TCP 路径、关闭旧连接并验证原 SOCKS 地址恢复，再强制删除 Gateway Pod验证第二次恢复。所有健康请求都显式 `CloseWrite` 后读取完整响应。同一测试还建立两个不同 principal/device/session 的数据面，验证各自访问、跨 Session token 拒绝和指标不泄露任何 principal、device 或 Session ID。

### M6：现有功能迁移

- [x] **V2-600：迁移 Port Forward。**
  - 资源解析和 Kubernetes 连接由 Gateway 执行，本地只保留监听端口。
  - Task 重连后应保持原 local port；端口被占用时返回明确冲突。
  - 2026-08-10：Controller 提供 Session/Principal 绑定的 `port-forward` Task API，写入使用 `Idempotency-Key`，Pod IP、Service ClusterIP、Service 端口与协议全部由持有 Kubernetes Provider 的 Controller 解析；Gateway Policy 以独立 `port-forwards` resource kind 授权。桌面只创建 `127.0.0.1` TCP/UDP listener，并经稳定 Data Plane SOCKS endpoint 转发；显式端口冲突会回滚远端 Task。namespace 切换、logout、Profile 删除和正常退出会同时关闭本地 listener 与远端 Task；进程崩溃无法发送 DELETE 时，由 Session heartbeat TTL 和 Controller 有界 maintenance pass 级联回收 Task。真实 Minikube E2E 通过实际 Controller API/Kubernetes Provider 解析同一 ClusterIP Service 的 TCP/UDP 端口，在网络路径切换和 Gateway Pod 优雅重建前后持续使用完全相同的两个 local port，且复用原 Data Plane/TUN；显式停止后验证 loopback 端口可重新绑定、SQLite Task 状态为 `stopped`。跨层崩溃测试串联 chi API、远程 SDK、本地 listener、SQLite 与 maintenance worker，验证本地 FD 丢失且无 DELETE 后 Task 随过期 Session 删除。

- [x] **V2-601：迁移 Pod exec 和终端。**
  - Gateway 选择 Pod/container 并创建 exec stream。
  - 支持 TTY resize、stdin/stdout/stderr、exit code、取消和超时。
  - 命令与输出默认不写审计日志。
  - 2026-08-10：Controller 提供 `pod-exec` Task 与单次 claim WebSocket stream；Pod/container 由 Gateway Kubernetes Provider 校验，实际 SPDY `pods/exec` 仅在 Controller 创建，Data Plane 不挂载 ServiceAccount Token。二进制协议分离 stdin/stdout/stderr、resize、close-stdin 和 exit status，客户端 SDK 支持 Access Token 刷新后的握手；普通 API 保留短超时，仅严格合法的 WebSocket Upgrade 使用 Session 生命周期。Gateway Policy/SSAR capability 使用 `pod-exec`/`pods.exec`，HTTP 审计只记录 Task/Session/策略结果，不记录命令内容或流数据。桌面 Go manager 持有凭据与 WebSocket，Wails 只暴露 profile/task 绑定的 stdin、resize、stop，并以事件向懒加载 xterm.js 传输原始字节；切换 namespace、logout、删除 Profile 和退出都会关闭活动流。活动流采用 Access Token、Token Family、Cluster Session 的最早失效边界，撤销或停止会取消 Kubernetes exec，终态落库后发送 cancelled exit。Controller Server 现为所有普通及已升级请求提供同一可取消根 context，并在 graceful shutdown 时先拒绝新请求、取消活动流、等待 WebSocket handler 完成状态落库，再允许关闭存储；ADR 0009 明确活动命令不自动跨进程重放。真实 chi 集成覆盖所有权、单次 claim、stdout/stderr/exit、重复连接、Token 撤销与客户端 manager；真实 Minikube Pod/SPDY E2E 覆盖 Token Family 撤销、PTY stdin、40x120 resize 真实到达容器、客户端 WebSocket 突然关闭后 Task 取消，以及 Controller 在同一地址重启前等待旧 Task 落为 `stopped`、重启后同一 Session 成功创建新 exec。

- [x] **V2-602：迁移 Pod SSH。**
  - SSH identity 验证和本地 SSH endpoint 留在客户端。
  - pods/exec 和容器选择移到 Gateway。
  - 验证跨用户不能访问其他用户创建的 SSH endpoint。
  - 2026-08-10：桌面端新增纯客户端 `clientv2/podssh` Manager，只在 `127.0.0.1` 分配临时端口并使用本机生成的 SSH key 执行 public-key-only 认证；端点严格绑定 Server Profile、当前 Cluster Session、namespace、Pod 和容器，每次 shell/exec/SFTP 操作都会重新确认同一活动 Session。SSH shell、命令、stdin/stdout/stderr、PTY resize 和 exit status 被适配到已有的认证 `pod-exec` WSS，实际 Pod/container 查询与 Kubernetes `pods/exec` 始终由 Controller Provider 执行，桌面依赖图继续禁止 `k8s.io/*`。Wails/React Workspace 支持选择 ready Pod/container、启停端点和打开本地终端；namespace 切换、logout、Profile 删除和应用退出都会关闭 listener 与活动 SSH 连接。ADR 0010 固化客户端/Gateway 信任边界。竞态测试使用两个独立本地用户密钥和相同 Pod IP，验证用户 B 无法认证用户 A 的端点而各端点可并存；真实 Minikube E2E 从本地 SSH 握手经实际 Controller API/SPDY 在 Pod 中执行命令，确认未授权密钥不会创建 Gateway Task、授权命令落库为终态，停止后 loopback 端口可立即复用。

- [x] **V2-603：迁移文件传输。**
  - 本地文件 IO 留在客户端，Pod tar/exec 留在 Gateway。
  - 实现流式上传/下载、进度、校验、取消、大小限制和路径安全检查。
  - 防止容器路径穿越和本地路径被远程参数任意指定。
  - 2026-08-10（完成）：已建立 Controller/客户端共用的中立二进制 `filestream` 协议，分离 data、complete、progress、result、cancel，单块数据硬限制为 256 KiB，结果错误限制为 4 KiB；进度使用 64-bit 字节计数，目录下载以 total=0 表示开始时总量未知，结果携带状态、已传输字节和可选 SHA-256。Controller 已提供 Session/Principal 绑定的 `file-transfer` Task 创建/查询与单次 claim WSS，支持 file/directory、upload/download、offset、overwrite 等统一模型；流复用 Access Token、Token Family、Cluster Session 租约，终态落库后才发送 result，重复领取返回 409。请求在访问 Kubernetes 前校验绝对 POSIX 路径、拒绝 `.`/`..`、控制字符、容器根、越界 allowed root、超限大小和非法 SHA-256；每次容器命令还会在 Pod 内解析 allowed root 与目标父目录的物理路径，拒绝父目录 symlink 逃逸，下载目标本身也不能是 symlink。上传大小/校验由流验证，下载大小/校验禁止客户端指定。Pod/container 由 Controller Provider 解析，幂等重放不重复访问 Kubernetes，`pods.files` capability 同时要求 Gateway Policy `file-transfers/create+stream` 与 Kubernetes `pods/exec/create`。Pod 侧命令全部由 Controller 固定生成并逐路径 shell quote；目录 tar 在解包前拒绝穿越、绝对路径、链接、特殊文件、重复/超量条目和超限内容，下载 tar 会重编码并清除不可信元数据。客户端类型化 SDK 已支持 Token 刷新后的 WSS 握手、大于默认 32 KiB 的 256 KiB data frame、并行进度、取消、字节数和 SHA-256 校验。桌面 V2 manager 将 Profile/Session 与本地绝对路径绑定：文件上传先固定 size/hash，目录上传先生成受限 tar 快照；下载写入目标同目录临时路径，校验后再事务化发布，客户端再次校验 tar 并拒绝逃逸、链接、特殊和重复条目。任务历史以 0600 的 `transfers-v2.json` 保存，重启时未完成项明确标为 interrupted；namespace 切换、logout、Profile 删除和退出会取消并等待流退出。Gateway Workspace 已提供 Pod/container、上传/下载、文件/目录、本地选择器、远端绝对路径、覆盖、实时进度、取消和历史 UI，且不调用 kubeconfig Provider。远端目录管理已新增 `pod-files/list` 与 Session/Principal 绑定的 `pod-file-operation` Task；create/rename/delete 强制 `Idempotency-Key`，授权分别映射 list/create/update/delete/get，`pods.files.manage` capability 要求全部 Gateway Policy 权限与 Kubernetes `pods/exec/create`。目录输出以 NUL 分隔并限制为 8 MiB/10,000 条，固定命令逐路径 quote，所有 mutation 拒绝 allowed root 和 symlink，父目录物理路径逃逸测试覆盖真实 shell。客户端 SDK、Wails 绑定和 Workspace 浏览器支持进入/上级、创建、重命名、递归删除及直接选择传输目标。文件断点续传现使用客户端稳定 UUID `resumeId` 命名 Pod 内 partial，创建新 Task 时由 Controller 在容器内权威探测实际字节偏移，客户端只按返回 offset seek，避免信任可能超前的本地进度；下载 partial 使用目标同目录的稳定 0600 文件，完整流结束后重新计算全文件 SHA-256 才原子发布。状态文件升级为 v2 并兼容读取 v1，启动将活动项标记 interrupted，新的活动 Session 可恢复原 Task；显式取消/切换 Profile 清理 partial，应用关闭保留 partial。UI 对 interrupted/failed 项提供 Resume。跨 Manager 实例测试验证上传只发送剩余 tail、下载复用 partial 并完成全量校验。真实 WSS 与本地文件系统测试覆盖大分块、原子发布、只读目录、恶意 tar、父目录 symlink 逃逸、持久化恢复和 Profile 停止。桌面组合根和 Wails 绑定已彻底移除 V1 kubeconfig、Cluster Provider、Session Manager、旧文件管理器和 MCP 初始化；生产桌面依赖图测试禁止任何 `k8s.io/*`、`internal/cluster` 或 `internal/session` 回流。上传 WebSocket 读取现绑定授权租约 Context，Token Family 撤销会在有界检查周期内取消 Pod exec 并持久化 cancelled 终态。真实 Minikube E2E 使用实际 Controller API、Kubernetes Provider 和 SPDY exec：慢速上传在 Token 撤销后及时中止；另一个活动上传在 Controller graceful restart 时取消并落终态；替换进程在同一地址从 Pod 内权威 partial offset 续传，完成后再完整下载并逐字节验证原始内容。

- [x] **V2-604：迁移 Exchange。**
  - Gateway 保存 Service/Endpoints/EndpointSlice 快照并执行事务化修改。
  - 本地目标通过绑定 Session 的反向 WSS stream 提供服务。
  - 用户断线、Token 撤销或 Task 停止时恢复原资源。
  - 2026-08-10（完成）：ADR 0011 固化 reverse WSS claim 所在 Controller 副本对临时 TCP/UDP listener、授权租约、Kubernetes 修改和恢复的共同所有权。Controller 已提供 Session/Principal 绑定且幂等的 Exchange Task create/get/delete/stream API；Task 在 Capture Service selector、EndpointSlice 或 legacy Endpoints 后，先事务化持久化回滚快照再 Apply，部分写入会立即补偿，ready 只在 Kubernetes 修改与 running 状态均成功后发送。中立 `exchangestream` 二进制协议覆盖 ready/open/data/half-close/close/datagram/stop，限制 256 KiB TCP 数据和 UDP datagram，严格校验帧方向；单条反向 WSS 将集群侧 TCP/UDP listener 多路复用至客户端内存中保留的明确本地 host/port，Gateway 不能替换本地目标。Token Family、Access Token、Session、durable stop、断连和 shutdown 均先关闭 listener 再由 Controller system client 恢复资源；恢复失败进入 `recovering` 并保留快照，stale-owner worker 以 state+updated_at CAS 保证多副本仅一个恢复者，失败可重试，Session 过期维护不会级联删除仍持有快照的 Task。Controller 路由、Gateway Policy/Kubernetes capability、Service/EndpointSlice 恢复 RBAC、Pod IP/owner downward API、Helm worker 生命周期均已接入。桌面类型化 SDK、纯客户端 Exchange Manager、Wails 绑定和 Workspace UI 支持服务/端口、本地目标、启停与 Profile/namespace/logout/退出清理，桌面依赖图继续禁止 Kubernetes 客户端回流。竞态与集成测试覆盖持久化先于 Apply、TCP half-close、UDP 边界、跨用户所有权、断连/撤权/Session 停止、恢复失败重试、双 worker 竞争及客户端拒绝 Gateway 端口替换；真实 Minikube E2E 从 Pod 经 Service、managed EndpointSlice、Controller listener 和反向 WSS 到本地 TCP/UDP echo，验证显式停止、Token Family 撤销与模拟旧 Controller 恢复失败后由替换 worker 使用真实 Kubernetes API 恢复原 selector/EndpointSlice，随后集群原服务流量恢复。全量 `go test ./...`、`go vet ./...`、关键包 race、前端生产构建、Wails 绑定生成、Helm lint/template/chart test 均通过。

- [x] **V2-605：迁移 Mirror。**
  - Gateway 维护 primary 与 shadow 转发，shadow 响应始终丢弃。
  - 对 shadow 慢响应设置独立背压与超时，不能拖慢 primary。
  - 2026-08-10（完成）：ADR 0012 固化 primary/shadow 隔离：Controller 从持久化的 Service/EndpointSlice 或 legacy Endpoints 回滚快照中解析 ready、非 terminating 的原 Pod TCP/UDP 地址，桌面只能提供本地 shadow host/port，不能提交或替换 primary；同一快照先于 Kubernetes Apply 事务化持久化，并同时作为 primary 权威来源与停止/撤权/失主后的补偿来源。独立 `mirror` Task API、Gateway Policy/SSAR capability、chi v5 路由、owner heartbeat 与 stale-owner CAS worker 已接入 Controller；Task 的 WSS owner 同时持有临时 listener、primary socket、授权租约和资源恢复。中立 `mirrorstream` 协议仅允许 Controller 下发 ready/open/data/half-close/close/datagram，客户端只能发送 stop；集群响应同步取自原 backend，本地 TCP/UDP 响应持续读取后丢弃且永不编码回 WSS。Controller 全局 shadow 队列和桌面每流 actor 都有独立有界队列、dial/write/idle timeout；溢出、慢写、本地早关与晚到帧只淘汰对应 shadow，不阻塞 primary 或误杀 Task，terminal close 也以有界 best-effort 清理。类型化 TLS SDK、桌面 Manager、Wails 绑定和 Gateway Workspace 已支持 Service/端口、本地目标、启停及 Profile/namespace/logout/退出清理，并在 UI 阻止同一 Service 同时 Exchange/Mirror。单元、竞态与集成测试覆盖权威 backend 解析、TCP half-close、UDP 边界、shadow 响应丢弃、队列背压、快速本地关闭、跨用户所有权、Token/Session 撤销、恢复失败重试和多 worker 竞争；真实 Minikube E2E 联合等待 EndpointSlice 收敛与副本到达，证明 TCP/UDP 主响应始终来自原 Pod、本地只收到请求副本，且显式停止、Token Family 撤销及模拟 Controller 恢复失败后的替换 worker 均恢复原 selector/EndpointSlice。全量 `go test ./...`、`go vet ./...`、关键包 race、前端生产构建、Wails 生成、Helm lint 与合法 Values template 均通过。

- [x] **V2-606：迁移 Preview。**
  - Gateway 创建带 owner label/annotation 的 Service 和 EndpointSlice。
  - 名称冲突不覆盖用户资源；停止和过期回收只删除自身拥有的资源。
  - 2026-08-10（完成）：ADR 0013 固化 exact-owner 资源协议：独立 `preview` Task 在任何 Kubernetes 写入前持久化 `preview-service` cleanup intent，以 Task UUID 同时标记 Service 与 EndpointSlice 的完整 owner annotation、稳定哈希 label 及用途限定标签；创建严格使用 create-only，Service 或 EndpointSlice 任一名称冲突都失败，部分创建仅补偿本次精确 owner。停止、断连、Token Family/Access Token 撤权、Session 终止与 Controller 失主统一进入清理路径；删除前分别读取对象并校验全部 owner metadata，并以 UID precondition 防止检查后的同名替换被误删。用户身份负责创建，Controller system identity 负责撤权后的补偿；失败进入 `recovering` 并保留 snapshot，多副本 worker 通过 state+updated_at CAS 仅由一个实例回收。chi v5 API、Gateway Policy/SSAR capability、Helm RBAC 与 Controller worker 已接入；ready 只在资源创建和 running 状态都持久化后发送。类型化 TLS SDK 接受 pending 阶段未分配 ClusterIP、要求 running 阶段合法 IP，并通过认证 WSS 复用中立 Exchange TCP/UDP 协议；纯客户端 Preview Manager 保留明确 local host/port，拒绝 Gateway 替换 Service 名称或端口，durable DELETE 先于 stream stop。应用组合根、Wails 绑定和 Gateway Workspace 支持创建、列出、停止，并在 namespace 切换、logout、Profile 删除和退出时清理。单元/竞态测试覆盖 Service/EndpointSlice 冲突、错误 owner、UID 防护、TCP/UDP、跨用户所有权、撤权、Session 终止、删除失败重试和双 worker 竞争；真实 Minikube E2E 证明 Pod 经临时 ClusterIP Service 和 EndpointSlice 可访问桌面 TCP/UDP 服务，已有用户 Service 不被覆盖，同名用户替换不被停止误删，显式停止、Token 撤销与替换 worker 均只删除自身资源。全量 Go 测试、vet、关键 race、前端生产构建、Wails 生成/生产构建、Helm lint/template/chart test 均通过。

- [x] **V2-607：统一远程 Task 模型。**
  - Port Forward、exec、file、Exchange、Mirror、Preview 使用一致状态机。
  - Task 至少支持 `pending/starting/running/recovering/failed/stopping/stopped`。
  - 写操作使用 idempotency key，防止网络重试创建重复资源。
  - 2026-08-10（完成）：ADR 0014 固化统一远程 Task 生命周期。新增共享 `remotetask.State` 与唯一状态词汇 `pending/starting/running/recovering/failed/stopping/stopped`，存储 Repository 在 create、CAS 更新和 stale-owner claim 入口统一拒绝非法值、终态回退及无效 heartbeat；Controller Document、持久化模型和类型化客户端 DTO 共用该状态类型，JSON/Wails TypeScript 仍保持字符串兼容。Port Forward 在服务端完成授权目标解析后持久化为 `running`；Exchange/Mirror/Preview 将旧 `preparing` 全部替换为 `starting`，并继续保证 cleanup intent、Kubernetes 修改和 running 状态持久化后才发送 ready；Pod exec 和文件传输先以 `starting` claim，只有 WebSocket 与授权/Session lease 均成功后才进入 `running`，升级或 lease 失败直接落 `failed`。SQLite/PostgreSQL schema migration 6 将历史 `active -> running`、`preparing -> starting`，保留结果、时间戳、snapshot 和幂等所有权。新增共享 `controller/taskapi`：所有 Task create（并一并覆盖远程文件操作）严格校验单个 128-byte、log-safe `Idempotency-Key`，按 Session ID、namespace 和规范 JSON 生成稳定请求哈希，并以 `task:<type>:<principal>` 事务化 reserve；同键同请求返回原 Task，异请求冲突，旧 Port Forward envelope hash 可继续重放。测试覆盖完整状态图、非法终态回退、旧状态数据库升级、稳定哈希、重复/非法 key、旧 Port Forward 幂等记录兼容、六类 Task 的单次 claim、撤权、停止和恢复。全量 `go test ./...`、`go vet ./...`、关键包 race、前端生产构建、Wails 绑定生成/生产构建、Helm lint/template/chart test 均通过；真实 Minikube V2 数据面 E2E 8/8 通过（259.701 秒），覆盖 Port Forward/Gateway 重启、exec、文件传输、Exchange、Mirror、Preview、Token 撤权、Controller 重启及 stale-owner 恢复。

- [x] **V2-608：迁移 MCP。**
  - 本地 MCP 只调用类型化 Gateway SDK。
  - MCP 工具权限不超过当前登录用户；敏感写操作保持显式参数和稳定错误。
  - 2026-08-10（完成）：桌面组合根重新接入纯 V2 MCP Controller，唯一生产后端 `mcp.RemoteBackend` 只依赖 `clientv2` 的 Profile、认证 Remote Client、Session/Data Plane、Exchange/Mirror/Preview/Port Forward、exec 与文件传输 manager；删除 V1 `cluster/session/store/filemanager/podssh` backend 以及会修改本机特权状态的 `manage_helper`、`manage_network` 和本地 sing-box DNS 工具。保留的五个工具全部通过类型化 Gateway SDK/manager：资源读取、Gateway Session、远程流量 Task、Pod exec 与流式文件传输。每次调用必须显式提供当前 `profileId`，后端在任何 Gateway 调用前要求其等于 `ActiveProfileID`；非当前 Profile 直接返回 `forbidden`。除创建 Session 外，所有 mutation 还必须携带与当前活动 Session 完全一致的 `sessionId + namespace`；停止/取消要求精确 `taskId` 并校验本地 Task 所属 Session，流量与文件写入要求完整远端目标、本地 endpoint/path、方向、类型和覆盖选择。Pod exec 改为显式 argv，不再隐式 `/bin/sh -c`，限制 1-300 秒与 stdout/stderr 各 1 MiB。Gateway HTTP/API、OAuth 刷新、Policy/SSAR、Session/Task owner 与 Token Family 撤销继续是唯一权限来源，MCP 无独立 Kubernetes 身份。错误以稳定 JSON tool error 输出 `invalid_argument/unauthenticated/forbidden/not_found/conflict/unavailable/internal`，保留安全的 field/requestId 而不泄露内部错误链或 Token。MCP listener 仍只绑定 loopback，并改用解析后的精确 Host/Origin 校验以拒绝 `localhost.evil`；Bearer Token 默认启用、随机 256-bit，仅进入系统钥匙串，0600 的 `mcp-v2.json` 只保存版本、开关和端口。App startup/shutdown、设置 Wails binding、Codex/Claude/Cursor/VS Code 安装入口和中英文 UI 已恢复。架构测试禁止 MCP 回流 V1/Kubernetes 运行时；单元与真实 Streamable HTTP 测试覆盖非当前 Profile、Session 三元组、显式参数、稳定错误、Bearer/Origin、钥匙串不落明文、listener 重启与五工具清单。`go test ./...`、`go vet ./...`、MCP/App/架构 race、前端生产构建及完整 Wails 生成/打包均通过；ADR 0015 固化信任边界。

- [x] **V2-609：功能对等 E2E。**
  - 为 v1 已支持的每项功能建立远程 Gateway E2E。
  - 测试正常停止、客户端崩溃、Gateway 崩溃、Token 撤销和 Kubernetes API 暂时不可用。
  - 2026-08-10（完成）：新增 `docs/v2-e2e-coverage.zh-CN.md`，逐项将 V1 的 SOCKS/TUN（ServiceIP、PodIP、集群 DNS、手工网络范围）、Service/Pod TCP/UDP Port Forward、复用与恢复、Exchange、Mirror、Preview、Pod exec/TTY、文件传输与管理、Pod SSH/SCP/SFTP 和本地 Helper 边界映射到真实远程 Gateway E2E。真实 Minikube 用例均通过类型化客户端、Controller API、远程 Task、WSS 和实际 Kubernetes/SPDY 资源执行，桌面侧不读取 kubeconfig。故障矩阵覆盖显式停止、客户端异常断开、Gateway Pod 删除重建、Controller 重启与 stale-owner 接管、Token Family 撤销、Kubernetes client-go transport 注入真实 503、网络路径切换和多身份隔离；恢复断言同时检查 Task 终态、listener/Helper/DNS 释放、rollback snapshot 清空及 Kubernetes 资源精确恢复。E2E 发现并修复了同时存在 EndpointSlice 与 legacy Endpoints 时旧 Pod 后端被 mirroring controller 重建的问题：接管前同时快照两类对象，接管时依次删除 legacy Endpoints 与原 EndpointSlice，恢复时分别还原两类快照。文件对等用例覆盖 8 MiB 断点续传、特殊路径、嵌套目录和空目录；Pod SSH 使用外部 OpenSSH `scp` 验证双向文件及递归目录。当前工作树构建镜像后的 `e2e/v2dataplane` 真实 Minikube 最终验收 8/8 通过（125.367 秒），新增 Data Plane core 异常退出清理目标用例复跑通过（12.930 秒）；实际子进程 SIGKILL、Helper ACL 与平台恢复继续由 `e2e/connect` 和 `e2e/platform` 特权 E2E 验证。ADR 0011/0012 同步固化双 Endpoint 表示和异常桌面断连的所有权/恢复不变量。

- [x] **V2-610：引入 TrafficBinding Operator。**
  - 使用同一根 Go module 中独立部署的 `kubeloop-operator`，并将 Operator API/Reconciler 放在 `internal/operator`，避免与应用 Controller 包和进程职责冲突。
  - 以 namespaced `TrafficBinding` CRD 统一声明 Port Forward、Preview、Exchange 和 Mirror；Controller 只管理认证、Task、流和 CR 生命周期，Operator 独占 Service、Endpoints、EndpointSlice 的写入、恢复与 finalizer。
  - Helm 同时安装 Controller、Gateway、Operator 和 CRD，并为 Controller/Operator 使用独立 ServiceAccount 与最小 RBAC。
  - 2026-08-10（完成）：根目录已加入 Kubebuilder `PROJECT`、生成命令、CRD/RBAC、Operator 镜像入口与 Helm 第三工作负载；`TrafficBinding.spec` 以 Task UUID、Session UUID/generation、目标、relay 与端口建立不可变所有权，四种 mode 通过 CEL 校验。Controller 新增 `trafficbindingclient`，使用确定性 CR 名称实现幂等 create/conflict、等待 `Ready/Degraded + observedGeneration`、删除后等待 finalizer 完成，并周期清理由缺失、终态或所有权不匹配 Task 遗留的 CR。Exchange/Mirror 只保留用户授权的快照读取，Preview 和 Port Forward 也已切换到 CRD 后端，所有 Kubernetes 资源写入与恢复均由 Operator 执行。Operator 在状态中持久化 Exchange/Mirror 回滚快照，在删除 CR 时精确恢复或清理资源。架构测试禁止四类 Controller workflow 回流旧的直接写函数；fake-client、EnvTest、Helm 渲染和 CRD 同步测试覆盖跨组件激活、状态等待、降级、删除/finalizer、孤儿清理、RBAC 与三工作负载安装。ADR 0020 固化该边界。

### M7：客户端瘦身与用户迁移

- [x] **V2-700：新增 Server Profile Store v2。**
  - 保存 server ID、base URL、显示名称、上次用户和 UI 偏好。
  - 不保存 kubeconfig 路径、OIDC client secret、AD bind secret 或明文 Token。
  - Store 写入前备份，schema 迁移失败时可回滚。
  - 2026-08-09：已新增独立 `servers-v2.json`，仅保存服务 ID、规范化 base URL、显示名称、上次用户和 UI 偏好；严格拒绝未知 Secret 字段。Store 使用同目录原子写、写前有效备份和损坏主文件回退，Token 与 device credential 只进入系统安全存储。

- [x] **V2-701：重做首次使用流程。**
  - 页面只有服务地址、连接测试和登录按钮。
  - 自动补全 HTTPS，但明确展示最终 Origin；禁止无提示降级到 HTTP。
  - 展示服务器身份、TLS 错误、版本不兼容和认证方式。
  - 2026-08-09：默认 V2 桌面启动直接进入服务地址页，不扫描 kubeconfig；自动补全 HTTPS、明确展示 discovery 返回的最终 Origin、服务 ID/版本和认证方式。连接测试会拒绝 redirect、跨 Origin、超大/畸形 JSON、API/协议/最低客户端版本不兼容以及不安全认证描述。

- [x] **V2-702：重做导航信息架构。**
  - Clusters 改为 Server/Environment。
  - 顶部持续展示 server、用户、namespace 和 Session 状态。
  - 所有 Task 从统一任务中心进入，减少独立页面之间的状态分裂。
  - 2026-08-10（完成）：认证后的客户端信息架构改为 Server Environment 与 Task center 两级导航，常驻状态区持续展示 Server、当前用户、namespace、Kubernetes/Gateway 版本、Session 与 Data Plane 状态。Task center 聚合当前 Session 的 Port Forward、Exchange、Mirror、Preview、Pod SSH、Pod Exec 和文件传输任务，统一提供停止、打开、恢复和历史清理操作；各功能块只保留任务创建入口，移除重复的长期任务列表。exec 与文件传输组件将实时任务状态上提到工作区；TypeScript 类型检查、前端生产构建及全量非 E2E Go test/vet 通过。

- [x] **V2-703：移除 v2 客户端 kubeconfig 能力。**
  - 删除添加/删除 kubeconfig UI 和 Wails binding。
  - 桌面启动不扫描默认 kubeconfig 文件。
  - 构建检查防止 v2 client package 重新依赖本地 Kubernetes Provider。
  - 2026-08-10：桌面组合根只构造 V2 Profile、认证、远程 Session/Data Plane、exec、文件和 Port Forward manager；已删除 kubeconfig/Cluster/旧 Session/旧文件管理/MCP Wails binding 与 V1 启动恢复开关。React 入口只进入 Server Profile 流程。架构测试执行 `go list -deps .`，禁止桌面产物包含 `k8s.io/*`、`internal/cluster` 或 `internal/session`；DNS-1123 校验已改为本地无 Kubernetes 依赖实现。

- [x] **V2-704：实现账号和服务器管理。**
  - 支持添加、切换、重新登录、退出和删除 Server Profile。
  - 删除 Profile 时撤销 Refresh Token、关闭 Session 并清除系统安全存储。
  - 2026-08-10（完成）：Server 页面新增 Profile 选择器和独立的 Add server 流程，可在已保存 Gateway 之间切换并自动恢复各自 discovery、认证状态、namespace 与资源视图；支持主动刷新登录、退出后重新登录，以及删除后自动选择剩余 Profile。删除与退出统一停止 inventory watch、exec、文件、Pod SSH、Port Forward、Exchange、Mirror、Preview、Data Plane 和远程 Session；删除改为即使远端 Refresh Token 撤销失败也继续清理本地系统凭据和 Profile，并返回聚合清理错误。新增远端撤销失败的本地清理回归测试；关键 race、全量非 E2E test/vet 和前端生产构建通过。

- [x] **V2-705：处理 v1 用户升级。**
  - 首次启动 V2 时保留 v1 状态备份，但不自动上传 kubeconfig 或凭证。
  - 显示迁移说明，引导用户填写管理员提供的 Gateway 地址。
  - V2 不自动连接旧的 Gateway Pod 或恢复 v1 Kubernetes 资源意图。
  - 2026-08-10（完成）：新增独立的客户端 V1→V2 迁移边界。首次 V2 启动仅用 `Lstat` 和有界字节复制检查同一应用目录下的 `state.json`，拒绝符号链接/非普通文件/超限文件，以 `0600` 原子写入 `state-v1.backup.json` 和版本化一次性迁移标记；后续启动验证标记及备份且不覆盖原备份。迁移过程不解码旧 state、不打开其中 kubeconfig 路径、不读取或上传凭据，也不构造 V1 Session/Kubernetes Provider，因此不会连接旧 Gateway Pod 或恢复资源意图。Bootstrap 返回迁移状态，Server 页面明确展示备份结果并引导手工填写管理员提供的 Gateway URL。幂等、权限、无旧文件、符号链接拒绝、应用组合测试、全量非 E2E test/vet 与前端生产构建通过。

### M8：安全、可靠性和发布

- [x] **V2-800：安全测试。**
  - 覆盖 OAuth callback、Token replay、JWT confusion、LDAP 注入、密码喷洒、跨用户 IDOR、SSRF、路径穿越、stream 越权和日志泄露。
  - 对 Gateway HTTP/WSS 入口执行 fuzz 和大小限制测试。
  - 2026-08-10（完成）：安全矩阵覆盖 OAuth state/nonce/PKCE/callback/交换码重放、Refresh Token 并发 replay 与整族撤销、JWT `alg=none`/HS256 confusion、AD filter 注入和账号/客户端双维度密码喷洒、统一 Authorizer 与 principal/session/namespace 所有权 IDOR、Gateway discovery/Origin/redirect 与 Provider 出站配置 SSRF 边界、文件/归档路径穿越、stream 单次 claim/续租越权以及 API/audit/readiness/storage/Task 日志脱敏。新增 64 KiB HTTP header 上限、16 KiB auth body 回归、Pod Exec WSS 连接级帧上限，并提供可持续运行的 Gateway HTTP 与 WSS exec frame fuzz target；本轮短时执行约 6 万/70 万输入无失败。完整映射见 `docs/v2-security-test-matrix.zh-CN.md`；Controller 全量 race 与全量非 E2E test/vet 通过。

- [x] **V2-801：资源恢复测试。**
  - 在 Task 生命周期的每个步骤注入失败。
  - 验证 Exchange/Mirror/Preview 不遗留或错误恢复他人资源。
  - Gateway 重启后的回收器只处理带正确 owner identity 的过期资源。
  - 2026-08-11（完成）：`make recovery-test` 以 race detector 覆盖统一 Task 状态机、Service/EndpointSlice/legacy Endpoints 快照、TrafficBinding finalizer、Controller stale-owner CAS、Port Forward/Exec/File/Exchange/Mirror/Preview 补偿和 maintenance 孤儿回收。故障注入覆盖 snapshot 持久化、部分 Apply、ready 前断连、显式停止、Token Family 撤销、Controller 重启、真实 Kubernetes 503、错误 owner/UID、同名用户资源接管和双 worker 竞争。通用 Minikube E2E 自动安装 CRD 并启动当前工作树 Operator；`e2e/v2dataplane` 8/8 复跑通过（173.842 秒），验证 Exchange/Mirror 恢复原 Service/Endpoint 表示、Preview 只清理仍由对应 TrafficBinding UID 控制的资源、Gateway Pod 重启与 NetworkSpec 权限刷新后本地 Task/端口/TUN 安全收敛。隔离 kubeconfig 的 Kubebuilder Operator 部署、受保护 metrics、Preview TrafficBinding 调谐与 finalizer 清理套件 3/3 通过；真实验证 Service、EndpointSlice 端口映射和 CR 删除后的级联资源回收，并支持中断后幂等重跑。

- [x] **V2-802：性能与容量测试。**
  - 建立单 Pod 的用户数、物理 WSS、逻辑 stream、吞吐和内存基线。
  - 验证限流不会阻塞健康检查、登出和资源清理。
  - 2026-08-11（完成）：在可重复的 Gateway 进程内容量门禁和三轮 CI benchmark 之外，新增单 Gateway Pod 的真实 Minikube 容量 E2E。测试以四名 Principal 建立四条物理 WSS 和十六条逻辑 stream，验证单用户/全局物理连接上限、每连接逻辑流上限、释放后容量复用，以及满载时 `/health/live`、`/health/ready` 和 `/metrics` 不被阻塞；并发 32 KiB 集群内往返吞吐实测 59.33 MiB/s，kubelet working-set 峰值 10.13 MiB、相对基线增长 5.42 MiB。满载期间通过真实 Controller/Operator 创建 Preview，Token Family 撤销后 130.48 ms 内停止 Task，并清空客户端 relay、TrafficBinding、Service、EndpointSlice 与 durable snapshot。独立用例 12.275 秒通过，完整 `e2e/v2dataplane` 复跑 194.739 秒通过。

- [x] **V2-803：跨平台客户端 E2E。**
  - macOS、Windows、Linux 覆盖安装、登录、TUN、DNS、退出、升级和卸载。
  - 操作系统休眠、网络切换后 Session 能恢复或安全清理。
  - 2026-08-11（完成）：新增可移植的休眠间隔检测器和 Data Plane `ResumeAll` 恢复路径；唤醒时主动中断陈旧 transport、刷新权威 Session/RelayTicket，并原位重连，保持 SOCKS 地址和 Helper TUN Session 不变。`e2e/remotetun` 使用实际特权 Helper、sing-box TUN、WSS/smux Gateway、RelayTicket/NetworkSpec 授权和精确目标路由，验证休眠间隔前后直接访问 `100.64.0.42:443` 均成功，且停止后 Helper Session 为零。GitHub Actions push workflow [31454657118](https://github.com/fengqi-dev/kube-loop/actions/runs/31454657118) 的 Windows、macOS、Linux、Helm、主 Go 与前端六个作业全部通过：Windows/macOS 均完成 Helper 安装、升级、ACL/DNS 平台恢复、真实远端 TUN/唤醒和卸载；Linux 完成 Minikube V2 数据面及相同远端 TUN/唤醒门禁。Windows SQLite 路径、固定容器 Secret 路径、PodSSH WebSocket 关闭和 sing-box `.exe` 产物/夹具差异均已纳入回归。

- [ ] **V2-804：可观测性。**
  - 添加 RED 指标、active session/stream/task gauge、Kubernetes API latency 和 cleanup failure。
  - 日志和指标通过 request/session ID 关联，但不使用用户隐私数据作为指标 label。

- [ ] **V2-805：发布与兼容文档。**
  - 编写管理员 Helm 安装、OIDC/AD Provider、RBAC、升级回滚和故障排查文档。
  - 编写用户从 v1 迁移、登录、连接和常见错误文档。
  - 发布 protocol compatibility matrix。

- [ ] **V2-806：Beta/RC 发布。**
  - `v2.0.0-beta.1`：登录、Inventory、SOCKS/TUN 基线。
  - `v2.0.0-beta.2`：核心功能对等。
  - `v2.0.0-rc.1`：协议与 Store schema 冻结，只接受阻断问题修复。
  - `v2.0.0`：所有 GA Gate 通过。

- [x] **V2-807：精简并可复现打包 sing-box。**
  - 以 patch 而不是长期 fork 维护，绑定上游 tag 和 commit。
  - 本地构建、二进制/patch 归档、SHA-256 与 Release workflow 可复现。
  - 2026-08-10（完成）：固定 sing-box `v1.13.16`/`17ec3c71...`，仅保留 KubeLoop TUN mixed stack 与 Clash API 必需的 `with_gvisor,with_clash_api`，macOS arm64 产物由 50,041,106 bytes 降至 22,383,650 bytes（55.3%）。新增严格 source revision/patch apply 校验、`make singbox-patch-check/build/package`、本地二进制与 patch 归档/校验和、CI submodule 校验和 Release patch asset；桌面默认从隔离临时源码应用 patch 构建，上游包仅用于显式对比。

- [x] **V2-808：Namespace 网络权限闭环。**
  - Controller Policy、Kubernetes impersonation、RelayTicket 和 Gateway 目标校验共同绑定 namespace。
  - NetworkSpec 心跳刷新；物理 WSS 不超过 RelayTicket 生命周期。
  - 2026-08-10（完成）：ADR 0021 固化 namespace 权限单元。Controller 仅通过用户 impersonating client 发现目标 namespace 的非 host-network PodIP 与 Service ClusterIP，CIDR 只做路由；RelayTicket 强制签名绑定 namespace、Session generation 和 NetworkSpecHash。Gateway 注册 control spec 时核对 token/namespace/hash，每次 TCP/UDP open 只允许精确 IP，DNS 必须为 cluster suffix 且解析结果再次过 allowlist，CoreDNS 仅 53 端口。Session heartbeat 重新发现并原子更新 NetworkSpec/hash，发现失败不续期；WSS 使用 Ticket expiry 作为不可延长 deadline，把 IP 回收、权限变更和旧 generation 的暴露限制在两分钟内。单元/contract 测试覆盖缺失 hash、过期身份和跨 namespace DNS 拒绝；真实 Minikube 新建独立 denied namespace，验证其 ServiceIP/FQDN 均被 Gateway 拒绝，同 namespace 精确 ServiceIP 初始可访问、heartbeat 撤权后不可访问。NetworkSpec hash 变化时客户端保持本地 SOCKS/Port Forward 地址，替换底层 transport、清理旧 Helper TUN 并按新 PodIP/ServiceIP 重装；随后单纯 Gateway Pod 重启继续复用该 TUN。

### M9：后台管理（建议在 V2.0 Beta 后按切片交付）

- [x] **V2-900：管理面信任边界 ADR。**
  - 2026-08-10（完成）：新增 ADR 0022 和威胁模型，冻结 Controller-only Management Plane、无管理员默认拒绝、bootstrap 正式策略发布后持久退役、Secret-backed 短期 break-glass、专用 HttpOnly 管理 Session、同步 CSRF Token、严格 CSP、Secret alias-only API 和事务审计边界。Data Plane/Operator 不接收管理路由、数据库、身份 Secret 或管理角色；V2.0 管理 API 不接受 Secret 明文、任意 Kubernetes/SQL/脚本或通用网络探测。

- [x] **V2-901：管理角色与授权。**
  - 2026-08-10（完成）：已新增独立 Management Plane authorizer，固定 `platform-admin`、`security-admin`、`operator`、`auditor`、`namespace-admin` 五类角色和显式 `admin.<resource>/<operation>` 矩阵；namespace 委派、dry-run、revision 原子失效及鉴权前禁止对象查询均有单元/race 测试。Helm bootstrap 只接受 Principal UUID/精确 group；schema v7 持久保存不可自动回退的退役标记，并随 SQLite 备份、逻辑导出/PostgreSQL 导入保留。Break-glass 只从 Controller Secret alias 挂载读取 32～64 随机字节 base64url 凭据，常量时间比较、CIDR 限制和 Secret generation 轮换失效已实现。schema v8 新增只存 Session/CSRF SHA-256 的短期管理 Session；`POST /api/v2/admin/sessions/break-glass` 使用独立 chi v5 管理路由、可信 Origin、TLS（仅 loopback 可 HTTP）、全局/来源限流和 `__Host-kubeloop-admin` Cookie，Secret 轮换即时失效，成功 Session 与审计同事务提交，拒绝尝试只记录无原因高层审计。首个正式策略发布已在事务中永久退役 bootstrap。普通 OIDC/AD Access Token 现可通过独立限流的 `POST /api/v2/admin/sessions/token` 换取管理 Session；接口只接受同源、唯一 Bearer 和空 JSON 对象，事务内重新读取 Principal/Token Family，拒绝已撤销、过期或身份不匹配的 Family，只持久化 Session/CSRF SHA-256。普通 Session 使用 15 分钟滑动 idle 与 8 小时/Token Family 到期的较早绝对期限，每次请求重新解析当前 group 并检查 Family 状态；bootstrap 身份类型不会升级为普通角色。成功交换与所有有界失败均写入不含 Token 的审计。
- [x] **V2-902：配置 revision Repository。**
  - 2026-08-10（完成）：schema v9 新增不可变 policy/provider revision、assignment、单调 active ETag 和幂等 change request；SQLite/PostgreSQL 共用 Repository contract、逻辑导出/导入与一致性验证。策略草稿、发布和回滚把 active pointer、状态、bootstrap 永久退役及 append-only 审计放在同一事务；并发发布使用 CAS，回滚 revision 时 ETag 仍递增。Controller 启动和轮询会交叉校验 revision spec/hash 与 assignment 行，再原子更新内存 authorizer；存储或聚合异常时立即 fail closed，恢复后可安全重载。
- [x] **V2-903：只读管理 API。**
  - 2026-08-10（完成）：独立 chi v5 管理路由已新增 Cookie Session 鉴权、当前 Principal group 解析、Token Family 吊销/过期检查、同源与重复 Cookie 拒绝，以及为后续写接口共用的同步 CSRF 中间件。`GET /api/v2/admin/capabilities` 和 `/status` 按当前 policy revision 服务端授权，只返回 Controller protocol、存储 backend/schema、集群能力键与当前身份匹配的 namespace capability scopes。Principal、Session、Task、Audit 已实现 SQLite/PostgreSQL 共用的 `(created_at,id)` 降序稳定 opaque cursor，严格限制过滤器和 1～100 页大小；Session/Task 的 namespace 授权在 Repository 查询前完成。Relay 使用线程安全 Registry snapshot、Relay ID cursor 与有界 state/online 过滤。响应隐藏 Principal external ID、Session NetworkSpec、Task spec/result/idempotency key、Relay endpoint/topology/身份和 Audit metadata；全部成功/拒绝路径写入不含 Token/Cookie/CSRF/Secret 的审计。Repository conformance 覆盖翻页期间插入新对象不造成重复或越界，HTTP 测试覆盖跨 namespace IDOR、游标类型、过滤器、分页、脱敏与稳定错误。
- [x] **V2-904：只读管理 UI。**
  - 2026-08-10（完成）：Controller 在 `/api/v2/admin/ui` 内嵌无 CDN、无第三方运行时代码的响应式只读后台，覆盖运行状态、Principal、Cluster Session、Task、Relay 和 Audit；列表复用服务端 opaque cursor，namespace-admin 只显示当前身份 `namespaceScopes` 内的 namespace 与 Session/Task 导航，其他页面按 capability 完全裁剪。浏览器可从 discovery 选择 OIDC 或 AD，也可使用 break-glass；OIDC 管理回调必须由 Controller 以完整 URL 精确列入白名单，只允许 HTTPS 或 loopback HTTP，并继续使用 state、nonce 与两层 PKCE。Access/Refresh Token 仅在 JS 内存中用于一次管理 Session 交换，随后清空且不进入 localStorage/sessionStorage；管理 Cookie 保持 HttpOnly/SameSite=Strict，sessionStorage 只保存同步 CSRF 与短期 OIDC 事务材料。退出使用 CSRF 防护的 `DELETE /sessions/current`，在同一事务中撤销 Session 并审计。静态 handler 只允许 GET/HEAD 和三个固定资产，设置 `default-src 'none'`、self-only script/style/connect、frame/object/base 禁止及 COOP/CORP；自动测试检查固定路径、CSP、无远程代码/`eval`/Token 持久化，真实本地浏览器检查桌面和 390px 响应式布局、语义导航/表格及零控制台错误。Minikube/OIDC/AD 真实 E2E 仍按约定统一留到 V2-908。
- [x] **V2-905：访问/网络策略管理。**
  - 2026-08-10（完成）：新增管理策略单例读取和 draft/dry-run/publish/rollback chi v5 API。所有 POST 统一经过 Cookie Management Session、同源校验和同步 CSRF，并要求强整数 `If-Match` 与有界 `Idempotency-Key`；draft/rollback 通过持久 change request 做请求哈希回放，publish 必须复用 draft 幂等键且按 change ID 可安全重试。服务端严格 JSON、64 KiB 请求上限和稳定 400/409/412/428/503 错误，候选 Snapshot 在内存 authorizer 中以 normal identity dry-run，不会制造 bootstrap/break-glass 上下文；缺少正式 `platform-admin` 的策略可验证但不可发布。发布/回滚继续在同一事务中提交 active pointer、bootstrap 永久退役和审计，成功后同步校验不可变 revision/assignment 聚合并原子刷新进程内 authorizer，失败立即 fail closed。内嵌控制台按 capability 裁剪策略读取、编辑、dry-run、发布和显式 revision 回滚，pending change 仅存 sessionStorage 且不包含 Token/Secret。SQLite HTTP 合约、幂等回放、错误幂等键、陈旧 ETag、CSRF、即时重载、回滚及 race 测试通过；Minikube/浏览器真实 E2E 仍留到 V2-908。
- [x] **V2-906：OIDC/AD Provider 管理。**
  - 2026-08-10（完成）：新增 OIDC/AD Provider validate/draft/publish/rollback revision 流程。Helm 静态 Provider 与数据库 active revision 聚合为不可变 Registry；发布在事务前解析部署 allowlist 的 Secret alias、执行 OIDC discovery/AD 连通性检查并预构建完整快照，数据库 CAS 成功后以 Registry CAS 只合并变更 Provider，保留并发无关发布。Controller 启动和按 active pointer revision/ETag 指纹轮询收敛，动态 discovery、登录与 readiness 使用同一原子 Registry。chi v5 管理 API/UI 继续强制 Cookie Session、RBAC、CSRF、强 If-Match 和幂等键，响应/审计/Web Storage 不回显 alias；Helm Secret key 仅投影到 Controller 固定只读路径，不进入 ConfigMap、Data Plane 或 Operator。SQLite 生命周期、HTTP 脱敏、并发 Registry、Helm contract、全量非 E2E 与目标 race 测试通过；真实 OIDC/AD/Minikube E2E 留到 V2-908。
- [x] **V2-907：幂等、owner-safe 运维动作。**
  - 2026-08-10（完成）：新增统一的 `internal/controller/admin/operations` 服务和 chi v5 管理端点，支持撤销单个 Device Session、按 Principal 原子撤销全部 Token Family、按 generation 强制停止 Cluster Session、按 `updatedAt` 版本停止 Preview/Exchange/Mirror/Port Forward 等 Task、触发既有五类 owner-safe recovery reconciler，以及 Relay drain/recover。所有写入继续经过 Management Session、RBAC、同步 CSRF、有界原因与 SHA-256 幂等键；状态、幂等记录和成功审计在同一数据库事务提交，明文幂等键不进入数据库或审计。Session 先持久化停止再通知运行时，失败返回待收敛；Task 使用合法 `stopping/stopped` 状态交给现有 worker 清理，不移除 finalizer、不绕过 owner/UID 防护。schema v10 持久保存 Relay desired state 与单调 control version，Controller 重启时在 Relay 注册前恢复，离线 Relay 重新注册也不会意外接流。schema v11 提供异步审计导出任务，跨副本 CAS claim、30 秒 stale claim 接管、创建者隔离读取、最多 1000 条/4 MiB NDJSON 和稳定失败码；逻辑导出/导入包含 Relay 控制意图和审计导出任务。内嵌后台按 capability 显示撤销、停止、恢复、排空及导出操作。SQLite/PostgreSQL repository conformance、事务/重放/跨 owner/运行时待收敛、HTTP CSRF/ETag/幂等与非 E2E 全量测试通过；真实 Minikube/浏览器 E2E 留到 V2-908。
- [x] **V2-908：管理面安全与统一 E2E。**

管理面属于 `kubeloop-controller`，不新建项目、不进入 Data Plane，也不扩大
Gateway Kubernetes/数据库权限。默认 SQLite、外部 PostgreSQL；Secret 仅使用
Kubernetes/外部 Secret alias。详细数据模型、API、依赖顺序和发布门槛见
`docs/v2-admin-console-plan.zh-CN.md`。

## 7. 关键依赖路径

```text
ADR / Threat Model
  → API + WSS Contract
  → Controller + Data Plane Skeleton + Helm
  → OIDC/AD + Authorization
  → Remote Kubernetes Provider + Session Registry
  → Cluster Data Stream
  → TUN/SOCKS
  → Port Forward / Exec / File / Traffic Workflows
  → Client Slimming
  → Security / Recovery / Cross-platform RC
```

身份认证、授权和 Session ownership 未完成前，不应迁移具有 Kubernetes 写权限的功能。远程 Inventory 可以先行，但 Exchange、Mirror、Preview 必须等逐操作授权和资源归属模型稳定后再迁移。

## 8. GA 验收门槛

### 产品门槛

- 新用户只填写服务地址即可发现服务器并登录。
- 客户端在没有 kubeconfig 的机器上支持全部 V2 功能。
- 登录、连接、选择 Namespace 的主流程不要求用户理解 OIDC 或 AD 参数。
- 常见错误能区分 TLS、OIDC/AD 登录、权限、Gateway、Kubernetes API、本地 Helper 和数据面问题。

### 安全门槛

- 所有非 discovery/health endpoint 默认要求认证。
- 所有 Kubernetes 操作和 WSS stream 都执行授权。
- Gateway 不能作为任意内网/公网代理。
- Token、OIDC Secret、AD bind secret、kubeconfig 内容、文件内容和命令输出不进入日志。
- 两个用户不能读取、停止或复用彼此的 Session、Task 和 Stream。

### 可靠性门槛

- 正常退出、客户端崩溃、Gateway 崩溃和 Token 撤销后均有确定的资源清理路径。
- Helm upgrade/rollback 不破坏持久配置；活动连接可以中断，但必须安全重建或明确失败。
- TUN Session 失败后不遗留系统 route、DNS 或 Helper 子进程。
- Exchange、Mirror、Preview 的故障注入测试无错误资源恢复。

### 质量门槛

- API 与 WSS contract test 全部通过。
- macOS、Windows、Linux 客户端 E2E 全部通过。
- Helm install/upgrade/rollback/uninstall E2E 全部通过。
- Auth、Authorizer、Session Registry 和资源恢复路径具备 race test 和关键单元测试。
- Beta 期间没有未解决的跨用户越权、数据损坏或系统网络残留问题。

## 9. 第一批建议创建的 Issue

为了尽快形成可运行的纵向切片，第一批只创建以下 Issue：

1. V2-001：客户端/Gateway 信任边界 ADR。
2. V2-002：OIDC/AD 认证模型与桌面登录 ADR。
3. V2-003：Gateway Policy/Impersonation 授权 ADR。
4. V2-008：SQLite/PostgreSQL 存储 ADR。
5. V2-009：Control Plane/Data Plane 拆分 ADR。
6. V2-010：桌面端 Kubernetes 调用点盘点。
7. V2-100：独立 Controller command。
8. V2-111：独立 Data Plane command。
9. V2-101：`/.well-known/kubeloop`。
10. V2-102：`/api/v2` 基础框架。
11. V2-200：Helm Chart 最小安装。
12. V2-201：最小 RBAC。
13. V2-205：Helm install/uninstall E2E。

第一批完成后的演示目标是：在空测试集群中通过 Helm 安装 Gateway，客户端或命令行只使用服务地址读取可信的 discovery 信息和 Gateway 版本。该纵向切片不包含身份认证和 Kubernetes 写操作。
