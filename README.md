# KubeLoop

[![CI](https://github.com/fengqi-dev/kube-loop/actions/workflows/ci.yml/badge.svg)](https://github.com/fengqi-dev/kube-loop/actions/workflows/ci.yml)
[![Release](https://github.com/fengqi-dev/kube-loop/actions/workflows/release.yml/badge.svg)](https://github.com/fengqi-dev/kube-loop/actions/workflows/release.yml)
[![Latest release](https://img.shields.io/github/v/release/fengqi-dev/kube-loop)](https://github.com/fengqi-dev/kube-loop/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](https://opensource.org/license/mit)

[English](README.md) | [简体中文](README_zh-CN.md)

**[Website](https://fengqi-dev.github.io/kube-loop/)** ·
**[Download](https://github.com/fengqi-dev/kube-loop/releases/latest)** ·
**[System design](docs/design.md)** ·
**[Data-plane design](docs/singbox-traffic-dataplane.md)**

KubeLoop connects your workstation to a Kubernetes cluster like a focused VPN.
After one click, local browsers, IDEs, CLIs, and SDKs can use Pod IPs, ClusterIP
Services, and `*.cluster.local` names directly—without per-app proxy settings or
terminal-managed port-forwards.

## Why KubeLoop?

- **Transparent cluster access** — use cluster addresses from ordinary local apps.
- **Cluster traffic only** — unrelated traffic keeps its normal route.
- **No public Gateway** — the data plane travels through Kubernetes API Server
  port-forward; no NodePort, LoadBalancer, or Ingress is required.
- **Desktop workflow** — connect, inspect, forward, exchange, mirror, preview, and
  diagnose from one UI.
- **Cross-platform** — macOS, Windows, and Linux packages for amd64 and arm64.
- **No local `kubectl` dependency** — KubeLoop uses client-go and your kubeconfig.

## Workflows

| Goal | KubeLoop workflow |
| --- | --- |
| Open an internal Service locally | Connect, then use its ClusterIP or cluster DNS name |
| Debug a Pod directly | Connect, then use the Pod IP |
| SSH or SFTP to a Pod without installing sshd | Enable **SSH** for the Pod in **Workload**, then connect to its Pod IP |
| Expose a Pod or Service port locally | **Port Forward** |
| Make an existing Service call a local app | **Exchange** |
| Observe an existing Service without replacing its primary Pods | **Service Mirror** |
| Expose a local app through a new ClusterIP Service | **Preview** |

### Pod SSH

Pod SSH is available in TUN mode and does not install or run `sshd` in the
container. KubeLoop intercepts every ready Pod IP on port 22 by default,
authenticates the local client with the user's standard SSH identity, and maps
the SSH channel to the Kubernetes `pods/exec` API.

- Each container has an SSH action that copies
  `ssh <container-name>@<Pod IP>`. The login name selects the Kubernetes
  container; it does not change the process user inside that container.
- Existing `~/.ssh/id_ed25519`, `id_ecdsa`, or `id_rsa` identities are preferred.
  If none is available, KubeLoop creates `~/.ssh/id_ed25519` without overwriting
  any existing identity.
- Interactive shells and remote commands require `/bin/sh` in the selected
  container.
- Modern `scp` and `sftp` clients use the built-in SFTP adapter. File transfer
  uses `tar` streams like `kubectl cp`, so the container must provide `tar`.
- Endpoints are runtime-only. Disconnecting KubeLoop closes active sessions and
  removes the routes without changing the Pods.

### Exchange, Service Mirror, and Preview

- **Exchange** keeps an existing Service's ClusterIP and DNS name but replaces its
  backends with a local process.
- **Service Mirror** operates on an existing Service. Original Pods remain the
  Primary path; request data is also sent to a local Shadow process, whose response
  is discarded.
- **Preview** creates a new temporary ClusterIP Service backed by a local process.

KubeLoop restores or deletes the affected Service, Endpoints, and EndpointSlices
when a feature stops or the cluster session disconnects.

## How it works

```text
Local applications
  → platform TUN + split DNS
  → managed sing-box
  → local SOCKS Bridge
  → Kubernetes API Server port-forward
  → unprivileged in-cluster Gateway
  → Pods / Services / CoreDNS
```

Only discovered or manually configured cluster routes enter the tunnel. The
Gateway performs final in-cluster connections and has no ServiceAccount token,
`hostNetwork`, `privileged`, or `NET_ADMIN`.

Port Forward, Exchange, Service Mirror, and Preview share fixed, authenticated
sing-box feature inbounds. Creating a feature does not restart the core.

## Install

### macOS and Linux

The installer resolves the latest release and selects the current CPU architecture:

```bash
curl -fsSL https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.sh | bash
```

- macOS: downloads the matching DMG; open it and drag KubeLoop into Applications.
- Linux: installs a matching deb/rpm when available, otherwise extracts the tarball.

Select a specific release with `VERSION=v1.5.0`, or select a Linux format with
`PACKAGE=deb`, `PACKAGE=rpm`, or `PACKAGE=tarball`.

### Windows

Run the latest NSIS installer from PowerShell:

```powershell
irm https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.ps1 | iex
```

From a source checkout, download and extract the portable build with:

```powershell
.\scripts\install.ps1 -Package portable
```

You can also download the matching portable zip directly from Releases.

### Homebrew

```bash
brew tap kube-loop/kubeloop https://github.com/fengqi-dev/kube-loop
brew install --cask kubeloop
```

Manual DMG, installer, portable zip, deb, rpm, and tar.gz downloads are available
on the [Releases page](https://github.com/fengqi-dev/kube-loop/releases/latest).
Each release includes `SHA256SUMS`.

## First connection

1. Ensure the workstation can reach the Kubernetes API Server with a valid kubeconfig.
2. Open KubeLoop and select a kubeconfig Context and default Namespace.
3. Click **Connect**.
4. Approve installation of the local virtual-network Helper on first use.
5. If permitted, KubeLoop installs or upgrades its Gateway in `kubeloop-system`.

The Helper is installed once and can be inspected or removed from Settings. It
manages only KubeLoop's sing-box process, TUN, routes, split DNS, and recovery state.

## Restricted RBAC

KubeLoop supports namespace-scoped developer accounts:

1. An administrator preinstalls the Gateway manifest shown in Overview.
2. The user receives `get/list` and `pods/portforward` access to the Gateway Pod.
3. Pod and Service inventory is limited to authorized Namespaces.
4. If Nodes or CoreDNS cannot be read, enter Pod CIDR, Service CIDR, cluster DNS,
   and DNS Namespace manually in Overview.
5. Pod SSH additionally requires `create` on the `pods/exec` subresource in the
   target Namespace.

Capabilities degrade independently. Missing Service, Endpoints, or EndpointSlice
write access disables Exchange, Service Mirror, and Preview without disabling
transparent cluster access.

## Session diagnostics

Active TCP sessions have an icon-only connectivity action:

- Port Forward tests its active local listener.
- Exchange, Service Mirror, and Preview test Gateway control registration and each
  configured local target.
- The result diagram distinguishes tested paths from topology-only paths and can
  identify `local-listener`, `gateway-control`, or `local-target` failures.

These checks verify transport reachability, not application-level response semantics.
Generic UDP testing is intentionally unsupported because it requires a
protocol-specific payload.

## MCP for editors and agents

KubeLoop can expose the same backend through a local MCP server using Streamable
HTTP on `127.0.0.1`.

1. Open the **MCP** page in KubeLoop.
2. Choose Codex, Claude Code, Cursor, or VS Code.
3. Click **Install MCP server**, or copy the generated client configuration.

MCP is off by default. It never binds to a LAN address; optional Bearer
authentication can be enabled.

The MCP surface is intentionally compact: related operations use `action` and
`type` instead of separate start/stop/list tools.

| Tool | Operations |
| --- | --- |
| `manage_cluster` | Reload/probe contexts; list Namespaces, Services, and Pods |
| `manage_connection` | Get status, connect, disconnect, or read the active sing-box config |
| `manage_network` | Get/set manual network overrides and Host Aliases |
| `manage_traffic` | Start/stop/list Exchange, Mirror, Preview, and Port Forward sessions |
| `manage_helper` | Get status, install, or uninstall the privileged Helper |
| `exec_pod_command` | Execute a shell command in a Pod container |
| `manage_file_transfer` | Start/list/cancel local ↔ Pod file and directory transfers |

Port Forward requires an explicit target kind so a Service name is never
mistaken for a Pod name:

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

Use `targetKind: "pod"` with a Pod name for direct Pod forwarding. Service
results retain the requested Service in `name` and expose the resolved backend
Pod in `podName`. Pod command execution returns structured stdout, stderr, and
exit code results. File transfers are asynchronous and support files or
directories in both upload and download directions.

## Security model

- kubeconfig credentials remain in the Go desktop process.
- The Gateway is unprivileged, has no Kubernetes credentials, and is not publicly exposed.
- The privileged Helper accepts authenticated, field-constrained IPC—not commands
  or caller-selected executable/config paths.
- sing-box and feature inbounds bind locally and use per-session credentials.
- Pod SSH accepts the user's standard local SSH identities (or a generated
  mode-`0600` `~/.ssh/id_ed25519`) and delegates authorization to the active
  kubeconfig identity through `pods/exec`.
- Cluster routing is limited to validated Pod/Service destinations.
- Feature mutations are transactional and recoverable.
- Logs redact credentials, certificates, tokens, and kubeconfig content.

See [System design](docs/design.md#11-security-and-permissions) for trust boundaries
and capability behavior.

## Platform support

| Area | Support |
| --- | --- |
| Desktop | macOS, Windows, Linux |
| CPU | amd64, arm64 |
| UI | System-aware light/dark theme; English and 简体中文 |
| Packages | DMG, NSIS, zip, deb, rpm, tar.gz, Homebrew Cask |
| App state | `~/.kubeloop` |
| Core | Pinned sing-box bundled with each release |
| Updates | GitHub Release check from Settings |

## Development

Requirements:

- Go 1.26+
- Node.js 22+
- Wails 2.13

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0
npm ci --prefix frontend
wails dev
```

`wails dev` builds and embeds the platform Helper automatically. To use a local
Gateway image:

```bash
KUBELOOP_GATEWAY_IMAGE=kube-loop-gateway:dev wails dev
```

### Tests

```bash
npm run build --prefix frontend
go test ./...
```

Minikube end-to-end:

```bash
./e2e/run.sh
```

Platform-local suites install the Helper, exercise TUN/DNS and feature flows, and
clean temporary resources:

```bash
./e2e/scripts/run-local-macos.sh --timeout 25m
./e2e/scripts/run-local-linux.sh --timeout 25m
```

Windows:

```powershell
.\e2e\scripts\run-local-windows.ps1 -Timeout 25m
```

## Build and release

Build the desktop application:

```bash
VERSION=v1.5.0
VITE_APP_VERSION="$VERSION" wails build -ldflags "-X main.version=${VERSION}"
```

Pushing a `v*` tag triggers the release workflow. It builds six desktop targets,
Gateway binaries, a multi-architecture Gateway image, checksums, the Homebrew Cask
update, and the project website.

## Documentation

- [System design](docs/design.md)
- [Unified traffic data plane](docs/singbox-traffic-dataplane.md)
- [Website](https://fengqi-dev.github.io/kube-loop/)
- [Third-party notices](THIRD_PARTY_NOTICES.md)

## License

KubeLoop is distributed under the MIT License. Bundled third-party components retain
their own licenses; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
