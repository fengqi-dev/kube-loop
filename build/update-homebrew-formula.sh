#!/usr/bin/env bash
# Generate Formula/kubeloop.rb from native TUI assets in a GitHub Release.
#
# Usage:
#   VERSION=v2.1.0 ./build/update-homebrew-formula.sh
#   SHA256SUMS=dist/SHA256SUMS ./build/update-homebrew-formula.sh v2.1.0
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
FORMULA="${FORMULA:-${ROOT}/Formula/kubeloop.rb}"
REPO="${REPO:-${GITHUB_REPOSITORY:-fengqi-dev/kube-loop}}"

version_raw="${1:-${VERSION:-${GITHUB_REF_NAME:-}}}"
if [[ -z "${version_raw}" ]]; then
  echo "usage: VERSION=vX.Y.Z $0" >&2
  exit 1
fi

tag="${version_raw}"
version="${version_raw#v}"
if [[ ! "${version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "stable semantic version required: ${version_raw}" >&2
  exit 1
fi

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/kubeloop-formula.XXXXXX")"
cleanup() { rm -rf "${tmpdir}"; }
trap cleanup EXIT

sums_file="${SHA256SUMS:-}"
if [[ -n "${sums_file}" && -f "${sums_file}" ]]; then
  cp "${sums_file}" "${tmpdir}/SHA256SUMS"
elif [[ -f "${ROOT}/dist/SHA256SUMS" ]]; then
  cp "${ROOT}/dist/SHA256SUMS" "${tmpdir}/SHA256SUMS"
else
  if ! command -v gh >/dev/null 2>&1; then
    echo "gh is required to download SHA256SUMS from ${REPO}@${tag}" >&2
    exit 1
  fi
  gh release download "${tag}" \
    --repo "${REPO}" \
    --pattern "SHA256SUMS" \
    --dir "${tmpdir}"
fi

hash_for() {
  local name="$1"
  local line
  line="$(grep -E "[[:space:]](\./)?${name}\$" "${tmpdir}/SHA256SUMS" | head -n1 || true)"
  if [[ -z "${line}" ]]; then
    echo "missing ${name} in SHA256SUMS" >&2
    exit 1
  fi
  awk '{print $1}' <<<"${line}"
}

darwin_arm64_sha="$(hash_for "kubeloop-tui-${version}-darwin-arm64.tar.gz")"
darwin_amd64_sha="$(hash_for "kubeloop-tui-${version}-darwin-amd64.tar.gz")"
linux_arm64_sha="$(hash_for "kubeloop-tui-${version}-linux-arm64.tar.gz")"
linux_amd64_sha="$(hash_for "kubeloop-tui-${version}-linux-amd64.tar.gz")"

mkdir -p "$(dirname "${FORMULA}")"
cat >"${FORMULA}" <<EOF
class Kubeloop < Formula
  desc "K9s-style terminal client for KubeLoop"
  homepage "https://fengqi-dev.github.io/kube-loop/"
  version "${version}"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/${REPO}/releases/download/${tag}/kubeloop-tui-${version}-darwin-arm64.tar.gz"
      sha256 "${darwin_arm64_sha}"
    else
      url "https://github.com/${REPO}/releases/download/${tag}/kubeloop-tui-${version}-darwin-amd64.tar.gz"
      sha256 "${darwin_amd64_sha}"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/${REPO}/releases/download/${tag}/kubeloop-tui-${version}-linux-arm64.tar.gz"
      sha256 "${linux_arm64_sha}"
    else
      url "https://github.com/${REPO}/releases/download/${tag}/kubeloop-tui-${version}-linux-amd64.tar.gz"
      sha256 "${linux_amd64_sha}"
    end
  end

  def install
    libexec.install "kubeloop"
    bin.write_exec_script libexec/"kubeloop"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/kubeloop --version")
  end
end
EOF

echo "updated ${FORMULA} -> version ${version}"
echo "  darwin-arm64 ${darwin_arm64_sha}"
echo "  darwin-amd64 ${darwin_amd64_sha}"
echo "  linux-arm64  ${linux_arm64_sha}"
echo "  linux-amd64  ${linux_amd64_sha}"
