# ADR 0007: Control Plane persistence uses Bun as a SQL-first ORM

## Status

Accepted on 2026-08-10.

## Context

Control Plane already has stable Repository and TransactionManager contracts,
versioned SQLite/PostgreSQL migrations, and production-sensitive optimistic
updates. Hand-written row scanning and placeholder conversion make each new
stored field expensive, while a conventional stateful ORM could hide SQL
semantics that are security-critical for Session, token and audit state.

## Decision

Use Bun v1.2.18 as the Control Plane's internal SQL-first ORM. Bun wraps the
existing `database/sql` pool, so SQLite continues to use the pure-Go
`modernc.org/sqlite` driver and PostgreSQL continues to use pgx. The selected
Bun dialect is derived from the configured backend.

The Repository interfaces remain the business-facing persistence boundary.
Bun models are private storage rows and must not appear in Control Plane API or
domain packages. Repositories migrate incrementally, beginning with Cluster
Session because its NetworkSpec snapshot adds structured columns. Existing
repositories may continue to execute reviewed raw SQL through Bun during the
transition.

Versioned, backend-specific SQL migrations remain the only schema authority.
Bun automatic table creation or automatic migration is not used. Optimistic
generation guards, bounded deletes, append-only audit rules, migration locks,
error mapping and dual-backend conformance tests remain explicit.

## Consequences

- ORM adoption is incremental and does not require a flag day or change public
  Repository contracts.
- Queries that depend on SQLite `rowid` or PostgreSQL `ctid` may remain explicit
  dialect-specific SQL.
- Adding or changing a Bun model never changes the database by itself; every
  schema change requires a numbered SQLite/PostgreSQL migration and tests.
- Ent was not selected because its generated client/schema would require a
  broad persistence rewrite. GORM was not selected because its implicit update,
  hook and AutoMigrate conventions are a poor default for the Session and token
  state machines.
