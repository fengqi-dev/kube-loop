# ADR 0016: V2 domain model, ownership and identity

- Status: Accepted
- Date: 2026-08-10

## Context

V2 separates a local desktop, a persistent Control Plane and a credential-free
Data Plane. Earlier implementation milestones introduced the required records,
but some names reflected storage mechanics (`TokenFamily`, `Session`) rather
than the product domain. The ownership chain, persistence boundary and identity
rules must be explicit before adding more protocols or migration behavior.

This decision builds on ADR 0005. It does not make a WebSocket or bearer token
an owner: every operation must resolve back to a stable Principal,
DeviceSession, ClusterSession and, for mutations or streams, Task.

## Decision

### Canonical aggregates

| Aggregate | Canonical fields and identity | Owner and lifecycle | Persistence |
| --- | --- | --- | --- |
| `ServerProfile` | `schemaVersion`, server `id`, canonical `baseURL`, `tunnelPath`, display name and non-secret last-used UI fields | Owned by the local OS user. `id` is the server's stable discovery identity, not an authorization secret. | Versioned `servers.json`; no token, kubeconfig path or provider secret |
| `Principal` | UUID v4 `id`, provider, immutable provider external key, mutable display name/email/groups | Created/upserted after OIDC or AD authentication. It is the root server-side user identity. | Control Plane database row with object schema version |
| `DeviceSession` | UUID v4 `id`, Principal ID, stable client-generated Device ID, refresh-token hash, expiry/revocation | One login on one device. Refresh rotation records are children of this aggregate; replay revokes the whole DeviceSession. | Control Plane `token_families` row with object schema version; client projection in versioned keyring metadata |
| `ClusterSession` | UUID v4 `id`, Principal ID, Device ID, cluster ID, namespace, state, generation, immutable NetworkSpec/hash, heartbeat and expiry | Owned jointly by the authenticated Principal and DeviceSession for exactly one cluster/namespace. It cannot be reactivated after stop or expiry. | Control Plane `sessions` row with object schema version |
| `Task` | UUID v4 `id`, Principal ID, ClusterSession ID, type, unified state, immutable spec, result, idempotency key and expiry | A durable operation intent. It cannot outlive its ClusterSession except while a rollback snapshot requires compensation. | Control Plane `tasks` row with object schema version; local histories are versioned projections, never authority |
| `Stream` | Owning Task/ClusterSession UUID, authenticated connection identity, protocol kind and connection-local channel handle | Ephemeral child of one Task or ClusterSession. The Control Plane/Data Plane replica that accepted the authenticated claim owns its context, sockets and lease. | Never persisted; payload, command output and traffic bytes are not stored |
| `AuditEvent` | UUID v4 `id`, optional Principal ID, action, resource type/ID, outcome, request ID, safe metadata and timestamp | Append-only evidence emitted after authentication/ownership resolution. | Control Plane `audit_events` row with object schema version |

The concrete client type remains `profile.Profile`, with
`profile.ServerProfile` as its canonical domain alias;
`storage.TokenFamily` aliases `DeviceSession`, and `storage.Session` aliases
`ClusterSession`. Repository and API names can migrate without duplicating
domain records or changing the database wire format.

### Ownership graph

The only valid server-side ownership chain is:

```text
ServerProfile (local routing/configuration only)
  -> Principal
    -> DeviceSession
      -> ClusterSession (cluster + namespace + NetworkSpec)
        -> Task
          -> Stream
          -> ResourceSnapshot / cleanup intent
        -> RelayTicket (short-lived capability, not an owner)
```

An Access Token authenticates a request but does not own resources. A
RelayTicket authorizes a bounded Data Plane claim but cannot create a
ClusterSession or Task. Kubernetes resources created or intercepted for a Task
must carry the Task UUID in exact owner metadata and are restored from the
Task's durable snapshot.

### Schema version rules

- Every independently persisted aggregate has an object-level positive schema
  version. Container files also have an envelope version so their collection
  layout can evolve independently.
- A missing object version is accepted only by an explicit legacy migration
  path and is normalized to version 1 before the next write. Unknown future
  versions fail closed; they are never silently truncated.
- Control Plane database migrations and object schema versions are separate.
  A database migration changes physical storage; an object migration changes
  the encoded domain contract. ORM auto-migration remains forbidden.
- Refresh-token history, idempotency rows and resource snapshots are child
  persistence records. They either carry their own schema version or inherit
  an immutable parent aggregate contract where the row contains no independently
  decoded domain payload.
- Stream payloads are ephemeral and therefore have protocol versions and frame
  validators, not persistence schema versions.

`ServerProfile`, keyring credential metadata and local file-transfer Task
history now normalize legacy missing object versions and reject future
versions. Principal, DeviceSession, ClusterSession, server Task, resource
snapshot, idempotency and AuditEvent repositories already apply the same
fail-closed rule.

### Identity rules

- Principal, DeviceSession, ClusterSession, Task, ResourceSnapshot, AuditEvent,
  RelayTicket and authentication transaction IDs are generated from
  cryptographically random UUID v4 values. Device IDs are also client-generated
  UUID v4 values and remain stable for the profile until logout/removal.
- PKCE values, OAuth state/nonces, exchange codes, refresh tokens and local
  credential generations use `crypto/rand` with at least 128 bits of entropy.
  Failure to obtain randomness fails the operation; there is no deterministic
  identity fallback for an authorizing resource.
- ServerProfile IDs are stable server identities and may be human-readable.
  Kubernetes names are target identifiers. Neither is treated as a bearer
  secret or as sufficient authorization.
- A connection-local numeric channel handle is only a framing correlation
  value inside an already authenticated, Task-bound stream. It is never
  accepted by an HTTP API, stored as an owner, or authorized without the
  enclosing connection and Task/ClusterSession UUID; consequently it is not a
  domain resource ID.
- IDs may appear in audit output only after ownership is validated. Tokens,
  secrets, payload bytes, local file contents and command output never do.

## Consequences

- Product language can use DeviceSession and ClusterSession consistently while
  existing repository call sites remain source compatible during migration.
- Every durable owner can be decoded and migrated independently, and a newer
  object cannot be accidentally accepted by an older client or Control Plane.
- A guessed Task, Session or channel value cannot cross the Principal,
  DeviceSession, namespace, authenticated-connection and Policy checks.
- Stream loss is handled by cancellation and durable Task compensation; stream
  frames themselves do not become a second persistence system.
