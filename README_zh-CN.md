# KubeLoop

[![CI](https://github.com/fengqi-dev/kube-loop/actions/workflows/ci.yml/badge.svg)](https://github.com/fengqi-dev/kube-loop/actions/workflows/ci.yml)
[![Release](https://github.com/fengqi-dev/kube-loop/actions/workflows/release.yml/badge.svg)](https://github.com/fengqi-dev/kube-loop/actions/workflows/release.yml)
[![Latest release](https://img.shields.io/github/v/release/fengqi-dev/kube-loop)](https://github.com/fengqi-dev/kube-loop/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](https://opensource.org/license/mit)

[English](README.md) | [简体中文](README_zh-CN.md)

**[官网](https://fengqi-dev.github.io/kube-loop/)** ·
**[下载](https://github.com/fengqi-dev/kube-loop/releases/latest)** ·
**[系统设计](docs/design.zh-CN.md)** ·
**[数据面设计](docs/singbox-traffic-dataplane.zh-CN.md)**

KubeLoop 像一条只面向 Kubernetes 的 VPN，把开发者工作站连接到集群。点击连接后，本地浏览器、
IDE、CLI 和 SDK 可以直接访问 Pod IP、ClusterIP Service 和 `*.cluster.local`，无需给每个
应用配置代理，也无需在终端维护 port-forward。

## 为什么选择 KubeLoop？

- **透明访问集群**——普通本地应用可直接使用集群地址。
- **只接管集群流量**——无关流量继续使用原有网络。
- **不暴露公网 Gateway**——数据面经过 Kubernetes API Server port-forward，不需要
  NodePort、LoadBalancer 或 Ingress。
- **完整桌面工作流**——连接、查看、转发、Exchange、Service Mirror、Preview 和诊断都在一个 UI 中。
- **跨平台**——为 macOS、Windows、Linux 提供 amd64 和 arm64 安装包。
- **不依赖本机 `kubectl`**——直接使用 client-go 和用户 kubeconfig。

## 常见工作流

| 目标 | KubeLoop 工作流 |
| --- | --- |
| 在本地打开集群内部 Service | 连接后使用 ClusterIP 或集群 DNS 名称 |
| 直接调试 Pod | 连接后使用 Pod IP |
| 不安装 sshd，通过 SSH 或 SFTP 连接 Pod | 在**工作负载**中为 Pod 开启 **SSH**，然后连接其 Pod IP |
| 把 Pod 或 Service 端口暴露到本地 | **Port Forward** |
| 让现有 Service 调用本地应用 | **Exchange** |
| 在不替换原始 Pod 主路径的情况下观察现有 Service | **Service Mirror** |
| 通过新的 ClusterIP Service 暴露本地应用 | **Preview** |

### Pod SSH

Pod SSH 仅在 TUN 模式可用，不会在容器中安装或运行 `sshd`。KubeLoop 默认接管所有
Ready Pod IP 的 22 端口，使用用户的标准 SSH identity 认证本地客户端，再将 SSH
channel 映射到 Kubernetes `pods/exec` API。

- 每个容器都有对应的 SSH 操作，点击后复制
  `ssh <container-name>@<Pod IP>`。login name 用来选择 Kubernetes container，
  不会改变容器内进程的实际用户。
- 优先使用现有的 `~/.ssh/id_ed25519`、`id_ecdsa` 或 `id_rsa` identity；均不可用时，
  KubeLoop 会创建 `~/.ssh/id_ed25519`，不会覆盖任何已有 identity。
- 交互 shell 和远程命令要求所选容器提供 `/bin/sh`。
- 新版 `scp` 与 `sftp` 使用内置 SFTP 适配器。文件传输和 `kubectl cp` 一样使用
  `tar` stream，因此容器需要提供 `tar`。
- SSH endpoint 只在当前运行期间存在。断开 KubeLoop 会终止活动会话并移除 route，
  不会修改 Pod。

### Exchange、Service Mirror 与 Preview

- **Exchange** 保留现有 Service 的 ClusterIP 和 DNS 名称，但用本地进程替换后端。
- **Service Mirror** 的操作对象是现有 Service。原始 Pod 保持 Primary 路径，请求同时复制
  给本地 Shadow 进程，Shadow 响应会被丢弃。
- **Preview** 创建一个由本地进程提供服务的临时 ClusterIP Service。

功能停止或集群 Session 断开时，KubeLoop 会恢复或删除相关 Service、Endpoints 和 EndpointSlices。

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

只有发现或手工配置的集群 route 会进入隧道。Gateway 在集群内建立最终连接，没有
ServiceAccount token、`hostNetwork`、`privileged` 或 `NET_ADMIN`。

Port Forward、Exchange、Service Mirror 和 Preview 共享固定且经过认证的 sing-box feature
inbound；创建新功能不会重启 core。

## 安装

### macOS 与 Linux

脚本会解析最新 Release，并自动选择当前 CPU 架构：

```bash
curl -fsSL https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.sh | bash
```

- macOS：下载对应 DMG；打开后将 KubeLoop 拖入 Applications。
- Linux：优先安装对应 deb/rpm，否则解压 tarball。

可通过 `VERSION=v1.5.0` 选择指定版本；Linux 可使用 `PACKAGE=deb`、`PACKAGE=rpm` 或
`PACKAGE=tarball` 指定格式。

### Windows

在 PowerShell 中启动最新 NSIS installer：

```powershell
irm https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.ps1 | iex
```

在源码 checkout 中下载并解压 portable 版本：

```powershell
.\scripts\install.ps1 -Package portable
```

也可直接从 Releases 下载对应架构的 portable zip。

### Homebrew

```bash
brew tap kube-loop/kubeloop https://github.com/fengqi-dev/kube-loop
brew install --cask kubeloop
```

也可从 [Releases 页面](https://github.com/fengqi-dev/kube-loop/releases/latest)手工下载 DMG、
installer、portable zip、deb、rpm 和 tar.gz。每个 Release 都包含 `SHA256SUMS`。

## 首次连接

1. 确保工作站可使用有效 kubeconfig 访问 Kubernetes API Server。
2. 打开 KubeLoop，选择 kubeconfig Context 和默认 Namespace。
3. 点击**连接**。
4. 首次使用时批准安装本地虚拟网络 Helper。
5. 权限允许时，KubeLoop 会在 `kubeloop-system` 安装或升级 Gateway。

Helper 只需安装一次，可在设置中查看或卸载。它仅管理 KubeLoop 的 sing-box 进程、TUN、
route、split DNS 和恢复状态。

## Kubernetes RBAC

KubeLoop 使用所选 kubeconfig Context 中的身份。Gateway 不挂载 ServiceAccount token，
也不会调用 Kubernetes API。以下权限均授予桌面端用户的 kubeconfig 身份。

在 KubeLoop 需要访问的每个 Namespace（包括 `kubeloop-system`）中创建以下 `Role`，
并绑定到 kubeconfig 用户：

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

`namespaces`、`nodes`、`servicecidrs` 和 `kube-system` DNS 资源等集群级读取权限仅用于
自动发现；没有这些权限时可手工配置网络参数。若用户不能创建 Namespace，请由管理员
预先创建 `kubeloop-system`。MCP Server 共用同一 kubeconfig 身份，不需要额外权限。

## Session 诊断

活动 TCP Session 提供纯图标连通性操作：

- Port Forward 测试活动本地监听。
- Exchange、Service Mirror 和 Preview 测试 Gateway control 注册及每个本地目标。
- 结果图会区分实际测试路径和仅拓扑路径，并能定位 `local-listener`、
  `gateway-control` 或 `local-target` 失败。

这些检查验证传输层可达性，不验证应用层响应语义。通用 UDP 测试需要协议专用 payload，
因此有意不支持。

## 编辑器与 Agent 的 MCP

KubeLoop 可通过 `127.0.0.1` 上的 Streamable HTTP MCP Server 暴露同一个后端。

1. 打开 KubeLoop 的 **MCP** 页面。
2. 选择 Codex、Claude Code、Cursor 或 VS Code。
3. 点击**安装 MCP Server**，或复制生成的客户端配置。

MCP 默认关闭，永远不会绑定 LAN 地址，可选启用 Bearer 认证。工具覆盖连接状态、发现、
Port Forward、Exchange、Service Mirror、Preview、Pod 命令执行、文件传输和 Helper 管理。

MCP 接口有意保持精简：相关操作通过 `action` 和 `type` 区分，而不是为
start/stop/list 分别注册 Tool。

| Tool | 操作 |
| --- | --- |
| `manage_cluster` | 重新加载/探测 Context；列出 Namespace、Service 和 Pod |
| `manage_connection` | 查询状态、连接、断开或读取当前 sing-box 配置 |
| `manage_network` | 查询/设置手工网络参数和 Host Alias |
| `manage_traffic` | 启动/停止/列出 Exchange、Mirror、Preview 和 Port Forward |
| `manage_helper` | 查询状态、安装或卸载特权 Helper |
| `exec_pod_command` | 在 Pod 容器中执行 Shell 命令 |
| `manage_file_transfer` | 启动/列出/取消本机 ↔ Pod 文件及目录传输 |

Port Forward 必须明确指定目标类型，避免把 Service 名称误当成 Pod 名称：

```json
{
  "action": "start",
  "type": "port_forward",
  "targetKind": "service",
  "targetName": "api",
  "namespace": "default",
  "remotePort": 8080,
  "localPort": 0
}
```

直接转发 Pod 时使用 `targetKind: "pod"` 和 Pod 名称。Service 转发结果中的
`name` 保留请求的 Service，`podName` 表示解析到的后端 Pod。Pod 命令执行以结构化
结果返回 stdout、stderr 和退出码；文件传输异步执行，支持文件和目录双向上传/下载。

## 安全模型

- kubeconfig 凭证留在 Go 桌面进程。
- Gateway 无特权、没有 Kubernetes 凭证，也不暴露公网入口。
- 特权 Helper 只接受经过认证、字段受限的 IPC，不接受命令或调用方选择的可执行文件/配置路径。
- sing-box 和 feature inbound 只监听本地，并使用每 Session 凭证。
- Pod SSH 接受用户的标准本地 SSH identity；如果没有，则生成权限为 `0600` 的
  `~/.ssh/id_ed25519`。实际授权由当前 kubeconfig 身份通过 `pods/exec` 完成。
- 集群路由限制到经过校验的 Pod/Service 目标。
- 功能资源变更具备事务和恢复能力。
- 日志脱敏凭证、证书、token 和 kubeconfig 内容。

信任边界和 capability 行为详见
[系统设计](docs/design.zh-CN.md#11-安全与权限)。

## 平台支持

| 项目 | 支持 |
| --- | --- |
| 桌面系统 | macOS、Windows、Linux |
| CPU | amd64、arm64 |
| UI | 跟随系统的明暗主题；English 和简体中文 |
| 安装包 | DMG、NSIS、zip、deb、rpm、tar.gz、Homebrew Cask |
| 应用状态 | `~/.kubeloop` |
| Core | 每个 Release 随附固定版本 sing-box |
| 更新 | 在设置中检查 GitHub Release |

## 开发

环境要求：

- Go 1.26+
- Node.js 22+
- Wails 2.13

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0
npm ci --prefix frontend
wails dev
```

`wails dev` 会自动构建并嵌入平台 Helper，同时构建带源码内容哈希的本地 Gateway
镜像。Docker Desktop Kubernetes 可直接使用该镜像；当前 Minikube、kind 或 k3d
集群会通过各自的本地镜像加载命令接收镜像。仅在需要显式覆盖开发镜像时设置
`KUBELOOP_GATEWAY_IMAGE`。

### 测试

```bash
npm run build --prefix frontend
go test ./...
```

Minikube E2E：

```bash
./e2e/run.sh
```

平台本地 suite 会安装 Helper、测试 TUN/DNS 和功能流，并清理临时资源：

```bash
./e2e/scripts/run-local-macos.sh --timeout 25m
./e2e/scripts/run-local-linux.sh --timeout 25m
```

Windows：

```powershell
.\e2e\scripts\run-local-windows.ps1 -Timeout 25m
```

## 构建与发布

构建桌面应用：

```bash
VERSION=v1.5.0
VITE_APP_VERSION="$VERSION" wails build -ldflags "-X main.version=${VERSION}"
```

推送 `v*` tag 会触发 Release workflow，构建六个平台目标、Gateway binary、多架构 Gateway
image、校验文件、Homebrew Cask 更新和项目官网。

## 文档

- [系统设计](docs/design.zh-CN.md)
- [统一流量数据面](docs/singbox-traffic-dataplane.zh-CN.md)
- [官网](https://fengqi-dev.github.io/kube-loop/)
- [第三方声明](THIRD_PARTY_NOTICES.md)

## 许可证

KubeLoop 使用 MIT License 分发。随附第三方组件保留各自许可证，详见
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
