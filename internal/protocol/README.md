# Protocol contracts

This directory owns versioned contracts that cross a process or network
boundary. Packages here contain wire messages, codecs, validation, and protocol
constants; they do not own controllers, state machines, Kubernetes operations,
or transport lifecycle.

Current contracts:

- `exchangestream`, `execstream`, `filestream`, and `mirrorstream`: streaming
  operation frames.
- `helper`: desktop-to-privileged-helper JSON-line RPC.
- `networkspec`: the normalized network document exchanged by the control plane,
  gateway, and client.
- `relaycontrol` and `relayticket`: relay control-plane messages and signed
  admission tickets.
- `tunnel`: tunnel data and control frames.
- `wssprotocol`: WebSocket multiplexing handshake and frame metadata.

Domain state remains with its owning package. Transport implementations such as
`websocketmux` also stay outside this directory and depend on these contracts.
