# ADR 0019: WSS v2 handshake and stream semantics

- Status: Accepted
- Date: 2026-08-10

## Context

`kubeloop-mux-v2` previously started smux immediately after the HTTP WebSocket
upgrade. The bearer credential authenticated a physical connection, but there
was no explicit negotiation for protocol/client version, device binding,
capabilities or limits. A new client could therefore become partially connected
before discovering an incompatible peer.

The original roadmap described an Access Token at this boundary. V2-500 later
made the narrower RelayTicket the authoritative Data Plane credential. The
Control Plane validates the Access Token, Identity, device, policy and active
Cluster Session before issuing the short-lived, one-use ticket. Sending the
Access Token to Data Plane again would widen its privilege and contradict the
Control Plane/Data Plane split.

## Decision

### Authentication and ordering

The client performs these steps in order:

1. Obtain a RelayTicket and its assigned Relay ID, WSS endpoint and device ID
   from the authenticated Control Plane API.
2. Upgrade one HTTPS request with `Authorization: Bearer <RelayTicket>` and the
   exact WebSocket subprotocol `kubeloop-mux-v2`; the response must advertise
   `KubeLoop-WSS-Version: 2.0`.
3. Send one binary, strict-JSON `ClientHello` within ten seconds and 8 KiB.
4. Receive exactly one binary `ServerHello` or `Reject` before sending smux
   bytes.
5. Start smux v2 only after a valid `ServerHello`.

Data Plane verifies the RelayTicket first, including issuer, assigned Relay
audience, expiry, operation, one-use `jti`, Cluster Session generation and
revocation. It then requires the `ClientHello.deviceId` to equal the signed
ticket claim. A malformed, timed-out or rejected handshake closes the physical
WebSocket and never creates a logical-stream session.

RelayTicket expiry is an admission boundary only. Once the authenticated WSS
handshake succeeds, the physical connection and its logical streams remain
active until generation fencing, Session revocation, Gateway shutdown, network
failure or an explicit close ends them; ticket expiry alone never stops a
running Task.

### Handshake documents

`ClientHello` contains an ordered `protocolVersions` list, desktop
`clientVersion`, `deviceId`, and a unique capability list. `ServerHello`
selects exactly one offered version and returns the server version,
capabilities and effective limits. V2 currently implements only protocol
`2.0`; advertising another server version requires an implementation change,
not only configuration.

Both peers require `traffic.websocket.v1` in addition to `smux.v2` and
`tunnel.open.v2`. This capability means Exchange, Mirror and Preview use a
bounded KCG2 logical stream inside `/tunnel`; no secondary public WebSocket
path is part of the negotiated protocol.

`Reject` contains a stable machine-readable `code`, bounded human-readable
`message`, and the server's supported versions for `VERSION_MISMATCH`.
Defined codes are:

- `VERSION_MISMATCH`
- `CLIENT_VERSION_UNSUPPORTED`
- `DEVICE_MISMATCH`
- `INVALID_HANDSHAKE`
- `USER_CAPACITY_EXCEEDED`

All documents reject unknown fields, duplicate identifiers, control
characters, missing required values, trailing JSON and payloads over 8 KiB.
Handshake messages are binary WebSocket messages; text messages are invalid.

### Limits

`ServerHello.limits` is authoritative and includes:

- maximum WebSocket binary-frame bytes;
- maximum 64-KiB logical-stream data-frame bytes;
- maximum concurrent logical streams per physical connection;
- maximum physical connections on the Data Plane;
- maximum physical connections per Identity, shared across that Identity's
  devices;
- logical-stream idle timeout in explicit milliseconds.

The desktop clamps its local pool and per-connection stream use to the returned
limits. Helm exposes the Data Plane limits and validates the frame and per-user
bounds. The same `controlPlane.minClientVersion` is enforced by discovery and
the WSS handshake.

### Multiplexed stream semantics

The WSS handshake is the only JSON layer. After it, protocol behavior is
binary and maps as follows:

| Semantic operation | Wire representation |
| --- | --- |
| physical keepalive | WebSocket Ping/Pong plus smux NOP |
| logical stream open | smux SYN, followed by a bounded KCG2 TCP/UDP/control/traffic header |
| target accept | KCG2 `StatusOK` |
| target reject | KCG2 bounded `StatusError`, then stream close |
| data | smux PSH carrying KubeLoop data frames of at most 64 KiB |
| Exchange/Mirror/Preview | KCG2 traffic header selects mode and Task UUID, followed by a `kubeloop.traffic.v1` Gorilla WebSocket handshake and bounded binary messages on the same smux stream |
| half-close | KubeLoop logical FIN frame; the peer may continue sending |
| cancel/full close | close the smux stream; transport/context cancellation closes both directions |

The KCG2 open header binds every logical stream to the RelayTicket's immutable
Cluster Session ID/generation-derived tenant key and an authorized target,
control operation or traffic Task. The server rechecks generation and authorization
before handing a stream to the dialer. smux native FIN is not used as TCP
half-close because crossed native FINs can discard unread response data; the
explicit KubeLoop FIN preserves request-EOF/response-after-EOF behavior.

## Consequences

- Data Plane never receives the broader OAuth/OIDC Access Token.
- Protocol and client incompatibility is reported before smux exists, with a
  typed client error suitable for upgrade guidance.
- A device response field is returned with each RelayTicket so the desktop can
  construct `ClientHello`; Data Plane independently compares it with the signed
  claim.
- Per-user connection accounting keys on Identity, not Identity plus device,
  so adding devices cannot bypass the limit.
- Contract tests cover strict documents, explicit version/client/device/capacity
  rejection, exact advertised limits, no partial session, multiplexed data,
  half-close, malformed stream isolation and idle timeout.
