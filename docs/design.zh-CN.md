# KubeLoop V2 系统设计

KubeLoop 在不向桌面端交付 kubeconfig 或 Kubernetes 凭据的前提下，让开发机访问已授权的
Kubernetes 网络和工作负载操作。

## 组件

- **Desktop**：保存 Server Profile，并将 OAuth/OIDC 凭据放入系统凭证库；负责 UI、
  loopback SOCKS、托管 sing-box、流量 listener、Pod SSH adapter、文件传输和可选 MCP。
- **Local Helper**：仅供 TUN 模式使用，管理 TUN interface、route、split DNS、sing-box
  生命周期与异常恢复。SOCKS 模式不需要 Helper。
- **Control Plane**：负责身份认证、策略和 Kubernetes SSAR、Cluster Session、Task、
  Kubernetes API/SPDY 操作，并签发短期 RelayTicket。
- **Relay**：接收受 RelayTicket 约束的 WebSocket transport 并复用逻辑 stream；它不是权限数据库。
- **Data Plane**：分配给特定 Cluster Session，连接已授权的集群目标；不持有 OAuth token、
  kubeconfig 或 Kubernetes 凭据。
- **Operator**：调谐持久化 `TrafficBinding`，为 Exchange、Mirror、Preview 恢复或清理资源。

## 连接生命周期

1. 用户添加 KubeLoop Server HTTPS 地址，桌面端读取 discovery document 并保存 Server Profile。
2. 通过系统浏览器完成 Authorization Code + PKCE 登录，Access/Refresh 凭据只属于该 Profile。
3. 用户选择已授权 Namespace 与 SOCKS/TUN 模式。
4. 桌面端创建 Cluster Session；Control Plane 返回精确 NetworkSpec、分配的 Data Plane 与短期 RelayTicket。
5. 桌面端建立 RelayTicket 认证的 WSS。SOCKS 监听 loopback；TUN 还会通过 Helper 安装范围明确的 route 与 split DNS。
6. heartbeat 续期 Session 并轮换 transport 材料。断开、logout、切换 Profile/Namespace 或 grant 撤销都会清理旧 Task 与 Session。

```text
本地应用
  -> TUN 或 loopback SOCKS
  -> 托管 sing-box
  -> RelayTicket 认证的 WSS Relay
  -> 分配给当前 Session 的 Data Plane
  -> Pod / Service / CoreDNS
```

只有签名 NetworkSpec 中的目标进入数据路径。TUN 对这些路由必须 fail closed，不能把无关的
开发机流量静默导入代理。

## 控制面与数据面边界

桌面端只调用已认证的 Control Plane API。Kubernetes discovery、策略校验、Pod exec、
文件操作、Task 所有权与补偿全部在服务端完成。Data Plane 只获得承载某个 Session stream
所需的信息。RelayTicket 绑定 user、device、Session、generation、Data Plane、有效期和用途。

桌面生产依赖图不引入 Kubernetes client，也不读取 `KUBECONFIG`。Data Plane 不依赖
Control Plane、OAuth、数据库或 Kubernetes client。架构测试会阻止这两类回流。

## 流量工作流

- **Port Forward**：将 loopback TCP/UDP endpoint 映射到 Pod 或 Service。
- **Exchange**：保留现有 Service 身份，临时用本地进程替换后端。
- **Mirror**：保留 Pod 主响应，只把副本送给本地观察程序，本地响应不能进入主路径。
- **Preview**：创建临时 Service，把集群请求送到本地进程。

每个 mutation 都携带精确 `profileId`、`sessionId`、`namespace`，创建接口使用 idempotency key。
服务端持久化 snapshot 与 `TrafficBinding` ownership，使接替的 Control Plane 能在客户端或
controller 失败后继续完成清理。

## 工作负载操作

Pod exec/TTY 使用 Control Plane Task 和认证 WebSocket，实际 Kubernetes SPDY 操作由
Control Plane 执行。Pod SSH 是 loopback、public-key-only 的 exec adapter，不会在 Pod 中
安装 `sshd`。文件传输与 Pod 文件 list/create/rename/delete 均受当前 Profile、Session、
Namespace、Pod、container 的共同约束。

## MCP 边界

MCP 只监听 loopback，默认启用自动生成的 bearer token。V2 暴露六个工具：
`manage_cluster`、`manage_connection`、`manage_traffic`、`exec_pod_command`、
`manage_file_transfer`、`manage_pod_files`。MCP 与 UI 使用同一套 Control Plane client 和
活动身份，不能选择其他已保存 Profile、读取 kubeconfig 或修改 Helper/网络配置。

## 故障与恢复

- Relay/Data Plane 中断时轮换 transport 材料；恢复成功时保持本地 SOCKS 地址稳定。
- OAuth grant 撤销会主动终止 Session 与 Task，不等待桌面端发送 DELETE。
- TUN 正常关闭或异常退出时，Helper recovery record 清理 route、DNS、interface 与托管进程。
- Exchange/Mirror/Preview 按 owner 恢复；UID/controller ownership 不匹配时绝不删除资源。
- Task 终态与 request ID 用于审计诊断；凭据和 MCP token 不写入普通 Profile 文件。

MCP 授权详见 [ADR 0015](adr/0015-v2-mcp-trust-boundary.md)，transport 细节详见
[V2 数据面设计](singbox-traffic-dataplane.zh-CN.md)。
