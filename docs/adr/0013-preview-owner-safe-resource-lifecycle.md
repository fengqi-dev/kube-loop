# ADR 0013: Preview owner-safe resource lifecycle

## Status

Superseded in stream ownership by the Gateway traffic-control boundary in ADR 0020.

Control Plane still owns Task and TrafficBinding metadata. Gateway now owns the
Preview WebSocket, TCP/UDP listeners and byte forwarding.

## Context

Preview exposes a process running on the desktop as a temporary Kubernetes
Service. The Control Plane creates a ClusterIP Service and an EndpointSlice that
targets TCP/UDP listeners owned by the authenticated reverse WebSocket stream.

These resources share a user-controlled namespace and naming domain with
ordinary application resources. A retry, stale Control Plane, delayed cleanup, or
delete/create race must therefore never overwrite or delete a resource that the
user owns. The desktop also cannot be trusted with Kubernetes credentials or
with choosing the Gateway listener address.

## Decision

### Exact Task ownership

The durable Preview Task UUID is the resource owner identity. Both the Service
and EndpointSlice carry all of the following metadata:

- `kubeloop.dev/preview-id=<full Task UUID>` annotation;
- `kubeloop.dev/preview-owner=<stable SHA-256-derived value>` label;
- `app.kubernetes.io/managed-by=kubeloop`;
- `app.kubernetes.io/name=kubeloop-preview`.

The full annotation is the authoritative identity. The hash label is only a
Kubernetes-compatible selector/index value and is never sufficient by itself
to authorize deletion.

### Create-only namespace behavior

Preview uses Kubernetes `create`; it never adopts, patches, or replaces an
existing Service or EndpointSlice. A conflict on either name fails the Task.
If Service creation succeeds but EndpointSlice creation fails, the Control Plane
compensates only the just-created Service after rechecking all owner metadata.
If compensation fails, the Task retains durable cleanup intent and enters the
recovery path.

The desktop submits the Service name, Service ports, protocols, and explicit
local targets. The Control Plane allocates Gateway listener ports and writes those
ports and its own Pod IP into the EndpointSlice. A Gateway response that changes
the requested name or Service port mapping is rejected by the desktop before it
accepts traffic.

### Durable cleanup intent before mutation

Before creating Kubernetes resources, the stream owner persists a
`preview-service` ResourceSnapshot containing the namespace, Service name,
Gateway IP, and allocated listener mappings. It then creates the resources and
persists the Task as `running`. The `ready` frame is sent only after both the
Kubernetes mutation and running state are durable.

The WebSocket owner jointly owns:

- TCP/UDP listeners;
- authorization and Cluster Session leases;
- reverse relay state;
- Kubernetes resource cleanup.

Durable stop is requested before the desktop sends the stream stop frame. A
disconnect, Token Family revocation, Access Token expiry, Session termination,
Control Plane shutdown, or explicit stop closes listeners and invokes the same
cleanup path.

### Recheck ownership at deletion

Cleanup reads the Service and EndpointSlice independently. It deletes an object
only when every owner marker still matches the exact Task UUID. Deletion uses a
Kubernetes UID precondition so an object replaced after the ownership read is
not removed. Missing objects and objects with foreign metadata are successful
idempotent outcomes: the Task no longer owns them.

User-scoped Kubernetes credentials are used for creation so normal Kubernetes
authorization remains effective. Cleanup and stale-owner recovery use the
Control Plane system identity because user credentials may have expired or been
revoked by the time compensation runs. The system identity does not weaken the
metadata and UID ownership checks.

If deletion fails, the Task enters `recovering` and retains its snapshot. A
bounded stale-owner worker claims the Task with a state-and-`updated_at` compare
and swap, retries exact-owner deletion, records a terminal state, and only then
deletes the snapshot.

## Consequences

- Existing user resources always win name conflicts and are never overwritten.
- A user can delete and recreate the same Service name while Preview is active;
  later cleanup preserves the replacement.
- A stale Control Plane can safely retry cleanup without holding the original user
  credential or WebSocket.
- A Service and its EndpointSlice may be cleaned independently after partial
  failures.
- Preview readiness includes an extra durable Task update and resource read,
  but clients never observe a ready stream before the Service is usable.
- Operators must grant the Control Plane ServiceAccount create/get/delete access
  to Services and EndpointSlices. Gateway Policy independently requires
  `create/get/delete/stream` on `previews`.

## Rejected alternatives

- **Adopt resources with matching names:** a name is not proof of ownership and
  would let a retry mutate an unrelated application Service.
- **Use labels only:** Kubernetes label value constraints require hashing and a
  hash alone is weaker than the complete Task identity.
- **Delete by name without a fresh read or UID precondition:** this can delete a
  user replacement created between cleanup retries.
- **Let the desktop create or delete Kubernetes resources:** this violates the
  V2 trust boundary and reintroduces local kubeconfig requirements.
- **Rely only on process-local defers:** they do not survive Control Plane loss and
  cannot provide bounded recovery of leaked cluster resources.
