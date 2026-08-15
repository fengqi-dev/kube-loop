#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SUITE="${ROOT}/e2e/ui/admin"

for variable in KUBELOOP_UI_E2E_BASE_URL KUBELOOP_UI_E2E_BOOTSTRAP_TOKEN KUBELOOP_UI_E2E_ADMIN_USERNAME KUBELOOP_UI_E2E_ADMIN_PASSWORD; do
  if [[ -z "${!variable:-}" ]]; then
    echo "${variable} is required" >&2
    exit 2
  fi
done

if [[ "${KUBELOOP_UI_E2E:-}" != "1" ]]; then
  echo "KUBELOOP_UI_E2E=1 is required" >&2
  exit 2
fi

mkdir -p "${KUBELOOP_UI_E2E_ARTIFACTS:-${ROOT}/build/ui-e2e/admin}"
npm ci --prefix "${SUITE}"
(
  cd "${SUITE}"
  npx playwright install chromium
  npm test
)
