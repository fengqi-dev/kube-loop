# ADR 0010: Pod SSH client and Gateway boundary

- Status: Accepted
- Date: 2026-08-10

## Context

Pod SSH is a compatibility endpoint for local SSH clients; it is not an SSH
daemon running in the target Pod. V2 removes kubeconfig, Kubernetes clients and
ServiceAccount credentials from the desktop while preserving local terminal,
SSH identity and SFTP workflows. An endpoint must not become a way to bypass
the authenticated Cluster Session or address another user's Pod.

## Decision

The desktop owns an ephemeral `127.0.0.1` listener and the user's local SSH
identity. The listener accepts public-key authentication only. Its target is
bound to one Server Profile, Cluster Session, namespace, Pod and container;
the client rechecks that the same active Session is current before opening each
remote operation. Namespace changes, logout, Profile deletion and desktop
shutdown remove the endpoint and close its active SSH connections.

The Gateway remains authoritative for Pod/container discovery and creates the
actual Kubernetes `pods/exec` stream. SSH shell and command channels are
adapted to the authenticated V2 Pod exec API, including stdin, stdout, stderr,
TTY resize and exit status. SFTP remains a local SSH subsystem whose filesystem
operations are implemented through the same Gateway-controlled exec boundary.
The desktop does not dial a Pod IP, carry Kubernetes credentials or expose a
generic Kubernetes proxy.

Each desktop identity has its own generated SSH key. Knowing another user's
loopback address or targeting the same Pod IP is insufficient: a key not
authorized by the endpoint is rejected before any Gateway Task is created.
Gateway authorization and Session ownership are still applied when an
authorized local user starts the remote exec.

## Consequences

- Existing SSH and SFTP tools can use a generated command against a loopback
  port without requiring an SSH daemon in the Pod.
- A desktop restart intentionally loses ephemeral endpoints; users explicitly
  enable them again under the new active Session.
- Local key-file confidentiality is part of the desktop trust boundary, while
  Gateway Policy and Kubernetes RBAC remain the remote authorization boundary.
- SSH commands and payloads inherit Pod exec's rule that command content and
  stream data are not written to the default HTTP audit log.
