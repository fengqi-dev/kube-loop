#!/usr/bin/env bash
# Build the Gateway image, install the privileged Helper, then run TUN e2e tests.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

CONTEXT="${KUBELOOP_E2E_CONTEXT:-minikube}"
IMAGE="${KUBELOOP_GATEWAY_IMAGE:-kubeloop-gateway:dev}"
ARCH="$(go env GOARCH)"

BINARY="build/bin/kubeloop-gateway"
DOCKERFILE="build/gateway.e2e.Dockerfile"
GATEWAY_NS="kubeloop-system"
GATEWAY_LABEL="app.kubernetes.io/name=kubeloop-gateway"
HELPER_SRC="build/embedded/kubeloop-helper"
HELPER_BIN="build/bin/kubeloop-helper"
SINGBOX_BIN="build/bin/sing-box"
TOKEN_PATH="${HOME}/.kubeloop-dev/secrets/helper.token"

log() { echo "==> $*"; }

k() { kubectl --context="${CONTEXT}" "$@"; }

build_gateway() {
  log "Building Gateway binary (linux/${ARCH})"
  mkdir -p build/bin
  CGO_ENABLED=0 GOOS=linux GOARCH="${ARCH}" go build \
    -trimpath -ldflags="-s -w" \
    -o "${BINARY}" \
    ./cmd/kubeloop-gateway
  go run ./build/singbox-patched.go \
    -target "linux/${ARCH}" \
    -output build/bin/sing-box-gateway
}

host_docker_ok() {
  command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1
}

load_image_docker() {
  if command -v minikube >/dev/null 2>&1; then
    eval "$(minikube docker-env --shell bash)"
  fi
  docker build -t "${IMAGE}" -f "${DOCKERFILE}" .
}

load_image_minikube() {
  log "host Docker unavailable; building inside minikube"
  local remote="/tmp/gwbuild"

  minikube cp "${BINARY}" /tmp/kubeloop-gateway
  minikube cp build/bin/sing-box-gateway /tmp/sing-box-gateway
  minikube cp "${DOCKERFILE}" /tmp/Dockerfile.e2e
  minikube ssh -- "
    set -e
    sudo mkdir -p ${remote}/build/bin ${remote}/build/runtime
    sudo cp /tmp/kubeloop-gateway ${remote}/build/bin/kubeloop-gateway
    sudo cp /tmp/sing-box-gateway ${remote}/build/bin/sing-box-gateway
    sudo cp /tmp/Dockerfile.e2e ${remote}/Dockerfile
    sudo chmod 755 ${remote}/build/bin/kubeloop-gateway ${remote}/build/bin/sing-box-gateway
    cd ${remote} && sudo docker build -t ${IMAGE} -f Dockerfile .
  "
}

load_gateway_image() {
  log "Loading Gateway image (${IMAGE})"
  if host_docker_ok; then
    load_image_docker
  else
    load_image_minikube
  fi
}

restart_gateway() {
  if ! k -n "${GATEWAY_NS}" get deployment kubeloop-gateway >/dev/null 2>&1; then
    log "Gateway Deployment is not installed yet; the e2e harness will create it"
    return 0
  fi

  log "Restarting Gateway Pods to pick up image"
  k -n "${GATEWAY_NS}" delete pod \
    -l "${GATEWAY_LABEL}" \
    --ignore-not-found=true --wait=false || true

  log "Waiting for Gateway Deployment to be ready"
  k -n "${GATEWAY_NS}" rollout status deploy/kubeloop-gateway --timeout=180s
  k -n "${GATEWAY_NS}" wait \
    --for=condition=Ready pod \
    -l "${GATEWAY_LABEL}" \
    --timeout=120s
  sleep 2
}

ensure_helper_token() {
  install -d -m 0700 "$(dirname "${TOKEN_PATH}")"
  if [[ ! -s "${TOKEN_PATH}" ]]; then
    # 64 hex chars; matches helper.EnsureUserToken entropy.
    openssl rand -hex 32 >"${TOKEN_PATH}"
    chmod 600 "${TOKEN_PATH}"
  fi
}

helper_ready() {
  go run ./e2e/scripts/helper-ping.go >/dev/null 2>&1
}

install_helper() {
  log "Building privileged Helper"
  ./build/bundle-helper.sh

  log "Ensuring sing-box at ${SINGBOX_BIN}"
  go run ./e2e/scripts/ensure-singbox.go >/dev/null

  ensure_helper_token
  mkdir -p build/bin
  cp "${HELPER_SRC}" "${HELPER_BIN}"
  chmod 755 "${HELPER_BIN}" "${SINGBOX_BIN}"

  # Protocol-ready is not enough: local "dev" builds share Version/Protocol, so also
  # reinstall when the on-disk helper binary differs from this tree.
  installed_helper=""
  case "$(uname -s)" in
    Darwin) installed_helper="/Library/PrivilegedHelperTools/dev.fengqi.kubeloop.helper.dev" ;;
    Linux) installed_helper="/usr/local/libexec/kubeloop-helper-dev" ;;
  esac
  if helper_ready && [[ -n "${installed_helper}" && -x "${installed_helper}" ]] \
    && cmp -s "${HELPER_SRC}" "${installed_helper}"; then
    log "Helper already running (protocol + binary match); skipping install"
    go run ./e2e/scripts/stop-helper.go || true
    return 0
  fi

  log "Installing Helper (sudo / macOS admin prompt) for protocol/binary upgrade"
  # Prefer non-interactive sudo when available; otherwise fall back to
  # helper.EnsureInstall (osascript admin dialog on macOS).
  local -a install_cmd=(
    "${HELPER_SRC}" install
    --source "${ROOT}/${HELPER_SRC}"
    --token "$(tr -d '[:space:]' <"${TOKEN_PATH}")"
    --uid "$(id -u)"
    --home "${HOME}"
    --version dev
    --sing-box "${ROOT}/${SINGBOX_BIN}"
  )
  if [[ "$(id -u)" -eq 0 ]]; then
    "${install_cmd[@]}"
  elif sudo -n true >/dev/null 2>&1; then
    sudo -n "${install_cmd[@]}"
  else
    go run ./e2e/scripts/ensure-helper.go
  fi

  if ! helper_ready; then
    echo "error: Helper is not CoreReady after install (protocol ${ROOT} build)" >&2
    exit 1
  fi
  go run ./e2e/scripts/stop-helper.go || true
}

cleanup() {
  log "Stopping privileged TUN sessions"
  go run ./e2e/scripts/stop-helper.go || true

  log "Cleaning e2e resources"
  k delete namespace kubeloop-e2e \
    --ignore-not-found=true --wait=true --timeout=120s || true
}

run_tests() {
  log "Running TUN e2e against context ${CONTEXT}"
  KUBELOOP_E2E=1 \
  KUBELOOP_E2E_CONTEXT="${CONTEXT}" \
  KUBELOOP_GATEWAY_IMAGE="${IMAGE}" \
  KUBELOOP_SINGBOX_PATH="${ROOT}/${SINGBOX_BIN}" \
  KUBELOOP_E2E_TIMEOUT=30m \
    bash "${ROOT}/e2e/scripts/run-go-test.sh" "$@"
}

main() {
  trap cleanup EXIT
  build_gateway
  load_gateway_image
  restart_gateway
  install_helper
  run_tests "$@"
}

main "$@"
