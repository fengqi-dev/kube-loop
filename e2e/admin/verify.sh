#!/usr/bin/env bash

set -euo pipefail

NAMESPACE="${KUBELOOP_ADMIN_E2E_NAMESPACE:-kubeloop-helm-sqlite}"
RELEASE="${KUBELOOP_ADMIN_E2E_RELEASE:-sqlite}"
PUBLIC_ORIGIN="${KUBELOOP_ADMIN_E2E_PUBLIC_ORIGIN:-https://kubeloop.e2e.invalid}"
LOCAL_PORT="${KUBELOOP_ADMIN_E2E_LOCAL_PORT:-18081}"
CREDENTIAL="${KUBELOOP_ADMIN_E2E_BREAK_GLASS_CREDENTIAL:-}"
CONTROLLER_SERVICE="${RELEASE}-kubeloop-controller"
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/kubeloop-admin-e2e.XXXXXX")"
PORT_FORWARD_PID=""

if [[ -z "${CREDENTIAL}" ]]; then
  echo "KUBELOOP_ADMIN_E2E_BREAK_GLASS_CREDENTIAL is required" >&2
  exit 2
fi
for command in kubectl curl jq; do
  command -v "${command}" >/dev/null 2>&1 || { echo "${command} is required" >&2; exit 2; }
done

cleanup() {
  local status=$?
  set +e
  if [[ -n "${PORT_FORWARD_PID}" ]]; then
    kill "${PORT_FORWARD_PID}" >/dev/null 2>&1
    wait "${PORT_FORWARD_PID}" >/dev/null 2>&1
  fi
  case "${WORK_DIR}" in
    "${TMPDIR:-/tmp}"/kubeloop-admin-e2e.*) rm -rf -- "${WORK_DIR}" ;;
  esac
  exit "${status}"
}
trap cleanup EXIT

kubectl port-forward --namespace "${NAMESPACE}" "service/${CONTROLLER_SERVICE}" \
  "${LOCAL_PORT}:80" >"${WORK_DIR}/port-forward.log" 2>&1 &
PORT_FORWARD_PID=$!
BASE_URL="http://127.0.0.1:${LOCAL_PORT}"
for _ in $(seq 1 60); do
  if curl --silent --fail "${BASE_URL}/.well-known/kubeloop" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "${PORT_FORWARD_PID}" >/dev/null 2>&1; then
    cat "${WORK_DIR}/port-forward.log" >&2
    exit 1
  fi
  sleep 1
done
curl --silent --show-error --fail "${BASE_URL}/.well-known/kubeloop" >/dev/null

curl --silent --show-error --fail --dump-header "${WORK_DIR}/ui.headers" \
  --output "${WORK_DIR}/ui.html" "${BASE_URL}/api/v2/admin/ui/"
grep -qi '^Content-Security-Policy:.*script-src '\''self'\''' "${WORK_DIR}/ui.headers"
grep -qi '^X-Frame-Options: DENY' "${WORK_DIR}/ui.headers"
if grep -Eq 'https?://[^" ]+' "${WORK_DIR}/ui.html"; then
  echo "management UI contains a remote URL" >&2
  exit 1
fi
echo "Management UI security headers verified"

EXCHANGE_BODY="$(jq -nc --arg credential "${CREDENTIAL}" '{credential:$credential}')"
curl --silent --show-error --fail --dump-header "${WORK_DIR}/exchange.headers" \
  --output "${WORK_DIR}/exchange.json" \
  --header "Origin: ${PUBLIC_ORIGIN}" --header 'Content-Type: application/json' \
  --data "${EXCHANGE_BODY}" "${BASE_URL}/api/v2/admin/sessions/break-glass"
CSRF="$(jq -er '.csrfToken' "${WORK_DIR}/exchange.json")"
COOKIE="$(awk 'BEGIN{IGNORECASE=1} /^Set-Cookie:/ {sub(/^[^:]+:[[:space:]]*/, ""); split($0, parts, ";"); print parts[1]; exit}' "${WORK_DIR}/exchange.headers" | tr -d '\r')"
if [[ -z "${COOKIE}" ]]; then
  echo "management exchange did not issue a session cookie" >&2
  exit 1
fi
echo "Break-glass Management Session established"

admin_get() {
  curl --silent --show-error --fail --header "Cookie: ${COOKIE}" "${BASE_URL}/api/v2/admin$1"
}

admin_post() {
  local path=$1 etag=$2 key=$3 body=$4 output=$5
  local -a headers=(
    --header "Cookie: ${COOKIE}"
    --header "Origin: ${PUBLIC_ORIGIN}"
    --header 'Content-Type: application/json'
    --header "X-KubeLoop-CSRF: ${CSRF}"
    --header "Idempotency-Key: ${key}"
  )
  if [[ -n "${etag}" ]]; then
    headers+=(--header "If-Match: \"${etag}\"")
  fi
  curl --silent --show-error --output "${output}" --write-out '%{http_code}' \
    "${headers[@]}" --data "${body}" "${BASE_URL}/api/v2/admin${path}"
}

admin_get /status | jq -e '.storage.backend == "sqlite" and (.storage.schemaVersion >= 11)' >/dev/null
admin_get /capabilities | jq -e '.authenticationType == "break-glass" and (.capabilities | length > 0)' >/dev/null

NO_CSRF_STATUS="$(curl --silent --output "${WORK_DIR}/csrf.json" --write-out '%{http_code}' \
  --header "Cookie: ${COOKIE}" --header "Origin: ${PUBLIC_ORIGIN}" \
  --header 'Content-Type: application/json' --header 'If-Match: "0"' \
  --header 'Idempotency-Key: admin-e2e-no-csrf-0001' \
  --data '{"spec":{"version":1,"assignments":[]},"checks":[],"reason":"verify csrf rejection"}' \
  "${BASE_URL}/api/v2/admin/policy/dry-run")"
[[ "${NO_CSRF_STATUS}" == "403" ]]
echo "CSRF rejection verified"

# Seed a real Principal/Token Family through the installed anonymous provider;
# the management operation below must persist revocation before it returns.
LOGIN="$(curl --silent --show-error --fail --header 'Content-Type: application/json' \
  --data '{"deviceId":"admin-revocation-e2e"}' "${BASE_URL}/auth/anonymous/audit/login")"
ACCESS_TOKEN="$(jq -er '.accessToken' <<<"${LOGIN}")"
JWT_PAYLOAD="$(cut -d. -f2 <<<"${ACCESS_TOKEN}" | tr '_-' '/+')"
case $((${#JWT_PAYLOAD} % 4)) in 2) JWT_PAYLOAD="${JWT_PAYLOAD}==" ;; 3) JWT_PAYLOAD="${JWT_PAYLOAD}=" ;; esac
PRINCIPAL_ID="$(printf '%s' "${JWT_PAYLOAD}" | base64 --decode | jq -er '.sub')"
echo "Revocation Principal seeded"

SPEC_ONE="$(jq -nc --arg subject '11111111-1111-4111-8111-111111111111' \
  '{version:1,assignments:[{id:"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",role:"platform-admin",subjects:[$subject]}]}')"
SPEC_TWO="$(jq -nc --arg subject '22222222-2222-4222-8222-222222222222' \
  '{version:1,assignments:[{id:"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",role:"platform-admin",subjects:[$subject]}]}')"
KEY_ONE='admin-e2e-policy-draft-0001'
KEY_TWO='admin-e2e-policy-draft-0002'
BODY_ONE="$(jq -nc --argjson spec "${SPEC_ONE}" '{spec:$spec,reason:"publish primary platform administrators"}')"
BODY_TWO="$(jq -nc --argjson spec "${SPEC_TWO}" '{spec:$spec,reason:"publish secondary platform administrators"}')"
STATUS_ONE="$(admin_post /policy/drafts 0 "${KEY_ONE}" "${BODY_ONE}" "${WORK_DIR}/draft-one.json")"
STATUS_TWO="$(admin_post /policy/drafts 0 "${KEY_TWO}" "${BODY_TWO}" "${WORK_DIR}/draft-two.json")"
if [[ "${STATUS_ONE}" != "201" || "${STATUS_TWO}" != "201" ]]; then
  echo "policy draft status: ${STATUS_ONE}/${STATUS_TWO}" >&2
  jq -c '{error}' "${WORK_DIR}/draft-one.json" "${WORK_DIR}/draft-two.json" >&2 || true
  exit 1
fi
echo "Concurrent policy candidates created"
CHANGE_ONE="$(jq -er '.changeId' "${WORK_DIR}/draft-one.json")"
CHANGE_TWO="$(jq -er '.changeId' "${WORK_DIR}/draft-two.json")"
REVISION_ONE="$(jq -er '.revision' "${WORK_DIR}/draft-one.json")"
REVISION_TWO="$(jq -er '.revision' "${WORK_DIR}/draft-two.json")"

admin_post "/policy/changes/${CHANGE_ONE}/publish" 0 "${KEY_ONE}" \
  '{"reason":"publish primary platform administrators"}' "${WORK_DIR}/publish-one.json" >"${WORK_DIR}/publish-one.status" &
PID_ONE=$!
admin_post "/policy/changes/${CHANGE_TWO}/publish" 0 "${KEY_TWO}" \
  '{"reason":"publish secondary platform administrators"}' "${WORK_DIR}/publish-two.json" >"${WORK_DIR}/publish-two.status" &
PID_TWO=$!
wait "${PID_ONE}"
wait "${PID_TWO}"
PUBLISH_STATUSES="$(sort "${WORK_DIR}/publish-one.status" "${WORK_DIR}/publish-two.status" | tr '\n' ' ' | sed 's/ $//')"
[[ "${PUBLISH_STATUSES}" == "200 412" ]]
echo "Concurrent policy publish CAS verified"
if [[ "$(cat "${WORK_DIR}/publish-one.status")" == "200" ]]; then
  ACTIVE_REVISION="${REVISION_ONE}"
else
  ACTIVE_REVISION="${REVISION_TWO}"
fi
admin_get /policy | jq -e --argjson revision "${ACTIVE_REVISION}" '.active and .revision == $revision and .etag == 1' >/dev/null

REVOKE_STATUS="$(admin_post "/principals/${PRINCIPAL_ID}/revoke" '' 'admin-e2e-revoke-principal-01' \
  '{"reason":"revoke compromised e2e principal"}' "${WORK_DIR}/revoke.json")"
[[ "${REVOKE_STATUS}" == "200" ]]
jq -e --arg principal "${PRINCIPAL_ID}" '.principalId == $principal and .revokedCount >= 1' "${WORK_DIR}/revoke.json" >/dev/null
TOKEN_STATUS="$(curl --silent --output "${WORK_DIR}/revoked-token.json" --write-out '%{http_code}' \
  --header "Authorization: Bearer ${ACCESS_TOKEN}" "${BASE_URL}/api/v2/version")"
[[ "${TOKEN_STATUS}" == "401" ]]

echo "Management security E2E passed (CSP, CSRF, concurrent publish, revocation)"
