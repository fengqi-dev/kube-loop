# KubeLoop

[![CI](https://github.com/fengqi-dev/kube-loop/actions/workflows/ci.yml/badge.svg)](https://github.com/fengqi-dev/kube-loop/actions/workflows/ci.yml)
[![Release](https://github.com/fengqi-dev/kube-loop/actions/workflows/release.yml/badge.svg)](https://github.com/fengqi-dev/kube-loop/actions/workflows/release.yml)
[![Latest release](https://img.shields.io/github/v/release/fengqi-dev/kube-loop)](https://github.com/fengqi-dev/kube-loop/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[English](README.md) · [简体中文](README_zh-CN.md)

**[官网](https://fengqi-dev.github.io/kube-loop/)** ·
**[下载](https://github.com/fengqi-dev/kube-loop/releases/latest)** ·
**[设计文档](docs/design.zh-CN.md)**

KubeLoop 是面向 Kubernetes 开发的桌面网络工具。它将本地工作站接入集群网络，
让浏览器、IDE、CLI 和 SDK 可以直接使用 Pod IP、ClusterIP Service 与集群 DNS
——无需 VPN，无需公网入口。

## 为什么使用 KubeLoop

- **透明访问集群**——普通本地应用可以直接使用真实的集群地址。
- **不依赖 kubeconfig 或 `kubectl`**——桌面应用登录 KubeLoop Server，不持有 Kubernetes 凭据。
- **无需公网集群入口**——通过 RelayTicket 认证的 WebSocket 将流量送达分配的 Data Plane。
- **聚焦路由**——只有自动发现或手工配置的 Kubernetes 网段进入隧道。
- **本地迭代工具**——Port Forward、Exchange、Mirror 与 Preview 覆盖出站和入站流量。
- **流量检查**——通过代理拦截并解析实时 HTTP 与 gRPC 流量，自动生成 `curl`/`grpcurl` 复现命令，支持导入 `.proto` Schema 解码。
- **统一桌面工作流**——在一个 UI 中查看工作负载、使用 Pod SSH/SFTP、传输文件并诊断连接。
- **跨平台**——支持 macOS、Windows、Linux，以及 amd64 和 arm64。

## 安装

### 使用 Helm 安装 KubeLoop Server

前置条件：

- Kubernetes 1.25 或更高版本，以及 Helm 3
- 已路由到 Kubernetes Ingress Controller 的域名

安装已发布的 OCI Chart。默认情况下，Helm 会自动生成并保留 RelayTicket
签名密钥与内部 Relay Registry TLS Secret：

```bash
helm upgrade --install kubeloop \
  oci://ghcr.io/fengqi-dev/kube-loop/charts/kubeloop \
  --version 2.2.0-rc.1 \
  --namespace kubeloop-system \
  --create-namespace \
  --set publicURL=http://kubeloop.example.com \
  --set ingress.enabled=true \
  --set ingress.host=kubeloop.example.com \
  --set ingress.className=nginx \
  --wait
```

请根据实际环境替换版本、域名和 Ingress Class。安装后查看初始管理员说明，
并验证服务发现接口：

```bash
helm get notes kubeloop --namespace kubeloop-system
curl http://kubeloop.example.com/.well-known/kubeloop
```

Ingress 默认不启用 TLS。若需 HTTPS，请把 `publicURL` 改为 `https://…`，
并设置 `ingress.tls.enabled=true` 和 `ingress.tls.secretName`。

卸载工作负载：

```bash
helm uninstall kubeloop --namespace kubeloop-system --wait
```

Chart 会删除工作负载和由 Chart 创建的 SQLite PVC，但会有意保留 CRD，以及自动生成的
认证和初始化 Secret。密钥生成、Gateway API、外部 PostgreSQL/MySQL、升级与彻底清理步骤
请参阅[完整 Helm 指南](charts/kubeloop/README.md)。

### 桌面客户端

#### macOS 与 Linux

```bash
curl -fsSL https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.sh | bash
```

指定版本或 Linux 包格式：

```bash
VERSION=v2.2.0-rc.1 PACKAGE=deb \
  bash -c "$(curl -fsSL https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.sh)"
```

`PACKAGE` 可选 `deb`、`rpm` 或 `tarball`。
在 Debian/Ubuntu 上，安装脚本默认等待 unattended upgrades 或其他包管理器最多
300 秒以释放 dpkg 锁；可通过 `APT_LOCK_TIMEOUT` 调整。不要手动删除 dpkg lock 文件。

也可以通过 Homebrew 安装：

```bash
brew tap kube-loop/kubeloop https://github.com/fengqi-dev/kube-loop
brew install --cask kube-loop/kubeloop/kubeloop-desktop
```

#### Windows

```powershell
irm https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.ps1 | iex
```

[GitHub Releases](https://github.com/fengqi-dev/kube-loop/releases/latest)
提供 DMG、NSIS、portable zip、deb、rpm 与 tar.gz；每个版本均包含 `SHA256SUMS`。

### 终端客户端

K9s 风格的终端客户端提供核心连接与 Kubernetes 资源工作流，无需启动桌面 UI。

macOS 或 Linux：

```bash
curl -fsSL https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install-tui.sh | bash
```

Windows：

```powershell
irm https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install-tui.ps1 | iex
```

设置 `VERSION=v2.1.3` 可安装指定版本。安装脚本会选择匹配平台的
`kubeloop-tui-<version>-<os>-<arch>.tar.gz`，使用 Release 中的 `SHA256SUMS`
完成校验后再安装 `kubeloop`（Windows 为 `kubeloop.exe`）。

Release 同时提供 macOS、Windows、Linux 的 amd64 与 arm64 产物。TUI 与桌面客户端
共用 KubeLoop Server Profile 和 Control Plane API，不读取 kubeconfig，也不直接调用
Kubernetes。资源、命令、配置及测试边界参见 [TUI 使用指南](docs/tui.zh-CN.md)。

Homebrew 中的 `kubeloop-tui` Formula 与 `kubeloop-desktop` Cask 独立安装；
Formula 安装后的命令仍为 `kubeloop`：

```bash
brew tap kube-loop/kubeloop https://github.com/fengqi-dev/kube-loop
brew install --formula kube-loop/kubeloop/kubeloop-tui
```

## 连接集群

1. 打开 KubeLoop，添加 KubeLoop Server 的 HTTP 或 HTTPS 地址并完成能力发现。
2. 在系统浏览器中登录，然后选择已授权的 Namespace。
3. 选择 **SOCKS5 proxy** 或 **TUN mode**，再点击**连接**。
4. 仅 TUN 模式首次使用时需要批准安装本地网络 Helper。

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
| 流量检查 | 本地应用 → 集群（经代理） | 拦截并解析 HTTP/gRPC 请求，支持复现命令与 `.proto` 解码 |

工作流停止或集群连接关闭时，KubeLoop 会恢复或删除受影响的 Service、Endpoints 与
EndpointSlice。

## 架构

```text
本地应用
  → TUN 或 SOCKS5 + 托管 sing-box / split DNS
  → RelayTicket 认证的 WSS Relay
  → 分配给当前 Session 的 Data Plane
  → Pods / Services / CoreDNS
```

**Control Plane** 负责身份认证、策略、Cluster Session、任务所有权与 Kubernetes
资源操作。**Data Plane** 仅承载已授权 Session 的流量，不持有 Kubernetes 凭据。
本地 **Helper** 只在 TUN 模式管理 sing-box 进程、interface、route、split DNS
与恢复状态；SOCKS 模式无需特权 Helper。

完整控制面、数据面与恢复机制请参阅[系统设计](docs/design.zh-CN.md)和
[统一流量数据面](docs/singbox-traffic-dataplane.zh-CN.md)。

## Pod SSH

Pod SSH 使用 loopback、public-key-only endpoint，不会在容器中安装 `sshd`。
KubeLoop 验证本地 SSH 客户端，并将 channel 映射到当前 Cluster Session 的
Control Plane `pods/exec` Task。

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
当前已认证的 Server Profile 与 Cluster Session；MCP 不会加载本地 kubeconfig，也不能
绕过 Control Plane 策略或 Kubernetes 授权。

| 工具 | 范围 |
| --- | --- |
| `manage_cluster` | 读取集群能力；列出 Namespace、Service 与 Pod |
| `manage_connection` | 读取、连接或显式断开当前 Session |
| `manage_traffic` | 启动、停止和列出 Port Forward、Exchange、Mirror 与 Preview Task |
| `exec_pod_command` | 通过已认证的 Control Plane exec stream 执行精确 argv；输出使用 base64 |
| `manage_file_transfer` | 启动、列出或取消本地 ↔ Pod 文件传输 |
| `manage_pod_files` | 列出、创建、重命名或删除 Pod 文件与目录 |

配置方式参阅[官网 MCP 指南](https://fengqi-dev.github.io/kube-loop/#/mcp)。

## 从源码构建

环境要求：

- [`go.mod`](go.mod) 声明的 Go 版本
- Node.js 22+
- 当前平台的 [Wails 前置依赖](https://wails.io/docs/gettingstarted/installation)

```bash
make build          # 构建桌面应用
make test-local     # 运行非 E2E 测试与 vet
make vulncheck      # 检查 Go 依赖的已知漏洞
```

常用前端命令：

```bash
cd frontend
npm ci
npm run dev          # 桌面端前端
npm run dev:admin    # 管理控制台
npm run dev:auth     # 认证页面
npm run dev:site     # 公开站点
npm run build:site   # 将生产站点写入 ../site
```

管理控制台、认证页面与公开站点均使用 React、Vite、Tailwind CSS 4 和
shadcn 规范。GitHub Pages 会在发布前重新构建站点。

## 文档

- [系统设计](docs/design.zh-CN.md)
- [Operator 指南](docs/operator.zh-CN.md)
- [统一流量数据面](docs/singbox-traffic-dataplane.zh-CN.md)
- [V2 路线图](docs/v2-roadmap.zh-CN.md)
- [E2E 覆盖范围](docs/v2-e2e-coverage.zh-CN.md)
- [Kubernetes 调用点](docs/v2-kubernetes-call-sites.zh-CN.md)
- [DNS 延迟报告](docs/dns-latency-report.zh-CN.md)
- [安全测试矩阵](docs/v2-security-test-matrix.zh-CN.md)
- [架构决策记录](docs/adr/)

## License

[MIT](LICENSE)
