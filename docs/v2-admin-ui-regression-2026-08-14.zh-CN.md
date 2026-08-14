# V2 管理后台 UI 回归缺陷记录（2026-08-14）

本文记录 2026-08-13 至 2026-08-14 在本地浏览器与 Minikube
`kubeloop-dev` 环境执行的管理后台、认证页、用户与权限 UI 自动化回归。
该记录只描述当前已复现状态；缺陷修复后必须按本文的复验条件重新执行，
不能以单元测试通过替代真实浏览器结论。

## 测试环境与边界

- 管理后台：当前工作树构建资源，以及 Minikube Ingress
  `kubeloop.192.168.64.70.sslip.io` 上的真实 Control Plane。
- 浏览器：Codex 内置 Chromium，桌面视口与 `390x844` 移动视口。
- 认证：真实 Local account、OIDC authorization code、PKCE、Management Session
  与退出链路；凭据、Cookie、CSRF 和 Token 未写入文档或测试产物。
- 用户测试账号：`ui-e2e-msrrqzns` 与 `ui-e2e-ns-0814`。测试结束时均已撤销
  角色关联并禁用；系统当前没有删除本地用户 API，因此禁用记录保留用于审计。
- 权限测试完成了 Namespace Admin / `default` 的创建、发布、受限用户重新登录、
  菜单/API 权限验证和撤销闭环，以及 Auditor 的只读 UI 与即时撤权闭环。清理后的
  活动策略为 Revision `#7`、ETag `7`，
  测试用户没有活动角色绑定。

## 已通过场景

- Local account 登录、PKCE 回调、Management Session 建立与退出。
- 创建本地用户、大小写归一后的重复用户名拒绝、密码重置、旧密码失效、
  新密码登录、禁用后拒绝登录。
- 新建用户表单对短用户名、非法邮箱和弱密码保持 Confirm 禁用；修正为合法输入后
  Confirm 启用，取消操作不创建用户。
- 无角色用户登录后只显示总览与账户安全，Global capabilities 为 `0`；
  受保护的管理操作返回 `management operation is not permitted`。
- 管理员执行 Revoke all grants 后，独立 Chrome 中既有 Management Session 的下一次
  受保护请求立即返回登录页，旧导航与页面数据清除且控制台无错误；审计日志记录
  `admin.principal.revoke`、`admin.session.revoke`、用户更新和拒绝访问。
- Users、Principals、Identity Providers、OAuth Clients、Account Security、
  Role Assignments、Sessions、Tasks、Relays 与 Audit Log 的真实集群只读加载；
  Sessions 的空列表、记录数和禁用分页状态正确。
- Tasks 空状态与筛选、Run recovery 原因长度校验和取消操作通过；Relays 能显示
  当前在线 Relay、容量与心跳，Drain 原因长度校验和取消操作通过，未改变 Relay
  desired state。
- Audit Log 可按 action 搜索并定位 `admin.principal.revoke`，结果数随筛选更新。
- Auditor 策略 dry-run、Draft `#6` 和 Publish `#6` 成功；被测用户获得 12 个全局
  只读能力，可读取 Users 与 Role Assignments，但没有 New user、启停、重置密码、
  Draft 或 Publish 操作。撤销并发布 Revision `#7` 后受保护读取立即返回 403。
- 审计导出 Job 完成并在 Downloads 生成 78 行、29,195 字节的 NDJSON；页面始终停留
  在 Audit Log，导出创建和两次读取均有成功审计事件。
- 中英文切换、认证表单必填校验、管理入口安全响应头和浏览器零控制台错误。
- `ui-e2e-ns-0814` 关联 Namespace Admin / `default` 后重新登录，只获得 1 个
  Namespace delegation，并可见 Namespace delegations、Sessions 与 Tasks；撤销
  assignment 并发布 Revision `#5` 后已禁用该用户；后续 Auditor 复验完成后再次于
  Revision `#7` 清理并禁用。

## 缺陷与修复状态

### ADM-UI-001：策略 Draft 无法发布（发布阻断）

- 修复状态：已修复、部署并完成真实浏览器授予与撤销闭环复验。
- 严重性：高；阻断用户权限授予/撤销的浏览器 E2E 和 V2-905 发布验收。
- 前置条件：以 `platform-admin` 登录，活动策略 Revision `#1`、ETag `1`。
- 复现步骤：
  1. 打开 Role Assignments，新增 Principal assignment。
  2. 选择 `ui-e2e-msrrqzns` 和 `Auditor`，填写至少 8 字符的原因。
  3. 执行 Validate / Dry-run，确认候选可发布。
  4. 创建 Draft `#3`，随后点击 Publish `#3`。
- 实际结果：发布连续两次返回
  `Idempotency-Key was already used for another request`；活动 revision/ETag
  未变化，测试用户没有获得角色。
- 预期结果：Publish 使用符合服务端契约的幂等键，在相同 base ETag 下原子发布
  Draft；成功后活动 revision 和 ETag 前进，重复同一发布请求安全回放。
- 调查方向：对齐 UI `mutation(..., { idempotent: true })` 生成的随机键与服务端
  “publish 必须复用 draft 幂等键”的契约；增加真实浏览器
  `draft -> publish -> retry -> revoke/rollback` 回归，不能只覆盖 HTTP 单元测试。
- 修复内容：创建 Draft 前显式生成幂等键并保存在 pending draft 状态，Publish 与
  重试复用同一个键；同时将空 Namespace scope、非法 DNS label、重复 Namespace、
  缺少 Principal/Group 等校验前移到 UI，非法候选不能 Dry-run 或创建 Draft。
- 自动化证据：Admin Vitest 4 个文件、25 项测试通过；管理后台 TypeScript 检查与
  Vite 生产构建通过；`go test ./internal/controlplane/admin/...` 与 storage 回归通过。
- 复验条件：授予 Auditor 成功；新会话只获得角色定义允许的只读菜单/API；写入控件
  不可见或禁用且越权写入被拒；
  移除 assignment 并发布后权限即时失效，活动策略无测试绑定残留。

### ADM-UI-002：390px 导航抽屉覆盖主内容且无法关闭

- 修复状态：已修复、部署并通过 `390x844` 真实浏览器复验。
- 严重性：中。
- 复现步骤：已登录状态下将视口设置为 `390x844` 并重新加载总览页。
- 实际结果：侧栏默认覆盖主内容，品牌、标题、Policy revision、Sign out 等文本
  与页面标题重叠；点击 Close 后覆盖层仍在。页面没有横向滚动，但视觉和交互布局
  不可用。
- 预期结果：窄屏首次加载只显示关闭状态的导航；Open navigation 打开模态抽屉，
  Close、选择导航项或 Escape 均关闭抽屉且主内容不重叠。
- 修复内容：窄屏导航保持默认离屏；Close、遮罩、页面切换和 Escape 均关闭抽屉，
  打开期间锁定页面滚动，关闭时恢复原滚动状态。
- 复验条件：`390x844` 首次加载、打开、关闭、页面切换和刷新五个状态均截图通过。

### ADM-AUTH-003：取消授权显示通用 400 错误

- 修复状态：已修复、部署；真实 Local account 取消后显示明确的授权取消提示。
- 严重性：中。
- 复现步骤：从管理登录页选择 Local account，在认证页点击 Cancel。
- 实际结果：返回管理登录页，但显示 `Request failed (400)`。
- 预期结果：取消是正常用户动作；返回登录页并显示明确的“已取消授权”状态，或静默
  返回，不应作为通用请求失败呈现。
- 修复内容：管理端在校验 OIDC transaction/state 后识别 `access_denied`，不再以空
  authorization code 请求 Token endpoint，改为稳定的 `OIDC_ACCESS_DENIED` 提示。
- 复验条件：取消不建立 Management Session，不遗留 OIDC transaction，并且页面无
  通用 4xx 错误。

### ADM-AUTH-004：密码错误没有可见反馈

- 修复状态：已修复、部署；错误密码显示通用脱敏提示，正确密码仍可登录。
- 严重性：中。
- 复现步骤：对已完成密码重置的本地用户提交旧密码。
- 实际结果：仍停留在原认证表单，URL 和字段保持不变，但没有可见错误说明。
- 预期结果：保持脱敏的同时显示稳定、可访问的认证失败提示；不得区分用户名不存在、
  用户禁用或密码错误。
- 修复内容：服务端只在 return query 的 transaction/CSRF 与提交值完全一致时 303
  返回认证页，并附加通用 `authentication_failed`；认证页使用 `role="alert"` 显示
  中英文一致语义的脱敏提示，不区分失败原因。
- 复验条件：错误密码与禁用用户都显示相同的通用提示，正确新密码仍可登录。

### ADM-E2E-005：本地 browserfixture 未覆盖新增管理 API

- 修复状态：已修复并完成 fixture 运行态 HTTP 冒烟。
- 类型：测试基础设施缺口，不是已部署 Control Plane 的产品故障。
- 实际结果：本地 `e2e/admin/browserfixture` 中 Users、Identity Providers、
  OAuth Clients、Account Security 返回 `Not Found`；策略 dry-run/发布与退出也无法
  完整复现真实集群行为。真实 Minikube 中相同只读页面可正常加载。
- 预期结果：fixture 注册与生产 Management Plane 相同的用户、Provider、OAuth Client、
  Account Security、Policy 与 logout 路由，并提供隔离的 SQLite 数据和可重复凭据。
- 修复内容：fixture 现装配真实 Local User、Provider revision、OAuth Client、Operations
  与 recovery 服务，启动审计导出 worker；OIDC fixture Principal 绑定真实 Local User。
- 自动化证据：运行 fixture 后 `users`、`providers`、`oauth-clients`、`users/me`、
  `audit` 均返回 200；审计导出首次读取 202，worker 处理后返回 200。
- 复验条件：同一浏览器脚本可先在 fixture 完成全部可变操作，再只用 Minikube 验证
  真实认证与部署集成；fixture 结果不得因缺路由产生假失败。

### ADM-AUTH-006：会话被撤销后控制台不返回登录页

- 修复状态：已修复、部署并通过两个隔离浏览器的真实撤销会话复验。
- 严重性：中。
- 前置条件：测试用户已在独立浏览器建立 Management Session。
- 复现步骤：管理员在 Principals 对该用户执行 Revoke all grants，然后在测试用户
  控制台点击 Refresh。
- 实际结果：后端撤销已生效，请求先后显示
  `management operation is not permitted` 和 `management authentication failed`，
  但侧栏、退出按钮和旧页面数据仍保留，页面不自动跳转登录入口。
- 预期结果：收到确定的认证失效响应后清空本地认证状态并跳转登录页；授权不足的
  403 仍应留在当前页面，不能与认证失效混淆。
- 修复内容：所有 Management API 401 统一触发认证失效事件，Shell 清除同步 CSRF
  并切回登录视图；403 保留原有授权不足处理。
- 自动化证据：管理员在内置浏览器撤销，测试用户在独立 Chrome 点击 Refresh 后立即
  回到登录页，旧页面数据和导航消失，浏览器控制台错误为零。
- 复验条件：撤销后第一次受保护请求自动返回登录页，历史敏感页面数据不再显示，
  重新登录前不触发重复 session exchange。

### ADM-UI-007：OAuth Client 对非法 Redirect URI 缺少前置校验

- 修复状态：代码已修复，前端正反例单元测试与生产构建通过。
- 严重性：中。
- 复现步骤：打开 New client，填写 Client ID `x`、Name `x` 和 Redirect URIs
  `not-a-url`。
- 实际结果：Save 从禁用变为可用，页面没有字段错误提示。
- 预期结果：每个 Redirect URI 必须在提交前按 OAuth 客户端 URI 规则校验；非法值
  保持 Save 禁用并给出可访问的字段提示。
- 修复内容：只允许 HTTPS，或 localhost/127.0.0.1/`::1` 的 HTTP loopback URI；拒绝
  相对地址、凭据和 fragment，非法值显示提示并禁用 Save。
- 复验条件：HTTPS、允许的 localhost/custom-scheme 正例可保存；相对地址、缺少
  scheme、带 fragment 等反例均在前端阻止，服务端继续执行同等校验。

### ADM-UI-008：OIDC Issuer URL 非法时仍可创建 Draft

- 修复状态：代码已修复，前端正反例单元测试与生产构建通过。
- 严重性：中。
- 复现步骤：在 Identity Providers 填写完整必填字段和合法变更原因，将 Issuer URL
  设为 `not-a-url`，不执行 connectivity validation。
- 实际结果：Create draft 从禁用变为可用，页面没有 URL 格式错误提示。
- 预期结果：Issuer 必须是符合 OIDC Discovery 要求的绝对 HTTPS URL；非法格式应在
  前端阻止创建，服务端也必须独立拒绝。
- 修复内容：Issuer 必须为无凭据、query 和 fragment 的绝对 HTTPS URL；非法值同时
  禁用 connectivity validation 与 Create draft。
- 复验条件：合法 HTTPS issuer 可继续 connectivity validation；相对地址、非 URL
  和不允许的 scheme 均显示字段错误且无法创建 Draft。

### ADM-UI-009：列表搜索条件跨页面串扰并隐藏真实资源

- 修复状态：已修复、部署并通过 Tasks 到 Relays 的真实浏览器往返复验。
- 严重性：中。
- 复现步骤：在 Tasks 搜索框输入 `ui-e2e-no-match`，随后直接切换到 Relays。
- 实际结果：Relays 继承相同搜索词，显示 `No matching records` 和 `0 records`；
  清空搜索后才显示实际在线的 1 个 Relay。Principals、Sessions、Tasks、Relays 与
  Audit Log 共用同一个 `ListPage` 实例，`search`、cursor 和 history 状态没有随
  `view` 切换重置。
- 预期结果：每个资源页使用独立筛选状态，或切换资源类型时明确重置搜索与分页；
  不应因上一页的隐式条件隐藏运行资源。
- 修复内容：`ListPage` 以资源 `view` 作为 React key，资源类型切换时重建搜索、cursor
  和 history 状态。
- 复验条件：在五个列表页间带搜索词和分页游标往返切换，每页初始结果、记录数和
  Previous/Next 均与自身查询一致。

### ADM-AUDIT-010：审计导出停留在 running 并离开管理后台

- 修复状态：已修复、部署并完成真实下载闭环；Minikube 已验证页面不离开 Audit Log、
  worker 完成任务、轮询读取成功、文件落盘并产生创建/读取审计事件。
- 严重性：高；审计证据无法通过 UI 导出。
- 复现步骤：在 Audit Log 点击 Export NDJSON，填写合法变更原因并确认。
- 实际结果：创建 Job `a6cda8d8-08ad-4003-965a-71f2f5ceaa8e` 后，当前标签页被
  导航到 `/api/admin/audit/exports/<job-id>`，只显示原始 JSON；多次等待和刷新仍为
  `running`，没有产生 NDJSON 下载。该 Job 会按服务端 24 小时过期策略保留，当前
  没有 UI 删除操作。
- 代码证据：原 UI 使用 `location.assign` 直接打开 Job API。复查确认生产
  `server_runtime.go` 已将 `ManagementOperations.Run` 注册为受管后台 worker，先前
  “生产未启动 worker”的调查结论不正确，不应重复启动第二个 worker。
- 预期结果：后台 Worker 持续处理 Job；UI 留在 Audit Log 内轮询 pending/running，
  成功后触发带 `Content-Disposition: attachment` 的 NDJSON 下载，失败时显示稳定错误。
- 修复内容：UI 保留在 Audit Log，轮询 202 状态，收到 200 NDJSON Blob 后创建下载并
  释放对象 URL；401 同步触发认证失效，failed 与 timeout 显示稳定错误。fixture 同样
  启动真实 Operations worker，运行态已验证 202 到 200。
- 自动化证据：Job `9fe0969a-8c2b-4901-8c3c-4edc4df5f5fa` 下载为同名 NDJSON，
  共 78 行、29,195 字节；首行包含预期审计字段，页面 URL 保持 `#/audit`。
- 复验条件：创建、轮询、成功下载、失败提示、刷新恢复、跨用户不可见和过期清理均
  通过；下载内容与当前筛选条件一致，并记录创建与读取审计事件。

### ADM-UI-011：命名空间角色用户缺少 Sessions 与 Tasks 导航

- 修复状态：已修复、部署并完成真实浏览器复验。
- 严重性：高；Namespace Admin 已获得服务端权限，但无法从 UI 进入运行资源页面。
- 复现步骤：创建新用户，关联 Namespace Admin / `default`，发布策略并以该用户登录。
- 实际结果：Bootstrap 显示 Global capabilities `0`、Namespace delegations `1`，但
  侧栏只有 Operations Overview 与 Account Security。
- 根因：Sessions/Tasks 导航只接受平台级 read capability；Bootstrap capability
  checks 也未查询 Namespace policy 的 read/delegate 能力。
- 修复内容：Bootstrap 增加 Namespace policy read/create 检查；导航能力支持“平台级
  read 或 `namespace.tasks.read`”，并增加 capability 正反例单元测试。
- 自动化证据：部署 Revision `#4` 后，受限用户可见 Namespace delegations、Sessions
  与 Tasks；Global capabilities 仍为 `0`，Namespace delegations 为 `1`。随后撤销
  assignment、发布 Revision `#5` 并禁用测试用户。

### ADM-DEV-012：gateway-dev 无法跨存储基线重新部署

- 修复状态：已修复；`go run ./build/gateway-dev.go` 已连续完成全量与增量部署。
- 类型：开发部署基础设施故障。
- 实际结果：Control Plane 因 schema `18` 低于 breaking baseline `20` 拒绝启动；
  Helm 回滚与 PVC 清理存在竞态，旧 Pod 会在清理后重新写入旧 schema。
- 修复内容：开发基线更新为 `20`；清理前恢复 pending Helm release，删除 PVC 前记录
  PV，并等待 PV 删除；Minikube 仅清理已解析的精确 hostPath 数据目录。
- 部署证据：Helm Revision `28` 成功，Control Plane、Gateway、Operator 均为 `1/1`
  Ready；当前镜像分别为 `dev-9f474077ecb0`、`dev-6494018cb6bd`、
  `dev-165e60e5f5b3`。

### ADM-UI-013：角色撤销后仍停留在已失权页面

- 修复状态：已修复、已部署并完成真实浏览器回归。
- 严重性：低；服务端已正确拒绝访问，未观察到数据泄露。
- 复现步骤：Auditor 停留在 Role Assignments；管理员撤销该角色并发布新策略；
  Auditor 刷新当前页后再重新加载浏览器。
- 实际结果：受保护读取立即返回 403；重新加载后侧栏正确收敛为 Overview 与 Account
  Security，但 hash/current view 仍是 Role Assignments，主区只显示
  `management operation is not permitted`。
- 预期结果：Bootstrap 发现当前 view 已不再具备 capability 时，将 hash 和当前页切换
  到 Overview；403 仍保留为服务端安全边界。
- 修复：根据 Bootstrap 返回的权威 capabilities 校验当前 hash；当前 view 已失权时使用
  `replaceState` 收敛到 `#/overview`，不因单个请求的瞬时 403 改写路由。
- 自动化回归：管理后台 Vitest `4` 个测试文件、`26` 项测试通过，生产构建通过；
  Helm Revision `39` 部署完成，Control Plane、Gateway、Operator 均成功 rollout。
- 真实浏览器回归：创建并启用隔离本地用户，授予 Auditor 后确认可进入
  `#/assignments`；管理员撤销并发布策略后，原页面刷新和直接访问旧深链接均自动切换
  为 `#/overview`，Role Assignments 不再渲染。测试 assignment 已撤销，测试账户已停用。

### ADM-UI-014：零权限用户的 Overview 显示原始 403

- 修复状态：已修复、已部署并完成真实浏览器回归。
- 严重性：低；未观察到数据泄露，服务端拒绝符合权限边界。
- 复现步骤：撤销用户全部角色并发布策略，刷新已失权页面；路由按 ADM-UI-013
  正确回退到 Overview。
- 实际结果：侧栏只保留 Overview 与 Account Security，但 Overview 仍请求受保护的
  `/overview` 数据，主区显示 `management operation is not permitted`。
- 预期结果：零权限用户仍可查看自身身份和授权摘要；无权读取系统概览时，不发起受保护
  请求，并显示明确的受限状态说明而非原始服务端错误。
- 修复：Overview 根据 Bootstrap 的权威全局能力判断
  `platform.overview.read`；无该能力时不调用 `/overview`、不显示刷新动作，保留认证方式、
  全局能力数和 Namespace 委派摘要，并显示本地化受限状态。
- 自动化回归：管理后台 Vitest `4` 个测试文件、`27` 项测试及生产构建通过；新增测试
  覆盖平台概览能力、Namespace 能力和空能力三种判断。
- 部署与浏览器证据：Helm Revision `41` 三项 rollout 成功；零权限隔离用户重新登录和
  刷新后均显示明确的受限说明。刷新后的 Control Plane 近 15 秒日志没有
  `/api/admin/overview` 请求、403 或 forbidden 记录。测试账户已再次停用。

## 发布与清理要求

- ADM-UI-001 在修复并完成授予与撤销闭环前，管理后台权限写入不满足发布门槛。
- 修复 UI 后必须同时运行桌面和 `390x844` 两种视口，并记录关键页面截图、失败请求
  与控制台错误；任何敏感字段必须脱敏。
- 自动化账号统一使用 `ui-e2e-*` 前缀。当前没有删除用户 API，测试结束必须撤销全部
  assignment、撤销会话、随机化密码并禁用账号；后续若增加删除接口，再将删除纳入
  标准清理流程。
- 修复后更新本文每项状态，并同步更新 `docs/v2-e2e-coverage.zh-CN.md` 与
  `docs/v2-security-test-matrix.zh-CN.md` 的管理面证据。
