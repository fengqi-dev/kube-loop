#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PROFILE="${KUBELOOP_UI_E2E_MINIKUBE_PROFILE:-kubeloop-ui-e2e}"
NAMESPACE="kubeloop-dev"
ARTIFACTS="${KUBELOOP_UI_E2E_ARTIFACTS:-${ROOT}/build/ui-e2e}"
DEPLOY_LOG="${ARTIFACTS}/deployment.log"
RUNTIME_DIR="$(mktemp -d "${TMPDIR:-/tmp}/kubeloop-ui-e2e.XXXXXX")"
export KUBECONFIG="${RUNTIME_DIR}/kubeconfig"
export KUBELOOP_UI_E2E_PROFILE_PATH="${RUNTIME_DIR}/servers.json"

mkdir -p "${ARTIFACTS}"

redact_logs() {
  sed -E \
    -e 's/("token"[[:space:]]*:[[:space:]]*")[^"]+("?)/\1[REDACTED]\2/g' \
    -e 's/(token[=:][[:space:]]*)[^[:space:]]+/\1[REDACTED]/g'
}

collect_diagnostics() {
  kubectl get nodes,pods,deployments,replicasets,services,pvc --all-namespaces -o wide \
    >"${ARTIFACTS}/kubernetes-resources.log" 2>&1 || true
  kubectl get events --all-namespaces --sort-by=.lastTimestamp \
    >"${ARTIFACTS}/kubernetes-events.log" 2>&1 || true
  kubectl describe pods --namespace "${NAMESPACE}" \
    >"${ARTIFACTS}/kubeloop-pods.log" 2>&1 || true
  kubectl logs --namespace "${NAMESPACE}" \
    --selector app.kubernetes.io/component=control-plane --all-containers --tail=-1 2>&1 \
    | redact_logs >"${ARTIFACTS}/control-plane.log" || true
}

cleanup() {
  local status=$?
  if (( status != 0 )); then
    collect_diagnostics
  fi
  if [[ "${KUBELOOP_UI_E2E_KEEP_CLUSTER:-0}" != "1" ]]; then
    minikube delete --profile "${PROFILE}" >/dev/null 2>&1 || true
  fi
  rm -rf -- "${RUNTIME_DIR}"
  exit "${status}"
}
trap cleanup EXIT

if [[ ! "${PROFILE}" =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]*$ ]]; then
  echo "invalid Minikube profile name: ${PROFILE}" >&2
  exit 2
fi

for command in go helm kubectl minikube npm npx openssl curl wails; do
  if ! command -v "${command}" >/dev/null; then
    echo "required command is unavailable: ${command}" >&2
    exit 2
  fi
done
if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "the full UI environment requires the self-hosted macOS runner" >&2
  exit 2
fi

minikube delete --profile "${PROFILE}" >/dev/null 2>&1 || true
minikube start \
  --profile "${PROFILE}" \
  --driver "${KUBELOOP_UI_E2E_MINIKUBE_DRIVER:-docker}" \
  --cpus "${KUBELOOP_UI_E2E_MINIKUBE_CPUS:-4}" \
  --memory "${KUBELOOP_UI_E2E_MINIKUBE_MEMORY:-7168}"
if ! minikube addons enable ingress --profile "${PROFILE}"; then
  echo "ingress addon did not become ready on the first attempt; retrying with the same image cache" >&2
  minikube addons enable ingress --profile "${PROFILE}"
fi
kubectl config use-context "${PROFILE}"
kubectl wait --namespace ingress-nginx \
  --for=condition=Available deployment/ingress-nginx-controller --timeout=180s

export KUBELOOP_DEV_SERVICE_ID="kubeloop-ui-$(openssl rand -hex 8)"
(
  cd "${ROOT}"
  go run ./build/gateway-dev.go
) | tee "${DEPLOY_LOG}"
unset KUBELOOP_DEV_SERVICE_ID

BASE_URL="$(awk '/KubeLoop development server:/ {value=$NF} END {print value}' "${DEPLOY_LOG}")"
if [[ ! "${BASE_URL}" =~ ^https:// ]]; then
  echo "could not determine the deployed HTTPS URL" >&2
  exit 1
fi

for _ in {1..60}; do
  if curl --fail --silent --show-error --insecure "${BASE_URL}/.well-known/kubeloop" >/dev/null; then
    break
  fi
  sleep 2
done
curl --fail --silent --show-error --insecure "${BASE_URL}/.well-known/kubeloop" >/dev/null

CONTROL_PLANE_LOGS="$(kubectl logs --namespace "${NAMESPACE}" \
  --selector app.kubernetes.io/component=control-plane --all-containers --tail=-1)"
BOOTSTRAP_TOKEN="$(printf '%s\n' "${CONTROL_PLANE_LOGS}" \
  | sed -nE 's/.*"token"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p' \
  | tail -1)"
unset CONTROL_PLANE_LOGS
if [[ -z "${BOOTSTRAP_TOKEN}" ]]; then
  echo "the one-time IAM bootstrap token was not found in Control Plane logs" >&2
  exit 1
fi

export KUBELOOP_UI_E2E=1
export KUBELOOP_UI_E2E_BASE_URL="${BASE_URL}"
export KUBELOOP_UI_E2E_BOOTSTRAP_TOKEN="${BOOTSTRAP_TOKEN}"
export KUBELOOP_UI_E2E_ADMIN_USERNAME="ui-e2e-admin"
export KUBELOOP_UI_E2E_ADMIN_PASSWORD="UiE2e-$(openssl rand -hex 12)"
export KUBELOOP_UI_E2E_ARTIFACTS="${ARTIFACTS}/admin"
"${ROOT}/e2e/ui/run-admin.sh"
unset KUBELOOP_UI_E2E_BOOTSTRAP_TOKEN

(
  cd "${ROOT}"
  npm ci --prefix frontend
  wails build -clean
)
export KUBELOOP_UI_E2E_ARTIFACTS="${ARTIFACTS}/macos"
export KUBELOOP_UI_E2E_APP_PATH="${ROOT}/build/bin/KubeLoop.app"
"${ROOT}/e2e/ui/run-macos.sh"
unset KUBELOOP_UI_E2E_ADMIN_PASSWORD
