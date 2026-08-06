#!/usr/bin/env bash
# Shared implementation for run-local-linux.sh and run-local-macos.sh.
# The public wrappers set KUBELOOP_LOCAL_OS and keep platform defaults explicit.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

EXPECTED_OS="${KUBELOOP_LOCAL_OS:-}"
ACTUAL_OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "${ACTUAL_OS}" in
  linux) ACTUAL_OS=linux ;;
  darwin) ACTUAL_OS=darwin ;;
  *)
    echo "unsupported host OS: $(uname -s)" >&2
    exit 2
    ;;
esac
if [[ -n "${EXPECTED_OS}" && "${ACTUAL_OS}" != "${EXPECTED_OS}" ]]; then
  echo "this script targets ${EXPECTED_OS}, current host is ${ACTUAL_OS}" >&2
  exit 2
fi

if [[ "${ACTUAL_OS}" == "darwin" ]]; then
  CONTEXT="docker-desktop"
else
  CONTEXT="minikube"
fi
GATEWAY_IMAGE=""
TEST_TIMEOUT="25m"
SKIP_BUILD=0
SKIP_PLATFORM=0
KEEP_RESOURCES=0
IGNORE_NETWORK_PREFLIGHT=0
MINIKUBE_PROFILE="${KUBELOOP_MINIKUBE_PROFILE:-minikube}"

usage() {
  cat <<'EOF'
Usage:
  run-local-linux.sh [options]
  run-local-macos.sh [options]

Options:
  --context NAME               kubeconfig context (Linux: minikube; macOS: docker-desktop)
  --gateway-image IMAGE        Gateway image tag
  --timeout DURATION           Go E2E timeout (default: 25m)
  --skip-build                 Reuse existing Helper, sing-box, and Gateway image
  --skip-platform              Skip privileged Helper platform tests
  --keep-resources             Keep Kubernetes resources and Gateway image
  --ignore-network-preflight   Continue when a platform network preflight fails
  --minikube-profile NAME      Profile used by minikube image build/load
  -h, --help                   Show this help

Notes:
  When --context is minikube*, host Docker is optional; the Gateway image is
  built with "minikube image build".
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --context)
      CONTEXT="${2:?--context requires a value}"
      shift 2
      ;;
    --gateway-image)
      GATEWAY_IMAGE="${2:?--gateway-image requires a value}"
      shift 2
      ;;
    --timeout)
      TEST_TIMEOUT="${2:?--timeout requires a value}"
      shift 2
      ;;
    --skip-build)
      SKIP_BUILD=1
      shift
      ;;
    --skip-platform)
      SKIP_PLATFORM=1
      shift
      ;;
    --keep-resources)
      KEEP_RESOURCES=1
      shift
      ;;
    --ignore-network-preflight)
      IGNORE_NETWORK_PREFLIGHT=1
      shift
      ;;
    --minikube-profile)
      MINIKUBE_PROFILE="${2:?--minikube-profile requires a value}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ "${SKIP_BUILD}" -eq 1 && -z "${GATEWAY_IMAGE}" ]]; then
  echo "--skip-build requires --gateway-image so the existing image is unambiguous" >&2
  exit 2
fi
if [[ -z "${GATEWAY_IMAGE}" ]]; then
  GATEWAY_IMAGE="kube-loop-gateway:e2e-local-$$"
fi

MAIN_LOG="${ROOT}/e2e-local.log"
PLATFORM_LOG="${ROOT}/e2e-platform.log"
PLATFORM_TEST="${ROOT}/build/bin/helper-platform-e2e.test"
SINGBOX="${ROOT}/build/bin/sing-box"
HELPER_SOURCE="${ROOT}/build/embedded/kubeloop-helper"
GATEWAY_BINARY="${ROOT}/build/bin/kube-loop-gateway"
CACHE="$(mktemp -d "${TMPDIR:-/tmp}/kubeloop-e2e-cache.XXXXXX")"
export GOCACHE="${CACHE}"
: >"${MAIN_LOG}"
: >"${PLATFORM_LOG}"

MAIN_PACKAGES=(
  "./e2e/connect"
  "./e2e/dns"
  "./e2e/exchange"
  "./e2e/harness"
  "./e2e/mirror"
  "./e2e/portfwd"
  "./e2e/preview"
)

MAIN_EXIT=1
PLATFORM_EXIT=1
IMAGE_BUILT=0
HELPER_EXISTED=0
SUDO_KEEPALIVE_PID=""

log() {
  printf '==> %s\n' "$*"
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "required command is missing: $1" >&2
    return 1
  fi
}

kube() {
  kubectl --context="${CONTEXT}" "$@"
}

refresh_sudo() {
  if [[ "$(id -u)" -eq 0 ]]; then
    return 0
  fi
  require_command sudo
  if sudo -n true >/dev/null 2>&1; then
    log "Cached sudo credentials available for privileged Helper tests"
  else
    log "Refreshing sudo credentials for privileged Helper tests"
    # Interactive password prompt when available; otherwise Helper install
    # falls back to ensure-helper.go (macOS admin dialog).
    if [[ -t 0 ]]; then
      sudo -v
    else
      log "No TTY for sudo; will use non-interactive Helper install fallback"
      return 0
    fi
  fi
  (
    while sudo -n true >/dev/null 2>&1; do
      sleep 45
    done
  ) &
  SUDO_KEEPALIVE_PID="$!"
}

show_network_requirements() {
  local pod_cidrs service_ip
  pod_cidrs="$(kube get nodes \
    -o jsonpath='{range .items[*]}{range .spec.podCIDRs[*]}{.}{" "}{end}{end}')"
  service_ip="$(kube get service kubernetes -n default \
    -o jsonpath='{.spec.clusterIP}')"
  printf '\nNetwork preflight\n'
  printf '  Context: %s\n' "${CONTEXT}"
  printf '  Pod CIDR(s): %s\n' "${pod_cidrs:-unknown}"
  printf '  Kubernetes Service IP: %s\n' "${service_ip:-unknown}"

  if [[ "${ACTUAL_OS}" == "linux" && ! -c /dev/net/tun ]]; then
    if [[ "${IGNORE_NETWORK_PREFLIGHT}" -eq 0 ]]; then
      echo "error: /dev/net/tun is unavailable; use --ignore-network-preflight to continue" >&2
      return 1
    fi
    echo "  Warning: /dev/net/tun is unavailable"
  fi
  if [[ "${ACTUAL_OS}" == "darwin" ]]; then
    printf '  Active utun interfaces: %s\n' \
      "$(ifconfig -l 2>/dev/null | tr ' ' '\n' | grep '^utun' | tr '\n' ' ' || true)"
  else
    printf '  Active TUN interfaces: %s\n' \
      "$(ip -o link show 2>/dev/null | awk -F': ' '$2 ~ /^(tun|tap)/ {print $2}' | tr '\n' ' ' || true)"
  fi
  printf '\n'
}

install_helper() {
  if [[ "$(id -u)" -eq 0 ]]; then
    go run ./e2e/scripts/manage-helper.go \
      --operation install \
      --source "${HELPER_SOURCE}" \
      --sing-box "${SINGBOX}"
  elif sudo -n true >/dev/null 2>&1; then
    go run ./e2e/scripts/manage-helper.go \
      --operation install \
      --source "${HELPER_SOURCE}" \
      --sing-box "${SINGBOX}" \
      --elevate
  else
    # macOS: osascript admin dialog; Linux: may still require passwordless sudo.
    go run ./e2e/scripts/ensure-helper.go
  fi
  go run ./e2e/scripts/helper-ping.go
  go run ./e2e/scripts/stop-helper.go
}

uninstall_temporary_helper() {
  if [[ "${HELPER_EXISTED}" -eq 1 || ! -x "${HELPER_SOURCE}" ]]; then
    return 0
  fi
  local elevate=()
  if [[ "$(id -u)" -ne 0 ]]; then
    elevate=(--elevate)
  fi
  go run ./e2e/scripts/manage-helper.go \
    --operation uninstall \
    --source "${HELPER_SOURCE}" \
    "${elevate[@]}" || true
}

using_minikube() {
  [[ "${CONTEXT}" == "minikube" || "${CONTEXT}" == minikube-* ]]
}

host_docker_ok() {
  command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1
}

load_minikube_image_if_needed() {
  if using_minikube && host_docker_ok; then
    require_command minikube
    log "Loading Gateway image into minikube profile ${MINIKUBE_PROFILE}"
    minikube -p "${MINIKUBE_PROFILE}" image load "${GATEWAY_IMAGE}"
  fi
}

build_gateway_image() {
  log "Building Gateway image ${GATEWAY_IMAGE}"
  mkdir -p build/bin
  CGO_ENABLED=0 GOOS=linux GOARCH="$(go env GOARCH)" \
    go build -trimpath -ldflags="-s -w" \
      -o "${GATEWAY_BINARY}" ./cmd/kubeloop-gateway
  chmod 755 "${GATEWAY_BINARY}"

  if using_minikube; then
    require_command minikube
    log "Building Gateway image inside minikube (no host Docker required)"
    # minikube may exit 0 even when buildkit prints ERROR; verify the tag exists.
    minikube -p "${MINIKUBE_PROFILE}" image build \
      -t "${GATEWAY_IMAGE}" \
      -f build/gateway.e2e.Dockerfile \
      . || true
    if ! minikube -p "${MINIKUBE_PROFILE}" image ls |
      grep -F "${GATEWAY_IMAGE}" >/dev/null 2>&1; then
      echo "error: minikube image build failed for ${GATEWAY_IMAGE}" >&2
      return 1
    fi
  elif host_docker_ok; then
    docker build -t "${GATEWAY_IMAGE}" -f build/gateway.e2e.Dockerfile .
  else
    echo "error: host Docker unavailable and context ${CONTEXT} is not minikube" >&2
    echo "hint: pass --context minikube, or start Docker Desktop / a Docker daemon" >&2
    return 1
  fi
  IMAGE_BUILT=1
}

build_artifacts() {
  log "Building Helper and sing-box for $(go env GOOS)/$(go env GOARCH)"
  bash ./build/bundle-helper.sh
  go run ./e2e/scripts/ensure-singbox.go
  install_helper
  build_gateway_image
  load_minikube_image_if_needed
}

run_main_tests() {
  log "E2E test list"
  go test -tags=e2e ./e2e/... -list '^Test'

  export KUBELOOP_E2E=1
  export KUBELOOP_E2E_CONTEXT="${CONTEXT}"
  export KUBELOOP_GATEWAY_IMAGE="${GATEWAY_IMAGE}"
  export KUBELOOP_SINGBOX_PATH="${SINGBOX}"

  log "Running TUN/Kubernetes E2E"
  set +e
  go test -tags=e2e "${MAIN_PACKAGES[@]}" \
    -count=1 \
    -timeout="${TEST_TIMEOUT}" \
    -parallel=1 \
    -p=1 \
    -v 2>&1 | tee "${MAIN_LOG}"
  MAIN_EXIT="${PIPESTATUS[0]}"
  set -e
}

run_platform_tests() {
  if [[ "${SKIP_PLATFORM}" -eq 1 ]]; then
    PLATFORM_EXIT=0
    return 0
  fi
  log "Running ${ACTUAL_OS} Helper platform E2E"
  mkdir -p "$(dirname "${PLATFORM_TEST}")"
  if ! go test -tags=e2e -c -o "${PLATFORM_TEST}" ./e2e/platform; then
    PLATFORM_EXIT=1
    return 0
  fi
  set +e
  KUBELOOP_PLATFORM_E2E=1 \
    "${PLATFORM_TEST}" \
      -test.v \
      -test.timeout=5m \
      -test.skip '^TestPlatformDNSApplyAndRestore$' \
      2>&1 | tee "${PLATFORM_LOG}"
  local user_exit="${PIPESTATUS[0]}"
  sudo -n env KUBELOOP_PLATFORM_E2E=1 \
    "${PLATFORM_TEST}" \
      -test.v \
      -test.timeout=5m \
      -test.run '^TestPlatformDNSApplyAndRestore$' \
      2>&1 | tee -a "${PLATFORM_LOG}"
  local privileged_exit="${PIPESTATUS[0]}"
  if [[ "${user_exit}" -ne 0 ]]; then
    PLATFORM_EXIT="${user_exit}"
  else
    PLATFORM_EXIT="${privileged_exit}"
  fi
  set -e
}

print_summary() {
  printf '\n==> E2E summary\n'
  local paths=("${MAIN_LOG}")
  if [[ "${SKIP_PLATFORM}" -eq 0 ]]; then
    paths+=("${PLATFORM_LOG}")
  fi
  local summary
  summary="$(
    awk '
      /^--- (PASS|FAIL|SKIP): / {
        status=$2
        sub(":", "", status)
        test=$3
        duration=$4
        gsub(/[()]/, "", duration)
        printf "%-52s %-6s %s\n", test, status, duration
        count[status]++
        total++
      }
      END {
        printf "\nPASS=%d FAIL=%d SKIP=%d TOTAL=%d\n",
          count["PASS"], count["FAIL"], count["SKIP"], total
      }
    ' "${paths[@]}" 2>/dev/null || true
  )"
  if [[ -z "${summary}" ]]; then
    echo "No completed tests were parsed."
  else
    printf '%-52s %-6s %s\n' "Test" "Status" "Duration"
    printf '%s\n' "${summary}"
  fi
  echo "Main log: ${MAIN_LOG}"
  if [[ "${SKIP_PLATFORM}" -eq 0 ]]; then
    echo "Platform log: ${PLATFORM_LOG}"
  fi
}

cleanup() {
  set +e
  log "Stopping privileged TUN sessions"
  go run ./e2e/scripts/stop-helper.go
  if [[ "${KEEP_RESOURCES}" -eq 0 ]]; then
    log "Cleaning E2E resources"
    kube delete namespace kubeloop-e2e \
      --ignore-not-found=true --wait=false
    kube -n kubeloop-system delete deployment kubeloop-gateway \
      --ignore-not-found=true --wait=true --timeout=60s
    kube -n kubeloop-system wait \
      --for=delete pod \
      -l app.kubernetes.io/name=kubeloop-gateway \
      --timeout=60s >/dev/null 2>&1 || true
    kube -n kubeloop-system delete service kubeloop-gateway \
      --ignore-not-found=true --wait=false
    if [[ "${IMAGE_BUILT}" -eq 1 ]]; then
      if using_minikube && command -v minikube >/dev/null 2>&1; then
        for attempt in 1 2 3; do
          if minikube -p "${MINIKUBE_PROFILE}" image rm "${GATEWAY_IMAGE}"; then
            break
          fi
          if [[ "${attempt}" -lt 3 ]]; then
            sleep 2
          fi
        done
      elif host_docker_ok; then
        docker image rm "${GATEWAY_IMAGE}" --force || true
      fi
    fi
    uninstall_temporary_helper
  fi
  if [[ -n "${SUDO_KEEPALIVE_PID}" ]]; then
    kill "${SUDO_KEEPALIVE_PID}" >/dev/null 2>&1 || true
  fi
  case "${CACHE}" in
    "${TMPDIR:-/tmp}"/kubeloop-e2e-cache.*)
      rm -rf -- "${CACHE}"
      ;;
  esac
  set -e
}

finish() {
  local exit_code=$?
  trap - EXIT
  cleanup
  print_summary
  exit "${exit_code}"
}
trap finish EXIT

require_command go
require_command kubectl
if using_minikube; then
  require_command minikube
elif ! host_docker_ok; then
  echo "required command is missing: docker (or use --context minikube)" >&2
  exit 1
fi
if [[ "${ACTUAL_OS}" == "darwin" ]]; then
  require_command ifconfig
else
  require_command ip
fi

if host_docker_ok; then
  docker version
elif using_minikube; then
  log "Host Docker unavailable; Gateway image will be built with minikube"
fi
kube cluster-info
show_network_requirements
refresh_sudo

INSTALLED_HELPER=""
if [[ "${ACTUAL_OS}" == "darwin" ]]; then
  INSTALLED_HELPER="/Library/PrivilegedHelperTools/dev.fengqi.kubeloop.helper.dev"
else
  INSTALLED_HELPER="/usr/local/libexec/kubeloop-helper-dev"
fi
if [[ -e "${INSTALLED_HELPER}" ]] ||
  go run ./e2e/scripts/helper-ping.go >/dev/null 2>&1; then
  HELPER_EXISTED=1
fi

if [[ "${SKIP_BUILD}" -eq 0 ]]; then
  build_artifacts
elif [[ ! -x "${SINGBOX}" ]]; then
  echo "sing-box is missing: ${SINGBOX}" >&2
  exit 1
else
  go run ./e2e/scripts/helper-ping.go
  load_minikube_image_if_needed
fi

run_main_tests
run_platform_tests

if [[ "${MAIN_EXIT}" -ne 0 || "${PLATFORM_EXIT}" -ne 0 ]]; then
  exit 1
fi
exit 0
