#!/usr/bin/env bash
# Download the latest KubeLoop desktop release.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.sh | bash
#   VERSION=v1.1.0 ./scripts/install.sh
#   PACKAGE=deb|rpm|tarball ./scripts/install.sh   # Linux only
#   APT_LOCK_TIMEOUT=600 ./scripts/install.sh      # Debian/Ubuntu only
#
# macOS: downloads the .dmg into DEST (default: $PWD)
# Linux: prefers .deb/.rpm when available, otherwise extracts the .tar.gz
set -euo pipefail

REPO="${REPO:-fengqi-dev/kube-loop}"
DEST="${DEST:-$PWD}"
TAG="${VERSION:-${TAG:-}}"
PACKAGE="${PACKAGE:-auto}"
APT_LOCK_TIMEOUT="${APT_LOCK_TIMEOUT:-300}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "${os}" in
  linux) os=linux ;;
  darwin) os=darwin ;;
  *)
    echo "unsupported OS: $(uname -s) (use install.ps1 on Windows)" >&2
    exit 1
    ;;
esac

arch="$(uname -m)"
case "${arch}" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *)
    echo "unsupported arch: $(uname -m)" >&2
    exit 1
    ;;
esac

api="https://api.github.com/repos/${REPO}/releases"
if [[ -n "${TAG}" ]]; then
  json="$(curl -fsSL "${api}/tags/${TAG}")"
else
  json="$(curl -fsSL "${api}/latest")"
  TAG="$(printf '%s' "${json}" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
fi
if [[ -z "${TAG}" ]]; then
  echo "could not resolve release tag" >&2
  exit 1
fi

ver="${TAG#v}"

asset_exists() {
  printf '%s' "${json}" | grep -Fq "\"name\": \"${1}\""
}

require_asset() {
  local name="$1"
  if ! asset_exists "${name}"; then
    echo "missing ${name} in ${TAG}" >&2
    exit 1
  fi
  printf '%s' "${name}"
}

prefer_asset() {
  local preferred="$1"
  local legacy="$2"
  if asset_exists "${preferred}"; then
    printf '%s' "${preferred}"
    return
  fi
  require_asset "${legacy}"
}

download_asset() {
  local asset="$1"
  local out="$2"
  local url="https://github.com/${REPO}/releases/download/${TAG}/${asset}"
  echo "Downloading ${asset} (${TAG})..."
  curl -fsSL -o "${out}" "${url}"
}

mkdir -p "${DEST}"

if [[ "${os}" == "darwin" ]]; then
  asset="$(prefer_asset "kubeloop-desktop-${ver}-darwin-${arch}.dmg" "kubeloop-${ver}-darwin-${arch}.dmg")"
  out="${DEST}/${asset}"
  download_asset "${asset}" "${out}"
  echo "Saved ${out}"
  echo "Open the DMG and drag KubeLoop.app into Applications."
  exit 0
fi

detect_linux_package() {
  case "${PACKAGE}" in
    deb|rpm|tarball) printf '%s' "${PACKAGE}" ;;
    auto)
      if command -v dpkg >/dev/null 2>&1 || [[ -f /etc/debian_version ]]; then
        printf 'deb'
      elif command -v rpm >/dev/null 2>&1 || command -v dnf >/dev/null 2>&1 || command -v yum >/dev/null 2>&1; then
        printf 'rpm'
      else
        printf 'tarball'
      fi
      ;;
    *)
      echo "PACKAGE must be auto, deb, rpm, or tarball (got ${PACKAGE})" >&2
      exit 1
      ;;
  esac
}

install_deb() {
  local asset out
  asset="$(prefer_asset "kubeloop-desktop-${ver}-linux-${arch}.deb" "kubeloop-${ver}-linux-${arch}.deb")"
  out="${DEST}/${asset}"
  download_asset "${asset}" "${out}"
  echo "Installing ${out}..."
  if ! command -v apt-get >/dev/null 2>&1; then
    echo "apt-get is required to install ${asset} and resolve its dependencies" >&2
    exit 1
  fi
  if [[ ! "${APT_LOCK_TIMEOUT}" =~ ^[0-9]+$ ]]; then
    echo "APT_LOCK_TIMEOUT must be a non-negative number of seconds (got ${APT_LOCK_TIMEOUT})" >&2
    exit 1
  fi
  echo "Waiting up to ${APT_LOCK_TIMEOUT}s for other apt/dpkg operations to finish..."
  if command -v sudo >/dev/null 2>&1 && [[ "$(id -u)" -ne 0 ]]; then
    sudo env DEBIAN_FRONTEND=noninteractive apt-get \
      -o "DPkg::Lock::Timeout=${APT_LOCK_TIMEOUT}" \
      install --yes --no-install-recommends "${out}"
  else
    env DEBIAN_FRONTEND=noninteractive apt-get \
      -o "DPkg::Lock::Timeout=${APT_LOCK_TIMEOUT}" \
      install --yes --no-install-recommends "${out}"
  fi
  echo "Installed kubeloop from ${asset}"
}

install_rpm() {
  local asset out package_manager
  asset="$(prefer_asset "kubeloop-desktop-${ver}-linux-${arch}.rpm" "kubeloop-${ver}-linux-${arch}.rpm")"
  out="${DEST}/${asset}"
  download_asset "${asset}" "${out}"
  echo "Installing ${out}..."
  if command -v dnf >/dev/null 2>&1; then
    package_manager="dnf"
  elif command -v yum >/dev/null 2>&1; then
    package_manager="yum"
  else
    echo "dnf or yum is required to install ${asset} and resolve its dependencies" >&2
    exit 1
  fi
  if command -v sudo >/dev/null 2>&1 && [[ "$(id -u)" -ne 0 ]]; then
    sudo "${package_manager}" install --assumeyes "${out}"
  else
    "${package_manager}" install --assumeyes "${out}"
  fi
  echo "Installed kubeloop from ${asset}"
}

install_tarball() {
  local asset tmp
  asset="$(require_asset "kubeloop-desktop-${ver}-linux-${arch}.tar.gz")"
  tmp="$(mktemp "${TMPDIR:-/tmp}/kubeloop.XXXXXX")"
  cleanup() { rm -f "${tmp}"; }
  trap cleanup EXIT
  download_asset "${asset}" "${tmp}"
  echo "Extracting into ${DEST}..."
  tar -xzf "${tmp}" -C "${DEST}"
  trap - EXIT
  cleanup
  if [[ -x "${DEST}/KubeLoop" ]]; then
    echo "Installed binary: ${DEST}/KubeLoop"
  else
    echo "Extracted into ${DEST}"
  fi
}

case "$(detect_linux_package)" in
  deb) install_deb ;;
  rpm) install_rpm ;;
  tarball) install_tarball ;;
esac
