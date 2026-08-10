#!/bin/sh
set -eu

if [ -n "${KUBELOOP_TEST_POSTGRESQL_DSN:-}" ]; then
  exec go test -race ./internal/controller/storage -count=1
fi

if command -v podman >/dev/null 2>&1; then
  container_runtime=podman
elif command -v docker >/dev/null 2>&1; then
  container_runtime=docker
else
  echo "podman or docker is required when KUBELOOP_TEST_POSTGRESQL_DSN is unset" >&2
  exit 1
fi

container_name="kubeloop-postgresql-conformance-$$"
postgres_image="${KUBELOOP_POSTGRESQL_TEST_IMAGE:-docker.io/library/postgres:17-alpine}"

cleanup() {
  "$container_runtime" rm -f "$container_name" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

"$container_runtime" run -d \
  --name "$container_name" \
  -e POSTGRES_USER=kubeloop \
  -e POSTGRES_PASSWORD=kubeloop-test-only \
  -e POSTGRES_DB=kubeloop \
  -p 127.0.0.1::5432 \
  "$postgres_image" >/dev/null

attempt=0
until "$container_runtime" exec "$container_name" pg_isready -U kubeloop -d kubeloop >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 60 ]; then
    echo "PostgreSQL did not become ready" >&2
    exit 1
  fi
  sleep 1
done

published_port=$(
  "$container_runtime" port "$container_name" 5432/tcp |
    sed -n 's/.*:\([0-9][0-9]*\)$/\1/p' |
    head -n 1
)
if [ -z "$published_port" ]; then
  echo "could not resolve the PostgreSQL test port" >&2
  exit 1
fi

KUBELOOP_TEST_POSTGRESQL_DSN="postgres://kubeloop:kubeloop-test-only@127.0.0.1:${published_port}/kubeloop?sslmode=disable" \
  go test -race ./internal/controller/storage -count=1
