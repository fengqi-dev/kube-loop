# ADR 0012: Mirror primary-shadow isolation

- Status: Superseded in ownership by the Gateway traffic-control boundary in ADR 0020
- Date: 2026-08-10

## Context

> Current implementation: Control Plane derives authoritative original backends
> from the Kubernetes snapshot, while Gateway owns the primary/shadow sockets,
> listener and WebSocket data path.

Mirror must copy requests from a Kubernetes Service to a developer's local
process without replacing the Service's real response path. The desktop cannot
read Kubernetes endpoints or choose the authoritative backend, and a slow,
unreachable or malicious local shadow must not add latency or return bytes to
the cluster client. Control Plane replicas must also restore every temporary
Service and EndpointSlice mutation after stop, revocation, disconnect or owner
loss.

Redirecting the Service to a Control Plane listener is necessary to observe both
TCP and UDP requests. Once redirected, however, that listener must reconstruct
the original backend path itself. Reusing Exchange semantics would incorrectly
make the desktop response authoritative, while synchronously writing the
shadow would let local backpressure stall production traffic.

## Decision

Mirror is a distinct Session- and Identity-owned Task, protocol and recovery
queue. It reuses the compensating Service-intercept resource mechanism, but not
the Exchange data-path semantics.

Before mutation, Control Plane captures the Service selector and all authoritative
EndpointSlice and legacy Endpoints objects. Clusters may maintain both at once;
the Control Plane deletes both source representations while intercepted so the
EndpointSlice mirroring controller cannot recreate an old direct Pod path, and
restores each captured representation during compensation. The same durable
snapshot has two roles: it is the rollback source and the only source of primary
backend
addresses. Control Plane derives ready, non-terminating TCP/UDP targets for every
selected Service port from that snapshot, rejects an incomplete backend set,
and persists the snapshot before applying the managed Gateway EndpointSlice.
The desktop supplies only local shadow targets; it can never provide or replace
a Pod address. Primary targets remain fixed for the lifetime of the Task, so a
Mirror must be restarted to adopt a changed endpoint set.

For each TCP connection or UDP association, the Control Plane listener forwards
the request synchronously to an original backend and returns only that
backend's response to the cluster client. Backend selection is round-robin and
dial failure may fall through to another captured ready target. In parallel,
request bytes are offered to a bounded, non-blocking shadow queue. Queue
overflow drops that shadow stream; it never waits in a primary socket loop.
TCP half-close and UDP datagram boundaries are preserved independently.

The neutral `mirrorstream` protocol is directional. Control Plane may send
ready/open/data/close-write/close/datagram frames. The desktop may send only a
Task stop frame. Desktop shadow responses are continuously read and discarded;
they are never encoded onto the WebSocket. Any other client frame is a protocol
error.

The desktop gives every local shadow actor its own bounded queue, dial timeout,
write timeout and idle timeout. A local dial failure, early close, late frame,
queue overflow or slow writer retires only that actor. Frames for a retired
best-effort stream are ignored until its terminal close; they do not terminate
the Mirror Task. Terminal frames are themselves best-effort and local
tombstones are bounded. Task, Profile, namespace and application shutdown still
cancel all actors immediately.

The Control Plane replica that owns the authenticated WebSocket also owns the
Gateway listeners, primary sockets, authorization lease and restoration. Stop,
OAuth grant or Session revocation, WebSocket loss and graceful shutdown close
listeners and active sockets, restore the snapshot through the Control Plane
system Kubernetes client, and only then remove the snapshot. Restore failure
keeps the Task in `recovering`; stale-owner reconciliation uses the observed
state and `updated_at` value as a compare-and-swap claim before retrying the
same durable compensation.

## Consequences

- Cluster clients always observe original backend responses. A local process
  can inspect requests and may write responses, but those bytes are discarded.
- Shadow latency and failure are isolated twice: at the Control Plane WebSocket
  queue and at each desktop local-target actor.
- Kubernetes credentials, backend discovery, listener allocation and rollback
  remain in Control Plane. The desktop retains only explicit local host/port
  targets and a typed Control Plane client.
- Backends observe the Control Plane Pod as the connection source, and endpoint
  membership is a start-time snapshot rather than a live watch. These are
  explicit V2 limitations.
- Real Minikube coverage proves TCP and UDP requests traverse the managed
  EndpointSlice and original Pod backend while the local target receives the
  same request copy. It also proves local responses cannot replace primary
  responses, and that explicit stop, abrupt desktop loss, OAuth grant
  revocation and stale-owner recovery restore the original Service resources.
