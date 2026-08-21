# KubeLoop Helm Chart

This chart installs the server as three independent workloads:

- `kubeloop-control-plane`: discovery, authentication/API boundary, durable Task ownership and creation/deletion of `TrafficBinding` intents.
- `kubeloop-gateway`: WebSocket tunnel data plane. It has no database configuration and does not automount the general Kubernetes API credential. Registry mode projects only a short-lived, audience-bound Pod token.
- `kubeloop-operator`: watches `TrafficBinding` and exclusively coordinates Preview/Exchange/Mirror Service, Endpoints and EndpointSlice mutations with finalizer-based restoration.

The current implementation exposes discovery, OIDC login, token lifecycle, default-deny Gateway Policy, API audit, storage repositories, Kubernetes capability probes, read-only Namespace/Pod/Service inventory, owned Cluster Session lifecycles, short-lived RelayTicket-authenticated WebSocket transport, Session-bound Port Forward Tasks, Control Plane-owned Pod exec streams, resumable Control Plane-owned file-transfer Task streams, guarded remote directory management with desktop local-file integration, and TrafficBinding reconciliation through the Operator. Do not treat a development image tag as a production release.

Both workloads expose an independent `logLevel` value (`debug`, `info`, `warn`,
or `error`). The binaries validate the value again at startup. Control Plane and
Data Plane write structured JSON logs to stdout; changing one workload does not
change the verbosity of the other.

```yaml
controlPlane:
  logLevel: info
dataPlane:
  logLevel: info
```

## One public origin

`publicURL` is the exact HTTP or HTTPS origin entered by desktop clients. It
must not contain a path, query or fragment, and must match the route hostname
and the Ingress TLS setting.
The chart routes the same origin without rewriting paths:

| Public path | Backend |
| --- | --- |
| `/*` | Control Plane, including discovery, OAuth 2.0 / OIDC, `/api/*` and `/admin/*` |
| `/tunnel` | Data Plane WebSocket endpoint |

The Control Plane publishes that exact value as its OAuth2/OIDC issuer. Helm
fails rendering when a chart-managed route uses another hostname or scheme, or
when both Ingress and HTTPRoute are enabled.

The browser Management Plane is served by the Control Plane HTTP port under
`/admin/*`. It uses the top-level `publicURL`; there is no separate listener,
Service, public URL or Helm port setting.

Without a chart-managed external route, open it through a local tunnel:

```shell
kubectl -n kubeloop port-forward svc/kubeloop-control-plane 8080:8080
```

Then visit `http://127.0.0.1:8080/admin/ui`.

Ingress TLS is disabled by default, so the default scheme is HTTP and no
certificate Secret is required. Configure the timeout and
request-size annotations required by the selected Ingress controller. For
example, ingress-nginx can be configured as follows; `proxy-body-size` should
not exceed the Control Plane's
`maxRequestBodyBytes` unless the backend is intentionally the tighter limit:

```yaml
publicURL: http://kubeloop.example.com
ingress:
  enabled: true
  className: nginx
  host: kubeloop.example.com
  annotations:
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-send-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-body-size: "1m"
```

To enable HTTPS, change `publicURL` to `https://kubeloop.example.com` and
configure an existing certificate Secret:

```yaml
publicURL: https://kubeloop.example.com
ingress:
  tls:
    enabled: true
    secretName: kubeloop-public-tls
```

Gateway API mode requires Standard Channel v1.2 or later because it uses
HTTPRoute timeouts and the `kubernetes.io/ws` Service `appProtocol`. The tunnel
rule sets both request timeouts to `0s`; the Data Plane's own bounded stream idle
timeout remains authoritative. To attach to a platform-owned HTTPS listener:

```yaml
publicURL: https://kubeloop.example.com
gatewayAPI:
  enabled: true
  host: kubeloop.example.com
  parentRef:
    name: shared-public-gateway
    namespace: networking
    sectionName: https
```

The referenced listener must terminate TLS for the same hostname and allow an
HTTPRoute from the release namespace. Confirm the HTTPRoute `Accepted=True` and
`ResolvedRefs=True` conditions after installation. Alternatively, the chart can
create a dedicated HTTPS Gateway and reference an existing TLS Secret:

```yaml
gatewayAPI:
  enabled: true
  host: kubeloop.example.com
  gateway:
    create: true
    className: your-gateway-class
    tls:
      secretName: kubeloop-public-tls
```

Gateway API WebSocket backend protocol and request-timeout support are extended
conformance features. Verify that the selected implementation reports both;
KubeLoop's TLS proxy integration test independently covers discovery routing,
the Control Plane body limit, WSS upgrade and traffic after the proxy's ordinary
HTTP write timeout.

## IAM bootstrap and authentication keys

A new database creates the configured `controlPlane.admin.bootstrap` human
Identity, organization, `Administrators` group, group membership,
the default `KubeLoop` organization and its system `Administrators` group in
one transaction. Helm
stores a random initial password in the retained IAM bootstrap Secret;
Control Plane reads it from a read-only mount and requires replacement on first
sign-in. Existing IAM data is never recreated or reset. Read it with the command
printed by `helm install`, then delete the Secret after the password is changed.
Set `controlPlane.admin.bootstrap.enabled=false` to retain the manual single-use
bootstrap-token flow instead.

The retained auth Secret contains the authentication keys and, while bootstrap
is enabled, the initial administrator password:

- `oidc-signing-key.pem`: ECDSA P-256 PKCS#8 key for ES256 ID Tokens;
- `hmac-secret`: exactly 32 bytes for Fosite opaque token signatures;
- `initial-password`: the generated initial administrator password.

Never reuse authentication keys or database credentials. Passwords use Argon2id;
KubeLoop authentication uses local username and password credentials only.

## Relay Registry and RelayTicket keys

RelayTicket authentication is asymmetric. Control Plane alone receives an
Ed25519 PKCS#8 private key. A ready Data Plane registers with the Control Plane's
internal HTTPS Service, receives the current public-key set and revocation
summary, then acknowledges the applied generations by heartbeat. Control Plane
derives the Relay ID from authenticated namespace, ServiceAccount and Pod UID;
the registration body cannot choose it.

By default, Helm generates an Ed25519 RelayTicket signing key, a private CA and
a server certificate whose SAN covers the exact internal Registry Service DNS
name. The retained Secret is reused with `lookup` on later upgrades, so an
upgrade does not rotate the signing key, CA or server certificate.

The generated Secret contains `signing-key.pem`, `tls.crt`, `tls.key` and
`ca.crt`. Relay server key and certificate override parameters are intentionally
not exposed; the internal trust boundary is fully managed by the chart.

Registry authentication is fixed to `tokenreview`. Data Plane keeps
`automountServiceAccountToken: false`; Helm explicitly projects a ten-minute
token whose only audience is `kubeloop-relay`. Control Plane verifies its Pod UID
and ServiceAccount against Kubernetes before trusting topology or capacity.

For one Data Plane replica, the advertised endpoint defaults to
`publicURL + tunnelPath`. With multiple replicas, configure a routable endpoint
template containing `{podName}` or `{podUID}` and matching wildcard routing.
The chart rejects a shared endpoint because it cannot guarantee a
Relay-ID-bound Ticket reaches the selected Pod.

The current Registry is process-local and the chart therefore requires one
Control Plane replica while it is enabled. This is independent of Data Plane
replica count. Control Plane HA will require the later shared Registry/storage
conformance milestone; the chart fails fast instead of creating split leases.

```yaml
controlPlane:
  relay:
    keyID: primary
    ticketTTL: 1m
  relayRegistry:
    keyGeneration: 1
dataPlane:
  streamIdleTimeout: 30m
  relay:
    replayEntries: 65536
  relayRegistry:
    maxWebSocketSessions: 256
    maxWebSocketSessionsPerUser: 8
    maxStreamsPerSession: 128
    maxWebSocketFrameBytes: 1048576
    handshakeTimeout: 10s
```

The Data Plane atomically consumes each Ticket `jti`, verifies issuer, derived
Relay audience, key validity, revocation, expiry and `tunnel` scope, and binds
every protocol stream to the Ticket's Cluster Session. A key rotation first
publishes an overlapping generation; the Control Plane allocates only to relays
that have acknowledged it. Keep the previous public key through the two-minute
maximum Ticket window.

After the HTTP upgrade, the client must send a binary WSS v2 `ClientHello`
before any smux bytes. The Data Plane verifies protocol/client version and the
device ID bound into the one-time RelayTicket, then returns `ServerHello` with
the exact frame, logical-stream, physical-connection, per-user and idle limits.
Rejected negotiation returns a stable code such as `VERSION_MISMATCH` and does
not create a partially usable smux session. `controlPlane.minClientVersion`
applies consistently to discovery and this WSS handshake.

During rollout, Data Plane sends an immediate draining heartbeat, becomes
unready, rejects new WSS/logical connections, and lets existing streams finish
for `dataPlane.drainTimeout`. Control Plane stops new assignment to that lease.
Remaining streams are explicitly closed; clients obtain a fresh assignment and
generation-bound RelayTicket. Active streams are not described as migrated.

## Authorization

Authentication does not grant API access by itself. Roles, bindings and
Namespace scopes are stored in the Control Plane
database and managed from the Admin **Access Control** page. Helm does not
accept authorization rules.

Bindings target an Identity or organization Group selected in the Admin UI.
Scopes are platform, organization or one exact Namespace. Namespace ownership
and effective access are filtered in the repository query; the API never loads
all Namespaces and hides unauthorized rows afterwards.

## Management bootstrap

The Control Plane uses a deny-by-default role engine; ordinary runtime access
never grants IAM administration. Automatic bootstrap runs only against an empty
IAM database, creates one platform administrator and organization atomically,
and requires replacement of the random initial password on first sign-in. It
has no group fallback, recovery switch or first-login promotion. When automatic
bootstrap is disabled, the database-backed single-use token flow remains
available for an operator-driven initialization.

## Kubernetes access

The Control Plane and Operator use separate in-cluster ServiceAccounts; the
desktop client never loads kubeconfig or talks directly to the Kubernetes API.
Control Plane RBAC is split by workflow so an installation can disable
capabilities it does not offer:

| Permission group | Resources | Verbs | Used by |
| --- | --- | --- | --- |
| platform | namespaces, nodes, servicecidrs | get, list | namespace and NetworkSpec discovery |
| platform | selfsubjectaccessreviews | create | capability checks |
| platform (TokenReview only) | tokenreviews | create | projected Data Plane identity verification |
| dns-discovery | kube-system services/configmaps named kube-dns or coredns | get | CoreDNS Service IP and validated cluster-domain discovery |
| relay-registry | pods in the Helm release namespace | get, list | trusted Relay Pod/Node topology lookup |
| inventory | pods, services | get, list | Namespace/Pod/Service inventory |
| exec-file | pods; pods/exec | get; create | exec and file workflows, which both execute in the target Pod |
| traffic | pods; services/endpoints; endpointslices; trafficbindings | get; get/list; get/list; get/list/watch/create/delete | resolve authorized targets and manage Task-owned CR intents |

The Operator role is independent: it may reconcile `trafficbindings` and their
status/finalizers, read Pods, and create/update/delete Services, Endpoints and
EndpointSlices. It receives no database, OIDC, RelayTicket, exec/file or
Secret permissions. Control Plane no longer writes those traffic resources
directly; CR deletion waits until the Operator finalizer completes restoration.

The default `cluster` scope applies the enabled Inventory, exec/file and
traffic groups to every namespace. Each group has a distinct ClusterRole and
ClusterRoleBinding:

```yaml
controlPlane:
  kubernetes:
    timeout: 15s
    qps: 20
    burst: 40
  rbac:
    create: true
    scope: cluster
    permissions:
      inventory:
        enabled: true
      execFile:
        enabled: true
      traffic:
        enabled: true
```

Use `namespace` scope to render Roles and RoleBindings only in an explicit
allowlist. Cluster-scoped workload writes are not emitted in this mode:

```yaml
controlPlane:
  rbac:
    create: true
    scope: namespace
    namespaces: [development, staging]
    permissions:
      inventory:
        enabled: true
      execFile:
        enabled: true
      traffic:
        enabled: false
```

Namespace scope still creates one narrow ClusterRole for namespace/node/
ServiceCIDR discovery, SelfSubjectAccessReview and, when selected, TokenReview.
Relay Pod reads are always confined to the Helm release namespace. Kubernetes
DNS Service lookup is best-effort outside the allowed namespaces; grant a
separate `services/get` Role in `kube-system` only if that fallback is required.

Neither mode grants Secret reads, `nodes/proxy`, `impersonate`, wildcard
resources, wildcard API groups, or wildcard verbs. Operator and Data Plane
each have a different ServiceAccount. Data Plane is never included in a
Kubernetes workload RBAC binding and keeps
`automountServiceAccountToken: false`. Set `controlPlane.rbac.create=false` only
when equivalent Control Plane RBAC is managed externally.

Optional impersonation is a defense-in-depth layer. Identity groups are not
forwarded automatically: each Kubernetes group must be explicitly mapped.
The chart still does not grant the Control Plane permission to impersonate; the
operator must supply narrowly scoped RBAC separately and verify API Server audit
events before enabling this mode.

The repository's Minikube Helm lifecycle E2E enables API Server metadata auditing,
grants a temporary exact-user/exact-group impersonation role outside this
chart, calls `GET /api/version`, and asserts that the audit event contains
both the Control Plane ServiceAccount and the final prefixed Identity identity.
It also proves that an unmapped identity group is not forwarded.

```yaml
controlPlane:
  kubernetes:
    impersonation:
      enabled: true
      usernamePrefix: "kubeloop:"
      groupMappings:
        engineering: [k8s:developers]
```

Authenticated, policy-authorized clients can use:

- `GET /api/version`
- `GET /api/capabilities?namespace=<name>`
- `GET /api/namespaces[?limit=...&continue=...]`
- `GET /api/namespaces/<namespace>`
- `GET /api/namespaces/<namespace>/pods[/<name>]`
- `GET /api/namespaces/<namespace>/services[/<name>]`
- `GET /api/namespaces/<namespace>/{pods,services}?watch=true` as an
  authenticated WebSocket of versioned full snapshots
- `POST /api/sessions?namespace=<name>` with `Idempotency-Key`
- `GET /api/sessions/<id>?namespace=<name>`
- `POST /api/sessions/<id>/heartbeat?namespace=<name>` with `If-Match`
- `DELETE /api/sessions/<id>?namespace=<name>` with `If-Match`
- `POST /api/sessions/<id>/tickets?namespace=<name>` with JSON body
  `{"networkSpecHash":"<optional lowercase sha256>"}`
- `POST /api/sessions/<id>/port-forwards?namespace=<name>` with
  `Idempotency-Key` and a Pod/Service target document
- `GET /api/sessions/<id>/port-forwards?namespace=<name>`
- `DELETE /api/sessions/<id>/port-forwards/<task-id>?namespace=<name>`
- `POST /api/sessions/<id>/exec?namespace=<name>` with `Idempotency-Key`
- `GET /api/sessions/<id>/exec/<task-id>/stream?namespace=<name>` as an
  authenticated binary WebSocket stream
- `POST /api/sessions/<id>/file-transfers?namespace=<name>` with
  `Idempotency-Key`, a Pod/container target, an absolute container path, and
  upload metadata when applicable. File uploads may include a stable UUID
  `resumeId`; the Control Plane probes the Pod partial and returns the
  authoritative `offset` in the Task document.
- `GET /api/sessions/<id>/file-transfers/<task-id>?namespace=<name>`
- `GET /api/sessions/<id>/file-transfers/<task-id>/stream?namespace=<name>`
  as an authenticated binary WebSocket upload/download stream
- `POST /api/sessions/<id>/pod-files/list?namespace=<name>` with a
  Pod/container and absolute directory path
- `POST /api/sessions/<id>/pod-files/{create,rename,delete}?namespace=<name>`
  with `Idempotency-Key`; mutations return a terminal `pod-file-operation` Task
- `GET /api/sessions/<id>/pod-files/operations/<task-id>?namespace=<name>`
- `POST /api/sessions/<id>/{exchanges,mirrors,previews}?namespace=<name>`
  with `Idempotency-Key`; Exchange and Mirror select an existing Service while
  Preview declares a new temporary Service name and one or more ports
- `GET /api/sessions/<id>/{exchanges,mirrors,previews}/<task-id>?namespace=<name>`
- `DELETE /api/sessions/<id>/{exchanges,mirrors,previews}/<task-id>?namespace=<name>`
- Exchange, Mirror and Preview open an authenticated logical stream inside the
  assigned Data Plane `/tunnel` WebSocket. Preview resources carry the
  Task UUID as exact owner metadata; name conflicts are never overwritten and
  cleanup only deletes objects whose owner metadata still matches.

Capability results are the intersection of Gateway Policy and Kubernetes RBAC.
`/api/version` returns both `gitVersion` (Kubernetes) and `gatewayVersion`.
The versioned capability document includes `schemaVersion`, `identityId`,
`namespace`, `gatewayVersion`, and the authorized capability names. In addition
to inventory and service workflow names, `cluster.tunnel` requires the complete
Session/RelayTicket policy path, `ports.forward` requires the complete Port
Forward policy path, and exec/file capabilities require `pods/exec`. Clients
may cache this document only for a short period and must isolate it by the
authenticated identity, namespace, and exact Gateway version; it is never a
replacement for request-time authorization.
Inventory responses use stable, minimal documents rather than exposing raw
Kubernetes objects. List endpoints accept bounded `labelSelector` and
`fieldSelector` values. Namespace list results are additionally filtered by
the identity's namespace-scoped Gateway Policy. Watch streams share informers
only for the same identity/namespace/resource and keep a one-snapshot mailbox
per client, so slow clients drop intermediate states instead of blocking the
informer; periodic resync repairs any dropped edge.

Cluster Session creation returns the immutable NetworkSpec and the same
identity/namespace/Gateway-version-bound capability snapshot exposed by the
capability endpoint. The client validates both and uses the latter to prime its
short-lived capability cache; it does not replace per-request authorization.
Sessions are bound to the authenticated identity, device and namespace. The
desktop maintains heartbeats and disconnects the Session before
logout, profile deletion or application shutdown. The default heartbeat TTL is
two minutes and the absolute maximum lifetime is eight hours:

Active Control Plane streams are children of a Session runtime tree. Disconnect
and process shutdown cancel that tree and wait for handlers to close sockets,
reverse listeners and Kubernetes resources before releasing ownership. Exec
and file Task owner heartbeats plus compare-and-swap recovery prevent a hard
restart from leaving permanent `starting`/`running` rows; resource-backed
traffic workflows retain their typed recovery reconcilers.

```yaml
controlPlane:
  sessionTTL: 2m
  sessionMaxLifetime: 8h
  maintenanceInterval: 1m
  maintenanceBatchSize: 100
```

If a desktop exits without disconnecting, its heartbeat stops. After the
Session TTL, the Control Plane's bounded maintenance pass removes the expired
Session and the database cascades removal to its Port Forward and other owned
Tasks. `maintenanceInterval` bounds the additional cleanup delay.

## Runtime security, egress and availability

All three workloads run non-root with a read-only root filesystem,
`RuntimeDefault` seccomp, no privilege escalation and all Linux capabilities
dropped. Data Plane still does not automount a general Kubernetes API token.

Ingress NetworkPolicies are enabled by default. Restricted egress is opt-in
because the chart cannot safely infer the cluster's Kubernetes API address,
authorized workload CIDRs, OIDC endpoints or external PostgreSQL address.
When enabled, Helm requires explicit allow rules for Control Plane, Data Plane and
Operator; an incomplete policy fails rendering instead of producing workloads
that hang at startup:

```yaml
networkPolicy:
  enabled: true
  egress:
    enabled: true
    controlPlane:
      - to:
          - ipBlock: {cidr: 10.96.0.1/32} # Kubernetes API
        ports:
          - {protocol: TCP, port: 443}
      - to:
          - ipBlock: {cidr: 192.0.2.10/32} # identity/database example
        ports:
          - {protocol: TCP, port: 443}
    dataPlane:
      - to:
          - namespaceSelector:
              matchLabels: {kubeloop.io/traffic-enabled: "true"}
            podSelector:
              matchLabels: {kubeloop.io/traffic-target: "true"}
    operator:
      - to:
          - ipBlock: {cidr: 10.96.0.1/32}
        ports:
          - {protocol: TCP, port: 443}
```

The default DNS peer selects `kube-system` Pods labeled `k8s-app=kube-dns` and
can be overridden. Data Plane gets a separate automatic allowance only to the
Control Plane Relay Registry port. Do not include the Kubernetes API, cloud
metadata, Control Plane database or identity-provider CIDRs in its custom rules.
Limit workload peers with namespace/Pod labels or explicit cluster CIDRs;
Gateway authorization and the Session NetworkSpec remain mandatory
application-layer controls. NetworkPolicy enforcement requires a CNI that
implements both ingress and egress policy.

Topology spread is independently configurable through
`controlPlane.topologySpreadConstraints`, `dataPlane.topologySpreadConstraints`
and `operator.topologySpreadConstraints`. An optional Prometheus Operator
`ServiceMonitor` scrapes only the Data Plane's aggregate `/metrics` endpoint:

```yaml
monitoring:
  serviceMonitor:
    enabled: true
    labels: {release: prometheus}
controlPlane:
  topologySpreadConstraints:
    - maxSkew: 1
      topologyKey: topology.kubernetes.io/zone
      whenUnsatisfiable: DoNotSchedule
      labelSelector:
        matchLabels: {app.kubernetes.io/component: control-plane}
```

The Control Plane PodDisruptionBudget is enabled by default only when an external datasource
mode has more than one Control Plane replica. SQLite never renders a PDB, so its
single Pod cannot block voluntary node maintenance. Helm rejects a
`minAvailable` value that is zero or greater than or equal to the Control Plane
replica count.

### Health and metrics contract

Each workload has separate liveness and readiness probes:

| Workload | Liveness | Readiness | Readiness dependencies |
| --- | --- | --- | --- |
| Control Plane | `/health/live` | `/health/ready` | State database, configured identity providers, and a bounded Kubernetes `/version` request |
| Data Plane | `/health/live` | `/health/ready` | Local tunnel runtime plus an acknowledged, unexpired Relay Registry lease when Registry mode is enabled |
| Operator | `/healthz` | `/readyz` | controller-runtime manager health and readiness |

Liveness reports process health only, so a temporary database, identity,
Kubernetes API, or Relay Registry outage does not trigger a restart loop.
Readiness failures return only `unavailable` (or `draining` during deliberate
shutdown) and do not expose dependency errors. Checks are bounded and never
perform a full Kubernetes inventory scan.

The Data Plane `/metrics` endpoint exposes only unlabeled aggregate gauges for
readiness, drain state, logical tunnel connections, and physical WebSocket
sessions. It must not expose tokens, email addresses, identities, session IDs,
target addresses, or other user-controlled/high-cardinality labels. The
optional `ServiceMonitor` targets only this endpoint.

### CRD lifecycle

`TrafficBinding` is packaged in the chart's `crds/` directory. Helm installs it
before namespaced resources, but intentionally does not upgrade or delete CRDs.
Before upgrading to a chart version whose CRD schema changed, apply the new
definition first:

```shell
helm show crds ./charts/kubeloop | kubectl apply --server-side -f -
helm upgrade kubeloop ./charts/kubeloop --namespace kubeloop-system ...
```

`helm uninstall` removes the Control Plane, Data Plane, Operator, RBAC, Services,
policies and SQLite PVC, while the CRD remains. Delete it explicitly only after
all KubeLoop releases and `TrafficBinding` resources have been removed.

### Helm lifecycle E2E

Contributors can run the same isolated lifecycle suite used by CI:

```shell
make helm-test-e2e
make helm-cleanup-test-e2e
```

The suite creates a dedicated Minikube profile, builds all three images, installs
SQLite and TLS-enabled external PostgreSQL releases, and exercises component-
isolated upgrades/scales, rollback, data retention, Pod failure recovery, CRD
retention and uninstall cleanup. `e2e/helm/run.sh` refuses to run unless its
explicit opt-in flag and expected dedicated context both match.

## Install

The public URL is the only address desktop clients will need. It is the HTTP or HTTPS origin that routes discovery/API requests to the Control Plane and `/tunnel` to the Data Plane.

```shell
helm upgrade --install kubeloop \
  oci://ghcr.io/fengqi-dev/kube-loop/charts/kubeloop \
  --version 2.1.2 \
  --namespace kubeloop-system \
  --create-namespace \
  --set publicURL=http://kubeloop.example.com \
  --set ingress.enabled=true \
  --set ingress.host=kubeloop.example.com \
  --set ingress.className=nginx
```

Verify discovery after the workloads are ready:

```shell
curl http://kubeloop.example.com/.well-known/kubeloop
```

## Storage modes

SQLite is the default. The chart enforces one Control Plane replica, `Recreate` updates and a persistent volume:

```yaml
controlPlane:
  storage:
    sqlite:
      persistence:
        enabled: true
        size: 1Gi
```

External PostgreSQL and MySQL databases are selected by the datasource URL scheme. The URL is read from a Secret and external storage allows RollingUpdate deployments:

```yaml
controlPlane:
  replicas: 1
  storage:
    datasource:
      existingSecret: kubeloop-datasource
      urlKey: datasource-url
    connectTimeout: 10s
    queryTimeout: 5s
    maxOpenConnections: 20
    maxIdleConnections: 5
    connectionMaxLifetime: 30m
    transactionMaxRetries: 3
    transactionRetryBackoff: 25ms
```

Create the referenced Secret without putting the DSN in Helm values:

```shell
kubectl -n kubeloop-system create secret generic kubeloop-datasource \
  --from-literal=datasource-url='postgresql://user:password@postgres.example:5432/kubeloop?sslmode=require'
```

Use `mysql://user:password@mysql.example:3306/kubeloop?tls=true` for MySQL. The Control Plane detects the dialect from the URL and initializes the complete current schema in an empty database under an advisory lock. Existing databases without the current schema identity are rejected and must be recreated; no schema migration path is provided. Database availability is reported through readiness. External-database transactions use serializable isolation and bounded deadlock/serialization retries.
