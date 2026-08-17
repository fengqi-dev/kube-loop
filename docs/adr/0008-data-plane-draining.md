# ADR 0008: Data Plane draining and reselection

## Status

Accepted on 2026-08-10.

## Context

Gateway Pods are replaceable and may be rolled independently from Control Plane.
An active TCP or WebSocket stream cannot be moved between processes without a
different state-replication protocol. Claiming transparent stream migration
would hide loss and make stale Session generations unsafe.

## Decision

Data Plane uses a stable relay-pool audience and same-origin tunnel path. The
Kubernetes Service selects a ready Pod; Control Plane authorizes the selected pool
by issuing a fresh, generation-bound RelayTicket. Pod identity is deliberately
not exposed to the desktop, so the user still configures only the service URL.

On SIGTERM, Gateway first marks readiness unavailable and rejects both new
physical WebSocket sessions and new logical protocol connections. Existing
streams continue until `drainTimeout`. The process then closes remaining
streams explicitly before exiting; `terminationGracePeriodSeconds` must exceed
the drain timeout.

The desktop treats that close as transport loss. It isolates the stable SOCKS
and TUN path, heartbeats Control Plane to obtain the authoritative newer Session
generation, obtains a fresh RelayTicket and reconnects through the Service.
The local SOCKS listener and TUN/Helper Session stay in place. Gateway and the
desktop both keep generation high-water marks: existing older streams may
finish, but an older ticket, physical connection or in-flight recovery result
cannot publish new active traffic after a newer generation is observed.

## Consequences

- Active streams can finish within a bounded drain window, but are never
  advertised as losslessly migrated.
- A drain deadline causes an explicit disconnect and generation-based rebuild.
- Kubernetes readiness performs instance reselection while Control Plane remains
  the authority for Session generation and RelayTicket issuance.
- RelayTicket schema v1 is intentionally incompatible with this V2-only branch;
  Control Plane and Data Plane must roll with overlapping capacity.
