# ADR 0022：Admin IAM 信任边界

- 状态：Accepted
- 日期：2026-08-14

## 决策

Admin IAM 与 Auth UI 嵌入 Control Plane，通过同一个 HTTP listener 同源暴露
`/admin` 与 `/oauth2`，不增加端口、Service 或部署组件。浏览器只保存语言；OAuth 临时状态和 Admin CSRF 存在 sessionStorage，
认证 Session 使用 `HttpOnly; Secure; SameSite=Lax` Cookie。

Admin 身份来自统一 Identity 目录。用户从 Group 继承 Namespace 访问范围，
系统 `Administrators` 组拥有管理权和全部 Namespace 访问。管理权限不自动扩大
Kubernetes RBAC，也不提供 SQL、Secret 读取、任意 REST Proxy 或脚本执行入口。

新数据库默认由启动事务创建首个平台管理员和组织，随机初始密码保存在独立的
Kubernetes Secret 中；显式关闭自动 bootstrap 后，才使用日志中的一次性
bootstrap token 完成人工初始化。
Helm 只配置首次启动的管理员资料，不配置用户组或 Namespace 授权。
日常应急访问只能使用显式配置、短时、
紧急管理继续使用受审计的本地管理员身份和专用管理 Session，不提供绕过统一身份与授权策略的旁路。

所有管理写请求必须通过同源检查、CSRF、对象所有权与授权检查；支持更新的资源使用
ETag，重试敏感创建使用 Idempotency-Key，高风险操作要求变更原因并写入审计。
Client Secret 只在创建或轮换时返回并显示一次。

Namespace 列表在 repository 查询阶段按有效授权与组织边界过滤。禁止先返回全集再由
API 或 UI 隐藏。
