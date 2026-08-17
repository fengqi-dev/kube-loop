# ADR 0018: Control Plane/Data Plane internal protocol

- Status: Accepted
- Date: 2026-08-10

## Context

Control Plane currently signs short-lived RelayTickets while Data Plane verifies
them from a mounted public-key set. Both components receive one static relay ID
through Helm. This is sufficient for a single known Data Plane Service, but it
does not define registration, liveness/capacity, Session assignment, key
rotation, revocation propagation or drain coordination for multiple replicas.

The internal protocol is a separate trust boundary from the public `/api`
and `/tunnel` protocols. A desktop must never call it, and a JSON body must not
be able to choose its own relay identity.

## Decision

### Transport and identity

The protocol uses HTTPS inside the cluster and supports two workload identity
modes. In mTLS mode, Control Plane trusts only a configured internal CA and
requires a workload identity SAN (SPIFFE URI or an equivalently constrained
certificate identity) bound to the expected namespace and Data Plane
ServiceAccount. The scalable Helm default is Kubernetes TokenReview: Data Plane
continues to set `automountServiceAccountToken: false` and explicitly projects
only a short-lived token with the dedicated `kubeloop-relay` audience. The
Control Plane sends it to TokenReview, requires the expected ServiceAccount and a
single Pod-bound UID, then confirms that UID and topology through its system
Kubernetes client. A general API credential or long-lived shared bearer token
is not introduced.

The HTTP authentication layer produces `relaycontrol.PeerIdentity` containing
trust domain, namespace, ServiceAccount and Pod UID. The registration body has
no `relayId` field. Control Plane derives a stable `relay-<sha256>` ID from that
authenticated tuple; changing the Pod UID creates a new relay lease. An
advertised WSS endpoint is routing metadata, not identity, and the Registry
must additionally restrict it to configured cluster/public domains before use.

### Versioned messages

All bodies use strict JSON with `apiVersion: relay.kubeloop.io/v1` and a fixed
`kind`. Unknown fields, multiple JSON documents, bodies over 64 KiB, unknown
versions and wrong kinds fail before state mutation. V2-112 defines six
messages in `internal/protocol/relaycontrol`:

Registration is the version-negotiation bootstrap. It carries an ordered list
of supported internal versions; Control Plane selects its highest preference in
that list. During a two-version rolling upgrade, a new peer continues using the
v1 bootstrap envelope and both old-Control Plane/new-Data-Plane and
new-Control Plane/old-Data-Plane combinations select v1. A version switch occurs
only after both sides advertise it and accept the registration response. With
no common version, no lease is created.

| Kind | Direction | Purpose |
| --- | --- | --- |
| `RelayRegistration` | Data Plane → Control Plane | Advertised WSS endpoint, ready/draining state, maximum/current physical connections and logical streams, applied key and revocation generations |
| `RelayRegistrationResult` | Control Plane → Data Plane | Derived Relay ID, unpredictable lease ID, lease deadline/heartbeat interval, desired state, complete verification key set and revocation summary |
| `RelayHeartbeat` | Data Plane → Control Plane | Renew exact lease and report current state/capacity plus applied generations |
| `RelayHeartbeatResult` | Control Plane → Data Plane | New deadline, next heartbeat, desired ready/draining state and latest key/revocation snapshots |
| `SessionAllocation` | Control Plane internal allocator → Registry | ClusterSession UUID/generation and immutable NetworkSpec hash |
| `SessionAssignment` | Registry → ticket/session layer | Ready Relay ID, WSS endpoint, lease ID and assignment time |

Durations are encoded as integer nanoseconds by the Go JSON contract. The
contract limits heartbeat to 1–60 seconds and a lease to at most five minutes;
heartbeat must occur before lease expiry. Capacity counters cannot exceed their
declared maxima. Relay IDs, leases and ClusterSession IDs are validated before
use.

### Lease and allocation semantics

- Registration creates a new unpredictable lease for the authenticated Pod.
  A heartbeat renews only that exact Relay ID/lease pair; another Pod identity
  cannot adopt it.
- Only a `ready`, unexpired relay with spare declared capacity can receive a
  new Session assignment. Selection and capacity reservation are atomic in the
  Registry implemented by V2-113.
- A `draining` report or Control Plane desired state immediately removes the relay
  from new allocation. Existing assignments and streams remain bound to their
  original Relay ID until they finish, expire or fail; the Control Plane never
  reports that an active stream migrated.
- Missed heartbeat/lease expiry marks the relay offline and prevents new
  assignments. It does not rewrite Task/Stream ownership. Clients recover by
  obtaining a new Session generation/RelayTicket and opening new streams.
- A newly registered Pod with a new Pod UID receives a new Relay ID even if it
  has the same Deployment name or IP as a terminated Pod.

### RelayTicket verification-key rotation

Control Plane responses carry a generation-numbered set of at most eight Ed25519
public keys with `notBefore`/`notAfter`. At least one key must be usable now.
During rotation, Control Plane publishes the new verification key before signing
with it and retains the previous key until every ticket it signed has expired.
Data Plane reports `appliedKeyGeneration`; the signer must not switch to a new
key generation until the required ready relays acknowledge it. Unknown key IDs
remain fail-closed.

The private RelayTicket key never crosses this protocol and remains mounted
only in Control Plane. Data Plane receives public material only.

### Revocation summary

The summary has a monotonic generation, validity window and canonical digest.
Each sorted entry contains SHA-256 of a revoked ClusterSession ID, the maximum
revoked Session generation and an expiry after the last possibly valid
RelayTicket. Data Plane hashes the ticket's Session ID and rejects it when its
generation is at or below a live entry. This allows OAuth grant/Session
revocation without exposing Identity IDs in relay metrics or logs.

The digest covers each entry's hash, maximum generation and expiry; modified,
unsorted or duplicate entries fail validation. Data Plane reports
`appliedRevocationGeneration`. Control Plane must stop assigning/signing for a
relay whose summary is stale beyond its validity window. The 64 KiB/1024-entry
v1 bound is intentional; if scale exceeds it, a later protocol version may use
a partitioned or probabilistic structure without silently changing v1.

## Consequences

- Relay ID, Ticket issuer, verification keys and revocations are Control
  Plane-owned state delivered over authenticated registration and heartbeat
  responses; Data Plane has no static Relay compatibility configuration.
- Control Plane can coordinate capacity, drain, key rollout and revocation without
  granting Data Plane database, OAuth/OIDC or Kubernetes credentials.
- Active streams are never claimed to migrate. Recovery creates a new bounded
  assignment and RelayTicket after the old path is known unavailable.
- Contract tests cover all six round trips, both directions of a two-version
  rolling upgrade, strict decoding, unknown versions,
  client-supplied Relay ID rejection, capacity/lease bounds, active-key
  requirements, peer-derived identities and tamper-evident generation-bound
  revocation lookup.
