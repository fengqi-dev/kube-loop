# ADR 0005: Cluster Session lifecycle and ownership

- Status: Accepted
- Date: 2026-08-10

## Context

Relay streams, traffic tasks and temporary Kubernetes resources need one
server-side owner that survives individual HTTP requests. Access Tokens alone
are too short-lived, and a client retry must not accidentally create duplicate
owners.

## Decision

A Cluster Session is bound to a stable principal ID, device ID, Gateway cluster
ID and namespace. Creation is authorized for that namespace and requires an
`Idempotency-Key`. Matching retries return the original Session; using the key
for a different namespace returns a conflict.

Every Session has a monotonically increasing generation. Heartbeat and
disconnect use an `If-Match` generation so concurrent or stale clients cannot
overwrite newer state. Heartbeat extends a short expiry but never beyond the
absolute maximum lifetime. Once expired or disconnected, a Session cannot be
reactivated. Reads and mutations by another principal, device or namespace
return the same not-found response.

The desktop keeps the idempotency key across an ambiguous create failure,
maintains heartbeats, disconnects the prior Session on namespace changes, and
performs bounded disconnect during logout, profile deletion and application
shutdown. Session IDs enter API audit only after the handler validates
ownership.

Creation also discovers and validates a versioned NetworkSpec through the
Controller Kubernetes Provider. Its canonical JSON and SHA-256 digest are
stored immutably on the Session. Get, heartbeat and disconnect return the same
snapshot; a RelayTicket derives its NetworkSpec digest from this stored value
and never trusts a client-supplied digest.

The create response also carries the current namespace capability snapshot.
It is produced by the same discoverer as `GET /api/v2/capabilities`, so its
schema, principal, namespace and Gateway-version bindings cannot diverge from
the standalone endpoint. This authorization snapshot is advisory and may be
cached briefly by the desktop; every operation remains independently
authorized and Kubernetes RBAC changes are not persisted as Session state.

## Consequences

- RelayTicket, Task and Stream records can bind to one trusted Session ID.
- A lost create response and repeated disconnect are safe.
- The storage schema records namespace, last heartbeat and the NetworkSpec
  snapshot/hash, and uses generation as an optimistic concurrency guard on
  SQLite and PostgreSQL.
- A successful create primes the desktop's bounded capability cache without a
  second network request. Heartbeat and disconnect preserve that local
  snapshot but do not treat it as authorization proof.
- The Controller runs an immediate and periodic bounded maintenance pass.
  Expired Sessions are deleted by heartbeat expiry; database foreign-key
  cascades remove their Tasks and snapshots, so a crashed desktop does not
  need to send a final request for server-side ownership to be reclaimed.
  Expired authentication transactions, idempotency records and token families
  use the same bounded pass. Interval and batch size are operator-configurable.
- Process recovery and complete in-memory Task/Stream context trees remain
  governed by the runtime ownership rules in ADR 0019.
