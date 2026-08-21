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
workstation to cluster networking so browsers, IDEs, CLIs, and SDKs can reach
Pod IPs, ClusterIP Services, and cluster DNS directly — no VPN, no public
ingress.

## Why KubeLoop

- **Transparent cluster access** — use real cluster addresses from ordinary local applications.
- **No kubeconfig or `kubectl` dependency** — the desktop app signs in to a KubeLoop Server and never embeds Kubernetes credentials.
- **No public cluster ingress** — RelayTicket-authenticated WebSockets carry traffic to an assigned Data Plane.
- **Focused routing** — only discovered or configured Kubernetes routes enter the tunnel.
- **Local iteration tools** — Port Forward, Exchange, Mirror, and Preview cover outbound and inbound traffic.
- **Traffic inspection** — intercept and decode live HTTP and gRPC traffic through the proxy, with auto-generated `curl`/`grpcurl` replay commands and optional `.proto` schema import.
- **Desktop workflow** — inspect workloads, use Pod SSH/SFTP, transfer files, and diagnose connections in one UI.
- **Cross-platform** — macOS, Windows, and Linux on amd64 and arm64.

## Install

### KubeLoop Server with Helm

Requirements:

- Kubernetes 1.25 or later and Helm 3
- A hostname routed to a Kubernetes Ingress controller

Install the released OCI chart. Helm generates and retains the RelayTicket
signing key and internal Relay Registry TLS Secret by default:

```bash
helm upgrade --install kubeloop \
  oci://ghcr.io/fengqi-dev/kube-loop/charts/kubeloop \
  --version 2.1.2 \
  --namespace kubeloop-system \
  --create-namespace \
  --set publicURL=http://kubeloop.example.com \
  --set ingress.enabled=true \
  --set ingress.host=kubeloop.example.com \
  --set ingress.className=nginx \
  --wait
```

Replace the version, hostname, and Ingress class for your environment. Then
inspect the generated initial-admin instructions and verify service discovery:

```bash
helm get notes kubeloop --namespace kubeloop-system
curl http://kubeloop.example.com/.well-known/kubeloop
```

Ingress TLS is disabled by default. For HTTPS, set `publicURL` to `https://…`,
set `ingress.tls.enabled=true`, and provide `ingress.tls.secretName`.

Uninstall the workloads with:

```bash
helm uninstall kubeloop --namespace kubeloop-system --wait
```

The chart removes its workloads and chart-created SQLite PVC, but intentionally
retains its CRD and generated authentication/bootstrap Secrets. See the
[full Helm guide](charts/kubeloop/README.md) for key generation, Gateway API,
external PostgreSQL/MySQL, upgrades, and complete cleanup.

### Desktop client

#### macOS and Linux

```bash
curl -fsSL https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.sh | bash
```

To select a version or Linux package format:

```bash
VERSION=v2.1.2 PACKAGE=deb \
  bash -c "$(curl -fsSL https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.sh)"
```

`PACKAGE` may be `deb`, `rpm`, or `tarball`.

Homebrew is also supported:

```bash
brew tap kube-loop/kubeloop https://github.com/fengqi-dev/kube-loop
brew install --cask kube-loop/kubeloop/kubeloop-desktop
```

#### Windows

```powershell
irm https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.ps1 | iex
```

DMG, NSIS, portable zip, deb, rpm, and tar.gz artifacts are available from
[GitHub Releases](https://github.com/fengqi-dev/kube-loop/releases/latest).
Each release includes `SHA256SUMS`.

### Terminal client

The K9s-style terminal client implements the core connection and Kubernetes
resource workflows without requiring the desktop UI.

On macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install-tui.sh | bash
```

On Windows:

```powershell
irm https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install-tui.ps1 | iex
```

Set `VERSION=v2.1.2` to install a specific release. The installers select the
matching `kubeloop-tui-<version>-<os>-<arch>.tar.gz` archive and verify it using
the release `SHA256SUMS` before installing `kubeloop` (`kubeloop.exe` on Windows).

Release archives are available for macOS, Windows, and Linux on amd64 and
arm64. The TUI uses the same KubeLoop Server profiles and Control Plane APIs as
the desktop client; it does not read kubeconfig or call Kubernetes directly.
See the [TUI guide](docs/tui.md) for resources, commands, configuration, and
testing boundaries.

Homebrew installs the `kubeloop-tui` Formula separately from the
`kubeloop-desktop` Cask. The Formula still provides the `kubeloop` command:

```bash
brew tap kube-loop/kubeloop https://github.com/fengqi-dev/kube-loop
brew install --formula kube-loop/kubeloop/kubeloop-tui
```

## Connect

1. Open KubeLoop, add the HTTP or HTTPS URL of a KubeLoop Server, and run discovery.
2. Sign in through the system browser and choose an authorized Namespace.
3. Choose **SOCKS5 proxy** or **TUN mode**, then click **Connect**.
4. For TUN only, approve installation of the local network Helper on first use.

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
| Traffic Inspection | Local app → cluster (via proxy) | Intercept decoded HTTP/gRPC requests with replayable commands and `.proto` decoding |

KubeLoop restores or removes affected Services, Endpoints, and EndpointSlices
when a workflow stops or the cluster connection closes.

## Architecture

```text
Local applications
  → TUN or SOCKS5 + managed sing-box / split DNS
  → RelayTicket-authenticated WSS Relay
  → assigned, Session-scoped Data Plane
  → Pods / Services / CoreDNS
```

The **Control Plane** owns authentication, policy, Cluster Session state, task
ownership, and Kubernetes operations. The **Data Plane** carries only
authorized Session traffic and holds no Kubernetes credentials. The local
**Helper** is used only by TUN mode to manage KubeLoop's sing-box process,
interface, routes, split DNS, and recovery state; SOCKS mode requires no
privileged Helper.

Read [System design](docs/design.md) and
[Unified traffic data plane](docs/singbox-traffic-dataplane.md) for the full
control-plane, data-plane, and recovery model.

## Pod SSH

Pod SSH uses a loopback, public-key-only endpoint without installing `sshd` in
a container. KubeLoop authenticates the local SSH client and maps the channel
to the active Cluster Session's Control Plane `pods/exec` task.

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
Bearer token. Cluster operations use the active authenticated Server Profile and
Cluster Session; MCP does not load a local kubeconfig or bypass Control Plane
policy and Kubernetes authorization.

| Tool | Scope |
| --- | --- |
| `manage_cluster` | Read cluster capabilities; list Namespaces, Services, and Pods |
| `manage_connection` | Read, connect, or explicitly disconnect the active Session |
| `manage_traffic` | Start, stop, and list Port Forward, Exchange, Mirror, and Preview Tasks |
| `exec_pod_command` | Execute an exact argv through the authenticated Control Plane exec stream; output is base64 |
| `manage_file_transfer` | Start, list, or cancel local ↔ Pod transfers |
| `manage_pod_files` | List, create, rename, or delete Pod files and directories |

See the [website MCP guide](https://fengqi-dev.github.io/kube-loop/#/mcp) for setup.

## Build from source

Requirements:

- Go version declared in [`go.mod`](go.mod)
- Node.js 22+
- Platform prerequisites for [Wails](https://wails.io/docs/gettingstarted/installation)

```bash
make build          # build the desktop app
make test-local     # run non-E2E tests and vet
make vulncheck      # check Go dependencies for known vulnerabilities
```

Useful frontend commands:

```bash
cd frontend
npm ci
npm run dev          # desktop frontend
npm run dev:admin    # admin console
npm run dev:auth     # auth page
npm run dev:site     # public website
npm run build:site   # writes the production site to ../site
```

The Admin Console, auth page, and public website all use React, Vite,
Tailwind CSS 4, and shadcn conventions. GitHub Pages rebuilds the website
before deployment.

## Documentation

- [System design](docs/design.md)
- [Operator guide](docs/operator.zh-CN.md)
- [Traffic data plane](docs/singbox-traffic-dataplane.md)
- [V2 roadmap](docs/v2-roadmap.zh-CN.md)
- [E2E coverage](docs/v2-e2e-coverage.zh-CN.md)
- [Kubernetes call sites](docs/v2-kubernetes-call-sites.zh-CN.md)
- [DNS latency report](docs/dns-latency-report.zh-CN.md)
- [Security test matrix](docs/v2-security-test-matrix.zh-CN.md)
- [Architecture decisions](docs/adr/)

## License

[MIT](LICENSE)
