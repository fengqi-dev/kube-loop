# V2 远程功能对等 E2E 矩阵

本文档是 V2-609 的验收清单。V2 用例必须通过类型化客户端、Control Plane API、远程 Task 和真实 Kubernetes 资源完成操作；桌面端不得读取 kubeconfig 或直接调用 Kubernetes API。测试集位于 `e2e/dataplane`，使用真实 Minikube、真实 SPDY exec/port-forward、真实 TCP/UDP 和当前工作树构建的 Gateway 镜像。

## V1 功能到 V2 证据

| V1 能力 | V2 远程 E2E | 关键断言 |
| --- | --- | --- |
| SOCKS 访问 ClusterIP | `TestGatewayPodRestartRecoversV2DataPlane` | 固定 loopback SOCKS 经 WSS/RelayTicket 访问真实 ServiceIP。 |
| TUN 访问 ClusterIP、PodIP | `TestGatewayPodRestartRecoversV2DataPlane` | 真实 sing-box/Helper 安装 TUN；ServiceIP、观察到的 PodIP 均不旁路数据面。 |
| 手工网络范围 | `TestGatewayPodRestartRecoversV2DataPlane` | Session 使用显式、哈希绑定的 PodCIDR、ServiceIP、DNS 和 cluster domain NetworkSpec。 |
| 集群 DNS、更新与清理 | `TestGatewayPodRestartRecoversV2DataPlane` | SOCKS 和 TUN 解析真实 `*.svc.cluster.local`；正常停止与核心异常退出后 Helper/DNS 清理。 |
| Service/Pod Port Forward | `TestGatewayPodRestartRecoversV2DataPlane` | Service 与 Pod 两类目标分别覆盖 TCP/UDP；重连前后本地端口稳定，停止后端口释放且 Task 为 `stopped`。 |
| HTTP Gateway 多路复用 | `TestGatewayPodRestartRecoversV2DataPlane` | 同一 Gateway 上并发快/慢流、四个 Port Forward、两名 Identity；慢流不阻塞兄弟流。 |
| Exchange TCP/UDP | `TestRealExchangeLifecycleAndStaleOwnerRecovery` | 集群 Service 流量到桌面本地目标；正常停止、客户端崩溃、撤权和 stale-owner 恢复均还原 Service。 |
| Mirror TCP/UDP | `TestRealMirrorPreservesPrimaryPathAndRecoversStaleOwner` | 原 Pod 响应始终为主路径，桌面只收到副本；本地响应不能泄漏；覆盖停止、崩溃、撤权和恢复。 |
| Preview TCP/UDP | `TestRealPreviewLifecycleOwnershipAndStaleRecovery` | 创建真实 Service/EndpointSlice；名称冲突与用户接管安全；停止、崩溃、撤权和恢复按 TrafficBinding name/UID/controller reference 清理。 |
| Pod exec/TTY | `TestRealPodExecTTYDisconnectAndControllerRestart` | 真实 SPDY exec、stdin、TTY resize、异常 WebSocket 关闭、Control Plane 停止/重启及重启后再次执行。 |
| Pod exec 撤权 | `TestRealPodExecStopsWhenOAuthGrantIsRevoked` | OAuth grant 撤销主动终止真实 Pod 命令并写入取消终态。 |
| 文件上传/下载与恢复 | `TestRealFileTransferRevocationControllerRestartAndResume` | 8 MiB 真实文件、校验和、撤权、Control Plane 重启、Pod 端 partial resume 和完整下载校验。 |
| 文件管理特殊路径、目录 | `TestRealFileTransferRevocationControllerRestartAndResume` | 含空格、单引号、`$`、`;` 的创建/改名/列表/删除；嵌套文件和空目录双向传输并保持内容。 |
| Pod SSH 命令与容器目标 | `TestRealPodSSHThroughGatewayAndLocalIdentityIsolation` | loopback SSH 经远程 exec 到指定容器；非所有者本地公钥被拒绝。 |
| Pod SSH SCP/SFTP | `TestRealPodSSHThroughGatewayAndLocalIdentityIsolation` | OpenSSH SCP/SFTP 文件与递归目录双向传输，包含零字节文件和空目录。 |
| 本地 Helper 边界 | `TestGatewayPodRestartRecoversV2DataPlane` 与 `e2e/platform` | V2 仅用 Helper 安装本地 TUN/DNS；实际进程 SIGKILL、Helper ACL 和平台恢复继续由特权平台 E2E 验证。 |

## 故障与终止矩阵

| 场景 | 证据 | 预期结果 |
| --- | --- | --- |
| 正常停止 | Data Plane、Port Forward、Exchange、Mirror、Preview、Pod SSH、文件 Task | listener、Helper、DNS、Kubernetes 资源和 rollback snapshot 全部释放；Task 进入 `stopped`。 |
| 客户端崩溃 | Data Plane core `Done/Err` 注入；Exchange/Mirror/Preview `CloseNow`；Pod exec 异常 WebSocket 关闭 | 异常 Task 进入 `failed` 或取消终态，但资源必须恢复且 snapshot 清零；本地 SOCKS/TUN/DNS 必须清理。 |
| Gateway 崩溃 | 删除真实 Gateway Pod | 活跃流在 drain 窗口结束；稳定 SOCKS/TUN/Port Forward 地址不变；Session generation 和 RelayTicket 刷新后恢复。 |
| Control Plane 崩溃/重启 | Pod exec、文件传输、Exchange/Mirror/Preview stale owner | 活跃工作终止或保留恢复意图；接替 Control Plane 可继续执行或补偿 Kubernetes 资源。 |
| OAuth grant 撤销 | Exchange、Mirror、Preview、Pod exec、文件传输 | 无需客户端 DELETE，授权 lease 主动终止；所有资源与流被清理。 |
| Kubernetes API 暂时不可用 | Preview lifecycle 的 client-go transport 503 gate | 真实删除请求命中 503，Task 与 snapshot 保持 `recovering`；API 恢复后接替 Reconciler 完成精确所有者清理。 |
| 网络路径中断与权限刷新 | loopback Gateway proxy 切到不可达地址、heartbeat 刷新 NetworkSpec 后切换备用路径 | TUN 不允许静默旁路；本地 SOCKS/Port Forward 地址保持；NetworkSpec 变化时旧 Helper session 被清理并按新 allowlist 重装，随后普通 Gateway 重启不重复重装。 |
| 多身份隔离 | 两名 Identity、跨 Session token、metrics | 跨 Session 控制令牌拒绝；指标不暴露 Identity、Device 或 Session ID。 |

## Kubernetes 资源不变量

- Service 接管前同时快照 EndpointSlice 与 legacy Endpoints；两者同时存在时也不能择一忽略。
- 接管时先删除 legacy Endpoints，再删除原 EndpointSlice，避免 EndpointSlice mirroring controller 重建旧 Pod 后端。
- 恢复时分别恢复两类快照；只有恢复成功后才能删除 durable rollback snapshot。
- Preview CR 名称确定性包含 Task UUID；实际 Service/EndpointSlice 只由完全匹配的 TrafficBinding name、UID 与 controller reference 控制，不能删除用户接管的资源。
- Mirror E2E 的 Control Plane 运行在宿主机，生产默认仍直连 PodIP；测试注入的 primary dialer 仅把已由服务端快照授权的相同 TCP/UDP 后端映射到真实 Echo Pod NodePort，以跨越宿主机与 Minikube PodCIDR 的拓扑边界。

## 身份、容量与平台证据

- `e2e/ui/admin` 在全新真实 Control Plane 上完成一次性 IAM bootstrap、本地账号 Authorization Code + PKCE、管理会话、目录/Namespace/OAuth Client 写入、审计导出、退出登录以及 Password/Implicit/Hybrid 拒绝；`internal/client/auth` 与 `internal/controlplane/authn/oauthserver` 分层验证 Access/Refresh Token 轮换、重放拒绝与撤销。
- `make capacity-baseline` 验证全局/单用户物理 WSS、逻辑 stream 上限、容量释放，以及满载时 live/ready/metrics；三轮 32 KiB stream benchmark 记录吞吐和分配。`TestGatewayPodMultiUserCapacityRSSAndCleanup` 在单个真实 Gateway Pod 上以四名 Identity/四条物理 WSS/十六条逻辑 stream 验证限额、容量复用、集群内吞吐和 kubelet working-set 曲线；满载期间撤销 OAuth grant，确认 Preview Task、relay、TrafficBinding、Service、EndpointSlice 和 snapshot 在 5 秒预算内全部清理。
- Windows/macOS workflow 运行全量本地测试和真实 Helper 安装、升级、ACL、DNS 恢复、卸载；Linux 额外运行完整 Minikube TUN。`e2e/remotetun` 已接入三平台 workflow，使用实际 Helper、sing-box TUN、WSS/smux Gateway、RelayTicket/NetworkSpec 和精确目标路由，验证休眠间隔触发 transport 刷新后 SOCKS 地址、TUN core 与 Helper Session 保持不变，且停止后无特权资源残留。最终门禁已由 GitHub Actions push workflow [31454657118](https://github.com/fengqi-dev/kube-loop/actions/runs/31454657118) 完成：Windows、macOS、Linux、Helm、主 Go 与前端六个作业全部通过，V2-803 已关闭。

## UI 自动化回归

`e2e/ui/run-real-environment.sh` 是当前完整 UI 验收入口。它在隔离 Minikube 中部署当前
工作树的 Control Plane、Gateway 与 Operator，从启动日志安全取得一次性 bootstrap
token，先运行 Playwright 管理后台套件，再构建真实 `KubeLoop.app` 并运行 XCUITest。
测试不使用 mock API，也不恢复已经删除的旧 OIDC/browserfixture 兼容层。

浏览器套件覆盖 IAM 初始化、本地账号 PKCE 登录、所有管理页面、移动导航、用户组及
Namespace 权限、本地用户、邀请、OAuth Client、Recovery、审计导出、退出登录和旧
OAuth grant 拒绝。macOS 原生套件覆盖 Wails 主导航、系统浏览器登录、跨 Tab 登录状态、
Host Aliases、可编辑 SOCKS 端口、SOCKS/TUN 连接与断开、TUN 连接时不误选 SOCKS，
以及 MCP 启停和未携带 Bearer Token 的真实 HTTP 拒绝。

`.github/workflows/ui-e2e.yml` 每日定时运行，并作为 release workflow 的发布门禁；普通
CI 同时构建并测试 Admin/Auth 前端，运行 Linux Helper 平台 E2E 和独立 Operator E2E。

## 运行方式

```bash
KUBELOOP_E2E=1 \
KUBELOOP_E2E_CONTEXT=minikube \
KUBELOOP_REMOTE_TUN_E2E=1 \
KUBELOOP_GATEWAY_IMAGE=<由当前工作树构建的镜像> \
KUBELOOP_SINGBOX_PATH=<当前 sing-box 路径> \
KUBELOOP_E2E_PACKAGES='./e2e/dataplane ./e2e/remotetun' \
bash ./e2e/scripts/run-go-test.sh
```

通用入口会在目标 context 安装当前 TrafficBinding CRD，并以隔离 kubeconfig 启动当前工作树 Operator；专用 Kubebuilder 套件仍由 `make operator-test-e2e` 独立执行。2026-08-11 加入真实单 Pod 容量/RSS/满载撤权，以及 Helper/TUN/WSS 休眠恢复后，`e2e/dataplane` 完整复跑通过（`194.739s`），`e2e/remotetun` 通过（`1.647s`）。此前 File Transfer 同场景连续 10 次复跑通过；Preview 专项确认 TrafficBinding、Service、EndpointSlice 与 durable snapshot 全部释放（`30.398s`）；隔离 kubeconfig 的 Operator 部署、受保护 metrics、Preview TrafficBinding 调谐与 finalizer 清理套件 3/3 通过，且中断后可幂等重跑；`make helm-test-e2e` 在 Minikube Kubernetes v1.35.1 完整通过 SQLite/PostgreSQL 安装、独立升级、扩缩容、回滚、Pod 恢复、审计/管理撤权、数据保留和卸载/CRD retain。
