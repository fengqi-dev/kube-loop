# ADR 0017: V2 backend cutover and no mixed Session

- Status: Accepted
- Date: 2026-08-10

## Context

The original migration plan proposed shipping local kubeconfig and remote
Gateway implementations in one desktop binary behind a feature flag. The
migration has now crossed the production cutover: the V2 composition root,
first-use flow, MCP and all feature managers are remote, while V1 packages are
kept only as isolated migration references and tests.

Reintroducing a runtime local option would again require kubeconfig discovery,
Kubernetes credentials and client-go in the desktop. It would also allow a
single UI state to combine a local inventory or exec stream with a remote
ClusterSession, defeating the ownership and authorization model.

## Decision

V2 uses a source-isolated dual implementation during repository migration, but
the production V2 desktop has exactly one backend mode: `remote`.

- `internal/cluster`, `internal/session`, `internal/filemanager` and the V1
  intercept/port-forward adapters are the local reference implementation. They
  remain outside the desktop dependency graph and may only be used by their own
  unit tests and explicit V1 E2E packages.
- `internal/client` is the remote implementation. Its managers depend on
  narrow feature interfaces rather than Kubernetes types: discovery/inventory,
  ClusterSession, Data Plane, exec, file transfer/file operations, Port
  Forward, Exchange, Mirror, Preview and Pod SSH each accept a typed remote
  client or Session source that tests can replace with a fake.
- `internal/app.NewApp` is the only desktop composition root. It constructs
  remote managers only. `BootstrapData.backendMode` is always `remote`; `mode`
  only distinguishes first-use `setup` from an active `v2` ServerProfile and is
  not a backend selector.
- There is no environment variable, profile property or hidden fallback that
  can select a local Kubernetes backend. A ServerProfile contains only the
  service identity and URL; a ClusterSession can therefore never mix local and
  remote calls.
- The old proposed runtime feature flag is intentionally superseded. A release
  that still needs V1 behavior must use the V1 artifact/code line, not switch an
  active V2 process. This makes rollback an application-version rollback rather
  than a per-Session authority change.

## Feature ownership

| Feature boundary | Local reference | Remote V2 interface/manager | Production selection |
| --- | --- | --- | --- |
| Cluster discovery/inventory | `cluster.Provider`, informer watcher | `clientv2/discovery`, `clientv2/remote.Client` | Remote only |
| Cluster Session/network | `session.Manager` | `clientv2/remotesession.Manager`, `clientv2/dataplane.Manager` | Remote only |
| exec/TTY | `cluster.Provider.Exec` | `clientv2/exec.Client` and Manager | Remote only |
| file transfer/operations | `filemanager.Manager` | `clientv2/filetransfer.Client` and Manager | Remote only |
| Port Forward | `client/portforward/listener.Manager` | `client/portforward` remote Client/Data Plane interfaces | Remote only |
| Exchange/Mirror/Preview | `intercept.Manager` | feature-specific `clientv2` Client/Session interfaces | Remote only |
| Pod SSH | `client/podssh/sshserver.Executor` | `client/podssh` remote exec interface | Remote only |
| MCP | V1 local managers | `mcp.RemoteBackend` using typed Gateway SDK | Remote only |

Manager unit tests validate behavior through these narrow interfaces using
fakes, so retry, cancellation, ownership and state-machine tests do not require
either a kubeconfig or a live Gateway. Real Minikube E2E then validates the
remote implementation end to end. Local V1 tests remain useful as behavior
references but are not a second production backend conformance target after
cutover.

## Enforcement

Architecture tests fail if:

- the desktop composition root imports V1 Kubernetes/session/store packages;
- the complete desktop dependency graph contains `k8s.io/*`,
  `internal/cluster` or `internal/session`;
- `clientv2` imports a local Kubernetes or server runtime package;
- MCP imports local cluster/session/intercept/file manager packages; or
- Data Plane imports Control Plane, Kubernetes, OAuth or database packages.

Bootstrap tests assert both setup and active-profile states report the constant
remote backend mode and do not read `KUBECONFIG`.

## Consequences

- The desktop remains service-address-only and cannot accidentally bypass
  Gateway Policy, OAuth grant revocation, ClusterSession ownership or audit.
- No active Session can change backend authority in place. Switching server or
  namespace first drains all remote tasks and disconnects the old Session.
- Migration comparison remains available in isolated tests without carrying a
  security-sensitive local fallback into the V2 product.
- Rollback from V2 to V1 is coarse-grained and requires using the older
  application version; V2 profile/token data is not imported into a V1 Session.
