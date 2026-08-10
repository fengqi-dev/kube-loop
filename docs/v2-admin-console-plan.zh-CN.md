# KubeLoop V2 后台管理规划

> 状态：实施中（V2-900～V2-905 已完成）
> 目标：提供企业管理员可审计、可回滚的 Controller 管理面；不扩大 Data Plane 权限。

管理面信任边界和威胁模型已冻结在
[`adr/0022-management-plane-trust-boundary.md`](adr/0022-management-plane-trust-boundary.md)。

## 1. 产品边界

后台管理属于 `kubeloop-controller` 的 Management Plane，使用同一 Go
module、同一存储和同一 Helm Chart。它不是新的独立项目，也不进入
`kubeloop-gateway` Data Plane。建议路由：

- Web UI：`/admin/`
- Management API：`/api/v2/admin/*`
- 普通用户 API：继续使用 `/api/v2/*`

第一阶段不让管理后台直接编辑任意 Kubernetes 对象、不提供 SQL 控制台、
不允许自定义脚本，也不把 OIDC Client Secret、AD Bind 密码或数据库 DSN
返回浏览器。

## 2. 身份与权限

后台复用现有 OIDC/AD 登录和 Gateway Token，不另建管理员密码库。初始
管理员通过 Helm 配置精确的 bootstrap Principal UUID/group；完成首次策略配置
后由版本化管理策略接管。Break-glass 身份默认关闭，只能引用 Kubernetes
Secret，并且每次使用必须产生高等级审计事件。

建议内置角色：

| 角色 | 权限范围 |
| --- | --- |
| `platform-admin` | 系统配置、Provider、存储状态、Relay、升级与全部读取 |
| `security-admin` | 身份映射、访问/网络策略、Session 撤销、审计导出 |
| `operator` | 只处理 Relay、Session、Task、诊断和安全停止，不修改身份策略 |
| `auditor` | 只读配置版本、身份、Session、Task 和审计 |
| `namespace-admin` | 仅管理明确委派 namespace 的成员与访问规则 |

管理 API 每个端点都映射为 `admin.<resource>/<operation>`，继续经过统一
Authorizer。前端隐藏按钮不能替代服务端鉴权。高风险操作要求 `If-Match`
配置版本、幂等键和可读的变更原因。

## 3. 功能模块

### 3.1 概览与运行状态

- Controller、Data Plane、Operator 版本和兼容性。
- Relay ready/draining、容量、活动物理连接/逻辑流。
- 活动 Principal、Device Session、Cluster Session、Task 和失败恢复数。
- 数据库 backend/schema、迁移状态、SQLite 单副本约束和 PostgreSQL HA 状态。
- OIDC/AD、Kubernetes API、签名钥匙、Operator 和审计 sink 健康状态。

### 3.2 身份 Provider

- 查看 OIDC/AD Provider、claim/group mapping、启停状态和最近健康检查。
- 新建/修改采用“草稿 → 连通性验证 → 发布”的版本化流程。
- 浏览器和管理 API 不提交 Secret 明文；部署操作者预先在 Kubernetes/外部
  Secret 系统中创建并挂载 Secret，再由管理配置选择 Helm allowlist 中的稳定
  alias。数据库仅保存 alias、非敏感用途、掩码和配置 revision。
- Provider 发布失败不替换当前有效配置；支持回滚到上一有效 revision。

### 3.3 访问与网络策略

- 按 Principal/group 授予 namespace、operation、resource kind。
- 独立展示最终生效结果：Gateway Policy、Kubernetes impersonation/SSAR、
  namespace NetworkSpec 三层，避免管理员误认为单层放行即生效。
- 提供 dry-run：输入用户、组、namespace、操作，返回 allow/deny、命中规则、
  Kubernetes capability 和安全的拒绝原因。
- 网络权限默认只能选择 namespace；V2.0 不接受任意 CIDR、跨 namespace 或
  Internet egress。显示该 namespace 当前可达 PodIP/ServiceIP 数量，但默认
  不长期保存完整资源清单。

### 3.4 用户、设备与会话

- Principal、身份 Provider 映射、组和最近登录。
- Device/Refresh Token Family 状态；撤销单设备或整个 Principal 的 Token。
- Cluster Session namespace、generation、NetworkSpec hash、Relay、过期时间。
- 强制断开必须先持久化撤销，再通知运行时；失败显示为待收敛状态。

### 3.5 Task、流量与 Operator

- Port Forward、Exchange、Mirror、Preview、exec、文件传输统一 Task 列表。
- 查看 owner、namespace、状态、age、恢复次数和关联 TrafficBinding，不展示
  命令输出、文件内容或流量正文。
- 允许安全停止/重试恢复；禁止直接移除 finalizer 或越过 owner 校验删除资源。

### 3.6 审计与诊断

- 以 request/session/task ID、时间、Principal、namespace、operation、结果查询。
- 隐私字段最小化；Token、密码、Secret、文件内容、命令输出不入库。
- 导出采用异步、有限期、可审计任务；大范围导出需要 `security-admin`。
- 诊断包先在服务端脱敏并显示清单，管理员确认后下载。

## 4. 后端与数据模型

继续使用 Bun Repository：默认 SQLite，可配置外部 PostgreSQL。新增逻辑表：

- `admin_policy_revisions`：访问/管理策略规范 JSON、状态、版本、创建者和原因。
- `provider_config_revisions`：非 Secret Provider 配置、Secret reference、验证结果。
- `admin_assignments`：管理角色及可选 namespace 范围。
- `config_change_requests`：草稿、校验、发布、回滚状态和幂等键。
- `admin_sessions`：只保存随机管理 Session ID 的哈希、Principal、认证类型、
  Token Family、期限和撤销状态，不保存 Gateway Token 或 CSRF Token 明文。
- 管理元数据保存 bootstrap 永久退役标记；revision 回滚不能隐式重新启用它。

不使用 ORM AutoMigrate；SQLite/PostgreSQL 继续共享显式 migration 与
Repository contract。所有写操作在一个数据库事务内同时提交 revision、
active pointer 和审计事件。使用整数 revision/ETag 做乐观并发，禁止最后写入
静默覆盖。配置缓存以数据库 revision 为准，Controller 多副本通过轮询或
轻量通知失效；Data Plane 只接收签名钥匙、吊销摘要等现有窄协议，不读取
管理表。

## 5. API 与前端约束

- chi v5 路由；严格 JSON、未知字段拒绝、请求体/分页上限和稳定错误码。
- 列表使用稳定 cursor，不接受无限 `limit`；导出走异步 Task。
- 写 API 要求 `Idempotency-Key`；修改/发布/回滚要求 `If-Match`。
- 管理 UI 使用同源、HttpOnly/SameSite 管理会话或现有短期 Token；启用 CSP、
  CSRF 防护、禁止第三方脚本和 Token 落 localStorage。
- UI 与 API 独立 capability discovery；不兼容版本时只读并提示升级。

## 6. 实施 Roadmap

| ID | 状态 | 任务 | 交付物 | 验收标准 |
| --- | --- | --- | --- | --- |
| V2-900 | 已完成 | 管理面信任边界 ADR | Management Plane ADR/威胁模型 | 明确 bootstrap、break-glass、Secret、CSRF、审计边界 |
| V2-901 | 已完成 | 管理角色与授权 | admin authorizer + dry-run + Management Session | 五类角色、namespace 委派、跨租户 IDOR、break-glass 短期 Session/轮换失效/事务审计测试通过 |
| V2-902 | 已完成 | 配置 revision Repository | migration + Bun repository | SQLite/PostgreSQL conformance、ETag、事务审计、回滚通过 |
| V2-903 | 已完成 | 只读管理 API | status/principal/session/task/relay/audit | 有界分页、脱敏、稳定错误与审计通过 |
| V2-904 | 已完成 | 只读管理 UI | 概览、会话、任务、Relay、审计 | 不包含 Secret/Token，权限裁剪与可访问性通过 |
| V2-905 | 已完成 | 访问/网络策略管理 | draft/dry-run/publish/rollback | 发布原子、旧会话权限即时失效、错误配置可回滚 |
| V2-906 | 已完成 | Provider 管理 | OIDC/AD validation + Secret reference | 草稿验证不影响当前 Provider，Secret 不入 DB/响应/日志 |
| V2-907 | 已完成 | 运维动作 | revoke/drain/stop/recover/export | 全部幂等、owner-safe、失败可观测且可重试 |
| V2-908 | 已完成 | 管理面安全/E2E | fuzz、race、浏览器和 Minikube E2E | CSRF/CSP/IDOR/并发发布/撤销/升级回滚通过 |

推荐切片顺序：先交付 V2-900～904 的只读后台；再实现 V2-905 的策略写入；
Provider 写入和高风险运维动作最后开放。V2-900～907 完成代码后，再按当前
约定统一执行 V2-908 E2E。

V2-905 使用单例 `/api/v2/admin/policy` 资源管理五类管理角色及
`namespace-admin` 委派。候选策略可在不改动 active pointer 的情况下执行
regular-identity dry-run；draft 保存不可变 revision，publish/rollback 使用强
整数 `If-Match` 做 CAS，并在成功事务后同步重载进程内 authorizer。所有 POST
同时要求同步 CSRF 与 16～256 字节 `Idempotency-Key`；发布复用 draft 的幂等键，
因此提交成功但响应中断后仍可安全重试。控制台只在当前 capability 允许时展示
编辑、dry-run、发布和回滚入口，pending change 元数据仅保存在当前浏览器
session，Access/Refresh Token 仍不进入 Web Storage。

V2-906 将 Helm 静态 Provider 作为基线，并把数据库已发布 OIDC/AD revision
聚合为一个原子 Registry。Controller 启动及有界轮询按 active pointer 的
revision/ETag 指纹收敛；publish/rollback 在数据库 CAS 前完成 Secret alias
解析、OIDC discovery/AD 连接和完整候选 Registry 构建，提交成功后只以 CAS
合并发生变化的 Provider，避免并发发布覆盖无关 Provider。chi v5 管理 API 提供
list/current/validate/draft/publish/rollback，全部写请求继续要求 Management
Session、同源 CSRF、强 `If-Match` 和幂等键。Helm 只把 allowlist 中的 Secret key
投影到 Controller 固定只读路径；数据库保存 alias，API/UI/审计只返回用途名，
pending change 也不把 alias 写入 Web Storage。Provider 发布会即时更新 discovery、
登录和 readiness。

V2-908 增加管理 HTTP fuzz 入口，并对管理授权、Session、revision、operations、
SQLite/PostgreSQL Repository 和 UI 包执行 race。真实浏览器通过
`e2e/admin/browserfixture` 复用生产 SQLite、Management Session、authorizer、
revision、chi v5 API 与嵌入式 UI，完成桌面/390px 窄屏、break-glass、OIDC PKCE
callback、AD 密码登录、列表导航和策略 dry-run；同源测试 Provider 只替代外部
IdP/目录，OIDC/AD 协议、TLS、LDAP filter 与限流继续由 Provider 安全测试覆盖。
`e2e/admin/verify.sh` 已并入 Minikube Helm 生命周期，验证 CSP、CSRF、同一 ETag
并发发布只有一个成功、Principal Token Family 撤销后旧 Access Token 立即 401。
同一轮 Helm E2E 验证 SQLite/PostgreSQL 安装、组件独立升级/扩缩容、PVC/外部数据库
持久化、回滚、Pod 故障恢复、卸载与 CRD 保留；跨租户 IDOR 继续由授权前置且不读取
对象的单元/竞态测试覆盖。2026-08-10 最终复跑全部通过。

## 7. 发布门槛

- 初次部署即使管理 UI 关闭，也可完全通过 Helm 配置启动。
- 没有 bootstrap 管理员时管理 API 必须 fail closed，而不是允许首个注册用户。
- 所有 Secret 往返、配置发布、权限 dry-run、撤销和导出都有安全测试与审计。
- SQLite 模式仍只允许单 Controller；多副本后台必须使用 PostgreSQL。
- 任一配置 revision 回滚后，Controller readiness、登录和已授权用户主流程恢复。
