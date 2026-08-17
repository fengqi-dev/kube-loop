# ADR 0016: V2 domain model, ownership and identity

- Status: Accepted
- Date: 2026-08-10

## Context

V2 separates a local desktop, a persistent Control Plane and a credential-free
Data Plane. Earlier implementation milestones introduced the required records,
but some names reflected storage mechanics (`Session`) rather
than the product domain. The ownership chain, persistence boundary and identity
rules must be explicit before adding more protocols or migration behavior.

This decision builds on ADR 0005. It does not make a WebSocket or bearer token
an owner: every operation must resolve back to a stable Identity,
OAuth Grant, ClusterSession and, for mutations or streams, Task.

## Decision

### Canonical aggregates

| Aggregate | Canonical fields and identity | Owner and lifecycle | Persistence |
| --- | --- | --- | --- |
| `ServerProfile` | server `id`, canonical `baseURL`, `tunnelPath`, display name and non-secret last-used UI fields | Owned by the local OS user. `id` is the server's stable discovery identity, not an authorization secret. | Current-version `servers.json`; no token, kubeconfig path or provider secret |
| `Identity` | UUID v4 `id`, provider, immutable provider external key, mutable display name/email/groups | Created/upserted after OIDC authentication. It is the root server-side user identity. | Control Plane database row governed by the database migration version |
| `OAuth Grant` | Fosite request ID, Identity ID, Client ID, stable client-generated Device ID, hashed token signatures, expiry/revocation | One OAuth authorization. Refresh rotation and replay revoke the complete grant. | Control Plane `oauth_sessions` rows and versioned client keyring metadata |
| `ClusterSession` | UUID v4 `id`, Identity ID, Device ID, cluster ID, namespace, state, generation, immutable NetworkSpec/hash, heartbeat and expiry | Owned jointly by the authenticated Identity and OAuth Grant for exactly one cluster/namespace. It cannot be reactivated after stop or expiry. | Control Plane `sessions` row governed by the database migration version |
| `Task` | UUID v4 `id`, Identity ID, ClusterSession ID, type, unified state, immutable spec, result, idempotency key and expiry | A durable operation intent. It cannot outlive its ClusterSession except while a rollback snapshot requires compensation. | Control Plane `tasks` row; local histories are versioned projections, never authority |
| `Stream` | Owning Task/ClusterSession UUID, authenticated connection identity, protocol kind and connection-local channel handle | Ephemeral child of one Task or ClusterSession. The Control Plane/Data Plane replica that accepted the authenticated claim owns its context, sockets and lease. | Never persisted; payload, command output and traffic bytes are not stored |
| `AuditEvent` | UUID v4 `id`, optional Identity ID, action, resource type/ID, outcome, request ID, safe metadata and timestamp | Append-only evidence emitted after authentication/ownership resolution. | Control Plane `audit_events` row governed by the database migration version |

The concrete client type remains `profile.Profile`, with
`profile.ServerProfile` as its canonical domain alias;
`storage.Session` aliases
`ClusterSession`. Repository and API names can migrate without duplicating
domain records or changing the database wire format.

### Ownership graph

The only valid server-side ownership chain is:

```text
ServerProfile (local routing/configuration only)
  -> Identity
    -> OAuth Grant
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

- The Control Plane database has one migration version for the complete schema.
  V2 starts at baseline 1 and does not carry a constant version column on each
  row. Physical schema changes must use an explicit reviewed migration; ORM
  auto-migration remains forbidden.
- Independently stored client files retain their own envelope/object versions so
  they can be validated before decoding. Protocol messages retain protocol
  versions because peers can run different application releases.
- Refresh-token history, idempotency rows and resource snapshots inherit the
  database schema contract; they do not duplicate a fixed row-level version.
- Stream payloads are ephemeral and therefore have protocol versions and frame
  validators, not persistence schema versions.

`ServerProfile`, keyring credential metadata and local file-transfer Task
history validate their file formats independently. Database rows are validated
through constraints and repository decoding under the current migration.

### Identity rules

- Identity, OAuth Grant, ClusterSession, Task, ResourceSnapshot, AuditEvent,
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

- Product language can use OAuth Grant and ClusterSession consistently while
  existing repository call sites remain source compatible during migration.
- Every durable owner can be decoded and migrated independently, and a newer
  object cannot be accidentally accepted by an older client or Control Plane.
- A guessed Task, Session or channel value cannot cross the Identity,
  OAuth Grant, namespace, authenticated-connection and Policy checks.
- Stream loss is handled by cancellation and durable Task compensation; stream
  frames themselves do not become a second persistence system.
