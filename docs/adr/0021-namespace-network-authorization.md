# ADR 0021: Namespace-bound network authorization at Data Plane

- Status: Accepted
- Date: 2026-08-10
- Scope: V2.0

## Context

A desktop Cluster Session selects one Kubernetes namespace. Kubernetes Pod and
Service CIDRs are cluster-wide routing metadata, so allowing a whole CIDR would
turn Data Plane into a cross-namespace proxy even when the Control Plane API had
correctly authorized only one namespace. Pod IP reuse also means a snapshot
that is never refreshed can eventually authorize a Pod that moved to another
namespace.

Data Plane deliberately has no Kubernetes client, business database, OIDC/AD
configuration, or broad ServiceAccount. It therefore needs a bounded,
cryptographically verifiable permission snapshot supplied by Control Plane.

## Decision

### Authorization chain

1. Every Control Plane request is authenticated to a Principal and mapped to a
   policy request containing operation, resource kind and namespace.
2. Session creation lists Pods and Services through the Principal's
   impersonating Kubernetes client in exactly the selected namespace. A system
   client may read only cluster routing metadata and CoreDNS configuration.
3. `NetworkSpec` contains exact non-host-network Pod IPs and namespace Service
   ClusterIPs. Pod/Service CIDRs remain routing metadata and never grant dial
   permission. The cluster DNS Service IP is granted only on port 53.
4. Control Plane persists canonical `NetworkSpec` JSON and its SHA-256 hash. A
   RelayTicket signs Principal, device, Session ID, Session generation,
   namespace, operation, assigned Relay, hash, expiry and one-use ticket ID.
5. Data Plane verifies the Ed25519 signature, issuer, Relay audience, operation,
   generation, revocation, expiry and replay state before accepting WSS. The
   protocol Session token, namespace and registered NetworkSpec hash must all
   match the signed claims.
6. Every outbound TCP/UDP open is checked again. Literal addresses must be an
   exact PodIP/ServiceIP in the snapshot. DNS names must use a configured
   Kubernetes cluster suffix; every resolved answer is filtered by the same
   exact-IP allowlist. Kubernetes API, loopback, link-local, multicast, public
   and unmatched private addresses are denied.

### Freshness and revocation

Control Plane rediscovers and atomically replaces the namespace NetworkSpec on
every Session heartbeat. Discovery failure fails closed and does not extend the
Session. A changed snapshot receives a new hash together with the next Session
generation, so a new RelayTicket cannot register an older permission set.

The physical WSS connection receives the RelayTicket expiry as an immutable
deadline. It cannot outlive the signed permission snapshot. Newer generations
reject new streams on older physical connections; already-running streams are
allowed to finish only within the old ticket's maximum two-minute lifetime.
Session disconnect, token-family revocation and Relay revocation continue to
deny new tickets and connections.

### Policy semantics

The V2.0 permission unit is a namespace:

- Gateway Policy decides whether a Principal/group may create or use a Session
  in that namespace.
- Kubernetes impersonation remains the second authorization gate for resource
  discovery and operations.
- A namespace grant permits any non-zero port on exact discovered Pod and
  Service IPs, except cluster DNS, which is restricted to port 53.
- Cross-namespace, namespace wildcard network routing, arbitrary CIDR and public
  Internet egress are not supported.

Declared Service/container-port restrictions may be added later as a new
NetworkSpec/protocol version. They must not be inferred into V2 because
Kubernetes port-forward and development workloads legitimately use undeclared
Pod ports.

## Consequences

- Data Plane can enforce permissions offline without database or Kubernetes
  credentials.
- Broad cluster CIDRs cannot grant cross-namespace access.
- Permission changes and IP reuse converge within heartbeat plus RelayTicket
  lifetime; they are not instantaneous for an already-running stream.
- Session heartbeat now depends on namespace discovery. Transient Kubernetes
  failures may require a client retry and eventually reconnect, which is a
  deliberate fail-closed tradeoff.

## Verification

Unit and contract tests must cover exact namespace Pod/Service IPs,
cross-namespace literal IP and DNS denial, DNS port restriction, hash/namespace
mismatch, missing hash, ticket expiry, stale generation, replay and NetworkSpec
refresh. Minikube E2E remains deferred until the consolidated E2E phase.
