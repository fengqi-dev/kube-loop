# ADR 0024: v3 Trojan over WebSocket data plane

- Status: Accepted
- Date: 2026-09-05
- Target release: v3

## Context

KubeLoop v2 uses an authenticated WebSocket, a KubeLoop handshake, and smux to
carry control, forward traffic, and reverse traffic through the Gateway. The
desktop already uses sing-box for the local TUN and SOCKS entry points, while
the Gateway still owns target dialing, UDP framing, authorization, and relay
lifecycle.

The v3 data plane replaces the forward proxy implementation with sing-box. A
Tunnel must remain WebSocket-based so that it works through the same HTTP/1.1
upgrade path as the existing deployment. QUIC-based transports such as TUIC
and Hysteria2 are therefore outside the v3 protocol.

Standard Trojan cannot carry the KubeLoop control and reverse-task semantics.
It also cannot authorize a target from RelayTicket claims by itself. Treating
Trojan as a replacement for every KubeLoop frame would either remove existing
features or create a private, incompatible Trojan dialect.

## Decision

### Transport split

v3 uses two authenticated WSS subprotocols on the data-plane endpoint:

1. **Trojan over WebSocket** carries client-initiated TCP and UDP traffic.
2. **KubeLoop control over WebSocket** carries Session registration, heartbeat,
   generation fencing, and reverse tasks used by Exchange, Preview, and Mirror.

Both channels use RelayTicket authentication during the HTTP Upgrade. No v3
Tunnel may fall back to raw TCP, HTTP CONNECT, QUIC, or an unencrypted `ws://`
endpoint outside local tests.

### Component boundary

The v3 Gateway workload contains:

- a thin KubeLoop adapter for RelayTicket validation, NetworkSpec authorization,
  Session reconciliation, health, draining, and reverse-task control; and
- sing-box for Trojan framing, multiplexing, TCP/UDP proxying, DNS, and route
  execution.

The adapter owns policy; sing-box owns packet and stream transport. The adapter
must not reimplement Trojan framing, and sing-box must not read Kubernetes CRDs
or Control Plane storage directly.

### Authentication and authorization

- RelayTicket remains the public authentication credential and is validated
  before WebSocket Upgrade.
- The forward endpoint permits the same unexpired ticket to authenticate a
  reconnect because sing-box WebSocket headers are static for the process
  lifetime. Signature, audience, operation, revocation and newest-generation
  checks still run on every Upgrade; the control endpoint remains one-use.
- The loopback Trojan credential is stable for one Session generation and is
  derived from the generation-bound Session token with a domain-separated
  SHA-256 function. It is only framing authentication: the loopback inbound is
  not externally reachable, and a fresh RelayTicket remains mandatory on the
  public WebSocket Upgrade. Neither credential is stored in a CRD label, log,
  status, or frontend model.
- The Gateway reconciles the credential and NetworkSpec into its local sing-box
  runtime before admitting traffic for that Session generation.
- The outer authenticated identity is bound to the same Session ID and
  generation as the selected sing-box runtime.
- sing-box uses an allow-list route derived from NetworkSpec. Its final route is
  reject; it must not provide unrestricted cluster or Internet access.
- Session CRDs continue to be selected by the `userID` label.

### Traffic semantics

- Port Forward and TUN share the desktop sing-box SOCKS/TUN runtime and the same
  Trojan/WSS outbound.
- Trojan TCP carries TCP workloads such as HTTP, gRPC, and Redis.
- Trojan UDP carries DNS and other UDP workloads. UDP-over-WebSocket inherits
  TCP head-of-line blocking; this is an accepted compatibility trade-off.
- Exchange remains bidirectional.
- Preview forwards selected traffic to the local workload.
- Mirror copies traffic to the local workload and discards the shadow response.
- Reverse modes remain on the KubeLoop WSS control/data stream and are not
  encoded as Trojan route aliases.

### Reconciliation and cleanup

The Session CRD is desired state. Client and Gateway runtimes reconcile toward
it and publish observed state back to status.

- `Running` ensures the WSS control channel, Trojan credential, sing-box runtime,
  and routes exist.
- `Paused` stops admission and restores intercepted resources idempotently.
- `Deleting` restores resources, removes runtime state, and only then removes
  the finalizer.
- An empty snapshot after the Kubernetes resource is already restored is a
  successful cleanup.
- Metadata-only changes do not trigger a second resource rollback.

## Migration plan

1. Add versioned v3 transport configuration and RelayTicket claims without
   changing the v2 runtime.
2. Add a Gateway sing-box runtime manager with deterministic per-Session
   configuration and lifecycle tests.
3. Add the authenticated Trojan/WSS endpoint and enforce Session-generation
   binding before proxying bytes to sing-box.
4. Add the desktop Trojan/WSS outbound while retaining the existing WSS control
   channel.
5. Route Port Forward and TUN through the shared outbound and verify TCP/UDP.
6. Move Exchange, Preview, and Mirror control to the versioned v3 control
   channel without changing their traffic-direction semantics.
7. Switch Helm defaults and images to the v3 Gateway workload.
8. Remove the v2 forward proxy, legacy route aliases, Noise traffic layer, and
   compatibility code only after v3 end-to-end tests pass.

Every migration step must build and pass its targeted tests independently. The
v2 and v3 wire formats must never be negotiated on the same WebSocket
subprotocol.

## Required verification

- RelayTicket rejection before Upgrade and replay protection.
- Session/user/generation isolation and NetworkSpec deny-by-default behavior.
- HTTP, gRPC, Redis, DNS, and generic UDP through Trojan/WSS.
- One shared local SOCKS entry for Port Forward and TUN.
- Exchange, Preview, and Mirror directionality and disconnect recovery.
- Gateway rolling restart and client reconciliation.
- idempotent Pause/Delete cleanup and finalizer removal.
- `go test ./...`, `go test -race` for changed concurrent packages,
  `go vet ./...`, lint, Wails build, Helm tests, and Linux/Windows/macOS E2E.

## Consequences

The forward data path becomes compatible with sing-box Trojan/WebSocket and no
longer depends on KubeLoop's custom TCP/UDP framing. KubeLoop still owns a small
control-plane-specific WebSocket protocol because reverse tasks and Kubernetes
authorization are product semantics rather than proxy protocol features.

The design uses two logical protocols instead of pretending that Trojan can
replace the reverse control plane. This adds an explicit runtime boundary, but
keeps the public Tunnel WebSocket-only and avoids maintaining a private Trojan
fork.
