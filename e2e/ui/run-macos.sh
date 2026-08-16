#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PROJECT="${ROOT}/e2e/ui/macos/KubeLoopUITests.xcodeproj"
APP_PATH="${KUBELOOP_UI_E2E_APP_PATH:-${ROOT}/build/bin/kube-loop.app}"
ARTIFACTS="${KUBELOOP_UI_E2E_ARTIFACTS:-${ROOT}/build/ui-e2e/macos}"

if [[ "$(uname -s)" != "Darwin" || "${KUBELOOP_UI_E2E:-}" != "1" ]]; then
  echo "native UI E2E requires macOS and KUBELOOP_UI_E2E=1" >&2
  exit 2
fi
for variable in KUBELOOP_UI_E2E_BASE_URL KUBELOOP_UI_E2E_ADMIN_USERNAME KUBELOOP_UI_E2E_ADMIN_PASSWORD KUBELOOP_UI_E2E_PROFILE_PATH KUBELOOP_UI_E2E_SERVICE_ID; do
  if [[ -z "${!variable:-}" ]]; then
    echo "${variable} is required" >&2
    exit 2
  fi
done
if [[ ! -d "${APP_PATH}" ]]; then
  echo "KubeLoop app not found: ${APP_PATH}" >&2
  exit 2
fi

BUNDLE_ID="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "${APP_PATH}/Contents/Info.plist")"
mkdir -p "${ARTIFACTS}"
KUBELOOP_UI_E2E_APP_PATH="${APP_PATH}" \
KUBELOOP_UI_E2E_BUNDLE_ID="${BUNDLE_ID}" \
xcodebuild test \
  -project "${PROJECT}" \
  -scheme KubeLoopUITests \
  -destination 'platform=macOS' \
  -resultBundlePath "${ARTIFACTS}/KubeLoopUITests.xcresult"
