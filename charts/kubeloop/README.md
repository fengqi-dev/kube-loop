# KubeLoop Helm Chart

This chart installs the V2 server as three independent workloads:

- `kubeloop-controller`: discovery, authentication/API boundary, durable Task ownership and creation/deletion of `TrafficBinding` intents.
- `kubeloop-gateway`: WebSocket tunnel data plane. It has no database configuration and does not automount the general Kubernetes API credential. Registry mode projects only a short-lived, audience-bound Pod token.
- `kubeloop-operator`: watches `TrafficBinding` and exclusively coordinates Preview/Exchange/Mirror Service, Endpoints and EndpointSlice mutations with finalizer-based restoration.

The current V2 implementation slice exposes discovery, OIDC/AD login, token lifecycle, default-deny Gateway Policy, API audit, storage repositories, Kubernetes capability probes, read-only Namespace/Pod/Service inventory, owned Cluster Session lifecycles, short-lived RelayTicket-authenticated WebSocket transport, Session-bound Port Forward Tasks, Controller-owned Pod exec streams, resumable Controller-owned file-transfer Task streams, guarded remote directory management with desktop local-file integration, and TrafficBinding reconciliation through the Operator. The remaining security, observability and release milestones in the V2 roadmap still apply; do not treat a development image tag as a production release.

Both workloads expose an independent `logLevel` value (`debug`, `info`, `warn`,
or `error`). The binaries validate the value again at startup. Controller and
Data Plane write structured JSON logs to stdout; changing one workload does not
change the verbosity of the other.

```yaml
controller:
  logLevel: info
dataPlane:
  logLevel: info
```

## One public HTTPS origin

`publicURL` is the exact origin entered by desktop clients. It must be HTTPS,
must not contain a path, query or fragment, and must match the route hostname.
The chart routes the same origin without rewriting paths:

| Public path | Backend |
| --- | --- |
| `/.well-known/*` | Controller |
| `/auth/*` | Controller |
| `/api/*` | Controller |
| `/tunnel` | Data Plane WebSocket endpoint |

The Controller publishes that exact value in discovery and derives every OIDC
callback as `<publicURL>/auth/callback/<providerID>`. Helm fails rendering when
a chart-managed route uses another hostname, when TLS is disabled, or when both
Ingress and HTTPRoute are enabled.

For a Kubernetes Ingress, configure the timeout and request-size annotations
required by the selected Ingress controller. For example, ingress-nginx can be
configured as follows; `proxy-body-size` should not exceed the Controller's
`maxRequestBodyBytes` unless the backend is intentionally the tighter limit:

```yaml
publicURL: https://kubeloop.example.com
ingress:
  enabled: true
  className: nginx
  host: kubeloop.example.com
  annotations:
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-send-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-body-size: "1m"
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
the Controller body limit, WSS upgrade and traffic after the proxy's ordinary
HTTP write timeout.

## Relay Registry and RelayTicket keys

RelayTicket authentication is asymmetric. Controller alone receives an
Ed25519 PKCS#8 private key. A ready Data Plane registers with the Controller's
internal HTTPS Service, receives the current public-key set and revocation
summary, then acknowledges the applied generations by heartbeat. Controller
derives the Relay ID from authenticated namespace, ServiceAccount and Pod UID;
the registration body cannot choose it.

Generate the RelayTicket signing key and a CA/server certificate for the
internal Registry Service. The certificate SAN must cover its exact DNS name.
For the installation example below that name is
`kubeloop-kubeloop-controller-relay.kubeloop-system.svc`:

```shell
openssl genpkey -algorithm ED25519 -out relay-signing-key.pem

openssl req -x509 -newkey rsa:3072 -nodes -days 3650 \
  -subj '/CN=KubeLoop Relay Registry CA' \
  -keyout relay-registry-ca.key -out relay-registry-ca.crt
openssl req -newkey rsa:3072 -nodes \
  -subj '/CN=kubeloop-kubeloop-controller-relay.kubeloop-system.svc' \
  -addext 'subjectAltName=DNS:kubeloop-kubeloop-controller-relay.kubeloop-system.svc' \
  -keyout relay-registry.key -out relay-registry.csr
openssl x509 -req -days 365 -sha256 \
  -in relay-registry.csr -CA relay-registry-ca.crt -CAkey relay-registry-ca.key -CAcreateserial \
  -copy_extensions copy -out relay-registry.crt

kubectl -n kubeloop-system create secret generic kubeloop-relay-controller \
  --from-file=signing-key.pem=relay-signing-key.pem \
  --from-file=tls.crt=relay-registry.crt \
  --from-file=tls.key=relay-registry.key \
  --from-file=ca.crt=relay-registry-ca.crt
```

The scalable default authentication is `tokenreview`. Data Plane keeps
`automountServiceAccountToken: false`; Helm explicitly projects a ten-minute
token whose only audience is `kubeloop-relay`. Controller verifies its Pod UID
and ServiceAccount against Kubernetes before trusting topology or capacity.
An mTLS/SPIFFE mode is also available, but each replica must receive a distinct
Pod-bound client certificate.

For one Data Plane replica, the advertised endpoint defaults to
`publicURL + tunnelPath`. With multiple replicas, configure a routable endpoint
template containing `{podName}` or `{podUID}` and matching wildcard routing.
The chart rejects a shared endpoint because it cannot guarantee a
Relay-ID-bound Ticket reaches the selected Pod.

The current Registry is process-local and the chart therefore requires one
Controller replica while it is enabled. This is independent of Data Plane
replica count. Controller HA will require the later shared Registry/storage
conformance milestone; the chart fails fast instead of creating split leases.

```yaml
controller:
  relay:
    existingSecret: kubeloop-relay-controller
    signingKeyKey: signing-key.pem
    keyID: primary
    ticketTTL: 1m
  relayRegistry:
    enabled: true
    authentication: tokenreview
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
publishes an overlapping generation; the Controller allocates only to relays
that have acknowledged it. Keep the previous public key through the two-minute
maximum Ticket window.

After the HTTP upgrade, the client must send a binary WSS v2 `ClientHello`
before any smux bytes. The Data Plane verifies protocol/client version and the
device ID bound into the one-time RelayTicket, then returns `ServerHello` with
the exact frame, logical-stream, physical-connection, per-user and idle limits.
Rejected negotiation returns a stable code such as `VERSION_MISMATCH` and does
not create a partially usable smux session. `controller.minClientVersion`
applies consistently to discovery and this WSS handshake.

During rollout, Data Plane sends an immediate draining heartbeat, becomes
unready, rejects new WSS/logical connections, and lets existing streams finish
for `dataPlane.drainTimeout`. Controller stops new assignment to that lease.
Remaining streams are explicitly closed; clients obtain a fresh assignment and
generation-bound RelayTicket. Active streams are not described as migrated.

## OIDC Provider

Create the confidential client secret separately, then reference it from values. The Secret is projected only into the Controller; issuer/client metadata is written to a separate ConfigMap and the Data Plane receives neither.

```yaml
controller:
  auth:
    token:
      existingSecret: kubeloop-token-signing
      signingKeyKey: signing-key.pem
      keyID: primary
    providers:
      - id: corporate
        type: oidc
        displayName: Corporate SSO
        oidc:
          issuer: https://login.example.com
          clientID: kubeloop
          existingSecret: kubeloop-oidc
          clientSecretKey: client-secret
          # caKey: ca.crt  # optional private CA in the same Secret
          scopes: [openid, profile, email, groups]
          allowedSigningAlgs: [RS256]
          requiredClaims: [sub]
          claims:
            displayName: name
            email: email
            groups: groups
```

The token signing Secret value must be an Ed25519 private key in unencrypted PKCS#8 PEM form (for example, generated offline with `openssl genpkey -algorithm ED25519`). Keep the corresponding Secret under normal Kubernetes/External Secrets rotation controls.

Register the exact callback `https://<public-origin>/auth/callback/corporate` at the identity provider. Controller startup fails closed if discovery does not match the configured issuer, endpoints are not HTTPS, PKCE S256 is not advertised, or no configured signing algorithm is supported.

## Active Directory / LDAPS Provider

Use this mode only when the directory is not already exposed through Entra ID, AD FS, Keycloak, Dex or another OIDC Broker. The default and recommended transport is LDAPS:

```yaml
controller:
  auth:
    token:
      existingSecret: kubeloop-token-signing
    providers:
      - id: legacy-ad
        type: ad
        displayName: Corporate Active Directory
        ad:
          directoryID: corp.example
          url: ldaps://dc.corp.example:636
          baseDN: DC=corp,DC=example
          userFilter: "(&(objectClass=user)(sAMAccountName={username}))"
          bindDN: CN=kubeloop-reader,OU=Service Accounts,DC=corp,DC=example
          existingSecret: kubeloop-ad
          bindPasswordKey: bind-password
          caKey: ca.crt
          objectIDAttribute: objectGUID
          nestedGroupDepth: 0
          maxGroups: 200
```

`ldap://` is rejected unless `startTLS: true` is explicitly set. TLS always verifies the server certificate and hostname. If `bindDN` is omitted, directory search must permit anonymous read of the configured user attributes. User passwords are used only for a fresh per-login bind and are never added to the ConfigMap, database, Token, audit metadata, logs or metrics.

## Development authentication (unsafe for production)

`static-token` and `anonymous` are disabled unless `controller.auth.developmentMode=true`. Both are bootstrap login methods only: after login, Controller issues the same short-lived, refreshable Gateway Token Family used by OIDC and AD, so authorization, audit, Session and WSS enforcement are unchanged.

For a controlled development environment, create a random token of at least 32 characters and keep it in a Secret:

```bash
kubectl -n kubeloop-system create secret generic kubeloop-development-auth \
  --from-literal=token="$(openssl rand -base64 32)"
```

```yaml
controller:
  auth:
    developmentMode: true
    token:
      existingSecret: kubeloop-token-signing
    providers:
      - id: local
        type: static-token
        displayName: Development Token
        staticToken:
          existingSecret: kubeloop-development-auth
          tokenKey: token
          subject: local-developer
          groups: [developers]
```

The static token is projected only into Controller, hashed in memory, never written to the auth ConfigMap, and cleared from client input after the request. Discovery advertises it explicitly as `type: static-token`, `interaction: token`.

Anonymous mode requires the same explicit development gate and an `anonymous` Provider. It asks for no credential and therefore must never be enabled on a production or untrusted network. Controller emits a high-visibility `SECURITY WARNING` at every startup while it is enabled; discovery advertises `type: anonymous`, `interaction: none`.

## Gateway Policy

Authentication does not grant API access by itself. The default policy has no
rules and denies every `/api/v2` operation. Add allow rules explicitly:

```yaml
controller:
  policy:
    rules:
      - id: developers-discovery
        groups: [developers]
        namespaces: [$cluster]
        operations: [get, list]
        resourceKinds: [version, namespaces]
      - id: developers-inventory
        groups: [developers]
        namespaces: [development]
        operations: [get, list, watch]
        resourceKinds: [capabilities, pods, services]
      - id: developers-session
        groups: [developers]
        namespaces: [development]
        operations: [create, get, heartbeat, delete]
        resourceKinds: [sessions]
      - id: developers-relay
        groups: [developers]
        namespaces: [development]
        operations: [create]
        resourceKinds: [relay-tickets]
      - id: developers-port-forward
        groups: [developers]
        namespaces: [development]
        operations: [create, list, delete]
        resourceKinds: [port-forwards]
      - id: developers-pod-exec
        groups: [developers]
        namespaces: [development]
        operations: [create, stream]
        resourceKinds: [pod-exec]
      - id: developers-files
        groups: [developers]
        namespaces: [development]
        operations: [create, get, stream]
        resourceKinds: [file-transfers]
      - id: developers-file-management
        groups: [developers]
        namespaces: [development]
        operations: [list, create, update, delete, get]
        resourceKinds: [pod-files]
      - id: developers-service-streams
        groups: [developers]
        namespaces: [development]
        operations: [create, get, delete, stream]
        resourceKinds: [exchanges, mirrors, previews]
```

Every configured selector is required to match. Use `$cluster` for a
cluster-scoped request, and use `*` only for an intentional wildcard grant.
Policy is mounted only into the Controller. Invalid policy prevents Controller
startup; a runtime update is compiled completely before it atomically replaces
the prior policy.

## Management bootstrap and break-glass

The Controller Management Plane has a separate deny-by-default role engine;
ordinary Gateway Policy access never grants an `admin.*` operation. Initial
deployment may identify exact stable OIDC/AD subjects or normalized groups:

```yaml
controller:
  management:
    bootstrap:
      subjects: ["00000000-0000-4000-8000-000000000001"]
      groups: ["platform-bootstrap"]
      recoveryEnabled: false
```

Subjects are Controller Principal UUIDs; wildcards, `$cluster`, upstream email
addresses, and display names are not valid bootstrap selectors. A normalized
group is normally the practical first-install selector because a new Principal
UUID is assigned at its first successful OIDC/AD login. After a formal
`platform-admin` assignment revision is
published, the persistent retirement marker prevents old values or a rollback
from restoring bootstrap access. Disaster recovery requires an explicit Helm
change to `recoveryEnabled: true` and a Controller restart, and still requires
one of the configured exact identities.

Break-glass is disabled by default. When enabled, the selected stable alias
must map to an existing Secret; the credential itself never enters values,
ConfigMaps, the database, logs, Data Plane, or Operator:

```yaml
controller:
  management:
    breakGlass:
      enabled: true
      secretAlias: emergency
      sessionTTL: 10m
      allowedSourceCIDRs: ["10.0.0.0/8"]
      secretAliases:
        emergency:
          existingSecret: kubeloop-break-glass
          credentialKey: credential
```

The Secret value must be an unpadded base64url encoding of 32–64 random bytes.
Controller validates it at startup and readiness, projects it read-only under
`/var/run/secrets/kubeloop/management`, compares credentials in constant time,
and derives a SHA-256 generation so Secret rotation invalidates prior emergency
sessions. Emergency sessions are non-refreshable and may never become ordinary
Gateway Token Families or RelayTickets.

## Kubernetes access

The Controller and Operator use separate in-cluster ServiceAccounts; the
desktop client never loads kubeconfig or talks directly to the Kubernetes API.
Controller RBAC is split by workflow so an installation can disable
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
EndpointSlices. It receives no database, OIDC/AD, RelayTicket, exec/file or
Secret permissions. Controller no longer writes those traffic resources
directly; CR deletion waits until the Operator finalizer completes restoration.

The default `cluster` scope applies the enabled Inventory, exec/file and
traffic groups to every namespace. Each group has a distinct ClusterRole and
ClusterRoleBinding:

```yaml
controller:
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
controller:
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
`automountServiceAccountToken: false`. Set `controller.rbac.create=false` only
when equivalent Controller RBAC is managed externally.

Optional impersonation is a defense-in-depth layer. Identity groups are not
forwarded automatically: each Kubernetes group must be explicitly mapped.
The chart still does not grant the Controller permission to impersonate; the
operator must supply narrowly scoped RBAC separately and verify API Server audit
events before enabling this mode.

The repository's Minikube Helm lifecycle E2E enables API Server metadata auditing,
grants a temporary exact-user/exact-group impersonation role outside this
chart, calls `GET /api/v2/version`, and asserts that the audit event contains
both the Controller ServiceAccount and the final prefixed Principal identity.
It also proves that an unmapped identity group is not forwarded.

```yaml
controller:
  kubernetes:
    impersonation:
      enabled: true
      usernamePrefix: "kubeloop:"
      groupMappings:
        engineering: [k8s:developers]
```

Authenticated, policy-authorized clients can use:

- `GET /api/v2/version`
- `GET /api/v2/capabilities?namespace=<name>`
- `GET /api/v2/namespaces[?limit=...&continue=...]`
- `GET /api/v2/namespaces/<namespace>`
- `GET /api/v2/namespaces/<namespace>/pods[/<name>]`
- `GET /api/v2/namespaces/<namespace>/services[/<name>]`
- `GET /api/v2/namespaces/<namespace>/{pods,services}?watch=true` as an
  authenticated WebSocket of versioned full snapshots
- `POST /api/v2/sessions?namespace=<name>` with `Idempotency-Key`
- `GET /api/v2/sessions/<id>?namespace=<name>`
- `POST /api/v2/sessions/<id>/heartbeat?namespace=<name>` with `If-Match`
- `DELETE /api/v2/sessions/<id>?namespace=<name>` with `If-Match`
- `POST /api/v2/sessions/<id>/tickets?namespace=<name>` with JSON body
  `{"networkSpecHash":"<optional lowercase sha256>"}`
- `POST /api/v2/sessions/<id>/port-forwards?namespace=<name>` with
  `Idempotency-Key` and a Pod/Service target document
- `GET /api/v2/sessions/<id>/port-forwards?namespace=<name>`
- `DELETE /api/v2/sessions/<id>/port-forwards/<task-id>?namespace=<name>`
- `POST /api/v2/sessions/<id>/exec?namespace=<name>` with `Idempotency-Key`
- `GET /api/v2/sessions/<id>/exec/<task-id>/stream?namespace=<name>` as an
  authenticated binary WebSocket stream
- `POST /api/v2/sessions/<id>/file-transfers?namespace=<name>` with
  `Idempotency-Key`, a Pod/container target, an absolute container path, and
  upload metadata when applicable. File uploads may include a stable UUID
  `resumeId`; the Controller probes the Pod partial and returns the
  authoritative `offset` in the Task document.
- `GET /api/v2/sessions/<id>/file-transfers/<task-id>?namespace=<name>`
- `GET /api/v2/sessions/<id>/file-transfers/<task-id>/stream?namespace=<name>`
  as an authenticated binary WebSocket upload/download stream
- `POST /api/v2/sessions/<id>/pod-files/list?namespace=<name>` with a
  Pod/container and absolute directory path
- `POST /api/v2/sessions/<id>/pod-files/{create,rename,delete}?namespace=<name>`
  with `Idempotency-Key`; mutations return a terminal `pod-file-operation` Task
- `GET /api/v2/sessions/<id>/pod-files/operations/<task-id>?namespace=<name>`
- `POST /api/v2/sessions/<id>/{exchanges,mirrors,previews}?namespace=<name>`
  with `Idempotency-Key`; Exchange and Mirror select an existing Service while
  Preview declares a new temporary Service name and one or more ports
- `GET /api/v2/sessions/<id>/{exchanges,mirrors,previews}/<task-id>?namespace=<name>`
- `DELETE /api/v2/sessions/<id>/{exchanges,mirrors,previews}/<task-id>?namespace=<name>`
- `GET /api/v2/sessions/<id>/{exchanges,mirrors,previews}/<task-id>/stream?namespace=<name>`
  as an authenticated reverse WebSocket stream. Preview resources carry the
  Task UUID as exact owner metadata; name conflicts are never overwritten and
  cleanup only deletes objects whose owner metadata still matches.

Capability results are the intersection of Gateway Policy and Kubernetes RBAC.
`/api/v2/version` returns both `gitVersion` (Kubernetes) and `gatewayVersion`.
The versioned capability document includes `schemaVersion`, `principalId`,
`namespace`, `gatewayVersion`, and the authorized capability names. In addition
to inventory and service workflow names, `cluster.tunnel` requires the complete
Session/RelayTicket policy path, `ports.forward` requires the complete Port
Forward policy path, and exec/file capabilities require `pods/exec`. Clients
may cache this document only for a short period and must isolate it by the
authenticated principal, namespace, and exact Gateway version; it is never a
replacement for request-time authorization.
Inventory responses use stable, minimal V2 documents rather than exposing raw
Kubernetes objects. List endpoints accept bounded `labelSelector` and
`fieldSelector` values. Namespace list results are additionally filtered by
the principal's namespace-scoped Gateway Policy. Watch streams share informers
only for the same identity/namespace/resource and keep a one-snapshot mailbox
per client, so slow clients drop intermediate states instead of blocking the
informer; periodic resync repairs any dropped edge.

Cluster Session creation returns the immutable NetworkSpec and the same
principal/namespace/Gateway-version-bound capability snapshot exposed by the
capability endpoint. The client validates both and uses the latter to prime its
short-lived capability cache; it does not replace per-request authorization.
Sessions are bound to the authenticated principal, device and namespace. The
desktop maintains heartbeats and disconnects the Session before
logout, profile deletion or application shutdown. The default heartbeat TTL is
two minutes and the absolute maximum lifetime is eight hours:

Active Controller streams are children of a Session runtime tree. Disconnect
and process shutdown cancel that tree and wait for handlers to close sockets,
reverse listeners and Kubernetes resources before releasing ownership. Exec
and file Task owner heartbeats plus compare-and-swap recovery prevent a hard
restart from leaving permanent `starting`/`running` rows; resource-backed
traffic workflows retain their typed recovery reconcilers.

```yaml
controller:
  sessionTTL: 2m
  sessionMaxLifetime: 8h
  maintenanceInterval: 1m
  maintenanceBatchSize: 100
```

If a desktop exits without disconnecting, its heartbeat stops. After the
Session TTL, the Controller's bounded maintenance pass removes the expired
Session and the database cascades removal to its Port Forward and other owned
Tasks. `maintenanceInterval` bounds the additional cleanup delay.

## Runtime security, egress and availability

All three workloads run non-root with a read-only root filesystem,
`RuntimeDefault` seccomp, no privilege escalation and all Linux capabilities
dropped. Data Plane still does not automount a general Kubernetes API token.

Ingress NetworkPolicies are enabled by default. Restricted egress is opt-in
because the chart cannot safely infer the cluster's Kubernetes API address,
authorized workload CIDRs, OIDC/AD endpoints or external PostgreSQL address.
When enabled, Helm requires explicit allow rules for Controller, Data Plane and
Operator; an incomplete policy fails rendering instead of producing workloads
that hang at startup:

```yaml
networkPolicy:
  enabled: true
  egress:
    enabled: true
    controller:
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
Controller Relay Registry port. Do not include the Kubernetes API, cloud
metadata, Controller database or identity-provider CIDRs in its custom rules.
Limit workload peers with namespace/Pod labels or explicit cluster CIDRs;
Gateway authorization and the Session NetworkSpec remain mandatory
application-layer controls. NetworkPolicy enforcement requires a CNI that
implements both ingress and egress policy.

Topology spread is independently configurable through
`controller.topologySpreadConstraints`, `dataPlane.topologySpreadConstraints`
and `operator.topologySpreadConstraints`. An optional Prometheus Operator
`ServiceMonitor` scrapes only the Data Plane's aggregate `/metrics` endpoint:

```yaml
monitoring:
  serviceMonitor:
    enabled: true
    labels: {release: prometheus}
controller:
  topologySpreadConstraints:
    - maxSkew: 1
      topologyKey: topology.kubernetes.io/zone
      whenUnsatisfiable: DoNotSchedule
      labelSelector:
        matchLabels: {app.kubernetes.io/component: controller}
```

The Controller PodDisruptionBudget is enabled by default only when PostgreSQL
mode has more than one Controller replica. SQLite never renders a PDB, so its
single Pod cannot block voluntary node maintenance. Helm rejects a
`minAvailable` value that is zero or greater than or equal to the Controller
replica count.

### Health and metrics contract

Each workload has separate liveness and readiness probes:

| Workload | Liveness | Readiness | Readiness dependencies |
| --- | --- | --- | --- |
| Controller | `/health/live` | `/health/ready` | State database, configured identity providers, and a bounded Kubernetes `/version` request |
| Data Plane | `/health/live` | `/health/ready` | Local tunnel runtime plus an acknowledged, unexpired Relay Registry lease when Registry mode is enabled |
| Operator | `/healthz` | `/readyz` | controller-runtime manager health and readiness |

Liveness reports process health only, so a temporary database, identity,
Kubernetes API, or Relay Registry outage does not trigger a restart loop.
Readiness failures return only `unavailable` (or `draining` during deliberate
shutdown) and do not expose dependency errors. Checks are bounded and never
perform a full Kubernetes inventory scan.

The Data Plane `/metrics` endpoint exposes only unlabeled aggregate gauges for
readiness, drain state, logical tunnel connections, and physical WebSocket
sessions. It must not expose tokens, email addresses, principals, session IDs,
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

`helm uninstall` removes the Controller, Data Plane, Operator, RBAC, Services,
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

The public URL is the only address desktop clients will need. It must be the HTTPS origin that routes discovery/API requests to the Controller and `/tunnel` to the Data Plane.

```shell
helm upgrade --install kubeloop ./charts/kubeloop \
  --namespace kubeloop-system \
  --create-namespace \
  --set publicURL=https://kubeloop.example.com \
  --set controller.relay.existingSecret=kubeloop-relay-controller \
  --set ingress.enabled=true \
  --set ingress.host=kubeloop.example.com \
  --set ingress.className=nginx \
  --set ingress.tls.secretName=kubeloop-tls
```

Verify discovery after the workloads are ready:

```shell
curl https://kubeloop.example.com/.well-known/kubeloop
```

## Storage modes

SQLite is the default. The chart enforces one Controller replica, `Recreate` updates and a persistent volume:

```yaml
controller:
  storage:
    type: sqlite
    sqlite:
      persistence:
        enabled: true
        size: 1Gi
```

The PostgreSQL deployment contract accepts an external DSN from a Secret and allows Controller replicas to scale independently:

```yaml
controller:
  replicas: 3
  storage:
    type: postgresql
    postgresql:
      existingSecret: kubeloop-postgresql
      dsnKey: dsn
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
kubectl -n kubeloop-system create secret generic kubeloop-postgresql \
  --from-literal=dsn='postgres://user:password@postgres.example:5432/kubeloop?sslmode=require'
```

The Controller opens the selected backend, applies a server-side statement timeout, runs advisory-lock migrations before becoming ready and reports database availability through readiness. PostgreSQL transactions use serializable isolation and retry bounded serialization/deadlock failures. Principal, refresh-token family, Session, Task, resource snapshot, idempotency, authentication transaction and append-only audit repositories share the same storage contract.
