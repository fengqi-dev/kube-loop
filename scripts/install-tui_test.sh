#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temporary="$(mktemp -d "${TMPDIR:-/tmp}/kubeloop-tui-installer-test.XXXXXX")"
cleanup() { rm -rf "${temporary}"; }
trap cleanup EXIT HUP INT TERM

mkdir -p "${temporary}/fixture" "${temporary}/fake-bin"
cat >"${temporary}/fixture/kubeloop" <<'SCRIPT'
#!/usr/bin/env sh
if [ "${1:-}" = "version" ]; then
  echo "kubeloop v9.9.9"
  exit 0
fi
exit 1
SCRIPT
chmod 0755 "${temporary}/fixture/kubeloop"
tar -czf "${temporary}/fixture/archive.tar.gz" -C "${temporary}/fixture" kubeloop
checksum="$(sha256sum "${temporary}/fixture/archive.tar.gz" | awk '{print $1}')"
printf '%s  %s\n' "${checksum}" "kubeloop-tui-9.9.9-linux-amd64.tar.gz" >"${temporary}/fixture/SHA256SUMS"

cat >"${temporary}/fake-bin/uname" <<'SCRIPT'
#!/usr/bin/env sh
case "${1:-}" in
  -s) echo Linux ;;
  -m) echo x86_64 ;;
  *) exit 1 ;;
esac
SCRIPT
cat >"${temporary}/fake-bin/curl" <<'SCRIPT'
#!/usr/bin/env sh
set -eu
output=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) output="$2"; shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
case "$url" in
  */releases/latest) printf '%s\n' '{"tag_name":"v9.9.9"}' ;;
  */kubeloop-tui-9.9.9-linux-amd64.tar.gz)
    if [ "${FAKE_CORRUPT:-}" = "1" ]; then
      printf 'corrupt' >"$output"
    else
      cp "${INSTALLER_FIXTURE}/archive.tar.gz" "$output"
    fi
    ;;
  */SHA256SUMS) cp "${INSTALLER_FIXTURE}/SHA256SUMS" "$output" ;;
  *) echo "unexpected URL: $url" >&2; exit 1 ;;
esac
SCRIPT
chmod 0755 "${temporary}/fake-bin/uname" "${temporary}/fake-bin/curl"

install_dir="${temporary}/installed"
PATH="${temporary}/fake-bin:${PATH}" \
  INSTALLER_FIXTURE="${temporary}/fixture" \
  DEST="${install_dir}" \
  bash "${root}/scripts/install-tui.sh" >/dev/null

test -x "${install_dir}/kubeloop"
test "$("${install_dir}/kubeloop" version)" = "kubeloop v9.9.9"

corrupt_dir="${temporary}/corrupt"
if PATH="${temporary}/fake-bin:${PATH}" \
  INSTALLER_FIXTURE="${temporary}/fixture" FAKE_CORRUPT=1 \
  VERSION="v9.9.9" DEST="${corrupt_dir}" \
  bash "${root}/scripts/install-tui.sh" >/dev/null 2>&1; then
  echo "installer accepted a corrupt archive" >&2
  exit 1
fi
test ! -e "${corrupt_dir}/kubeloop"

echo "TUI installer tests passed"
