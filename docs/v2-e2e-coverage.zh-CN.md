# V2 远程功能对等 E2E 矩阵

本文档是 V2-609 的验收清单。V2 用例必须通过类型化客户端、Controller API、远程 Task 和真实 Kubernetes 资源完成操作；桌面端不得读取 kubeconfig 或直接调用 Kubernetes API。测试集位于 `e2e/v2dataplane`，使用真实 Minikube、真实 SPDY exec/port-forward、真实 TCP/UDP 和当前工作树构建的 Gateway 镜像。

## V1 功能到 V2 证据

| V1 能力 | V2 远程 E2E | 关键断言 |
| --- | --- | --- |
| SOCKS 访问 ClusterIP | `TestGatewayPodRestartRecoversV2DataPlane` | 固定 loopback SOCKS 经 WSS/RelayTicket 访问真实 ServiceIP。 |
| TUN 访问 ClusterIP、PodIP | `TestGatewayPodRestartRecoversV2DataPlane` | 真实 sing-box/Helper 安装 TUN；ServiceIP、观察到的 PodIP 均不旁路数据面。 |
| 手工网络范围 | `TestGatewayPodRestartRecoversV2DataPlane` | Session 使用显式、哈希绑定的 PodCIDR、ServiceIP、DNS 和 cluster domain NetworkSpec。 |
| 集群 DNS、更新与清理 | `TestGatewayPodRestartRecoversV2DataPlane` | SOCKS 和 TUN 解析真实 `*.svc.cluster.local`；正常停止与核心异常退出后 Helper/DNS 清理。 |
| Service/Pod Port Forward | `TestGatewayPodRestartRecoversV2DataPlane` | Service 与 Pod 两类目标分别覆盖 TCP/UDP；重连前后本地端口稳定，停止后端口释放且 Task 为 `stopped`。 |
| HTTP Gateway 多路复用 | `TestGatewayPodRestartRecoversV2DataPlane` | 同一 Gateway 上并发快/慢流、四个 Port Forward、两名 Principal；慢流不阻塞兄弟流。 |
| Exchange TCP/UDP | `TestRealExchangeLifecycleAndStaleOwnerRecovery` | 集群 Service 流量到桌面本地目标；正常停止、客户端崩溃、撤权和 stale-owner 恢复均还原 Service。 |
| Mirror TCP/UDP | `TestRealMirrorPreservesPrimaryPathAndRecoversStaleOwner` | 原 Pod 响应始终为主路径，桌面只收到副本；本地响应不能泄漏；覆盖停止、崩溃、撤权和恢复。 |
| Preview TCP/UDP | `TestRealPreviewLifecycleOwnershipAndStaleRecovery` | 创建真实 Service/EndpointSlice；名称冲突与外部替换安全；停止、崩溃、撤权和恢复按 Task UUID 清理。 |
| Pod exec/TTY | `TestRealPodExecTTYDisconnectAndControllerRestart` | 真实 SPDY exec、stdin、TTY resize、异常 WebSocket 关闭、Controller 停止/重启及重启后再次执行。 |
| Pod exec 撤权 | `TestRealPodExecStopsWhenTokenFamilyIsRevoked` | Token Family 撤销主动终止真实 Pod 命令并写入取消终态。 |
| 文件上传/下载与恢复 | `TestRealFileTransferRevocationControllerRestartAndResume` | 8 MiB 真实文件、校验和、撤权、Controller 重启、Pod 端 partial resume 和完整下载校验。 |
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
| Controller 崩溃/重启 | Pod exec、文件传输、Exchange/Mirror/Preview stale owner | 活跃工作终止或保留恢复意图；接替 Controller 可继续执行或补偿 Kubernetes 资源。 |
| Token Family 撤销 | Exchange、Mirror、Preview、Pod exec、文件传输 | 无需客户端 DELETE，授权 lease 主动终止；所有资源与流被清理。 |
| Kubernetes API 暂时不可用 | Preview lifecycle 的 client-go transport 503 gate | 真实删除请求命中 503，Task 与 snapshot 保持 `recovering`；API 恢复后接替 Reconciler 完成精确所有者清理。 |
| 网络路径中断 | loopback Gateway proxy 切到不可达地址再切换备用路径 | TUN 不允许静默旁路；本地 listener 和 Helper session 不重装；新 generation 恢复。 |
| 多身份隔离 | 两名 Principal、跨 Session token、metrics | 跨 Session 控制令牌拒绝；指标不暴露 Principal、Device 或 Session ID。 |

## Kubernetes 资源不变量

- Service 接管前同时快照 EndpointSlice 与 legacy Endpoints；两者同时存在时也不能择一忽略。
- 接管时先删除 legacy Endpoints，再删除原 EndpointSlice，避免 EndpointSlice mirroring controller 重建旧 Pod 后端。
- 恢复时分别恢复两类快照；只有恢复成功后才能删除 durable rollback snapshot。
- Preview 只删除携带完全匹配 Task UUID owner metadata 的 Service/EndpointSlice，不能删除用户替换的资源。
- Mirror E2E 的 Controller 运行在宿主机，生产默认仍直连 PodIP；测试注入的 primary dialer 仅把已由服务端快照授权的相同 TCP/UDP 后端映射到真实 Echo Pod NodePort，以跨越宿主机与 Minikube PodCIDR 的拓扑边界。

## 运行方式

```bash
KUBELOOP_E2E=1 \
KUBELOOP_E2E_CONTEXT=minikube \
KUBELOOP_GATEWAY_IMAGE=<由当前工作树构建的镜像> \
KUBELOOP_SINGBOX_PATH=<当前 sing-box 路径> \
go test -tags=e2e ./e2e/v2dataplane -count=1 -timeout=20m -parallel=1 -p=1 -v
```

2026-08-10 的 V2-609 最终验收复跑为 8/8 通过，整包耗时 `125.367s`；加入 Data Plane core 异常退出清理后的目标用例单独复跑耗时 `12.930s`。此前同一覆盖集复跑耗时 `120.620s`。
