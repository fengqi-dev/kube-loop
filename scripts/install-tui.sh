#!/usr/bin/env bash
# Install the KubeLoop terminal client for macOS or Linux.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install-tui.sh | bash
#   VERSION=v2.1.3 DEST="$HOME/.local/bin" ./scripts/install-tui.sh
set -euo pipefail

REPO="${REPO:-fengqi-dev/kube-loop}"
TAG="${VERSION:-${TAG:-}}"
DEST="${DEST:-${INSTALL_DIR:-${HOME}/.local/bin}}"

case "$(uname -s)" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *)
    echo "unsupported OS: $(uname -s) (use install-tui.ps1 on Windows)" >&2
    exit 1
    ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *)
    echo "unsupported arch: $(uname -m)" >&2
    exit 1
    ;;
esac

if [[ -n "${TAG}" && "${TAG}" != v* ]]; then
  TAG="v${TAG}"
fi
if [[ -z "${TAG}" ]]; then
  release_json="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest")"
  TAG="$(printf '%s' "${release_json}" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
fi
if [[ -z "${TAG}" ]]; then
  echo "could not resolve the latest release tag" >&2
  exit 1
fi

version="${TAG#v}"
asset="kubeloop-tui-${version}-${os}-${arch}.tar.gz"
base_url="https://github.com/${REPO}/releases/download/${TAG}"
temporary="$(mktemp -d "${TMPDIR:-/tmp}/kubeloop-tui.XXXXXX")"
staged=""
cleanup() {
  rm -rf "${temporary}"
  if [[ -n "${staged}" ]]; then
    rm -f "${staged}"
  fi
}
trap cleanup EXIT HUP INT TERM

echo "Downloading ${asset} (${TAG})..."
curl -fsSL -o "${temporary}/${asset}" "${base_url}/${asset}"
curl -fsSL -o "${temporary}/SHA256SUMS" "${base_url}/SHA256SUMS"

expected="$(awk -v name="${asset}" '$2 == name || $2 == "./" name { print $1; exit }' "${temporary}/SHA256SUMS")"
if [[ ! "${expected}" =~ ^[[:xdigit:]]{64}$ ]]; then
  echo "missing checksum for ${asset} in ${TAG}" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${temporary}/${asset}" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "${temporary}/${asset}" | awk '{print $1}')"
fi
if [[ "${actual}" != "${expected}" ]]; then
  echo "checksum mismatch for ${asset}: got ${actual}, want ${expected}" >&2
  exit 1
fi

entries="$(tar -tzf "${temporary}/${asset}" | sed 's#^\./##' | sed '/^$/d')"
if [[ "${entries}" != "kubeloop" ]]; then
  echo "unexpected files in ${asset}: ${entries//$'\n'/, }" >&2
  exit 1
fi

mkdir -p "${DEST}"
staged="${DEST}/.kubeloop.$$.tmp"
tar -xOzf "${temporary}/${asset}" kubeloop >"${staged}"
chmod 0755 "${staged}"
mv -f "${staged}" "${DEST}/kubeloop"
staged=""

echo "Installed ${DEST}/kubeloop"
"${DEST}/kubeloop" version
case ":${PATH}:" in
  *":${DEST}:"*) ;;
  *) echo "Add ${DEST} to PATH to run kubeloop from any terminal." ;;
esac
