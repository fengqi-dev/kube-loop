# KubeLoop V2 traffic data plane

## Goals

The V2 data plane provides stable local SOCKS and optional TUN access to the
destinations authorized by a Cluster Session. It does not expose the Kubernetes
API Server, depend on kubeconfig, or give the in-cluster Data Plane Kubernetes
credentials.

## Path

```text
application -> TUN/SOCKS -> sing-box -> WSS Relay -> Data Plane -> destination
```

The Control Plane is not in the packet path. It creates and authorizes the
Session, returns a NetworkSpec, assigns a Data Plane, and issues RelayTickets.
Relay and Data Plane accept traffic only when the ticket and live Session
generation agree.

## Local modes

**SOCKS mode** starts a stable loopback SOCKS5 endpoint and managed sing-box.
It needs no privileged Helper. Applications may use the endpoint directly, and
the desktop reuses it for Port Forward and workload tools.

**TUN mode** uses the same Session transport, plus the platform Helper. The
Helper installs only the Pod/Service CIDRs and split-DNS rules in NetworkSpec,
supervises sing-box, and records enough state to undo partial installation or a
crash. Changing namespace or NetworkSpec creates a new Helper generation.

## Transport

One physical WSS transport may carry multiple logical streams. Each open
request includes the destination and protocol authorized for that Session.
RelayTickets are short-lived and bind identity, device, Session, generation,
assigned Data Plane, and purpose. Rotation opens a new authenticated transport,
drains the old generation, and preserves local endpoints where possible.

TCP streams preserve half-close and terminal errors. UDP uses bounded logical
associations with idle expiry and source/destination metadata. Capacity limits
apply globally, per identity, per Session, and per transport; rejected work does
not consume leaked slots.

## DNS and routing

NetworkSpec is the only source of cluster CIDRs, DNS servers, search domains,
and cluster domain. Split DNS sends only cluster names to cluster DNS. TUN
routes only the authorized CIDRs. An unavailable Session fails closed for those
routes and never falls back to a direct workstation path.

## Task data paths

- Port Forward terminates on a stable loopback listener and opens one authorized
  stream per connection or UDP association.
- Exchange and Preview deliver Data Plane traffic to a Session-owned local
  endpoint through Relay.
- Mirror duplicates the request stream while the original backend remains the
  only primary response source.
- Pod exec and file operations use separate authenticated Control Plane task
  streams because Kubernetes SPDY and authorization remain a control-plane
  responsibility.

## Recovery invariants

- A transport cannot be reused across Profile, Session, Namespace, generation,
  or assigned Data Plane boundaries.
- Logout, grant revocation, Session expiry, or explicit disconnect closes all
  streams and local listeners owned by that Session.
- Helper cleanup is idempotent and owner/generation checked.
- Durable traffic resources are reconciled by Task and TrafficBinding ownership,
  not by names alone.
- Metrics use bounded labels and do not expose user, device, Session, token, or
  destination secrets.

The end-to-end evidence is tracked in
[the V2 E2E coverage matrix](v2-e2e-coverage.zh-CN.md).
