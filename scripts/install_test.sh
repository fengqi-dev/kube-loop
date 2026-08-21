#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temporary="$(mktemp -d "${TMPDIR:-/tmp}/kubeloop-installer-test.XXXXXX")"
cleanup() { rm -rf "${temporary}"; }
trap cleanup EXIT HUP INT TERM

mkdir -p "${temporary}/fake-bin" "${temporary}/downloads"
cat >"${temporary}/fake-bin/uname" <<'SCRIPT'
#!/usr/bin/env sh
case "${1:-}" in
  -s) echo Linux ;;
  -m) echo x86_64 ;;
  *) exit 1 ;;
esac
SCRIPT
cat >"${temporary}/fake-bin/id" <<'SCRIPT'
#!/usr/bin/env sh
test "${1:-}" = "-u" && echo 0
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
  */releases/tags/v9.9.9)
    printf '%s\n' '{"tag_name": "v9.9.9", "assets": [{"name": "kubeloop-desktop-9.9.9-linux-amd64.deb"}]}'
    ;;
  */kubeloop-desktop-9.9.9-linux-amd64.deb)
    printf 'fixture package' >"$output"
    ;;
  *) echo "unexpected URL: $url" >&2; exit 1 ;;
esac
SCRIPT
cat >"${temporary}/fake-bin/apt-get" <<'SCRIPT'
#!/usr/bin/env sh
printf '%s\n' "$@" >"${APT_ARGS_FILE}"
SCRIPT
chmod 0755 "${temporary}/fake-bin/uname" "${temporary}/fake-bin/id" \
  "${temporary}/fake-bin/curl" "${temporary}/fake-bin/apt-get"

args_file="${temporary}/apt-args"
PATH="${temporary}/fake-bin:${PATH}" \
  APT_ARGS_FILE="${args_file}" APT_LOCK_TIMEOUT=17 \
  VERSION=v9.9.9 PACKAGE=deb DEST="${temporary}/downloads" \
  bash "${root}/scripts/install.sh" >/dev/null

grep -Fxq -- "DPkg::Lock::Timeout=17" "${args_file}"
grep -Fxq -- "install" "${args_file}"
grep -Fxq -- "${temporary}/downloads/kubeloop-desktop-9.9.9-linux-amd64.deb" "${args_file}"

if PATH="${temporary}/fake-bin:${PATH}" \
  APT_ARGS_FILE="${args_file}" APT_LOCK_TIMEOUT=invalid \
  VERSION=v9.9.9 PACKAGE=deb DEST="${temporary}/downloads" \
  bash "${root}/scripts/install.sh" >/dev/null 2>&1; then
  echo "installer accepted an invalid apt lock timeout" >&2
  exit 1
fi

echo "Desktop installer tests passed"
