# Protocol contracts

This directory owns versioned contracts that cross a process or network
boundary. Packages here contain wire messages, codecs, validation, protocol
constants, and declarative lifecycle/state enumerations shared by both sides
of a boundary. They do not own controllers, business state machines that
execute transitions, transport or connection lifecycle, Kubernetes operations,
or OS-level operations.

Current contracts:

- `capability`: the namespace-scoped capability snapshot returned by the
  Control Plane.
- `dns`: Kubernetes cluster DNS domain and label validation shared by other
  contracts.
- `exchangestream`, `execstream`, `filestream`, and `mirrorstream`: streaming
  operation frames.
- `helper`: desktop-to-privileged-helper JSON-line RPC.
- `networkspec`: the normalized network document exchanged by the control plane,
  gateway, and client. Target authorization decisions derived from it live in
  `internal/gateway`.
- `relaycontrol` and `relayticket`: relay control-plane messages plus signed
  admission ticket claims, signing, and verification. HTTP request middleware
  and replay guards live in `internal/auth/relaybearer`.
- `remotetask`: the declarative remote-operation lifecycle states and allowed
  transitions; executors stay with their owning packages.
- `supervisor`: length-prefixed JSON framing for the privileged-helper
  supervisor channel.
- `trafficcontrol`: traffic operation request/response contracts (modes,
  identity binding, claim requests) and their wire paths. The resolved
  service/port model they reference lives in `internal/controlplane/entity`.
- `tunnel`: tunnel data and control frames.
- `wss`: WebSocket multiplexing handshake and frame metadata.

Transport implementations such as `internal/transport/websocketmux` and
`internal/transport/streamcopy`, and connection-based framing such as
`internal/transport/trafficstream`, live outside this directory and depend on
these contracts.
