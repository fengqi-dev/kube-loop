# ADR 0006: RelayTicket authentication and session binding

## Status

Accepted on 2026-08-10.

## Context

The V1 WebSocket transport used one static shared bearer token. Possession of
that token was not bound to a user, device, Cluster Session, relay instance or
operation, and rotating it interrupted every client. The Data Plane must remain
stateless with respect to the Control Plane database and identity providers.

## Decision

Control Plane signs short-lived Ed25519 RelayTickets after normal access-token
authentication, Gateway Policy authorization and active Session ownership
validation. Ticket schema v2 binds issuer, relay audience, principal, device,
Session ID and generation, namespace, operation list, required Session NetworkSpec hash, key ID, issued
and not-before times, expiry and a unique ticket ID. Lifetime is at most two
minutes and never exceeds the current Session expiry.

Data Plane holds only a bounded set of Ed25519 public keys. It strictly decodes
the compact token, rejects unknown fields and algorithms, verifies all binding
and time claims, requires `tunnel` scope, and atomically consumes the ticket ID
in a bounded replay cache. Each physical WebSocket therefore obtains a fresh
ticket. Key rotation uses an overlap window containing old and new public keys.

Each Data Plane keeps bounded, expiry-aware high-water marks for Session
generations. Once generation N is observed, a ticket below N is rejected. The
WebSocket multiplexer also allows already accepted older-generation streams to
finish while rejecting new streams from their physical connection. The
protocol tenant key is derived from both Session ID and generation, preventing
an older connection from borrowing the authorization registered by a newer one.

The authenticated Session UUID is domain-separated and hashed into the
existing 32-byte protocol tenant key. Every logical stream inside that
WebSocket must present the matching key, so a ticket cannot be reused to open
streams for another Session. The protocol key is a partition identifier, not a
credential; the RelayTicket is the authorization capability.

The private key is projected only into Control Plane. Data Plane receives only a
public-key JSON Secret, has no database or Kubernetes ServiceAccount token, and
does not query OIDC. Tickets, authorization headers and claims are excluded
from logs, metrics and audit payloads. Helm no longer creates a static shared
token and disables the raw TCP listener by default.

## Consequences

- Revocation latency is bounded by the short ticket lifetime; emergency key
  removal invalidates tickets for that key after Data Plane rollout.
- Connection-pool expansion and reconnect require a new ticket per physical
  WebSocket.
- Full target authorization still requires the upcoming validated NetworkSpec
  stream target checks; RelayTicket authentication alone does not make the
  remaining V2 data path production complete.
