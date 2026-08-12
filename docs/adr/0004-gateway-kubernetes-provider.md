# ADR 0004: Gateway Kubernetes Provider and inventory boundary

- Status: Accepted
- Date: 2026-08-10

## Context

V2 clients know only the Gateway service URL. They must not load kubeconfig,
construct Kubernetes clients, infer cluster RBAC, or receive unrestricted raw
Kubernetes objects. The Control Plane therefore needs one auditable boundary for
all Kubernetes credentials, transport configuration and identity propagation.

## Decision

The Control Plane creates its base `rest.Config` exclusively from the in-cluster
ServiceAccount. The Provider copies this Config before every identity-specific
change and enforces bounded timeout, QPS/Burst, a Gateway User-Agent, JSON media
types and request contexts. It removes any pre-existing impersonation fields.

ServiceAccount mode is the default. Optional impersonation derives the username
from a fixed operator-controlled prefix plus the stable principal ID. Identity
groups reach Kubernetes only through explicit group mappings. The Helm chart
never creates `impersonate` permissions; operators enabling the feature must
grant narrowly scoped RBAC separately.

The CI Minikube profile enables a metadata-only API Server audit policy for
`/version`. Its impersonation E2E logs in through the real Control Plane, grants a
temporary identity-specific `impersonate` role outside the chart, and calls the
Gateway version API. The assertion requires one successful audit event whose
authenticated `user` is the Control Plane ServiceAccount and whose
`impersonatedUser` is the prefixed stable Principal ID with only the explicitly
mapped group. The fixture also supplies an unmapped identity group and rejects
any event that forwards it.

The HTTP layer first applies Gateway Policy. Namespace capability probes then
intersect that policy with Kubernetes `SelfSubjectAccessReview` results. The
inventory API returns minimal V2 DTOs for Namespace, Pod and Service resources,
uses bounded pagination, and validates every path and query before contacting
Kubernetes. Kubernetes error details are retained only as internal causes and
are not returned to clients.

The capability document is a versioned authorization snapshot, not a durable
permission grant. It carries the stable principal ID, namespace and exact
Gateway build version. Workflow capabilities require every Gateway Policy
operation used by that workflow and the corresponding Kubernetes access. This
includes Session plus RelayTicket operations for `cluster.tunnel`, Pod and
Service target reads for `ports.forward`, and `pods/exec` for exec and file
workflows.

The desktop keeps only a short-lived, bounded in-memory cache. Its key includes
the Server Profile and address, a one-way digest of the current device/refresh
credential, principal ID, namespace and Gateway version. A credential change,
principal change, namespace change, Gateway version change or 30-second expiry
forces a new authorized probe. Cached slices are copied at both boundaries and
are never persisted as authority.

Inventory list endpoints accept bounded Kubernetes label and field selectors in
addition to bounded pagination. Namespace listing treats the Kubernetes result
as candidates only and removes every namespace for which that principal cannot
perform the namespace-scoped capability probe, so neither names nor counts leak
through a broad cluster list grant.

Pod and Service watch endpoints use authenticated WebSockets with full-snapshot
resync documents. Informers are shared only within the same principal/group,
namespace and resource key. Informer callbacks enqueue a non-blocking dirty
signal; each subscriber has a one-element latest-snapshot mailbox. A slow or
paused desktop therefore drops intermediate snapshots and receives the newest
state without blocking the shared informer. Watches end at the access-token
expiry boundary, and the desktop reconnects with refreshed credentials. The
Wails bridge applies only exact profile/namespace events to the active page.

## Consequences

- Desktop V2 code has no kubeconfig or Kubernetes client dependency.
- Gateway Policy can only reduce Kubernetes RBAC; neither layer can expand the
  other.
- Capability UI hints cannot outlive an account switch or Gateway upgrade and
  every protected operation is still authorized independently at request time.
- API Server audit can distinguish the Gateway ServiceAccount and, when
  explicitly enabled, the impersonated principal and mapped groups.
- The Helm lifecycle CI continuously verifies this audit contract against a
  real API Server without adding impersonation RBAC to the production chart.
- Watch/resync, exec, file transfer, networking and session operations must use
  this Provider and the same authorization framework in later milestones.
