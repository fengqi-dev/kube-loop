# KubeLoop

[![CI](https://github.com/fengqi-dev/kube-loop/actions/workflows/ci.yml/badge.svg)](https://github.com/fengqi-dev/kube-loop/actions/workflows/ci.yml)
[![Release](https://github.com/fengqi-dev/kube-loop/actions/workflows/release.yml/badge.svg)](https://github.com/fengqi-dev/kube-loop/actions/workflows/release.yml)
[![Latest release](https://img.shields.io/github/v/release/fengqi-dev/kube-loop)](https://github.com/fengqi-dev/kube-loop/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[English](README.md) · [简体中文](README_zh-CN.md)

**[官网](https://fengqi-dev.github.io/kube-loop/)** ·
**[下载](https://github.com/fengqi-dev/kube-loop/releases/latest)** ·
**[设计文档](docs/design.zh-CN.md)**

KubeLoop 是面向 Kubernetes 开发的桌面网络工具。它像一条只连接集群的 VPN，
让本地浏览器、IDE、CLI 和 SDK 可以直接使用 Pod IP、ClusterIP Service 与集群 DNS。

## 为什么使用 KubeLoop

- **透明访问集群**——普通本地应用可以直接使用真实的集群地址。
- **聚焦路由**——只有自动发现或手工配置的 Kubernetes 网段进入隧道。
- **无需公网 Gateway**——通过 Kubernetes API Server port-forward 访问集群内无特权 Gateway。
- **本地迭代工具**——Port Forward、Exchange、Mirror 与 Preview 覆盖出站和入站流量。
- **统一桌面工作流**——在一个 UI 中查看工作负载、使用 Pod SSH/SFTP、传输文件并诊断连接。
- **跨平台**——支持 macOS、Windows、Linux，以及 amd64 和 arm64。
- **运行时不依赖 `kubectl`**——桌面应用通过 client-go 和选中的 kubeconfig 直接工作。

## 安装

### macOS 与 Linux

```bash
curl -fsSL https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.sh | bash
```

指定版本或 Linux 包格式：

```bash
VERSION=v1.5.0 PACKAGE=deb \
  bash -c "$(curl -fsSL https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.sh)"
```

`PACKAGE` 可选 `deb`、`rpm` 或 `tarball`。

也可以通过 Homebrew 安装：

```bash
brew tap kube-loop/kubeloop https://github.com/fengqi-dev/kube-loop
brew install --cask kubeloop
```

### Windows

```powershell
irm https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.ps1 | iex
```

[GitHub Releases](https://github.com/fengqi-dev/kube-loop/releases/latest)
提供 DMG、NSIS、portable zip、deb、rpm 与 tar.gz；每个版本均包含 `SHA256SUMS`。

## 连接集群

1. 确保开发机可以访问 Kubernetes API Server，并准备好有效的 kubeconfig。
2. 打开 KubeLoop，选择 Context 与默认 Namespace。
3. 点击**连接**。
4. 首次使用时批准安装本地网络 Helper。
5. 权限允许时，KubeLoop 会在 `kubeloop-system` 中安装或升级无特权 Gateway。

连接后，即可从任意本地应用使用 ClusterIP、Pod IP 或集群域名。

## 开发工作流

| 工作流 | 流量路径 | 使用场景 |
| --- | --- | --- |
| 透明访问 | 本地应用 → 集群 | 直接打开内部 Service 或调试 Pod IP |
| Port Forward | 本地端口 → Pod 或 Service | 将单个集群端口暴露到 `localhost` |
| Exchange | 现有 Service → 本地应用 | 保留 ClusterIP 与 DNS，同时用本地进程替换后端 |
| Mirror | 现有 Service → Pods + 本地 Shadow | 复制请求用于观察，不让本地应用进入主路径 |
| Preview | 新建临时 Service → 本地应用 | 让集群内调用方访问本地应用 |
| Pod SSH/SFTP | 本地 SSH 客户端 → `pods/exec` | 无需容器运行 `sshd` 即可使用 Shell 或传输文件 |

工作流停止或集群连接关闭时，KubeLoop 会恢复或删除受影响的 Service、Endpoints 与
EndpointSlice。

## 架构

```text
本地应用
  → 平台 TUN + split DNS
  → 托管 sing-box
  → 本地 SOCKS Bridge
  → Kubernetes API Server port-forward
  → 集群内无特权 Gateway
  → Pods / Services / CoreDNS
```

Gateway 负责最终的集群内连接，不包含 ServiceAccount token，也不使用
`hostNetwork`、`privileged` 或 `NET_ADMIN`。本地 Helper 仅管理 KubeLoop 的
sing-box 进程、TUN interface、route、split DNS 与恢复状态。

完整控制面、数据面与恢复机制请参阅[系统设计](docs/design.zh-CN.md)和
[统一流量数据面](docs/singbox-traffic-dataplane.zh-CN.md)。

## Pod SSH

Pod SSH 在 TUN 模式下工作，不会在容器中安装 `sshd`。KubeLoop 验证本地 SSH 客户端，
并将 channel 映射到 Kubernetes `pods/exec`。

- SSH login name 用于选择容器，不改变容器内实际进程用户。
- 交互式 Shell 和远程命令要求容器提供 `/bin/sh`。
- `scp` 与 `sftp` 使用内置 SFTP adapter；文件传输要求容器提供 `tar`。
- 缺少 SSH identity 时，KubeLoop 可以创建 `~/.ssh/id_ed25519`，不会覆盖已有 identity。
- 断开连接会删除仅运行时存在的 SSH endpoint，不会修改 Pod。

## 面向编辑器和 Agent 的 MCP

KubeLoop 可以通过 Streamable HTTP 暴露本地
[Model Context Protocol](https://modelcontextprotocol.io/) 服务，支持 Codex、
Claude Code、Cursor 与 VS Code。

MCP 默认关闭，仅监听 `127.0.0.1`，并默认使用自动生成的 Bearer token。集群操作使用
当前已认证的 Gateway Session；MCP 不会加载本地 kubeconfig，也不能绕过 Gateway 策略
或 Kubernetes 授权。

| 工具 | 范围 |
| --- | --- |
| `manage_cluster` | 读取集群能力；列出 Namespace、Service 与 Pod |
| `manage_connection` | 读取、连接或显式断开当前 Session |
| `manage_traffic` | 启动、停止和列出 Port Forward、Exchange、Mirror 与 Preview Task |
| `exec_pod_command` | 通过已认证的 Gateway exec stream 执行精确 argv |
| `manage_file_transfer` | 启动、列出或取消本地 ↔ Pod 文件传输 |

配置方式参阅[官网 MCP 指南](https://fengqi-dev.github.io/kube-loop/#/mcp)。

## 从源码构建

环境要求：

- [`go.mod`](go.mod) 声明的 Go 版本
- Node.js 22+
- 当前平台的 [Wails 前置依赖](https://wails.io/docs/gettingstarted/installation)

```bash
make build
```

常用前端命令：

```bash
cd frontend
npm ci
npm run dev          # 桌面端前端
npm run dev:admin    # 管理控制台
npm run dev:site     # 公开站点
npm run build:site   # 将生产站点写入 ../site
```

Admin Console 与公开站点均使用 React、Vite、Tailwind CSS 4 和 shadcn 规范。
GitHub Pages 会在发布前重新构建站点。

## 文档

- [系统设计](docs/design.zh-CN.md)
- [Operator 指南](docs/operator.zh-CN.md)
- [统一流量数据面](docs/singbox-traffic-dataplane.zh-CN.md)
- [安全测试矩阵](docs/v2-security-test-matrix.zh-CN.md)
- [架构决策记录](docs/adr/)

## License

[MIT](LICENSE)
