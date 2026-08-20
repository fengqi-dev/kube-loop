# KubeLoop TUI

`kubeloop` 是 KubeLoop 核心工作流的 K9s 风格终端客户端。它是另一种客户端实现，
不是直接 Kubernetes 客户端：认证、发现、资源操作和任务生命周期仍全部通过已配置的
KubeLoop Control Plane API 完成。

## 安装与运行

GitHub Releases 提供以下原生产物：

- `darwin-amd64` 与 `darwin-arm64`
- `windows-amd64` 与 `windows-arm64`
- `linux-amd64` 与 `linux-arm64`

压缩包命名为 `kubeloop-<version>-<os>-<arch>.tar.gz`，其中只包含一个自包含的
`kubeloop` 可执行文件（Windows 为 `kubeloop.exe`）。使用 Release 中的
`SHA256SUMS` 校验后运行，终端最小尺寸为 60x18。

开发环境可执行：

```sh
make tui
./build/bin/kubeloop

VERSION=v2.1.0 make tui
TUI_GOOS=linux TUI_GOARCH=arm64 make tui
```

`TUI_BINARY` 可覆盖输出路径，`TUI_LDFLAGS` 可覆盖链接参数。默认启用
`-trimpath`，使用 `-s -w` 移除调试表，并把 `VERSION` 注入 `main.version`。该目标
还会构建匹配平台的 Helper、Supervisor（仅 macOS）和 sing-box，并将其嵌入 TUI
可执行文件。

Homebrew 可安装独立 Formula：

```sh
brew tap kube-loop/kubeloop https://github.com/fengqi-dev/kube-loop
brew install kubeloop
```

在首个包含 TUI 压缩包的稳定版本发布前，使用
`brew install --HEAD kubeloop`。稳定版 Release 会使用 macOS 与 Linux 原生压缩包
的校验和自动更新 Formula。

Server 配置与可选 TUI 配置位于 `~/.kubeloop`。首次启动进入 Servers，可添加 KubeLoop
Server URL 并完成登录。恢复已有登录或完成登录后，TUI 默认使用 TUN 模式自动连接。

## 工作区

页面由全局状态栏、一个全宽资源表格或详情页、顶部命令/过滤输入栏，以及上下文快捷键
组成。顶部快捷键根据当前页面变化，只显示实际可用操作。

| 资源 | 命令 | 核心操作 |
| --- | --- | --- |
| Connection | `:connection`、`:conn` | 连接/断开、切换 SOCKS/TUN 模式、卸载 Helper Service |
| Pods | `:pods`、`:po` | 查看详情、Port Forward、交互式 SSH |
| Services | `:services`、`:svc` | 查看详情、Port Forward、Exchange、Mirror、Preview |
| Sessions | `:sessions` | 查看、停止、重跑、复制地址或 Exec 输出、清理已完成任务 |
| Servers | `:servers`、`:server` | 添加、选择、登录、删除 |
| Namespaces | `:namespaces`、`:ns` | 选择当前有权限的 Namespace |

TUI 不提供任意 Kubernetes 资源类型、直接 kubeconfig 访问、Shell 插件或任意本地
命令 Hook。

## 导航与过滤

- `:` 在页面顶部打开命令输入栏；`Tab`、`Shift+Tab` 切换补全，Up/Down 浏览命令历史。
- `/` 在页面顶部打开实时过滤输入栏；`/pattern` 使用大小写不敏感的 RE2 正则，`/!pattern` 反选，
  `/-f text` 使用模糊匹配。
- `j`/`k` 或方向键移动，`g`/`G` 跳转首尾，`Ctrl+u`/`Ctrl+d` 或
  `PgUp`/`PgDn` 翻页。
- `Enter` 打开详情或执行主要操作；`Esc` 返回或取消；`-` 或 `[` 后退资源历史，
  `]` 前进。
- `r` 刷新，`?` 打开上下文帮助，`:q` 退出。

## Alias 与 Hotkey

可创建 `~/.kubeloop/tui.yaml`：

```yaml
version: 1
aliases:
  pp: pods
hotkeys:
  ctrl+p: servers
```

目标必须解析到内置资源或命令。保留导航键、未知目标、未知 YAML 字段和不支持的版本
会被忽略并显示非致命警告。配置不能执行 Shell 命令或插件。

## 连接模式与安全边界

SOCKS 模式不需要特权网络 Helper。TUN 模式沿用桌面客户端托管的 sing-box 与 Helper
边界，可能需要本地授权。TUN 是默认模式，TUI 在恢复登录或完成登录后自动连接。连接期间
禁止切换 Server；切换 Namespace 会重建对应 Session 与 Data Plane。存在活动 Session 时
断开连接需要二次确认。

两个客户端会校验并共享
`~/.kubeloop/components/<version>/<os>-<arch>/` 下的版本化组件。该用户缓存只作为
分发源；TUN 安装会先把 Helper 和 sing-box 提升到受保护的系统路径，特权服务不会
直接执行缓存目录或软件包目录中的 sing-box。

客户端不保存 Kubernetes 凭据，也不会使用不可信展示字段选择拨号目标。Server 授权、
Namespace Scope、RelayTicket 与任务所有权仍是权威边界。

## 测试与发布门禁

```sh
make tui-test-e2e
```

该 CI 安全的 PTY fixture 驱动真实 Bubble Tea 事件循环，覆盖键盘、鼠标、粘贴、缩放、
命令、过滤、详情、操作、表单和确认框，不读取凭据，也不产生外部副作用。普通 CI 会
运行该门禁，Release workflow 在发布六个平台原生 TUI 压缩包前再次运行。

Live 门禁保持独立：

```sh
KUBELOOP_TUI_E2E_LIVE_HOME=/path/to/isolated/authenticated/home \
  make tui-test-live-e2e
```

仅应在可丢弃环境中设置 `KUBELOOP_TUI_E2E_LIVE_CONNECT=1`，用于真实 SOCKS/TUN
连接与模式切换。Fixture 通过不能证明真实 Server、本地 Helper、TUN 路由、DNS 或
外部网络环境已经验证。
