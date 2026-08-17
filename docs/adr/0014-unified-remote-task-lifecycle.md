# ADR 0014: Unified remote Task lifecycle

## Status

Accepted for V2.

## Context

V2 moves every Kubernetes-facing operation behind the Gateway. Port Forward,
Pod exec, file transfer, Exchange, Mirror, and Preview are durable remote Tasks,
but their first implementations used different state names and readiness
boundaries. Port Forward used `active`; resource-mutating streams used
`preparing`; and Pod exec and file transfer became `running` before their
WebSocket and authorization lease were established.

Those differences made SDK validation feature-specific, made stale-owner
recovery harder to reason about, and allowed a client to observe `running`
before an operation could actually carry traffic. Repeated HTTP writes also
need one contract so a timeout and retry cannot create a second Task or a
second Kubernetes mutation.

## Decision

### One state vocabulary

Every durable remote Task uses `remotetask.State` and exactly this externally
visible vocabulary:

| State | Meaning |
| --- | --- |
| `pending` | Persisted and unclaimed. |
| `starting` | Claimed; transport, lease, listener, snapshot, or resource preparation is in progress. |
| `running` | The operation is durable and ready to serve traffic or execute work. |
| `recovering` | The original owner was lost or cleanup failed; a bounded recovery worker owns retry work. |
| `stopping` | Durable stop intent exists and owned runtime or Kubernetes resources are being released. |
| `stopped` | Successful terminal outcome, including an idempotent stop. |
| `failed` | Unsuccessful terminal outcome with no remaining owned cleanup work. |

The storage repository validates every create and compare-and-swap transition.
Terminal states are immutable. Only owned non-terminal states may update
themselves as heartbeats. Invalid values and regressions are rejected before
SQL execution, so all Control Plane replicas enforce the same graph.

The initial implementation retains direct `pending -> running` for bounded,
synchronous work such as Port Forward target authorization and remote file
metadata mutations. Streaming or resource-mutating work uses
`pending -> starting -> running`. Stop, failure, and recovery edges remain
explicit and optimistic-concurrency protected.

### Readiness boundary

`running` means usable, not merely claimed:

- Pod exec and file transfer enter `starting` before WebSocket acceptance and
  become `running` only after both the WebSocket and the authorization/Session
  lease exist.
- Exchange, Mirror, and Preview use `starting` while allocating listeners,
  persisting cleanup intent, and changing Kubernetes resources. They become
  `running` before emitting their protocol `ready` frame.
- Port Forward is created as `running` only after the Control Plane has resolved
  and authorized the target and persisted its authoritative dial address.

An upgrade or lease failure while `starting` records `failed`; no client sees a
false-ready `running` Task.

### Shared API and SDK contract

Control Plane documents, storage models, and typed client Task DTOs all expose
`remotetask.State`. The SDK validates the shared vocabulary rather than keeping
feature-local lists. The initial SQLite, PostgreSQL, and MySQL schemas accept
only the current state vocabulary; no historical Task-state migration is part
of the V2 baseline.

### One idempotent create contract

Every remote Task create request requires exactly one `Idempotency-Key` of at
most 128 bytes using the log-safe `A-Z a-z 0-9 - . _ :` alphabet. A shared
helper binds the request hash to:

1. Session ID;
2. namespace;
3. canonical JSON request specification.

The reservation scope is `task:<task-type>:<identity-id>`. Reservation and
Task creation occur in one transaction. A retry with the same scope, key, and
hash returns the original Task; the same key with a different hash returns a
conflict. The hash byte format preserves the existing Exchange, Mirror,
Preview, Pod exec, and file-transfer records. Port Forward additionally accepts
its former envelope hash during replay, while all new reservations use the
shared format.

### One transition audit contract

The concrete Task Repository wraps every successful `UpdateState` and
`ClaimStale` in one storage transaction with an append-only `task.transition`
AuditEvent. This single boundary covers Port Forward, Pod exec, file transfer,
remote file operations, Exchange, Mirror, Preview, and all recovery workers;
new Task kinds cannot silently omit lifecycle auditing while using the shared
Repository.

Each transition event contains only the Identity ID, Task type/ID, request or
background correlation ID, Session ID, namespace, previous/next state, source,
outcome, and timestamp. Task spec/result JSON, command arguments and output,
file names or contents, network payloads, Tokens, and identity claims are never
copied. API transitions retain the framework request ID through context;
detached recovery and cleanup work receives a generated `background-` ID. If
the audit append fails, the state compare-and-swap rolls back in both SQLite
and PostgreSQL instead of creating an unaudited lifecycle change.

## Consequences

- Clients can render and automate every remote operation with one lifecycle.
- `running` is a reliable readiness signal across transports and features.
- Storage rejects illegal transitions even if a handler or recovery worker is
  buggy or stale.
- Existing databases and idempotency records remain usable during a rolling
  V2 upgrade.
- New Task kinds must use the shared state type, transition validator, and API
  idempotency helper rather than defining feature-local values.
- Task lifecycle changes and their audit evidence are committed atomically;
  callers cannot update the concrete Repository without emitting the safe
  transition event.
- Some bounded operations intentionally skip `starting`; the shared graph
  permits this without inventing a fake asynchronous preparation phase.

## Rejected alternatives

- **Keep aliases such as `active` and `preparing`:** aliases preserve ambiguity
  in clients, migrations, metrics, and recovery queries.
- **Mark a Task running before transport acceptance:** persistence would claim
  readiness while the user still cannot use the operation.
- **Validate states only in handlers:** recovery workers and future callers can
  bypass handler-local checks; the repository is the common enforcement point.
- **Use a database enum or CHECK constraint as the only guard:** it validates
  values but not legal transitions and complicates SQLite/PostgreSQL parity and
  future additive migrations.
- **Let each API hash requests independently:** equivalent retry semantics
  would continue to drift, and clients could accidentally reuse a key across a
  different Session or namespace.
