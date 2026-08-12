# ADR 0022：Control Plane Management Plane 信任边界

- 状态：Accepted
- 日期：2026-08-10
- 决策范围：V2.0，V2-900

## 背景

KubeLoop V2 需要一个企业管理后台，用于查看运行状态、身份、Session、Task、
Relay 和审计，并在后续阶段管理访问策略、Provider 与高风险运维动作。管理面
拥有比普通用户 API 更大的影响范围，但不能因此扩大 Data Plane、Operator 或
Control Plane ServiceAccount 的权限，也不能建立第二套身份和密码系统。

本 ADR 冻结 Management Plane 的进程归属、初始管理员、应急访问、浏览器
Session、CSRF、Secret 与审计边界。角色规则、Repository 和具体 API 分别在
V2-901～V2-907 实现；V2-908 统一完成浏览器和 Minikube E2E。

## 受保护资产与信任区

受保护资产包括：

- 管理角色、namespace 委派、访问/网络策略及其历史 revision；
- OIDC 非 Secret 配置、Secret 引用和 Provider 启停状态；
- Principal、Device/Token Family、Cluster Session、Task、Relay 和审计记录；
- 撤销、排空、停止、恢复、回滚和导出等高风险动作。

信任区固定如下：

| 信任区 | 可以持有 | 明确禁止 |
| --- | --- | --- |
| 浏览器 `/admin/` | UI 状态、内存中的 CSRF Token、脱敏响应 | Secret、Refresh Token、localStorage Token、数据库 DSN |
| Control Plane Management Plane | 管理 Session、角色决策、管理 Repository、审计、受限 Secret alias | 任意 Kubernetes Proxy、任意脚本/SQL、向 Data Plane 下发管理表 |
| Control Plane 普通 API | Gateway Token、普通用户策略、Session/Task | 仅凭普通 API 权限调用管理操作 |
| Data Plane | 短期 RelayTicket、公钥、吊销摘要、NetworkSpec | 管理 API、业务数据库、OIDC Secret、管理角色和 Kubernetes 管理凭据 |
| Operator | TrafficBinding CRD 和所需 Kubernetes client | 管理数据库、身份 Secret、管理 Session、绕过 owner/finalizer 规则 |
| Kubernetes/外部 Secret 系统 | Secret 明文与轮换 | 向浏览器或管理 Repository 返回 Secret 明文 |

`kubeloop-control-plane` 在同一 Go module、同一二进制和同一 Helm Chart 内提供
`/admin/` 与 `/kubeloop/api/admin/*`。不创建独立后台项目，不在
`kubeloop-gateway` 注册管理路由，也不让 Data Plane 访问 Control Plane 数据库。

## 决策

### 1. 身份认证与管理授权分离

管理面复用 ADR 0002 的 OIDC 身份和 Gateway Token，不建立管理员用户名或
密码表。一次成功登录只证明 Principal 身份；每个管理请求仍必须通过独立的
`admin.<resource>/<operation>` 授权，并按当前 revision 重新计算角色和可选
namespace 范围。

管理身份不能自动扩大 Kubernetes 权限。需要访问 Kubernetes 的诊断或运维动作
仍受 Control Plane ServiceAccount、用户 impersonation、Gateway Policy 和
owner-safe 资源规则约束。`platform-admin` 也不能获得任意 Kubernetes REST
Proxy、Secret 读取、SQL 控制台或脚本执行能力。

不存在 bootstrap 身份、正式 assignment 或有效 break-glass Session 时，所有
管理 API 必须返回统一的 `FORBIDDEN`，不得把“第一个登录用户”提升为管理员。
认证与授权拒绝发生在对象查找前，避免通过 IDOR 判断其他租户对象是否存在。

### 2. Bootstrap 管理员是有界且可退役的部署授权

初次部署可通过 Helm 配置精确的 Control Plane Principal UUID 和/或规范化 group：

- 默认列表为空；subject 只接受内部稳定 Principal UUID，不接受 `*`、空值、
  email/display name 或客户端提交的 claim；新用户通常先使用受信 group bootstrap；
- bootstrap 只授予 `platform-admin`，不修改普通 Gateway Policy；
- 配置只来自 Control Plane Helm values/ConfigMap，Data Plane 不接收；
- 每次 bootstrap 登录和管理操作都带 `bootstrap=true` 高等级审计属性。

首次发布至少包含一个有效 `platform-admin` assignment 的正式管理策略时，
Control Plane 在同一数据库事务内写入 active revision、审计事件和不可自动回退的
`bootstrapRetiredAt`。从此即使配置 revision 回滚、Control Plane 重启或旧 Helm
values 仍存在，也不能自动恢复 bootstrap 权限。

部署操作者若需要在灾难恢复中重新启用 bootstrap，必须显式设置独立的
`management.bootstrapRecovery.enabled=true`、变更 Helm release 并重启
Control Plane。恢复模式启动和每次使用都写入安全日志及持久审计；正式 assignment
恢复后必须再次退役。应用 API 不能打开该模式。

### 3. Break-glass 默认关闭且不成为常驻管理员

Break-glass 是部署操作者控制的应急入口，不是第二套日常账号系统：

- 默认关闭，只能引用 Helm allowlist 中挂载到 Control Plane 的 Kubernetes/外部
  Secret alias；不接受 values 明文、环境命令参数、数据库值或 API 上传；
- 凭据必须为至少 256 bit 随机值，只在固定的 break-glass Session 交换端点使用，
  以常量时间比较；原值不进入日志、指标、审计或数据库；
- 端点执行严格速率限制，可选来源 CIDR 限制，并且只接受受信 public Origin 的
  TLS 请求；失败响应不区分“未启用”和“凭据错误”；
- 成功后只签发最长 15 分钟、不可刷新、不可转为普通 Token Family 的管理
  Session；所有请求带 `breakGlass=true` 并产生高等级审计；
- Secret 文件内容或 alias generation 变化时，旧 break-glass Session 立即失效；
  Control Plane 不需要 `secrets/get` RBAC，只读取预先挂载的文件；
- 如果持久审计不可提交，break-glass 登录及写操作 fail closed。结构化安全日志
  是额外告警通道，不能替代持久审计。

Break-glass 不进入 Data Plane，不签发 RelayTicket，不绕过 Repository 乐观并发、
变更原因、幂等键或 owner-safe 检查。

### 4. 浏览器使用专用管理 Session

`/admin/` 不把 Access/Refresh Token 写入 localStorage、sessionStorage、URL、
HTML 或 JavaScript 持久状态。浏览器通过同源登录把已认证 Principal 交换为短期、
服务端可撤销的管理 Session。Cookie 使用：

- `__Host-kubeloop-admin`；`Secure`、`HttpOnly`、`SameSite=Strict`、`Path=/`；
- 登录后轮换 Session ID，避免 fixation；数据库只保存随机 ID 的哈希；
- 15 分钟空闲、最多 8 小时绝对生命周期；高风险动作可要求近期重新认证；
- 绑定 Principal、Token Family、创建时间和当前认证上下文；Token Family 撤销、
  Principal 禁用、角色移除、Secret generation 变化或显式登出都会使其失效；
- 每个请求重新读取或校验当前 assignment revision，Session 本身不缓存永久角色。

Cookie 认证只在 `/kubeloop/api/admin/*` 和管理 Session 端点生效，普通 `/kubeloop/api/*`
不会因浏览器 Cookie 获得身份。非浏览器自动化必须使用短期 Bearer Token；Cookie
和 Bearer 同时出现时拒绝请求，避免认证来源混淆。

### 5. CSRF、XSS 与浏览器响应边界

所有 Cookie 认证的非安全方法必须同时满足：

1. `Origin` 精确等于配置的 `publicURL` Origin，不从 `Host` 或代理头动态推导；
2. `Sec-Fetch-Site` 存在时只能为 `same-origin`；
3. `X-KubeLoop-CSRF` 与管理 Session 关联的同步 Token 常量时间匹配；
4. `Content-Type` 为允许的 JSON 类型，严格解码且拒绝未知字段和尾随内容；
5. 操作不使用 GET/HEAD，写接口同时要求 `Idempotency-Key`，修改/发布/回滚还
   要求 `If-Match` 和非空、长度受限的变更原因。

CSRF Token 只保存在页面内存并在 Session 轮换后更新。Bearer-only 请求不依赖
Cookie，仍要求 JSON Content-Type 和管理授权。CORS 默认不允许其他 Origin，
预检不能反射任意 Origin 或允许 credentials。

管理 UI 不加载第三方脚本，默认 CSP 至少包含 `default-src 'self'`、
`object-src 'none'`、`base-uri 'none'`、`frame-ancestors 'none'`、
`form-action 'self'` 和受限 `connect-src 'self'`；禁止 `unsafe-eval`，内联资源使用
nonce/hash。响应同时设置 `X-Content-Type-Options: nosniff`、严格
`Referrer-Policy`，敏感页面和 API 使用 `Cache-Control: no-store`。

### 6. Secret 只以部署侧 alias 进入管理配置

私钥、CA 文件或任意 Kubernetes Secret 明文。部署操作者先通过 Kubernetes 或
外部 Secret 系统创建并挂载 Secret，再在 Helm 中声明稳定 alias；管理配置只可
选择 allowlist 中的 alias。

数据库仅保存 alias、非敏感用途、配置 revision、可选掩码和最近验证结果。API、
导出、审计和日志不返回 Secret 文件路径、namespace/name/key、长度、哈希或可用于
枚举的差异化错误。Provider 验证由专用实现消费 alias，不提供任意 URL/host、文件
路径或通用网络拨号测试。

Secret 轮换由挂载文件更新和受控 Control Plane reload/rollout完成。新 Secret 验证
失败不替换当前 active Provider revision；旧 Secret 不复制进数据库用于回滚。

### 7. 审计是管理写操作的提交条件

普通 API 框架审计继续记录 request ID、Principal、规范化 operation/scope、命中
规则、HTTP 结果和耗时。管理面额外要求：

- 配置发布、回滚、角色变更、撤销、排空、停止、恢复和导出在同一数据库事务内
  写业务 revision/状态与 append-only 审计；审计失败则业务写入回滚；
- 记录 actor、认证类型（normal/bootstrap/break-glass）、目标稳定 ID、旧/新
  revision、幂等键摘要、变更原因、结果和 request ID；
- 不记录请求体、Token、Cookie、CSRF Token、Secret/Secret ref 细节、身份原始
  claims、文件内容、命令输出、流量正文或数据库错误 cause；
- 拒绝、冲突、校验失败和越权尝试也产生有界的安全审计；审计存储不可用时高风险
  写操作 fail closed；
- 审计导出本身是有期限、可撤销、可审计的异步 Task，不提供无界同步下载。

审计 sink 或告警系统不能修改授权结果；Data Plane 只上报聚合连接状态，不接收
管理审计表。

## 威胁模型

| 威胁 | 主要控制 | 残余风险/后续验证 |
| --- | --- | --- |
| 首个登录用户接管空系统 | 无 bootstrap/assignment 时 deny-all；无 first-user 逻辑 | Helm 配置错误会锁住管理面，属安全失败 |
| Bootstrap 长期成为后门 | 精确 subject/group、持久 retire marker、显式恢复模式和高等级审计 | 部署操作者本身仍是高权限信任主体 |
| Break-glass 泄漏或暴力尝试 | Secret mount、256 bit、短期不可刷新 Session、限流、轮换失效、统一错误 | Secret 系统被攻陷时需要外部轮换与告警 |
| CSRF 触发配置发布/撤销 | Strict Cookie、精确 Origin、Fetch Metadata、同步 Token、JSON-only | 同源 XSS 可绕过 CSRF，依赖 CSP 与输出编码 |
| XSS/第三方脚本窃取管理能力 | 无第三方脚本、严格 CSP、HttpOnly Cookie、Token 不落存储、no-store | Control Plane/UI 供应链仍需依赖扫描和签名发布 |
| Session fixation、重放或撤销延迟 | 登录轮换、仅存哈希、短空闲/绝对 TTL、每请求 revision 校验 | 已进入的事务按事务边界完成，不能瞬时中断 |
| 跨租户 IDOR/namespace-admin 越权 | 对象查找前授权、owner/scope 查询、统一 not-found/forbidden、稳定 cursor | V2-901/903 必须覆盖枚举和直接 ID 测试 |
| Secret 经 API、日志、导出泄漏 | API 仅 alias、部署侧挂载、结构化 allowlist 审计、无原始 cause | Provider SDK 错误需统一脱敏适配 |
| Provider 验证成为 SSRF/任意拨号 | 只验证版本化配置和预声明 alias，禁止通用 URL/host 测试 | 管理员配置的合法 Provider 仍是受信外部依赖 |
| 并发发布静默覆盖或审计错位 | `If-Match`、整数 revision、事务内 active pointer + audit、幂等键 | PostgreSQL serialization retry 必须保持同一语义 |
| 管理权限扩散到 Data Plane/Operator | 独立 Deployment/SA/NetworkPolicy；窄签名协议；不挂 DB/Secret | 被攻陷 Control Plane 仍可签发其既有协议允许的消息 |
| 管理 API 资源耗尽 | body/page/limit/timeout 上限、稳定 cursor、导出异步、限流 | 合法管理员可制造负载，需 V2-903/907 配额指标 |
| 审计被绕过或敏感数据进入审计 | 写入与审计同事务、高风险 fail closed、字段 allowlist、追加写 | 数据库超级用户不在应用威胁边界内 |

## 明确不做

- 不创建独立管理项目、独立管理员密码库或 Data Plane 管理入口。
- 不提供 SQL 控制台、任意 Kubernetes YAML 编辑、任意脚本/命令或通用网络探测。
- 不让 Control Plane 因管理后台获得 `secrets/get/list/watch` 或通配 Kubernetes RBAC。
- 不允许浏览器提交、读取或导出 Secret 明文。
- 不允许前端按钮、路由隐藏或客户端 capability 代替服务端授权。
- 不在 V2-900 提前开放策略、Provider 或运维写 API；这些按 V2-905～907 交付。

## 实现与验证义务

- V2-901：实现五类角色、bootstrap retire/recovery、break-glass 与 namespace 委派，
  覆盖无管理员 fail-closed、IDOR 和角色 revision 失效。
- V2-902：显式 migration 和 Bun Repository 包含管理 revision、assignment、变更
  请求、管理 Session 哈希及 retire marker；SQLite/PostgreSQL conformance 一致。
- V2-903/904：只读 API/UI 严格脱敏、有界分页、CSP、Cookie 和 CSRF contract。
- V2-905～907：所有写操作执行 ETag、幂等、原因、事务审计、回滚和 Secret alias
  约束；旧连接按既有 Ticket/Session 有界失效。
- V2-908：fuzz/race、跨租户 IDOR、CSRF/CSP 浏览器测试、并发发布、Secret 泄漏、
  撤销、升级/回滚和 Minikube E2E。在此前继续遵守“不提前跑统一 E2E”的约定。

## 结果

管理后台可以复用现有 Control Plane 身份、存储和授权基础设施，同时保持 Data Plane
无数据库、无身份 Secret、无 Kubernetes 管理凭据的边界。代价是管理 Session、
bootstrap 退役和事务审计必须成为正式持久化模型，并且 Secret provisioning 仍由
部署系统负责，而不是由浏览器提供便利但高风险的明文写入。
