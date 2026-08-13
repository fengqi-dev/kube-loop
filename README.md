# KubeLoop

[![CI](https://github.com/fengqi-dev/kube-loop/actions/workflows/ci.yml/badge.svg)](https://github.com/fengqi-dev/kube-loop/actions/workflows/ci.yml)
[![Release](https://github.com/fengqi-dev/kube-loop/actions/workflows/release.yml/badge.svg)](https://github.com/fengqi-dev/kube-loop/actions/workflows/release.yml)
[![Latest release](https://img.shields.io/github/v/release/fengqi-dev/kube-loop)](https://github.com/fengqi-dev/kube-loop/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[English](README.md) · [简体中文](README_zh-CN.md)

**[Website](https://fengqi-dev.github.io/kube-loop/)** ·
**[Download](https://github.com/fengqi-dev/kube-loop/releases/latest)** ·
**[Design](docs/design.md)**

KubeLoop is a desktop network tool for Kubernetes development. It connects a
workstation to cluster networking like a focused VPN, so browsers, IDEs, CLIs,
and SDKs can use Pod IPs, ClusterIP Services, and cluster DNS directly.

## Why KubeLoop

- **Transparent cluster access** — use real cluster addresses from ordinary local applications.
- **Focused routing** — only discovered or configured Kubernetes routes enter the tunnel.
- **No public gateway** — traffic reaches an unprivileged Gateway through Kubernetes API Server port-forward.
- **Local iteration tools** — Port Forward, Exchange, Mirror, and Preview cover outbound and inbound traffic.
- **Desktop workflow** — inspect workloads, use Pod SSH/SFTP, transfer files, and diagnose connections in one UI.
- **Cross-platform** — macOS, Windows, and Linux on amd64 and arm64.
- **No `kubectl` runtime dependency** — the desktop app uses client-go and the selected kubeconfig directly.

## Install

### macOS and Linux

```bash
curl -fsSL https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.sh | bash
```

To select a version or Linux package format:

```bash
VERSION=v1.5.0 PACKAGE=deb \
  bash -c "$(curl -fsSL https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.sh)"
```

`PACKAGE` may be `deb`, `rpm`, or `tarball`.

Homebrew is also supported:

```bash
brew tap kube-loop/kubeloop https://github.com/fengqi-dev/kube-loop
brew install --cask kubeloop
```

### Windows

```powershell
irm https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.ps1 | iex
```

DMG, NSIS, portable zip, deb, rpm, and tar.gz artifacts are available from
[GitHub Releases](https://github.com/fengqi-dev/kube-loop/releases/latest).
Each release includes `SHA256SUMS`.

## Connect

1. Ensure the workstation can reach the Kubernetes API Server and has a valid kubeconfig.
2. Open KubeLoop and select a Context and default Namespace.
3. Click **Connect**.
4. Approve installation of the local network Helper on first use.
5. When permitted, KubeLoop installs or upgrades its unprivileged Gateway in `kubeloop-system`.

Once connected, use a ClusterIP, Pod IP, or cluster DNS name from any local application.

## Development workflows

| Workflow | Traffic path | Use it to |
| --- | --- | --- |
| Transparent access | Local app → cluster | Open internal Services or debug Pod IPs directly |
| Port Forward | Local port → Pod or Service | Expose one cluster port on `localhost` |
| Exchange | Existing Service → local app | Replace Service backends while preserving ClusterIP and DNS |
| Mirror | Existing Service → Pods + local shadow | Observe a copy without putting the local app on the primary path |
| Preview | New temporary Service → local app | Make a local application reachable from the cluster |
| Pod SSH/SFTP | Local SSH client → `pods/exec` | Use a shell or transfer files without running `sshd` in the container |

KubeLoop restores or removes affected Services, Endpoints, and EndpointSlices
when a workflow stops or the cluster connection closes.

## Architecture

```text
Local applications
  → platform TUN + split DNS
  → managed sing-box
  → local SOCKS Bridge
  → Kubernetes API Server port-forward
  → unprivileged in-cluster Gateway
  → Pods / Services / CoreDNS
```

The Gateway makes the final in-cluster connection. It has no ServiceAccount
token and does not use `hostNetwork`, `privileged`, or `NET_ADMIN`. The local
Helper manages only KubeLoop's sing-box process, TUN interface, routes, split
DNS, and recovery state.

Read [System design](docs/design.md) and
[Unified traffic data plane](docs/singbox-traffic-dataplane.md) for the full
control-plane, data-plane, and recovery model.

## Pod SSH

Pod SSH works in TUN mode without installing `sshd` in a container. KubeLoop
authenticates the local SSH client and maps the channel to Kubernetes `pods/exec`.

- The SSH login name selects the container; it does not change the process user.
- Interactive shells and remote commands require `/bin/sh`.
- `scp` and `sftp` use the built-in SFTP adapter; transfers require `tar` in the container.
- KubeLoop can create a missing `~/.ssh/id_ed25519` without overwriting an existing identity.
- Disconnecting removes runtime-only SSH endpoints without modifying Pods.

## MCP for editors and agents

KubeLoop can expose a local
[Model Context Protocol](https://modelcontextprotocol.io/) server over
Streamable HTTP for Codex, Claude Code, Cursor, and VS Code.

MCP is disabled by default, listens only on `127.0.0.1`, and uses a generated
Bearer token by default. Cluster operations use the active authenticated
Gateway Session; MCP does not load a local kubeconfig or bypass Gateway policy
and Kubernetes authorization.

| Tool | Scope |
| --- | --- |
| `manage_cluster` | Read cluster capabilities; list Namespaces, Services, and Pods |
| `manage_connection` | Read, connect, or explicitly disconnect the active Session |
| `manage_traffic` | Start, stop, and list Port Forward, Exchange, Mirror, and Preview Tasks |
| `exec_pod_command` | Execute an exact argv through the authenticated Gateway exec stream |
| `manage_file_transfer` | Start, list, or cancel local ↔ Pod transfers |

See the [website MCP guide](https://fengqi-dev.github.io/kube-loop/#/mcp) for setup.

## Build from source

Requirements:

- Go version declared in [`go.mod`](go.mod)
- Node.js 22+
- Platform prerequisites for [Wails](https://wails.io/docs/gettingstarted/installation)

```bash
make build
```

Useful frontend commands:

```bash
cd frontend
npm ci
npm run dev          # desktop frontend
npm run dev:admin    # admin console
npm run dev:site     # public website
npm run build:site   # writes the production site to ../site
```

The Admin Console and public website both use React, Vite, Tailwind CSS 4, and
shadcn conventions. GitHub Pages rebuilds the website before deployment.

## Documentation

- [System design](docs/design.md)
- [Operator guide](docs/operator.zh-CN.md)
- [Traffic data plane](docs/singbox-traffic-dataplane.md)
- [Security test matrix](docs/v2-security-test-matrix.zh-CN.md)
- [Architecture decisions](docs/adr/)

## License

[MIT](LICENSE)
