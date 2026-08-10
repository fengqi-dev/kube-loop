# ADR 0020: TrafficBinding Operator boundary

- Status: Accepted
- Date: 2026-08-10

## Context

V2 initially moved Kubernetes operations from the desktop into the Controller.
Exchange, Mirror and Preview therefore combined authenticated HTTP task
lifecycle, stream ownership and direct Service/Endpoints/EndpointSlice writes
in one process. Port Forward resolved its target directly without a declarative
Kubernetes lifecycle object. This made recovery depend on Controller workers
and required the API ServiceAccount to retain broad mutation permissions.

KubeLoop needs one durable, observable reconciliation contract for all four
traffic workflows while keeping OAuth/OIDC/AD credentials and local desktop
addresses outside Kubernetes objects.

## Decision

The root Go module contains a separately deployed `kubeloop-operator` process.
Its API lives in `internal/operator/api/v1alpha1`; its reconciler lives in
`internal/operator/trafficbinding`. Root-level Kubebuilder metadata and
generated manifests remain in `PROJECT` and `config/`, so the Operator is a
component of this project rather than a nested project or Go module.

The namespaced `traffic.kubeloop.io/v1alpha1 TrafficBinding` CRD represents
PortForward, Preview, Exchange and Mirror. Its immutable spec binds one Task UUID
to one Session UUID and generation. It may contain Kubernetes target names,
ports and a Controller-owned relay IP, but never user tokens, identity-provider
secrets or a desktop destination.

Controller follows this ordering:

1. Authorize and persist the Task and any rollback intent.
2. Create the deterministic `kubeloop-<task UUID>` TrafficBinding.
3. Wait for `Ready=True` with `status.observedGeneration` equal to the current
   object generation before marking the Task running.
4. On stop or recovery, delete the TrafficBinding and wait until its finalizer
   has restored or removed owned Kubernetes resources and the CR disappears.

An identical create is an idempotent replay; the same deterministic name with a
different immutable spec is a conflict. A Controller maintenance worker removes
managed CRs whose durable Task is missing, terminal or no longer matches the CR
owner. Exchange and Mirror snapshots are copied into CR status by the Operator
before mutation so finalization has Kubernetes-durable rollback state.

## Consequences

- Controller keeps authentication, policy, Task persistence, relay listeners
  and stream ownership; Operator owns Kubernetes resource reconciliation.
- Controller can read Services and endpoint representations for authorization
  and stream routing, but cannot write them. Operator has no access to OAuth,
  OIDC, AD or application storage.
- Helm installs three workloads and the CRD. Controller and Operator use
  distinct ServiceAccounts and least-privilege RBAC.
- Operator unavailability keeps new traffic Tasks pending or fails them with a
  bounded error; it does not silently bypass reconciliation with direct writes.
- Finalizer completion, readiness conditions, restart recovery and orphan
  cleanup are cross-component compatibility contracts covered by EnvTest and
  architecture tests.
