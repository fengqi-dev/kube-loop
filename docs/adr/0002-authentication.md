# ADR 0002：内置 IAM 与 OAuth2/OIDC 边界

- 状态：Accepted
- 日期：2026-08-15
- 决策范围：IAM baseline 24

## 决策

Control Plane 内置身份目录、本地认证、用户组、授权、OAuth2/OIDC 与审计。
Fosite 只负责 OAuth2/OIDC 协议状态机，不拥有 Identity、用户组或 Namespace 授权。

身份统一使用 `Identity`：`human` 表示人员，`machine` 表示 OAuth Client
Credentials 对应的机器身份。系统只有一个固定的 `KubeLoop` 组织。

人员认证只支持本地用户名和 Argon2id 密码。不支持外部身份 Provider、
第二因素、恢复凭据或公共注册。管理员创建密码后，用户可以直接登录。

OAuth2/OIDC 只发布并实现：

- Authorization Code + PKCE S256；
- Refresh Token rotation；
- confidential Client 的 Client Credentials。

不实现 Resource Owner Password Credentials、Implicit 或 Hybrid。内置
`kubeloop-desktop` 与 `kubeloop-management` 是不可删除的 public Client，只能使用
Authorization Code + PKCE。Access Token 为 opaque token，ID Token 使用 ES256；
Refresh Token 重放撤销整个 grant。

浏览器 SSO 使用 `HttpOnly; Secure; SameSite=Lax` Cookie。OAuth challenge、CSRF 和
Token 不写入 localStorage；localStorage 只保存语言。

## 授权

用户必须属于一个或多个扁平用户组。普通组直接关联可访问的 Namespace；
系统 `Administrators` 组拥有管理权和全部 Namespace 访问。不实现自定义角色、
直接用户绑定、委派或按 operation/resource kind 细分的授权。

Namespace 列表必须在 repository 查询阶段按用户组有效授权过滤。

## 数据与升级

IAM 使用单一全新 baseline，不迁移旧认证表、Token、Session、用户或授权数据。
发现旧 baseline 时拒绝启动，程序不会自动删除数据。

新库首次启动原子创建管理员、固定 `KubeLoop` 组织和系统 `Administrators` 组。
初始密码由 Helm 生成并保存在独立 Kubernetes Secret 中。
OAuth HMAC 与 ES256 私钥必须相互独立。
