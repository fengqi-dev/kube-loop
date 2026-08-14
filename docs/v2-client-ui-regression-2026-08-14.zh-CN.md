# KubeLoop 客户端自动化回归记录（2026-08-14）

## 测试环境

- macOS 客户端：`wails dev`
- 服务端：`wails dev` 触发 `gateway-dev`，最终 Helm revision 38
- Kubernetes：v1.35.1，namespace `default`
- 测试工作负载：Pod `static-web`（`10.244.0.25:80`）、Service `my-service`（`10.98.116.101:80`）
- 集群域：`cluster.local`

## 回归结果

| 场景 | 结果 | 证据 |
| --- | --- | --- |
| Server Profile 与服务发现 | 通过 | 客户端识别 Kubernetes v1.35.1、Gateway dev、Pod/Service CIDR 与集群 DNS |
| OAuth 登录与会话恢复 | 通过 | 系统凭据库恢复登录，会话状态为 active |
| SOCKS 数据通道 | 通过 | `127.0.0.1:1080` 监听；Pod IP、Service ClusterIP、Service DNS 均返回 nginx 页面 |
| 客户端网络自检 | 通过 | Gateway transport 与 cluster DNS 检查成功 |
| Workload 列表 | 通过 | 正确展示 `default/static-web`、Pod IP、Ready 状态、节点与 TCP/80 |
| Port Forward | 通过 | 自动分配 `127.0.0.1:58929`，HTTP 返回 nginx 页面；停止后无 active operation |
| TUN 首次连接 | 通过 | privileged helper 已运行，连接过程未再次请求管理员密码 |
| TUN Pod/Service 路由 | 通过 | Pod IP、Service ClusterIP 均可在无 SOCKS 参数时直接访问 |
| TUN 集群 DNS | 通过 | macOS resolver 指向本地 KubeLoop DNS；Service FQDN 可直接访问 |
| Chrome 实际访问 | 通过 | Chrome 打开 `my-service.default.svc.cluster.local` 并显示 nginx 页面 |
| TUN 断开清理 | 通过 | KubeLoop `utun1025` 路由和 cluster DNS resolver 被移除 |
| Pod SSH | 通过 | TUN 下创建 `10.244.0.25:22` 端点，`web@10.244.0.25` 执行命令返回 `KUBELOOP_SSH_OK` 与容器内 `uid=0(root)`；停止后无 active operation |
| SFTP 文件生命周期 | 通过 | `go.mod` 上传、远端重命名、下载和删除均成功；本机、Pod 两端 SHA-256 均为 `90f74468...0876cf`，History 显示双向任务 Completed |
| Service Exchange | 通过 | `my-service:80` 被交换到本机 `127.0.0.1:19080`，经 ClusterIP 获取的 `go.mod` SHA-256 与本机一致；停止后恢复 nginx |
| Service Mirror | 通过 | ClusterIP 主请求仍返回 nginx，本机镜像服务收到带回归标记的同一 GET 请求；停止后无 active session |
| Service Preview | 通过 | Pod 通过临时 Service 访问本机 `127.0.0.1:19080`，创建后立即及空闲 65 秒后两次内容哈希均一致；停止后临时 Service 已删除 |

## 本轮已修复缺陷

### CLIENT-AUTH-001：登录后版本发现被 namespace 授权错误拒绝

- 现象：客户端显示已登录，但 `GET /kubeloop/api/version` 返回 403，后续 Bootstrap 无法完成。
- 根因：版本发现发生在 namespace 选择之前，却被映射到 `namespace.access`；空 namespace 请求会被授权引擎拒绝。
- 修复：版本元数据读取仍要求认证，但不再进入 namespace 授权；后续 capability 与 inventory 请求继续执行 namespace 授权。
- 防回归：新增中间件测试，断言认证后的 `/version` 会进入 handler 且不会调用 namespace authorizer。
- 部署验证：重新运行 `gateway-dev`，Helm revision 30 滚动成功，客户端 Bootstrap、SOCKS 与 TUN 全链路通过。

## 后续修复（revision 31）

### CLIENT-UI-002：侧栏可能显示旧 UUID，而非当前登录用户（已修复）

- 现象：当前 OAuth access token 为 opaque token，客户端无法从 token 解码用户名，UI 回退到 profile 中旧的 `lastUserName`。
- 修复：从 OIDC ID Token 提取当前主体 ID 与显示名，并随凭据版本写入系统凭据库；refresh 响应没有新 ID Token 时保留当前身份。侧栏、概览卡片与当前会话徽标只显示当前 `AuthSession` 身份，不再回退到历史 profile。
- 兼容：凭据元数据升级为 schema v2，仍可读取无版本和 schema v1 的旧记录。
- 验证：实际 Wails 客户端侧栏显示 `KubeLoop Administrator`，不再显示旧 UUID。

### CLIENT-UI-003：开发控制台存在 React ref 警告（已修复）

- 现象：`AppSidebar` / `SidebarMenuItem` 路径出现 “Function components cannot be given refs”。
- 修复：`SidebarMenuItem` 使用 `React.forwardRef` 把 ref 转发到实际 `<li>` 元素；TypeScript 与生产构建通过。

### CLIENT-UI-007：TUN 模式自检成功文案仍写 “Local SOCKS”（已修复）

- 现象：TUN 已连接时执行网络自检，成功 toast 仍显示 “Local SOCKS, Gateway transport, and cluster DNS are reachable.”
- 修复：成功提示根据当前数据通道显示 `System TUN` 或 `Local SOCKS`。
- 验证：实际 TUN 自检提示为 “System TUN, Gateway transport, and cluster DNS are reachable.”

### CLIENT-PERF-006：前端构建产物存在大于 500 kB 的 chunk（已修复）

- 现象：主入口 chunk 为 611 kB，Vite 产生 chunk size warning。
- 修复：按 Radix、图标、图表、终端和通用 UI 依赖拆分稳定 vendor chunk。
- 验证：最大 chunk 约 331 kB，生产构建不再产生大 chunk 或循环 chunk 告警。

## 后续修复（revision 33）

### CLIENT-NET-008：Preview 等反向流量连接空闲 60 秒后失效（已修复）

- 现象：Preview 创建后短时间显示 `running`，但空闲约 60 秒后，Pod 访问临时 Service 得到空响应；Gateway 记录对应 `/traffic/v1/preview/...` WebSocket 在约 60 秒时结束，客户端 UI 随后才移除会话。
- 根因：Gateway 的业务心跳只请求 Control Plane，没有在反向流量 WebSocket 上产生数据。默认 60 秒空闲超时的入口会关闭该连接；Exchange、Mirror 在同样的长时间空闲条件下也存在风险。
- 修复：每次 traffic heartbeat 同时对 WebSocket 执行 Ping；Ping 或 Control Plane heartbeat 任一失败都会取消该流量会话并进入既有清理流程。
- 防回归：Gateway traffic API 测试通过计数连接断言 heartbeat 会写入 WebSocket keepalive frame；`go test ./internal/gateway/trafficapi -count=10` 及相关 reverse relay / Preview 包测试通过。
- 部署验证：`go run ./build/gateway-dev.go` 构建 Gateway `dev-bc3abbd46a1e` 并部署 Helm revision 33；Preview 创建后立即访问成功，空闲 65 秒后再次访问仍成功，两个响应 SHA-256 均为 `90f74468...0876cf`；停止后 `client-regression-preview` Service 不存在。

## 后续修复（revision 37）

### CLIENT-DEV-005：绑定生成器重复提示 `Not found: time.Time`（已修复）

- 现象：`wails dev` 每次生成 bindings 时重复提示找不到 `time.Time`，对应 TypeScript 字段退化为 `any`，但编译和运行仍成功。
- 根因：暴露给 Wails 的认证、文件、任务、远端操作与更新模型直接使用 `time.Time`，未声明生成端的 TypeScript 类型。
- 修复：为所有 Wails 暴露的时间字段增加 `ts_type:"string"`，保持 JSON 传输格式不变，并让生成模型使用明确的 ISO 时间字符串类型。
- 验证：`wails build -s -nopackage -m -nocolour` 不再输出 `Not found: time.Time`；生成的 `models.ts` 中相关字段均为 `string`；前端 TypeScript 与 Vite 生产构建通过。

### CLIENT-DEPLOY-009：开发栈滚动时 Gateway 首次启动可能瞬时失败（已修复）

- 现象：revision 33 整体滚动时，Gateway 首次连接 Control Plane Relay 注册端点得到 `connection refused`，容器退出并由 Kubernetes 重启一次。
- 根因：Gateway 启动只尝试注册一次，把 Control Plane 尚未 Ready 的短暂网络错误当成永久启动失败。
- 修复：启动注册默认最多尝试 10 次、间隔 500 ms，并响应 context 取消；仅对网络错误、HTTP 429 与 5xx 重试，永久 4xx 仍立即失败。
- 防回归：注册测试覆盖前两次 503 后成功、401 仅请求一次不重试，以及包装后的 `connection refused` 被判定为可重试；`go test ./internal/gateway/relayagent -count=10` 与竞态检测通过。
- 部署验证：`gateway-dev` 构建 Gateway `dev-b1e8b0670fbe` 并部署 Helm revision 37；Control Plane、Gateway、Operator 新 Pod 均为 Ready 且 restart count 为 0，Gateway 首次启动即完成 Relay 注册。

## 核心功能扩展回归（revision 38）

| 核心场景 | 结果 | 本轮证据 |
| --- | --- | --- |
| 凭据恢复与 OAuth 会话 | 通过 | Wails 启动后自动恢复 `KubeLoop Administrator`，服务端显示 Session active |
| namespace 与工作负载授权 | 通过 | namespace 列表可见，`default/static-web` 清单及 Pod IP、Ready、Node、TCP/80 信息正确；认证、授权、管理配置 Go 包回归通过 |
| SOCKS | 通过 | Pod IP `10.244.0.25`、Service IP `10.98.116.101`、Service DNS 均返回 nginx；客户端网络自检通过 |
| Port Forward | 通过 | UI 自动分配 `127.0.0.1:55064`，真实 HTTP 请求成功；停止后 Active operations 为空 |
| Service Exchange | 通过 | `my-service:80` 交换到本机 `127.0.0.1:19080`，经 Service IP 读取 `go.mod`，SHA-256 为 `90f74468...0876cf` |
| Service Mirror | 通过 | ClusterIP 主请求仍由 nginx 返回，本机镜像服务同步收到 `/mirror-core-check` 请求 |
| Service Preview | 通过 | 创建 `client-core-preview` 后，集群内 BusyBox Pod 通过 Service DNS 读取本机 `go.mod`，SHA-256 一致；停止后 Service 与 TrafficBinding 均删除 |
| System TUN | 通过 | 无代理参数访问 Pod IP、Service IP 与 Service DNS 均成功；路由使用 KubeLoop `utun1025`，客户端自检显示 System TUN、Gateway transport、cluster DNS 全部可达 |
| TUN 清理 | 通过 | 断开后 `utun1025` 路由与 cluster DNS resolver 消失，目标路由恢复到既有 `utun1024` |
| SSH / SFTP / Pod Exec | 通过 | E2E 的 SSH、多用户身份隔离、文件传输恢复与 Pod Exec/授权撤销用例通过 |
| Gateway 重启与远程 TUN | 通过 | Gateway Pod 重启恢复、真实 Helper/TUN wake refresh 用例通过 |
| 权限与认证单测 | 通过 | authorization、managementconfig、admin HTTP API、OAuth/OIDC/authn 全部通过；Admin 前端 25 项、Auth 前端 3 项测试通过 |

- 部署状态：Control Plane `dev-d58ee589178e`、Gateway `dev-4639a5678d24`、Operator `dev-ec1f206d1f44` 均 Ready，restart count 均为 0。
- 注意：Preview 新建 ClusterIP 不在创建会话时签发的 NetworkSpec 目标集合中，客户端经 SOCKS 主动访问会被 Gateway 拒绝；Preview 的设计入口是集群内工作负载访问临时 Service，本轮按该路径验证通过。

## 已知问题与修复跟踪

### CLIENT-DEV-004：Wails 开发桥偶发 runtime 消息错误

- 严重度：低
- 现象：开发日志重复出现 `Unknown message from front end: runtime:ready`，页面控制台曾出现读取空 `nodes` 的 IPC TypeError。
- 影响：本轮未阻断业务流程，仅在开发桥启动/热重载时出现。
- 定位：Wails v2.13.0 自带 desktop runtime 会发送 `runtime:ready`，同版本 dispatcher 不接受该消息；项目依赖与本机 CLI 均已对齐 v2.13.0。当前官方最新稳定 v2 仍为 v2.13.0，因此不在项目内修改依赖缓存或迁移到 Wails v3 alpha。
- 建议：作为上游开发模式问题跟踪；生产包回归继续确认不影响业务调用。

### CLIENT-DEV-010：开发 Helper 原地升级无法停止 KeepAlive LaunchDaemon

- 严重度：中
- 状态：已按 ADR 0023 修复并完成 macOS 实机回归
- 现象：`e2e/run.sh` 检测到 Helper 二进制不一致后执行升级，安装器终止旧进程，但 launchd 立即重新启动 `dev.fengqi.kubeloop.helper.dev`，最终报 `launchd service ... is still running`。
- 修复：新增稳定的 `dev.fengqi.kubeloop.supervisor.dev` LaunchDaemon，首次授权安装 Supervisor + Worker；后续精确 Worker 更新通过受限 Socket 完成。原安装器的 `bootout` 立即检查也改为 5 秒有界等待。
- 回归：Supervisor Socket 为当前用户所有的 `0600`；Supervisor/Worker 均由 root:wheel 安装并处于 running；byte-distinct Worker 无密码升级后 SHA-256 从 `e2fa0878…` 更新为 `dbf561fe…`，protocol 6、CoreReady；错误组件身份在切换前拒绝且 Worker 保持运行。

### CLIENT-E2E-011：宿主机监听型反向流量 E2E 与真实部署结果不一致

- 严重度：中（自动化覆盖缺口，当前未复现为部署功能故障）
- 状态：已修复、已部署并完成真实 Minikube 回归。
- 现象：`TestRealExchangeLifecycleAndStaleOwnerRecovery`、`TestRealMirrorPreservesPrimaryPathAndRecoversStaleOwner`、`TestRealPreviewLifecycleOwnershipAndStaleRecovery` 在宿主机测试 Gateway 模式下超时；隔离开发 Operator 后仍可复现。
- 对照验证：同一版本的真实 Wails 客户端与集群内 Gateway 上，Exchange、Mirror、Preview 均通过，TrafficBinding 与 EndpointSlice 状态正确且停止后清理完成。
- 根因：并非宿主机监听地址不可达。开发栈 Control Plane 的 TrafficBinding 恢复器会扫描同一集群内全部 `managed-by=kubeloop-control-plane` 的 CR；E2E 使用独立内存数据库创建 TrafficBinding，开发实例查不到对应 Task，因而在 CR Ready 的同一秒将其误判为 orphan 并删除。Operator 随后按 finalizer 正确恢复原 Service，探针只能得到原后端响应。
- 修复：TrafficBinding 新增稳定的 `traffic.kubeloop.io/control-plane-id` 所有权标签；创建、幂等校验、删除和 orphan 恢复均限定在同一 Control Plane ID。合法短 ID 保持可读，超长或非法 label 值使用稳定 SHA-256 摘要；E2E 使用独立 ID，避免与已部署开发栈互相清理。测试 Gateway 同时记录 stream Finish 的 failed/reason 诊断。
- 部署证据：Helm Revision `40`，Control Plane 镜像 `dev-b74fdde3dc6e`、Gateway `dev-155738cc0ae1`、Operator `dev-615d053e917a`，三项 rollout 均成功。
- 回归证据：TrafficBinding client、Operator controller、Control Plane 单元测试通过；真实 Minikube 上 Exchange `36.79s`、Mirror `46.47s`、Preview `17.46s` 全部通过，覆盖显式停止、客户端崩溃、stale owner 恢复、主路径保留与资源清理。
