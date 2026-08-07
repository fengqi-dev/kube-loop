# KubeLoop

[![CI](https://github.com/fengqi-dev/kube-loop/actions/workflows/ci.yml/badge.svg)](https://github.com/fengqi-dev/kube-loop/actions/workflows/ci.yml)
[![Release](https://github.com/fengqi-dev/kube-loop/actions/workflows/release.yml/badge.svg)](https://github.com/fengqi-dev/kube-loop/actions/workflows/release.yml)
[![Latest release](https://img.shields.io/github/v/release/fengqi-dev/kube-loop)](https://github.com/fengqi-dev/kube-loop/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[English](README.md) | [简体中文](README_zh-CN.md)

**[官网](https://fengqi-dev.github.io/kube-loop/)** ·
**[下载](https://github.com/fengqi-dev/kube-loop/releases/latest)** ·
**[文档](#文档)**

KubeLoop 是一款面向 Kubernetes 开发的桌面网络工具。它像一条只连接集群的 VPN，
让本地浏览器、IDE、CLI 和 SDK 可以直接访问 Pod IP、ClusterIP Service 和
`*.cluster.local` 域名。

![KubeLoop 概览](site/assets/kubeloop-social-preview.png)

## 核心特性

- **透明访问**——普通本地应用可直接使用集群地址。
- **分流路由**——只有 Kubernetes 流量进入隧道，不影响其他网络访问。
- **无需公网 Gateway**——流量经过 Kubernetes API Server port-forward，不需要
  NodePort、LoadBalancer 或 Ingress。
- **统一桌面工作流**——在一个 UI 中完成连接、查看、转发、Exchange、Mirror、
  Preview、文件传输和诊断。
- **跨平台**——支持 macOS、Windows、Linux，以及 amd64 和 arm64。
- **不依赖 `kubectl`**——KubeLoop 通过 client-go 和用户 kubeconfig 直接访问集群。

## 快速开始

### 1. 安装

**macOS 或 Linux**

```bash
curl -fsSL https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.sh | bash
```

安装脚本会自动选择最新版本和当前 CPU 架构。macOS 下载 DMG；Linux 优先安装
deb/rpm，没有对应包时解压 tarball。

指定版本或 Linux 包格式：

```bash
VERSION=v1.5.0 PACKAGE=deb \
  bash -c "$(curl -fsSL https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.sh)"
```

`PACKAGE` 支持 `deb`、`rpm` 和 `tarball`。

**Homebrew**

```bash
brew tap kube-loop/kubeloop https://github.com/fengqi-dev/kube-loop
brew install --cask kubeloop
```

**Windows**

```powershell
irm https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.ps1 | iex
```

也可以从 [GitHub Releases](https://github.com/fengqi-dev/kube-loop/releases/latest)
下载 DMG、NSIS installer、portable zip、deb、rpm 或 tar.gz。每个 Release 都提供
`SHA256SUMS`。

### 2. 连接集群

1. 确保工作站可以访问 Kubernetes API Server，并准备好有效的 kubeconfig。
2. 打开 KubeLoop，选择 kubeconfig Context 和默认 Namespace。
3. 点击**连接**。
4. 首次使用时批准安装本地虚拟网络 Helper。
5. Kubernetes 权限允许时，KubeLoop 会在 `kubeloop-system` 中安装或升级无特权
   Gateway。

连接成功后，即可从任意本地应用直接访问 ClusterIP、Pod IP 或集群域名。

## 可以做什么

| 目标 | 工作流 |
| --- | --- |
| 访问集群内部 Service | 连接后使用 ClusterIP 或集群 DNS 名称 |
| 直接调试 Pod | 连接后使用 Pod IP |
| 不安装 `sshd`，通过 SSH/SFTP 访问 Pod | 在**工作负载**中为 Pod 开启 **SSH** |
| 将 Pod 或 Service 端口暴露到本地 | **Port Forward** |
| 让现有 Service 调用本地应用 | **Exchange** |
| 将 Service 请求复制给本地观察程序 | **Service Mirror** |
| 通过临时 ClusterIP 暴露本地应用 | **Preview** |
| 在本机与 Pod 之间传输文件 | **文件管理器** |

### 流量工作流

- **Exchange** 保留现有 Service 的 ClusterIP 和 DNS 名称，将后端替换为本地进程。
- **Service Mirror** 保留原始 Pod 作为主路径，同时把每个请求复制给本地 Shadow
  进程；Shadow 响应会被丢弃。
- **Preview** 创建一个由本地进程提供服务的临时 ClusterIP Service。

工作流停止或集群连接断开时，KubeLoop 会恢复或删除相关 Service、Endpoints 和
EndpointSlices。

### Pod SSH

Pod SSH 在 TUN 模式下工作，不会在容器中安装或运行 `sshd`。KubeLoop 使用用户的标准
SSH identity 认证本地客户端，再将 SSH channel 映射到 Kubernetes `pods/exec`。

- SSH login name 用于选择 Kubernetes container，不会改变容器内进程的实际用户。
- 交互式 Shell 和远程命令要求容器提供 `/bin/sh`。
- `scp` 和 `sftp` 使用内置 SFTP 适配器，文件传输要求容器提供 `tar`。
- 如果不存在 `id_ed25519`、`id_ecdsa` 或 `id_rsa`，KubeLoop 会创建
  `~/.ssh/id_ed25519`，且不会覆盖现有 identity。
- 断开连接会删除仅在运行期间存在的 SSH endpoint，不会修改 Pod。

## 工作原理

```text
本地应用
  → 平台 TUN + split DNS
  → 托管 sing-box
  → 本地 SOCKS Bridge
  → Kubernetes API Server port-forward
  → 集群内无特权 Gateway
  → Pods / Services / CoreDNS
```

只有自动发现或手工配置的集群 route 会进入隧道。Gateway 在集群内建立最终连接，
不包含 ServiceAccount token，也不使用 `hostNetwork`、`privileged` 或 `NET_ADMIN`。

本地 Helper 只需安装一次，仅负责 KubeLoop 的 sing-box 进程、TUN interface、route、
split DNS 和恢复状态，可随时在设置中查看或卸载。

完整的控制面、数据面和恢复机制请参阅
[系统设计](docs/design.zh-CN.md)和
[统一流量数据面](docs/singbox-traffic-dataplane.zh-CN.md)。

## Kubernetes 权限

KubeLoop 使用所选 kubeconfig Context 中的身份。Gateway 不包含 Kubernetes 凭证，
也不会调用 Kubernetes API。

桌面端身份需要访问每个目标 Namespace（包括 `kubeloop-system`）中的 Pod 和 Service。
集群级读取权限只用于自动发现网络；权限不足时可以手工填写网络参数。如果当前身份不能
创建 Namespace，请让管理员预先创建 `kubeloop-system`。

<details>
<summary>Namespace Role 示例</summary>

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: kubeloop
  namespace: <namespace>
rules:
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list", "watch"]
  - apiGroups: [""]
    resources: ["pods/exec", "pods/portforward"]
    verbs: ["create"]
  - apiGroups: [""]
    resources: ["services"]
    verbs: ["get", "list", "watch", "create", "update", "delete"]
  - apiGroups: [""]
    resources: ["endpoints"]
    verbs: ["get", "create", "update"]
  - apiGroups: ["discovery.k8s.io"]
    resources: ["endpointslices"]
    verbs: ["get", "list", "create", "update", "delete"]
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["get", "list", "watch", "create", "update"]
```

请在 KubeLoop 需要访问的每个 Namespace 中，将此 Role 绑定到 kubeconfig 用户。

</details>

## 编辑器与 Agent 的 MCP

KubeLoop 可以通过 Streamable HTTP 将后端暴露为本地
[Model Context Protocol](https://modelcontextprotocol.io/) Server。

1. 打开 **MCP** 页面。
2. 选择 Codex、Claude Code、Cursor 或 VS Code。
3. 点击**安装 MCP Server**，或复制生成的配置。

MCP 默认关闭，只监听 `127.0.0.1`，并支持可选 Bearer 认证。它使用当前 kubeconfig
身份，不需要额外的 Kubernetes 权限。

| Tool | 操作 |
| --- | --- |
| `manage_cluster` | 重新加载/探测 Context；列出 Namespace、Service 和 Pod |
| `manage_connection` | 查询状态和配置、连接或断开 |
| `manage_network` | 查询或修改网络参数和 Host Alias |
| `manage_traffic` | 启动、停止或列出流量工作流 |
| `manage_helper` | 查看、安装或卸载 Helper |
| `exec_pod_command` | 在 Pod container 中执行命令 |
| `manage_file_transfer` | 启动、列出或取消本机 ↔ Pod 传输 |

配置方式和请求示例参阅 [MCP 指南](https://fengqi-dev.github.io/kube-loop/mcp.html)。

## 安全模型

- kubeconfig 凭证始终留在 Go 桌面进程中。
- Gateway 无特权、没有 Kubernetes 凭证，也没有公网入口。
- 每次连接使用随机的 256-bit Gateway capability。
- 特权 Helper 只接受经过认证、字段受限的 IPC，不接受任意命令或可执行文件路径。
- sing-box 和 feature inbound 只监听本地，并使用每个 Session 独立的凭证。
- 集群 route 仅限经过校验的 Pod 和 Service 目标。
- 资源变更具备事务和恢复能力。
- 日志会脱敏凭证、证书、token 和 kubeconfig 内容。

信任边界和 capability 模型详见
[安全与权限](docs/design.zh-CN.md#11-安全与权限)。

## 平台支持

| 项目 | 支持 |
| --- | --- |
| 桌面系统 | macOS、Windows、Linux |
| CPU | amd64、arm64 |
| UI | 跟随系统明暗主题；English 和简体中文 |
| 安装包 | DMG、NSIS、zip、deb、rpm、tar.gz、Homebrew Cask |
| 应用状态 | `~/.kubeloop` |
| 网络核心 | 每个 Release 随附固定版本 sing-box |
| 更新 | 在设置中检查 GitHub Release |

## 开发

环境要求：Go 1.26+、Node.js 22+、Wails 2.13。

```bash
git clone --recurse-submodules https://github.com/fengqi-dev/kube-loop.git
cd kube-loop
go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0
npm ci --prefix frontend
wails dev
```

`wails dev` 会构建前端和平台 Helper，并为 Docker Desktop Kubernetes、Minikube、
kind 或 k3d 准备本地 Gateway 镜像。仅在需要覆盖该镜像时设置
`KUBELOOP_GATEWAY_IMAGE`。

### 测试

```bash
npm run build --prefix frontend
go test ./...
```

运行 Minikube 端到端测试：

```bash
./e2e/run.sh
```

平台本地测试：

```bash
./e2e/scripts/run-local-macos.sh --timeout 25m
./e2e/scripts/run-local-linux.sh --timeout 25m
```

```powershell
.\e2e\scripts\run-local-windows.ps1 -Timeout 25m
```

<details>
<summary>调试随附的 sing-box 源码</summary>

如果 clone 时没有拉取 submodule，请先初始化：

```bash
git submodule update --init --recursive
```

在 VS Code 中先启动 `Wails: Debug KubeLoop` 并连接集群，再启动
`third_party: Debug Sing-box`。Delve 会附加到以 root 运行的 sing-box 进程。
TCP 和 UDP 的 SOCKS outbound 断点位于
`third_party/sing-box/protocol/socks/outbound.go`。

使用命令行开发：

```bash
KUBELOOP_SINGBOX_SOURCE=debug wails dev
sudo "$(go env GOPATH)/bin/dlv" attach "$(pgrep -n -x sing-box)"
```

</details>

### 构建

```bash
VERSION=v1.5.0
VITE_APP_VERSION="$VERSION" wails build -ldflags "-X main.version=${VERSION}"
```

推送 `v*` tag 会触发 Release workflow，构建桌面安装包、Gateway binary 和 image、
校验文件、Homebrew Cask 及项目官网。

## 文档

- [产品指南](https://fengqi-dev.github.io/kube-loop/product.html)
- [工作流](https://fengqi-dev.github.io/kube-loop/workflows.html)
- [系统设计](docs/design.zh-CN.md)
- [统一流量数据面](docs/singbox-traffic-dataplane.zh-CN.md)
- [第三方声明](THIRD_PARTY_NOTICES.md)

## 许可证

KubeLoop 使用 [MIT License](LICENSE)。随附第三方组件保留各自许可证，详见
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
