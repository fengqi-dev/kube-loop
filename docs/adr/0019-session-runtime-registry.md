# ADR 0019: Session runtime ownership and crash recovery

- Status: Accepted
- Date: 2026-08-10

## Context

Durable Session and Task rows identify ownership after a restart, but they do
not cancel an in-process WebSocket, reverse listener or Kubernetes executor.
Independent handler contexts also make Session disconnect and Control Plane
shutdown ordering ambiguous. A hard process failure can leave a Task in an
owned state after its stream owner is gone.

## Decision

The Control Plane owns one in-memory runtime tree rooted at its process context:

```text
Control Plane
  -> Cluster Session
       -> durable Task
            -> active Stream and its listener/socket/resource cleanup
```

Exec, file transfer, Exchange, Mirror and Preview streams attach to this tree
through the shared authorization lease. A Session disconnect cancels streams
and Tasks in reverse registration order, then waits for handlers to close
listeners and sockets, restore or delete Kubernetes resources, persist the
terminal Task state, and release their runtime nodes. Repeated disconnect is
idempotent. Process shutdown cancels the same root and waits within the normal
Control Plane shutdown deadline.

The database remains the recovery source of truth. Exec and file streams
heartbeat their owned Task row while active. A CAS recovery worker terminates
only stale owners; a concurrent heartbeat wins safely. Stale Port Forward
activation is failed, while a running Port Forward is retained as long as its
Session is active. Once its Session is disconnected, expired or missing, the
Task becomes terminal and the TrafficBinding orphan reconciler deletes the CR.
Exchange, Mirror and Preview keep their feature-specific snapshot recovery
workers because their compensating actions require typed Kubernetes state.

## Consequences

- Session termination has one immediate cancellation point instead of waiting
  for each feature's polling interval.
- Runtime state is never treated as durable and is rebuilt lazily for active
  Sessions after restart.
- Reverse listeners cannot be resumed without the desktop connection; durable
  cleanup intent is recovered instead.
- Control Plane replicas coordinate stale-owner recovery with Task state and
  `updated_at` compare-and-swap rather than an in-memory distributed lock.
