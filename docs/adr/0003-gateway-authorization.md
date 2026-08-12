# ADR 0003: Gateway authorization policy

- Status: Accepted
- Date: 2026-08-10

## Context

V2 moves every Kubernetes operation into the Gateway. Authentication alone is
not sufficient: a valid user must not be able to infer or operate on resources
outside the namespaces and workflows assigned by an administrator. HTTP APIs,
server-side tasks, and data-plane stream creation also need the same decision
model so a feature cannot accidentally invent a weaker permission check.

## Decision

KubeLoop uses an allow-only Gateway Policy with implicit deny. A rule matches
all configured dimensions:

- stable principal ID and/or normalized identity group;
- namespace (`$cluster` for cluster-scoped requests, `*` only when explicitly
  granting all namespaces);
- operation, such as `get`, `list`, `watch`, `create`, `update`, `patch`, or
  `delete`;
- resource kind, such as `namespaces`, `pods`, or `services`.

If both `subjects` and `groups` are present, both selectors must match. Empty
subject/group selectors, namespace selectors, operation selectors, or resource
kind selectors are rejected where they could accidentally broaden a rule. The
only broad grant is an explicit `*`.

Example:

```json
{
  "version": 1,
  "rules": [
    {
      "id": "developers-read",
      "groups": ["developers"],
      "namespaces": ["development"],
      "operations": ["get", "list", "watch"],
      "resourceKinds": ["pods", "services"]
    }
  ]
}
```

Policy input is strictly decoded and size bounded. An update is fully compiled
and validated before one atomic pointer swap; a failed update leaves the prior
policy active. An absent or empty policy is deny-all.

The authenticated `/kubeloop/api` framework authorizes the normalized operation and
resource scope before invoking its business handler. A denial always returns
the same `FORBIDDEN` envelope and occurs before any resource lookup, so it does
not disclose whether a resource exists. The authorization request and matched
rule ID are attached to request context as an authorization proof for task
creation, stream dispatch, and structured audit. `APIRouter` refuses to invoke
any exact route, feature prefix, or fallback handler without that allowed proof;
only the top-level framework can create it after calling the configured
`authorization.Authorizer`. This prevents a Task or WebSocket feature from
bypassing Gateway Policy by dispatching the Router directly.

RelayTicket creation is the `relay-tickets/create` resource. The Control Plane
issues the `tunnel` operation only after that request is allowed. The Data Plane
does not own a second policy implementation: it verifies the signed Ticket,
requires the `tunnel` operation, and binds every opened protocol stream to the
Ticket Session and NetworkSpec.

Kubernetes impersonation is a separate, optional enforcement layer. It cannot
expand permissions granted by Gateway Policy, and Gateway Policy cannot expand
the ServiceAccount or impersonated user's Kubernetes RBAC permissions.

## Consequences

- Administrators must grant every operation explicitly; initial deployments
  with no rules can authenticate but cannot call protected APIs.
- New HTTP resources, tasks, and stream types must define stable operation and
  resource-kind names, pass through the shared `authorization.Authorizer`, and
  retain the Router authorization-proof guard.
- Policy and identity-provider configuration remain Control Plane-only ConfigMap
  data. Neither is mounted into the Data Plane.
- API audit records contain principal ID, request ID, normalized scope, outcome,
  matched policy rule, status, and latency. They never contain request bodies,
  tokens, identity claims, file contents, command output, or passwords.
