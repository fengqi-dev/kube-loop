# Admin IAM E2E

Admin 与 Auth UI 使用真实 Control Plane、全新 IAM baseline 和内置 OAuth Client。
测试环境必须通过启动日志取得一次性 bootstrap token，并调用
`POST /api/admin/bootstrap/complete` 创建首个平台管理员和组织；不再创建或读取
管理员凭据 Secret，也不提供旧管理策略 fixture。

可执行的浏览器套件位于 `e2e/ui/admin`，完整真实环境入口为
`e2e/ui/run-real-environment.sh`。当前覆盖 Authorization Code + PKCE、本地密码登录、
目录与 OAuth Client 管理、邀请创建、用户组 Namespace 授权、Recovery、审计导出、
logout，以及 Password、Implicit、Hybrid 请求被拒绝。Refresh rotation/replay 与
token revoke 的协议级覆盖由 `internal/client/auth` 和
`internal/controlplane/authn/oauthserver` 承担，真实桌面登录与跨 Tab 状态由 macOS
XCUITest 覆盖。
