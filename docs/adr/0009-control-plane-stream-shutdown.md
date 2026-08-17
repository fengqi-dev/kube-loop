# ADR 0009: Control Plane stream shutdown and restart semantics

- Status: Accepted
- Date: 2026-08-10

## Context

Pod exec and file transfer upgrade authenticated HTTP requests to WebSockets.
Go's `http.Server.Shutdown` does not wait for hijacked connections, so closing
storage immediately after it returns can leave an active Task in `running`
state. Kubernetes exec streams also cannot be transferred to a replacement
Control Plane process.

## Decision

The Control Plane owns one cancellable root context and explicitly tracks every
HTTP handler, including upgraded WebSockets. Graceful shutdown first rejects
new requests, cancels the root context, closes the HTTP server, and then waits
within the configured shutdown deadline for all tracked handlers to finish.
Stream handlers must cancel their Kubernetes operation and persist a terminal
Task state before returning.

An active Pod exec is not migrated or replayed across Control Plane processes.
The old stream ends as cancelled; after the replacement Control Plane is ready,
the same still-active Cluster Session may create a new Task through the stable
service address. Commands are never automatically replayed because they may
have side effects.

## Consequences

- Storage is not closed while a graceful WebSocket handler is still writing
  its terminal Task result.
- Pod exec clients receive an explicit disconnect during restart and may offer
  a user-driven retry, but must not claim terminal continuity.
- The shutdown timeout must cover Kubernetes stream cancellation and Task
  persistence; exceeding it remains a forced process termination.
- Abrupt process loss cannot publish a terminal result; Session expiry and the
  maintenance cascade remain the final ownership cleanup boundary.
