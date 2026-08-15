# ADR 0015: V2 MCP trust boundary

## Status

Accepted for V2.

## Context

The V1 desktop MCP backend directly composed kubeconfig inventory, the local
Kubernetes provider, the V1 Session manager, Pod SSH exec, file management,
network overrides, and privileged Helper installation. That made an MCP client
an alternate local control plane: it could bypass the V2 Server Profile and
OAuth lifecycle, mutate host networking, and exercise whatever kubeconfig
identity happened to be present on disk.

V2 has a different trust boundary. Kubernetes credentials and operations live
behind Control Plane/Gateway, while the desktop keeps only Server Profiles,
OAuth/OIDC credentials, authenticated remote Sessions, and local data-plane
endpoints. MCP must be another caller of that same client boundary, not a
compatibility path back to V1.

## Decision

### One production backend

`mcp.RemoteBackend` is the only production MCP backend. It depends on the
typed `clientv2` Gateway client and the V2 Session, Data Plane, traffic, exec,
and file-transfer managers. The package does not import kubeconfig,
`internal/cluster`, the V1 `internal/session` or `internal/store`, a Kubernetes
client, or the privileged Helper.

The desktop constructs the backend after its V2 clients and managers, starts
the embedded MCP listener with the Wails application, and stops the listener
before shutting down active remote Tasks. An architecture test rejects any
future direct V1 or Kubernetes import from `internal/mcp` and continues to
reject Kubernetes packages from the desktop dependency graph.

### Active identity is an upper bound

Every tool requires an explicit `profileId`. Before any SDK or manager call,
the backend snapshots the Server Profile Store and requires that ID to equal
`ActiveProfileID`. A request for any other saved Profile returns `forbidden`
without reaching Gateway.

Gateway requests use the OAuth/OIDC-derived access and refresh tokens
already stored for that Profile. Token refresh follows the normal typed SDK
path. Gateway policy, namespace authorization, Kubernetes SSAR, Session
ownership, Task ownership, and token-family revocation therefore apply exactly
as they do to the desktop UI. MCP has no independent Kubernetes identity and
cannot obtain permissions beyond the signed-in identity.

### Explicit mutation identity

Read operations require `profileId`; namespace-scoped reads additionally
require `namespace`. Every mutation after Session creation requires the exact
triple returned by the active Session:

1. `profileId`;
2. `sessionId`;
3. `namespace`.

The backend compares all three to the live V2 Session before executing work.
Task stops and transfer cancellation also require an explicit `taskId` and
verify that the local Task belongs to that same Session and namespace.
Traffic starts require all remote targets and local endpoints. File transfers
require explicit local and remote paths, direction, kind, Pod/container, and
overwrite choice. Pod exec accepts an argv array, never an implicit
`/bin/sh -c` string, and has a bounded 1-300 second timeout and 1 MiB cap for
each output stream.

### Reduced tool surface

V2 exposes five tools:

| Tool | V2 authority |
| --- | --- |
| `manage_cluster` | Typed authenticated Gateway reads only. |
| `manage_connection` | Current V2 Session connect/status/disconnect. |
| `manage_traffic` | V2 Exchange, Mirror, Preview, and Port Forward managers. |
| `exec_pod_command` | Authenticated V2 Pod exec Task and WebSocket stream. |
| `manage_file_transfer` | V2 streaming transfer manager. |

`manage_helper`, `manage_network`, and `get_singbox_dns_config` are removed.
They mutate privileged host state or expose a local runtime configuration and
are not Gateway-authorized Kubernetes operations.

### Stable errors

Tool failures are emitted as a JSON object in MCP error text with the stable
codes `invalid_argument`, `unauthenticated`, `forbidden`, `not_found`,
`conflict`, `unavailable`, or `internal`. Gateway `requestId` and safe field
metadata are retained. Raw access/refresh tokens, MCP tokens, command output,
local paths from unrelated Tasks, and internal error chains are not included.

This is a tool error rather than a JSON-RPC protocol error, allowing an MCP
client to inspect the code, correct explicit parameters, and retry. The typed
Gateway SDK remains responsible for its bounded authentication refresh/retry;
MCP does not add an independent retry loop that could duplicate writes.

### Local MCP listener authentication

The listener remains loopback-only and validates Host and Origin using parsed,
exact `localhost`, `127.0.0.1`, or `::1` hostnames. Prefixes such as
`localhost.evil.example` are rejected. Bearer authentication is enabled by
default with a random 256-bit token.

`mcp.json` stores only version, enabled state, port, and token-auth switch
with mode 0600. The bearer token is stored separately in the operating-system
credential vault and is never serialized into the Server Profile or MCP
settings file.

## Consequences

- MCP and the desktop UI exercise the same typed Gateway and authorization
  paths.
- Switching the active Profile immediately prevents new MCP calls from using
  the previous Profile, even if credentials for it remain in the keychain.
- Agents must first read/connect a Session and carry its explicit identity into
  mutations, which makes prompts longer but prevents ambient-context writes.
- V1-only host mutation tools are intentionally not feature-compatible in V2.
- Local traffic endpoints and local file IO still run in the desktop, but their
  Kubernetes side is created and authorized only through Gateway.
- Existing MCP client configuration remains installable at the same loopback
  Streamable HTTP endpoint; the advertised tool schemas are V2-only.

## Rejected alternatives

- **Keep the V1 backend as a fallback:** this would reintroduce kubeconfig and
  Kubernetes clients into the desktop dependency graph and create two
  authorization models.
- **Allow any saved Profile ID:** possession of a local MCP token would become
  an ambient selector across accounts and servers instead of being bounded by
  the user-visible active login.
- **Infer Profile, Session, namespace, target, or local path:** an agent could
  mutate a stale or surprising context. Explicit identity is part of the
  mutation contract.
- **Retain Helper and network override tools behind confirmation text:** text is
  not an enforceable Gateway permission and cannot constrain an arbitrary MCP
  client.
- **Store the MCP bearer token in `servers.json` or `mcp.json`:** both are
  ordinary files and would violate the V2 rule that plaintext tokens stay out
  of profile/settings stores.
