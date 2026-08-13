# ADR 0011: Exchange reverse-stream ownership

- Status: Superseded by the Gateway traffic-control boundary in ADR 0020
- Date: 2026-08-10

## Context

> Current implementation: Control Plane retains Task and TrafficBinding metadata;
> the RelayTicket-selected Gateway owns the WebSocket, TCP/UDP listeners and
> byte forwarding, and calls Control Plane over authenticated internal Echo HTTP.

Exchange replaces a Kubernetes Service's selected backends with a developer's
local process. V2 cannot expose the local process directly, and it cannot let
the desktop mutate Service, Endpoints or EndpointSlice resources. Control Plane
may run with multiple replicas, so creating a Task on one replica and claiming
its reverse stream on another must not leave an unreachable listener or an
unowned rollback snapshot.

## Decision

Exchange is a Session- and Principal-owned Control Plane Task. Task creation only
validates the requested Service-port-to-local-target mapping and persists a
pending Task. The authenticated reverse WebSocket claim selects the owner: the
Control Plane replica handling that upgraded request allocates ephemeral TCP/UDP
listeners on its advertised Pod IP and remains responsible for every stream,
the authorization lease and resource restoration.

Before changing Kubernetes, the owner captures the authoritative Service plus
both EndpointSlice and legacy Endpoints state and commits that rollback
snapshot to Control Plane storage. Some clusters expose both representations and
run the EndpointSlice mirroring controller; they are not alternatives. During
takeover the owner clears the selector, deletes legacy Endpoints first, deletes
the original EndpointSlices, and only then installs the managed EndpointSlice
pointing to its ephemeral listeners. This prevents the mirroring controller
from recreating the old Pod path beside the Gateway path. Restoration restores
each representation that was captured. Partial mutations must compensate from
the persisted snapshot. The WebSocket is declared ready only after the resource
change and running Task state are durable.

Cluster connections are multiplexed over one neutral binary reverse-stream
protocol. TCP preserves data, half-close and close semantics. UDP associations
preserve datagram boundaries and are independently idle-bounded. The desktop
dials only the local targets explicitly retained in its in-memory request; a
Gateway response cannot replace them with an arbitrary local address.

Client disconnect, OAuth grant or Session lease invalidation, explicit Task
stop and graceful Control Plane shutdown all close listeners first, restore the
snapshot, and then persist the terminal Task state. A stop request handled by a
different replica marks the Task as stopping; the owner observes that durable
state and performs restoration. The desktop persists that stop intent through
the Task DELETE before sending the stream Stop frame, so a fast owner cannot
race two competing Task transitions. Commands and payload bytes are not
audit-log fields.

Every Control Plane also runs a stale-owner reconciler. It scans preparing,
running, stopping and recovering Exchange Tasks whose heartbeat is older than
the configured threshold, then atomically claims the exact observed state and
`updated_at` value. Only the successful claimant restores the snapshot through
the Control Plane system Kubernetes client. Recovery failure keeps both the Task
in recovering state and the snapshot durable for a later retry; successful
recovery writes a terminal state before deleting the snapshot. Session expiry
maintenance does not cascade-delete a Session while any owned Task still has a
rollback snapshot.

## Consequences

- Kubernetes credentials and rollback logic remain in Control Plane; the desktop
  owns only local sockets and target selection.
- WebSocket claim, listener allocation and mutation ownership are colocated,
  avoiding dependence on load-balancer stickiness.
- Snapshot persistence and Kubernetes mutation form a compensating saga, not
  a cross-system ACID transaction.
- Abrupt owner loss is compensated by a stale-owner reconciler using the
  Control Plane ServiceAccount. Two-worker race tests prove one restore claimant,
  and retry tests prove snapshots survive restore outages.
- Real Minikube coverage sends TCP and UDP traffic from Pods through the
  intercepted Service, Control Plane listeners and reverse WSS into loopback-only
  desktop targets. It rejects mixed Gateway/Pod endpoint convergence and
  verifies explicit stop, abrupt desktop loss, OAuth grant revocation and a
  replacement Control Plane restoring a stale owner before normal Service traffic
  resumes.
