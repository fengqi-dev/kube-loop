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

客户端与 Gateway 之间分为两类协议：

- **HTTPS 控制面：** 登录、资源查询、Session 和 Task 生命周期、配置、诊断。
- **WSS 数据面：** TUN TCP/UDP、exec、文件传输、Port Forward 和反向流量。

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

- [ ] **V2-010：盘点所有 Kubernetes 调用点。**
  - 输出桌面端直接调用 client-go 的 package、接口和功能清单。
  - 标记控制面调用、长连接调用和可流式传输调用。
  - 验收：Namespace、Inventory、Gateway、Port Forward、Intercept、exec、文件操作均有明确迁移归属。

- [ ] **V2-011：定义领域模型。**
  - 定义 `ServerProfile`、`Principal`、`DeviceSession`、`ClusterSession`、`Task`、`Stream` 和 `AuditEvent`。
  - 所有持久化对象包含 schema version；所有运行对象使用不可猜测 ID。
  - 依赖：V2-005。

- [ ] **V2-012：定义错误模型。**
  - 至少包含 `UNAUTHENTICATED`、`FORBIDDEN`、`NOT_FOUND`、`CONFLICT`、`INVALID_ARGUMENT`、`UNAVAILABLE`、`VERSION_MISMATCH` 和 `RATE_LIMITED`。
  - 错误响应包含稳定 code、用户消息、可选 field、request ID，不向客户端泄露敏感内部错误。

- [ ] **V2-013：定义迁移期双栈策略。**
  - 为 cluster、exec、file、traffic 定义本地和远程两套 backend interface。
  - 迁移期用显式 feature flag 选择实现，不在同一 Session 混用本地和远程 Kubernetes 控制面。
  - 验收：每迁移一个功能，都可以复用原 manager 测试验证两种 backend。

### M1：协议与 Gateway 骨架

- [ ] **V2-100：建立 Control Plane 可执行入口。**
  - 建立独立的 Controller command、配置加载、结构化日志和优雅关闭。
  - Controller 不加载桌面 Store、Wails runtime、本地 Helper 或 Data Plane socket runtime。
  - 提供 build version、commit、protocol min/max version。

- [ ] **V2-111：建立 Data Plane 可执行入口。**
  - Data Plane 只装配 WSS、multiplexer、dialer、reverse relay、流控、指标和优雅排空。
  - 构建检查确保其不依赖 Storage Repository、OIDC/LDAP SDK、Kubernetes Provider 或 Controller Secret 配置。
  - 提供独立镜像或同镜像不同 command；无论采用哪种打包方式，运行时必须是独立进程和 Pod。

- [ ] **V2-112：定义 Controller/Data Plane 内部协议。**
  - 定义 Relay 注册、健康、容量、Session 分配、Ticket 公钥轮换、吊销摘要和排空状态。
  - 内部协议版本独立于客户端 API/WSS version，并有双版本滚动升级 contract test。
  - 禁止通过内部协议传递 OIDC/AD Secret、Refresh Token 或用户密码。

- [ ] **V2-113：实现 Data Plane Registry。**
  - Controller 根据 ready、draining、active streams、容量和拓扑选择 Data Plane。
  - 注册使用 Pod identity 或双向认证，不信任客户端提交的 relay ID。
  - Data Plane 离线和租约过期后停止分配新 Session，但不伪造活动 Stream 已迁移。

- [ ] **V2-101：实现服务发现接口。**
  - `GET /.well-known/kubeloop` 返回服务 ID、API version、认证方式、功能、服务端版本和最低客户端版本。
  - 该接口不返回 Secret、内部地址或完整集群信息。
  - 增加缓存策略和 contract test。

- [ ] **V2-102：建立 `/api/v2` HTTP 框架。**
  - 统一 request ID、JSON 编解码、错误映射、超时、body 大小限制和 panic recovery。
  - 所有 handler 依赖窄接口，禁止直接访问全局 clientset。

- [ ] **V2-103：定义 WSS v2 握手。**
  - 握手校验 Access Token、protocol version、client version 和 device ID。
  - 定义 control frame、stream open/accept/reject、data、half-close、cancel、ping/pong。
  - 设置单 frame、单 stream、单连接和单用户限制。

- [ ] **V2-104：生成或维护类型化客户端 SDK。**
  - 客户端封装 discovery、Token、HTTP API、WSS 和错误类型。
  - UI/Wails binding 不直接拼接 endpoint 或解析裸 JSON。

- [ ] **V2-105：建立协议 Contract Test。**
  - 覆盖旧客户端、新 Gateway；新客户端、旧 Gateway；未知字段；缺失字段；版本不兼容。
  - 验收：不兼容组合收到明确 `VERSION_MISMATCH`，不会建立部分可用 Session。

- [ ] **V2-106：定义 Storage Repository。**
  - 为 Principal、Token Family、Session、Task、Resource Snapshot、Idempotency Key 和 Audit Event 定义窄接口。
  - 业务代码不得依赖 SQLite/PostgreSQL 专属类型或拼接数据库方言。
  - 明确每个写操作的事务、唯一约束、并发和过期清理语义。

- [ ] **V2-107：实现默认 SQLite Backend。**
  - 启动时创建目录、设置安全文件权限、执行 migration，并校验数据库完整性。
  - 配置 busy timeout、foreign keys、journal/synchronous 策略和连接数上限。
  - 启动时拒绝明显不安全的多副本配置；健康接口报告数据库状态但不暴露路径。
  - 覆盖进程崩溃、写入中断、磁盘已满、只读卷和 migration 失败测试。

- [ ] **V2-108：实现外部 PostgreSQL Backend。**
  - DSN 只从 Secret、环境变量或文件引用加载，日志中必须脱敏。
  - 支持 TLS、连接池、连接/查询超时、事务重试和健康检查。
  - 通过数据库约束实现 ID、幂等键和身份唯一性，不只依赖进程内检查。

- [ ] **V2-109：建立跨数据库 Conformance Test。**
  - 同一组 repository、migration、事务回滚、并发写、过期清理和恢复测试同时运行在 SQLite 与 PostgreSQL。
  - 两种 Backend 产生相同领域结果和稳定错误，不要求底层 SQL 完全相同。

- [ ] **V2-110：实现数据库导出、导入和备份工具。**
  - 支持 SQLite 一致性备份、逻辑导出、空 PostgreSQL 导入和导入前校验。
  - 导出文件带 schema version、校验和和创建版本，不包含 OIDC/AD/数据库 Secret。
  - 导入是显式、可审计、失败可回滚的离线管理操作。

### M2：Helm 与部署基线

- [ ] **V2-200：创建 Helm Chart。**
  - 创建 Controller 与 Data Plane 两套 Deployment、Service、ServiceAccount、ConfigMap、Secret 引用和 NOTES。
  - Values 提供 image、replicas、resources、log level、public URL、认证、授权和数据库配置。
  - SQLite 默认创建持久卷并强制 Controller 单副本/Recreate；PostgreSQL 模式允许多副本/RollingUpdate。
  - Data Plane 始终独立扩缩容，不挂载 Controller 数据库卷或认证 Secret。

- [ ] **V2-201：设计最小 RBAC。**
  - 按只读 Inventory、exec/file、流量工作流拆分权限说明。
  - 默认不授予 secrets 读取、nodes/proxy、任意 impersonate 或通配写权限。
  - 提供 namespace-scoped 与 cluster-scoped 两套安装模式。
  - Data Plane 使用独立 ServiceAccount，默认 `automountServiceAccountToken: false`。

- [ ] **V2-202：配置外部入口。**
  - 支持 Ingress 和 Gateway API HTTPRoute，允许二选一。
  - 验证 WebSocket upgrade、长连接超时、请求体限制和 TLS。
  - `publicURL` 必须与 OAuth callback 和服务发现地址一致。
  - 同一 Origin 下将 `/.well-known`、`/auth`、`/api` 路由到 Controller，将 `/tunnel` 路由到 Data Plane。

- [ ] **V2-203：加入运行安全基线。**
  - 非 root、只读 root filesystem、drop capabilities、seccomp、禁止 privilege escalation。
  - 添加 NetworkPolicy、ServiceMonitor 可选项和拓扑分散选项。
  - PodDisruptionBudget 默认只用于 PostgreSQL 多副本模式；SQLite 单副本模式不创建会阻止维护驱逐的 PDB。
  - NetworkPolicy 禁止 Data Plane 访问 Controller 数据库和 Secret 相关服务；Controller 不提供任意目标拨号能力。

- [ ] **V2-204：实现健康检查。**
  - `/health/live` 只表示进程存活。
  - `/health/ready` 验证配置和必要依赖，但不执行昂贵 Kubernetes 全量查询。
  - 指标不得包含 Token、用户邮箱、目标地址等高基数敏感标签。

- [ ] **V2-205：建立 Helm E2E。**
  - CI 在临时集群执行 install、upgrade、rollback、uninstall。
  - 默认 SQLite 与外部 PostgreSQL 两种模式分别执行安装、升级和数据保留测试。
  - 验证 CRD 为零依赖，或在未来引入 CRD 时单独验证兼容策略。
  - 卸载后不得残留 KubeLoop 创建的临时业务资源。
  - 验证 Controller 与 Data Plane 可以独立升级、扩缩容和失败重启。

### M3：OIDC/AD、授权与审计

Gateway 使用统一的 `AuthProvider` 抽象，首期支持：

- `oidc`：标准 OIDC Provider，包括 Keycloak、Dex、Microsoft Entra ID 和支持 OIDC 的 AD FS。
- `ldap`：面向没有 OIDC Provider 的本地 Active Directory，只允许 LDAPS 或经过严格校验的 StartTLS。
- `static-token`：仅用于开发或受控环境。

如果企业 AD 已接入 Entra ID、AD FS、Keycloak 或 Dex，应优先使用 OIDC。原生 LDAP 登录缺少通用 MFA、条件访问和浏览器 SSO，只作为兼容方案。V2.0 不直接实现跨平台 Kerberos/IWA 桌面单点登录。

- [ ] **V2-299：定义统一 AuthProvider 接口。**
  - Provider 负责返回登录方式、验证身份并生成标准化 Principal，不直接签发 Gateway Access Token。
  - Token Service、Authorizer 和 Session Registry 不依赖具体 OIDC/LDAP SDK。
  - discovery 只返回可公开的 Provider ID、类型、显示名称和登录交互类型。

- [ ] **V2-300：实现 OIDC Broker 配置。**
  - Helm 配置 issuer、client ID、Secret 引用、scope、claim mapping 和固定 callback URL。
  - 启动时验证 discovery document、issuer、算法和必要 claims。
  - 禁止通过客户端请求动态覆盖 issuer。

- [ ] **V2-309：实现 Active Directory/LDAPS Provider。**
  - Helm 配置 AD 地址、Base DN、用户/组搜索规则、CA Secret 引用和可选只读 Bind Account Secret。
  - 禁止默认明文 LDAP；连接必须验证服务端证书和主机名。
  - 用户密码只用于当次 bind，不写入 Store、Token、日志、指标或审计事件。
  - 验证禁用、锁定、过期账号，并为搜索和 bind 设置严格超时与限流。
  - 支持将 AD group 映射为标准化 Principal groups；嵌套组行为必须显式配置和设置深度上限。
  - 验收：错误密码、禁用账号、证书错误、目录超时、LDAP 注入和暴力尝试均安全失败。

- [ ] **V2-301：实现桌面登录流程。**
  - 客户端生成 state、nonce、PKCE verifier/challenge 和本地 loopback callback。
  - Gateway 完成 IdP callback 后只签发短时、单次、绑定 PKCE 的 exchange code。
  - 严格限制 callback 为允许的 loopback 或注册的应用 scheme。
  - 验收：重放 code、篡改 state、错误 verifier、过期 code 全部失败。

- [ ] **V2-310：实现客户端认证方式发现。**
  - OIDC Provider 显示“使用浏览器登录”，LDAP/AD Provider 显示 Gateway 返回的组织名称和账号登录表单。
  - 多 Provider 场景先选择登录方式；只有一个 Provider 时直接进入对应流程。
  - AD 密码只提交到当前 Server Profile 的 HTTPS Origin，并在请求完成后从 UI 状态清除。
  - TLS 错误或非 HTTPS 远程地址下禁止提交 AD 密码。

- [ ] **V2-302：实现 Token 生命周期。**
  - Access Token 短期有效；Refresh Token 支持轮换、撤销和复用检测。
  - 客户端 Token 保存在系统安全存储，不写入普通 JSON Store 或日志。
  - 退出登录立即关闭相关 WSS 和 Cluster Session。

- [ ] **V2-303：提供开发认证模式。**
  - `static-token` 仅用于开发/受控环境，并在 discovery 中明确标记。
  - `anonymous` 默认关闭，开启时打印高可见度安全警告。

- [ ] **V2-304：实现 Principal 和身份映射。**
  - 标准化 subject、issuer、display name、email 和 groups。
  - OIDC 用户主键使用 `issuer + subject`；AD 用户主键使用目录 ID + objectGUID/SID，禁止使用可变 email 或登录名作为唯一身份。

- [ ] **V2-305：实现 Gateway Policy。**
  - Policy 至少按 group、namespace、operation 和 resource kind 限制。
  - 默认拒绝；拒绝结果不得暴露未授权资源是否存在。
  - 配置更新必须校验并原子生效。

- [ ] **V2-306：实现 Kubernetes Impersonation（可配置）。**
  - Claims 映射只能来自可信 issuer 和显式允许的 claim。
  - Chart 不默认授予宽泛 impersonate 权限。
  - 验证 API Server audit 中可以看到最终用户和 Gateway 身份。

- [ ] **V2-307：实现统一授权中间层。**
  - HTTP handler、Task 创建和 WSS stream open 必须调用同一个 Authorizer。
  - 任何 feature manager 不允许绕过 Authorizer 直接创建流量。

- [ ] **V2-308：实现审计日志。**
  - 记录 request ID、principal ID、session ID、operation、namespace、resource、result 和 latency。
  - Token、命令输出、文件内容、OIDC claims 原文不进入审计日志。

- [ ] **V2-311：实现 RelayTicket。**
  - Control Plane 签发短期 Ticket，绑定 principal、device、session、relay、operation、NetworkSpec hash、expiry 和 jti。
  - Data Plane 离线验证签名、audience、expiry、jti 和 stream scope，不查询业务数据库。
  - 支持签名密钥轮换、短期双公钥窗口和紧急吊销；私钥只存在 Controller Secret。
  - 验收：Ticket 重放、跨 Relay、跨 Session、扩大 operation、篡改 NetworkSpec 和过期使用全部失败。

### M4：Kubernetes 控制面迁移

- [ ] **V2-400：建立 Gateway Kubernetes Provider。**
  - Gateway 使用 ServiceAccount 或 impersonated rest.Config。
  - 为 client、informer 和 transport 设置超时、QPS/Burst、User-Agent 和 context cancellation。

- [ ] **V2-401：迁移 ServerVersion 和 Capability Probe。**
  - Gateway 返回经过授权的能力集合，而不是让客户端推测 RBAC。
  - 能力结果与 principal、namespace 和 Gateway 版本绑定。

- [ ] **V2-402：迁移 Namespace/Pod/Service Inventory。**
  - 提供分页、过滤和 watch/resync 机制。
  - 不向用户返回无权限 Namespace 的名称或数量。
  - 慢客户端不会阻塞 shared informer。

- [ ] **V2-403：迁移集群网络发现。**
  - Gateway 发现 Pod CIDR、Service CIDR、Service IP、CoreDNS 和 cluster domain。
  - 返回客户端安装本地 route/DNS 所需的最小、已校验 NetworkSpec。
  - 客户端仍负责检测该 NetworkSpec 与本地网络的冲突。

- [ ] **V2-404：实现 Cluster Session API。**
  - `POST /api/v2/sessions` 创建 Session，返回 `sessionID`、能力、NetworkSpec 和过期时间。
  - 支持 get、heartbeat、disconnect 和幂等 create。
  - Session 与 principal/device 绑定，其他用户不能查询或停止。

- [ ] **V2-405：实现服务端 Session Registry。**
  - 管理 Session、Task、Stream、反向监听和 Kubernetes 资源所有权。
  - 使用 context tree 和逆序清理；重复 disconnect 必须幂等。
  - 进程退出时执行有界清理，并保留可恢复的资源归属标记。

- [ ] **V2-406：客户端接入 RemoteClusterBackend。**
  - Clusters 页面替换为 Server/Namespace 页面。
  - v2 模式下不调用 kubeconfig inventory、probe 或 Kubernetes client。
  - 加入测试，确保只配置 URL 的干净用户目录可以完成资源浏览。

### M5：远程数据面迁移

- [ ] **V2-500：将 WebSocket bearer token 替换为认证 Session。**
  - WSS 使用短期 Access Token，服务端验证 audience、issuer、expiry 和 session ownership。
  - Token 只允许出现在 Authorization header，不允许放入 URL query 或日志。

- [ ] **V2-501：扩展多路复用协议。**
  - 增加逻辑 stream 类型、流控、最大并发、idle timeout、half-close 和取消传播。
  - 一个异常 stream 不得导致其他 stream 或物理连接崩溃。

- [ ] **V2-502：实现 Cluster Dial Stream。**
  - Gateway 校验目标属于授权 NetworkSpec 后才能拨号 Pod IP、Service IP 或集群 DNS。
  - 防止借 Gateway 访问 metadata、API Server、Node 管理端口或任意公网地址。

- [ ] **V2-503：接入本地 SOCKS Bridge。**
  - 客户端的 SOCKS Bridge 将 cluster outbound 映射到认证 WSS stream。
  - TCP、UDP、DNS、取消和 backpressure 都有集成测试。

- [ ] **V2-504：接入 TUN/sing-box。**
  - Helper 继续只接收经过校验的本地 NetworkSpec。
  - Gateway 断线时停止或隔离 TUN，不允许静默回退到错误路径。
  - 恢复成功后不重复安装 route、DNS 或本地监听。

- [ ] **V2-505：实现断线恢复。**
  - 区分物理 WSS 重连、用户 Session 过期、Gateway Pod 更换和认证 Token 过期。
  - 使用 generation 拒绝过期恢复结果。
  - 达到重试上限后清理本地网络并给出可操作错误。

- [ ] **V2-507：实现 Data Plane 排空和重选。**
  - 排空中的实例不接收新 Session，已有 Stream 在期限内继续运行。
  - 活动 TCP/WSS Stream 不宣称无损迁移；超时后明确断开并由客户端按 generation 重建。
  - Controller 重选 Data Plane 后签发新的 RelayTicket，旧 generation 不能重新发布为活动状态。

- [ ] **V2-506：数据面 E2E。**
  - 覆盖 TCP、UDP、DNS、大流量、慢消费者、半关闭、网络切换和 Gateway 重启。
  - 验证两个用户之间不存在 stream、指标或目标泄露。

### M6：现有功能迁移

- [ ] **V2-600：迁移 Port Forward。**
  - 资源解析和 Kubernetes 连接由 Gateway 执行，本地只保留监听端口。
  - Task 重连后应保持原 local port；端口被占用时返回明确冲突。

- [ ] **V2-601：迁移 Pod exec 和终端。**
  - Gateway 选择 Pod/container 并创建 exec stream。
  - 支持 TTY resize、stdin/stdout/stderr、exit code、取消和超时。
  - 命令与输出默认不写审计日志。

- [ ] **V2-602：迁移 Pod SSH。**
  - SSH identity 验证和本地 SSH endpoint 留在客户端。
  - pods/exec 和容器选择移到 Gateway。
  - 验证跨用户不能访问其他用户创建的 SSH endpoint。

- [ ] **V2-603：迁移文件传输。**
  - 本地文件 IO 留在客户端，Pod tar/exec 留在 Gateway。
  - 实现流式上传/下载、进度、校验、取消、大小限制和路径安全检查。
  - 防止容器路径穿越和本地路径被远程参数任意指定。

- [ ] **V2-604：迁移 Exchange。**
  - Gateway 保存 Service/Endpoints/EndpointSlice 快照并执行事务化修改。
  - 本地目标通过绑定 Session 的反向 WSS stream 提供服务。
  - 用户断线、Token 撤销或 Task 停止时恢复原资源。

- [ ] **V2-605：迁移 Mirror。**
  - Gateway 维护 primary 与 shadow 转发，shadow 响应始终丢弃。
  - 对 shadow 慢响应设置独立背压与超时，不能拖慢 primary。

- [ ] **V2-606：迁移 Preview。**
  - Gateway 创建带 owner label/annotation 的 Service 和 EndpointSlice。
  - 名称冲突不覆盖用户资源；停止和过期回收只删除自身拥有的资源。

- [ ] **V2-607：统一远程 Task 模型。**
  - Port Forward、exec、file、Exchange、Mirror、Preview 使用一致状态机。
  - Task 至少支持 `pending/starting/running/recovering/failed/stopping/stopped`。
  - 写操作使用 idempotency key，防止网络重试创建重复资源。

- [ ] **V2-608：迁移 MCP。**
  - 本地 MCP 只调用类型化 Gateway SDK。
  - MCP 工具权限不超过当前登录用户；敏感写操作保持显式参数和稳定错误。

- [ ] **V2-609：功能对等 E2E。**
  - 为 v1 已支持的每项功能建立远程 Gateway E2E。
  - 测试正常停止、客户端崩溃、Gateway 崩溃、Token 撤销和 Kubernetes API 暂时不可用。

### M7：客户端瘦身与用户迁移

- [ ] **V2-700：新增 Server Profile Store v2。**
  - 保存 server ID、base URL、显示名称、上次用户和 UI 偏好。
  - 不保存 kubeconfig 路径、OIDC client secret、AD bind secret 或明文 Token。
  - Store 写入前备份，schema 迁移失败时可回滚。

- [ ] **V2-701：重做首次使用流程。**
  - 页面只有服务地址、连接测试和登录按钮。
  - 自动补全 HTTPS，但明确展示最终 Origin；禁止无提示降级到 HTTP。
  - 展示服务器身份、TLS 错误、版本不兼容和认证方式。

- [ ] **V2-702：重做导航信息架构。**
  - Clusters 改为 Server/Environment。
  - 顶部持续展示 server、用户、namespace 和 Session 状态。
  - 所有 Task 从统一任务中心进入，减少独立页面之间的状态分裂。

- [ ] **V2-703：移除 v2 客户端 kubeconfig 能力。**
  - 删除添加/删除 kubeconfig UI 和 Wails binding。
  - 桌面启动不扫描默认 kubeconfig 文件。
  - 构建检查防止 v2 client package 重新依赖本地 Kubernetes Provider。

- [ ] **V2-704：实现账号和服务器管理。**
  - 支持添加、切换、重新登录、退出和删除 Server Profile。
  - 删除 Profile 时撤销 Refresh Token、关闭 Session 并清除系统安全存储。

- [ ] **V2-705：处理 v1 用户升级。**
  - 首次启动 V2 时保留 v1 状态备份，但不自动上传 kubeconfig 或凭证。
  - 显示迁移说明，引导用户填写管理员提供的 Gateway 地址。
  - V2 不自动连接旧的 Gateway Pod 或恢复 v1 Kubernetes 资源意图。

### M8：安全、可靠性和发布

- [ ] **V2-800：安全测试。**
  - 覆盖 OAuth callback、Token replay、JWT confusion、LDAP 注入、密码喷洒、跨用户 IDOR、SSRF、路径穿越、stream 越权和日志泄露。
  - 对 Gateway HTTP/WSS 入口执行 fuzz 和大小限制测试。

- [ ] **V2-801：资源恢复测试。**
  - 在 Task 生命周期的每个步骤注入失败。
  - 验证 Exchange/Mirror/Preview 不遗留或错误恢复他人资源。
  - Gateway 重启后的回收器只处理带正确 owner identity 的过期资源。

- [ ] **V2-802：性能与容量测试。**
  - 建立单 Pod 的用户数、物理 WSS、逻辑 stream、吞吐和内存基线。
  - 验证限流不会阻塞健康检查、登出和资源清理。

- [ ] **V2-803：跨平台客户端 E2E。**
  - macOS、Windows、Linux 覆盖安装、登录、TUN、DNS、退出、升级和卸载。
  - 操作系统休眠、网络切换后 Session 能恢复或安全清理。

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
