#!/usr/bin/env bash
# Update Casks/kubeloop.rb version + sha256 from a GitHub Release (or local dist/).
#
# Usage:
#   VERSION=v1.1.0 ./build/update-homebrew-cask.sh
#   ./build/update-homebrew-cask.sh v1.1.0
#   SHA256SUMS=dist/SHA256SUMS ./build/update-homebrew-cask.sh v1.1.0
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CASK="${CASK:-${ROOT}/Casks/kubeloop.rb}"
REPO="${REPO:-${GITHUB_REPOSITORY:-fengqi-dev/kube-loop}}"

if [[ ! -f "${CASK}" ]]; then
  echo "cask not found: ${CASK}" >&2
  exit 1
fi

version_raw="${1:-${VERSION:-${GITHUB_REF_NAME:-}}}"
if [[ -z "${version_raw}" ]]; then
  echo "usage: VERSION=vX.Y.Z $0" >&2
  exit 1
fi

tag="${version_raw}"
version="${version_raw#v}"
if [[ -z "${version}" ]]; then
  echo "empty version from ${version_raw}" >&2
  exit 1
fi

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/kubeloop-cask.XXXXXX")"
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
  # sha256sum ./* writes "./name"; sha256sum * writes "name".
  line="$(grep -E "[[:space:]](\./)?${name}\$" "${tmpdir}/SHA256SUMS" | head -n1 || true)"
  if [[ -z "${line}" ]]; then
    echo "missing ${name} in SHA256SUMS" >&2
    exit 1
  fi
  awk '{print $1}' <<<"${line}"
}

arm_sha="$(hash_for "kubeloop-desktop-${version}-darwin-arm64.dmg")"
intel_sha="$(hash_for "kubeloop-desktop-${version}-darwin-amd64.dmg")"

python3 - "${CASK}" "${version}" "${arm_sha}" "${intel_sha}" <<'PY'
from pathlib import Path
import re
import sys

cask_path = Path(sys.argv[1])
version, arm_sha, intel_sha = sys.argv[2], sys.argv[3], sys.argv[4]
text = cask_path.read_text()

text, n = re.subn(
    r'^  version ".*"$',
    f'  version "{version}"',
    text,
    count=1,
    flags=re.M,
)
if n != 1:
    raise SystemExit("failed to update version line in cask")

text, n = re.subn(
    r'kubeloop(?:-desktop)?-#\{version\}-darwin-#\{arch\}\.dmg',
    'kubeloop-desktop-#{version}-darwin-#{arch}.dmg',
    text,
    count=1,
)
if n != 1:
    raise SystemExit("failed to update desktop asset URL in cask")

sha_block = (
    "  sha256 arm:   "
    f'"{arm_sha}",\n'
    "         intel: "
    f'"{intel_sha}"'
)
if re.search(r'^  sha256 :no_check$', text, flags=re.M):
    text, n = re.subn(r'^  sha256 :no_check$', sha_block, text, count=1, flags=re.M)
elif re.search(r'^  sha256 arm:', text, flags=re.M):
    text, n = re.subn(
        r'^  sha256 arm:\s+".*",\n\s*intel:\s+".*"$',
        sha_block,
        text,
        count=1,
        flags=re.M,
    )
else:
    raise SystemExit("failed to locate sha256 stanza in cask")

if n != 1:
    raise SystemExit("failed to update sha256 stanza in cask")

cask_path.write_text(text)
print(f"updated {cask_path} -> version {version}")
print(f"  arm64  {arm_sha}")
print(f"  amd64  {intel_sha}")
PY
