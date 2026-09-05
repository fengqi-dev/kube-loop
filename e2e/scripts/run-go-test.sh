#!/usr/bin/env bash
# Run TUN e2e packages and print a failed-case summary at the end.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

TIMEOUT="${KUBELOOP_E2E_TIMEOUT:-30m}"
LOG="$(mktemp "${TMPDIR:-/tmp}/kubeloop-e2e.XXXXXX")"
RUNTIME_DIR="$(mktemp -d "${TMPDIR:-/tmp}/kubeloop-e2e-runtime.XXXXXX")"
OPERATOR_PID=""

cleanup() {
  if [[ -n "${OPERATOR_PID}" ]]; then
    kill "${OPERATOR_PID}" >/dev/null 2>&1 || true
    wait "${OPERATOR_PID}" >/dev/null 2>&1 || true
  fi
  rm -f "${LOG}"
  rm -rf "${RUNTIME_DIR}"
}
trap cleanup EXIT

start_operator() {
  local context="${KUBELOOP_E2E_CONTEXT:-minikube}"
  echo "==> Installing TrafficBinding CRD in context ${context}"
  kubectl --context "${context}" apply \
    -f config/crd/bases/traffic.kubeloop.io_trafficbindings.yaml
  kubectl --context "${context}" wait \
    --for=condition=Established \
    crd/trafficbindings.traffic.kubeloop.io \
    --timeout=60s

  kubectl config view --raw --flatten --minify --context "${context}" \
    >"${RUNTIME_DIR}/kubeconfig"
  go build -trimpath -o "${RUNTIME_DIR}/kubeloop-operator" ./cmd/kubeloop-operator
  KUBECONFIG="${RUNTIME_DIR}/kubeconfig" \
    "${RUNTIME_DIR}/kubeloop-operator" \
      --metrics-bind-address=0 \
      --health-probe-bind-address=0 \
      >"${RUNTIME_DIR}/operator.log" 2>&1 &
  OPERATOR_PID=$!

  # Cache startup is asynchronous. Verify the process survives initialization;
  # reconciliation itself remains eventual and is asserted by the E2E cases.
  for _ in {1..20}; do
    if ! kill -0 "${OPERATOR_PID}" >/dev/null 2>&1; then
      echo "Operator exited during startup" >&2
      cat "${RUNTIME_DIR}/operator.log" >&2
      return 1
    fi
    sleep 0.1
  done
  echo "==> TrafficBinding Operator ready (pid ${OPERATOR_PID})"
}

if [[ "${KUBELOOP_E2E:-}" == "1" ]]; then
  start_operator
fi

echo "==> Running TUN e2e (log: ${LOG})"
if [[ -n "${KUBELOOP_E2E_PACKAGES:-}" ]]; then
  read -r -a E2E_PACKAGES <<<"${KUBELOOP_E2E_PACKAGES}"
else
  # Clients only know the Gateway address, so the acceptance gate exercises
  # the authenticated Data Plane and remote TUN paths.
  E2E_PACKAGES=(./e2e/dataplane)
fi
set +e
go test -tags=e2e "${E2E_PACKAGES[@]}" -count=1 -timeout="${TIMEOUT}" -parallel=1 -p 1 -v "$@" 2>&1 | tee "${LOG}"
EXIT_CODE=${PIPESTATUS[0]}
set -e

echo
echo "==> e2e summary"
FAILED="$(grep -E '^--- FAIL: ' "${LOG}" | sed -E 's/^--- FAIL: ([^ ]+).*/\1/' || true)"
PKG_FAIL="$(grep -E '^FAIL[[:space:]]' "${LOG}" || true)"

if [[ -z "${FAILED}" && "${EXIT_CODE}" -eq 0 ]]; then
  echo "All e2e tests passed."
  exit 0
fi

if [[ -s "${RUNTIME_DIR}/operator.log" ]]; then
  echo
  echo "==> Operator log tail"
  tail -80 "${RUNTIME_DIR}/operator.log"
fi

if [[ -n "${FAILED}" ]]; then
  echo "Failed tests:"
  while IFS= read -r name; do
    [[ -n "${name}" ]] || continue
    echo "  - ${name}"
  done <<<"${FAILED}"
else
  echo "No per-test FAIL lines parsed (exit=${EXIT_CODE})."
fi

if [[ -n "${PKG_FAIL}" ]]; then
  echo "Failed packages:"
  while IFS= read -r line; do
    [[ -n "${line}" ]] || continue
    echo "  - ${line#FAIL	}"
  done <<<"${PKG_FAIL}"
fi

# Print a short error snippet under each failed test name.
if [[ -n "${FAILED}" ]]; then
  echo
  echo "Failure details:"
  while IFS= read -r name; do
    [[ -n "${name}" ]] || continue
    echo "--- ${name}"
    # From RUN to FAIL for this test; keep the last diagnostic lines.
    awk -v name="${name}" '
      $0 ~ ("=== RUN   " name "$") { buf=""; capturing=1 }
      capturing { buf = buf $0 ORS }
      $0 ~ ("--- FAIL: " name " ") {
        n = split(buf, lines, ORS)
        start = n > 12 ? n - 12 : 1
        for (i = start; i <= n; i++) if (lines[i] != "") print lines[i]
        capturing=0
      }
    ' "${LOG}"
    echo
  done <<<"${FAILED}"
fi

exit "${EXIT_CODE}"
