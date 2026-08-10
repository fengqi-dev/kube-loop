# ADR 0002：Gateway 统一认证与桌面登录模型

- 状态：Accepted
- 日期：2026-08-09
- 决策范围：V2.0

## 背景

V2 桌面客户端只配置 KubeLoop 服务地址。OIDC issuer、OAuth client、AD 目录地址、Bind Account 和 claim/group mapping 都属于 Gateway 管理配置，不能下发为客户端配置或由客户端请求动态覆盖。

桌面应用是无法可靠保守静态 secret 的 native app。KubeLoop 同时还要支持没有 OIDC Broker 的本地 Active Directory，因此认证入口可以不同，但后续 Token、授权、Session 和审计必须使用同一种标准化身份。

## 决策

### 1. 统一 Provider 边界

Controller 提供 `AuthProvider` 抽象。Provider 只负责：

- 声明公开的登录方式；
- 校验管理员配置及上游可用性；
- 完成一次身份验证并返回标准化 Identity。

Provider 不签发 Gateway Access/Refresh Token，不执行 Kubernetes 授权，也不直接创建 Cluster Session。Token Service 将验证成功的 Identity 映射为稳定 Principal，之后所有 Controller API 和 Data Plane Ticket 都只信任 Gateway 自己签发的凭证。

服务发现只公开 Provider ID、类型、显示名称和交互类型，不公开 issuer 内部配置、client secret、Bind DN、目录地址、claim mapping 或 CA 内容。

### 2. OIDC 为首选企业认证方式

Keycloak、Dex、Microsoft Entra ID、支持 OIDC 的 AD FS 和其他标准 Provider 统一使用 Authorization Code Flow。Gateway 是固定配置的 confidential OIDC client，负责 IdP callback、code exchange、ID Token/JWKS 校验和 claim mapping；桌面端不直接获得 IdP token。

每次桌面登录必须同时使用：

- 随机且单次的 `state` 与 `nonce`；
- PKCE `S256` challenge；
- 固定 Gateway callback URL；
- Gateway 生成、短时、单次、绑定 Provider、客户端 callback 和 PKCE challenge 的 exchange code。

客户端只把 exchange code 发送到其随机端口的 loopback callback，然后用原始 PKCE verifier 向同一 Gateway Origin 换取 Gateway Token。允许的桌面 callback 仅为 `http://127.0.0.1:{ephemeral-port}/...`、`http://[::1]:{ephemeral-port}/...`，或未来显式注册并验证所有权的 app link；禁止任意 redirect URI 和通配 host。

OIDC 身份主键为规范化 `issuer + subject`。必须严格校验 issuer、签名、audience、时间、nonce 和所允许的算法；不得以 email、preferred_username 或 display name 作为稳定主键。启动 readiness 前验证 discovery metadata、JWKS 可达性、authorization/token endpoint 使用 HTTPS，以及 Provider 支持所需能力。管理员配置是 issuer 的唯一来源。

### 3. 原生 AD 只作为兼容 Provider

当企业已有 Entra ID、AD FS、Keycloak 或 Dex 时使用 OIDC。只有没有可用 Broker 的本地 Active Directory 才启用 AD Provider。

AD Provider 仅允许 LDAPS，或在同一连接上完成且严格校验结果的 StartTLS。必须验证 CA、服务端证书和主机名；禁止自动回退到明文 LDAP。可使用只读 Bind Account 搜索用户，再使用用户 DN 完成当次 bind。搜索值必须作为 LDAP filter assertion value 转义，Base DN 与 filter 模板只能由管理员配置。

用户密码只存在于一次 HTTPS 请求及短生命周期内存中，不写数据库、Token、日志、指标或审计 metadata；请求结束后不得保存在 session state。必须为搜索、bind 和 group expansion 设置超时、并发上限、按来源和账号限流。禁用、锁定或过期账号安全失败。嵌套组默认关闭，启用时必须设置深度和结果数量上限。

AD 身份主键为管理员配置的 directory ID 加不可变的 `objectGUID` 或 SID；UPN、sAMAccountName 和 email 只作为可变属性。

### 3.1 开发认证必须显式解锁

`static-token` 和 `anonymous` 只能在认证配置明确设置 `developmentMode: true` 后构造，Helm 默认值为 `false`，不能通过缺省空 Provider 意外进入匿名状态。`static-token` 从只读 Secret 文件读取至少 32 字符的随机值，仅保存 SHA-256 摘要并以常量时间比较；原始值不进入 ConfigMap、数据库、日志或 discovery。`anonymous` 不要求凭据，因此 Controller 每次启动都必须输出包含 `SECURITY WARNING` 和 `production_safe=false` 的高可见度警告。

两者只替换最初的身份验证步骤：discovery 分别公开 `static-token/token` 和 `anonymous/none`，成功后仍创建稳定 Principal 并签发标准 Access/Refresh Token Family。后续 Gateway Policy、审计、Cluster Session、RelayTicket 和 WSS 不允许绕过统一认证与授权边界。桌面端只在当前 HTTPS Origin 提交开发 Token，并在请求后清空输入；anonymous 也通过一次显式登录获取可撤销的 Gateway Token，而不是让 API 中间件无条件放行。

### 4. Gateway Token 边界

无论 OIDC 或 AD，认证完成后均签发相同格式的短期 Access Token 和可轮换 Refresh Token Family。Refresh Token 只以单向哈希形式持久化；复用已轮换 Token 会撤销整个 Family。桌面端通过操作系统安全存储保存 Refresh Token，普通配置文件只保存服务地址和非敏感 UI 状态。

退出登录、管理员撤销或检测复用时，Controller 撤销 Token Family，并关闭属于该 Principal/Device 的活动 Session；Data Plane 使用短期 RelayTicket，将撤销传播延迟限制在 Ticket 有效期内。

Refresh 轮换和复用撤销必须在数据库事务中提交，不能在返回复用错误后以 best-effort 方式异步撤销。Token Family、历史 Refresh Token 哈希与撤销状态存放在 Controller Store，因此 Controller 重启后仍能继续验证 Access Token、轮换 Refresh Token，并识别重启前 Token 的复用。桌面退出登录先停止本地功能流并关闭 Data Plane WSS，再断开远端 Cluster Session，随后撤销 Token Family 并删除系统安全存储中的凭据；各清理步骤独立执行并汇总错误，避免某一步失败阻止其余清理。

### 5. 明确不做

- V2.0 不实现 Resource Owner Password Credentials。
- V2.0 不把 AD 密码转发给 OIDC Provider。
- V2.0 不实现跨平台 Kerberos/IWA 桌面单点登录。
- V2.0 不允许客户端上传 issuer、JWKS、LDAP 地址、Bind DN 或 claim mapping。
- `static-token` 与 `anonymous` 仅用于显式开发模式，不能作为生产默认值。

## 安全依据

- OIDC Core 要求校验 issuer、audience、签名和 nonce，并以 issuer/subject 组合标识身份。
- OAuth Native Apps BCP 规定 native app 不能依赖共享 client secret，并推荐桌面 loopback redirect 与 PKCE。
- OAuth Security BCP 要求 Authorization Code + PKCE、防止 redirect/open-redirect、code injection、mix-up 和重放。
- LDAP authentication security specification要求在使用用户名/密码前建立 TLS，并校验服务端身份。

## 结果

客户端 UX 可以稳定为“填写服务地址 → 发现登录方式 → 浏览器 OIDC、组织 AD 表单或显式开发登录 → 获得 Gateway Session”。新增 Provider 不会改变 Token、授权、Session 和 Data Plane 接口。代价是 Controller 必须维护短时登录事务、exchange code、Token rotation/revocation 和针对凭据入口的更严格限流。
