# KubeLoop V2 流量数据面

## 目标

V2 数据面为 Cluster Session 已授权的目标提供稳定的本地 SOCKS，以及可选 TUN 访问。
它不暴露 Kubernetes API Server、不依赖 kubeconfig，也不会向集群内 Data Plane 交付
Kubernetes 凭据。

## 路径

```text
应用 -> TUN/SOCKS -> sing-box -> WSS Relay -> Data Plane -> 集群目标
```

Control Plane 不在 packet path 中。它创建并授权 Session，返回 NetworkSpec，分配 Data Plane，
签发 RelayTicket。只有 ticket 与活动 Session generation 一致时，Relay 与 Data Plane 才接收流量。

## 本地模式

**SOCKS 模式**启动稳定的 loopback SOCKS5 endpoint 与托管 sing-box，不需要特权 Helper。
应用可以直接使用该 endpoint，桌面端的 Port Forward 与工作负载工具也复用它。

**TUN 模式**复用相同 Session transport，并增加平台 Helper。Helper 只安装 NetworkSpec 中的
Pod/Service CIDR 与 split-DNS rule，监督 sing-box，并记录足以撤销部分安装或异常退出的状态。
切换 Namespace 或 NetworkSpec 会创建新的 Helper generation。

## Transport

一条物理 WSS 可以承载多个逻辑 stream。每次 open 都包含该 Session 已授权的 destination 与
protocol。短期 RelayTicket 绑定 identity、device、Session、generation、分配的 Data Plane 与用途。
轮换时先建立新认证 transport，再 drain 旧 generation，并尽量保持本地 endpoint 稳定。

TCP 保留 half-close 与终态错误。UDP 使用有界逻辑 association，带 idle expiry 和源/目标元数据。
容量限制同时作用于全局、identity、Session 和 transport；被拒绝的工作不能泄漏 slot。

## DNS 与路由

NetworkSpec 是 cluster CIDR、DNS server、search domain 与 cluster domain 的唯一来源。split DNS
只把集群域名送往集群 DNS；TUN 只安装已授权 CIDR。Session 不可用时这些路由必须 fail closed，
不能回退到开发机直连路径。

## Task 数据路径

- Port Forward 使用稳定 loopback listener，每个连接或 UDP association 打开一个授权 stream。
- Exchange/Preview 通过 Relay 把 Data Plane 流量交给 Session-owned 本地 endpoint。
- Mirror 复制请求 stream，原 backend 仍是唯一主响应来源。
- Pod exec 与文件操作使用独立的 Control Plane Task stream，因为 Kubernetes SPDY 与授权属于控制面。

## 恢复不变量

- transport 不能跨 Profile、Session、Namespace、generation 或 Data Plane 复用。
- logout、grant 撤销、Session 过期或显式断开会关闭该 Session 的全部 stream 与本地 listener。
- Helper cleanup 必须幂等，并校验 owner/generation。
- 持久化流量资源按 Task 与 TrafficBinding ownership 调谐，不能只依赖名称。
- metrics 使用有界 label，不暴露 user、device、Session、token 或 destination secret。

端到端证据见 [V2 E2E 覆盖矩阵](v2-e2e-coverage.zh-CN.md)。
