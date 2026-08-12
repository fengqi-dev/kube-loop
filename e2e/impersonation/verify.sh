#!/usr/bin/env bash

set -euo pipefail

NAMESPACE="${KUBELOOP_IMPERSONATION_E2E_NAMESPACE:-kubeloop-helm-sqlite}"
RELEASE="${KUBELOOP_IMPERSONATION_E2E_RELEASE:-sqlite}"
AUDIT_SOURCE="${KUBELOOP_IMPERSONATION_E2E_AUDIT_SOURCE:-${KUBELOOP_IMPERSONATION_E2E_AUDIT_LOG:-}}"
LOCAL_PORT="${KUBELOOP_IMPERSONATION_E2E_LOCAL_PORT:-18080}"
CONTROL_PLANE_SERVICE="${RELEASE}-kubeloop-control-plane"
CONTROL_PLANE_SERVICE_ACCOUNT="${RELEASE}-kubeloop-control-plane"
RBAC_PREFIX="${RELEASE}-kubeloop-impersonation-e2e"
IMPERSONATED_GROUP="kubeloop:audit-users"
UNMAPPED_GROUP="unmapped-claim"

if [[ -z "${AUDIT_SOURCE}" ]]; then
  echo "KUBELOOP_IMPERSONATION_E2E_AUDIT_SOURCE is required" >&2
  exit 2
fi
for command in kubectl curl jq base64; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "${command} is required" >&2
    exit 2
  fi
done

PORT_FORWARD_PID=""
cleanup() {
  local status=$?
  set +e
  if [[ -n "${PORT_FORWARD_PID}" ]]; then
    kill "${PORT_FORWARD_PID}" >/dev/null 2>&1
    wait "${PORT_FORWARD_PID}" >/dev/null 2>&1
  fi
  kubectl delete clusterrolebinding "${RBAC_PREFIX}-identity" "${RBAC_PREFIX}-access" \
    --ignore-not-found >/dev/null 2>&1
  kubectl delete clusterrole "${RBAC_PREFIX}-identity" "${RBAC_PREFIX}-access" \
    --ignore-not-found >/dev/null 2>&1
  exit "${status}"
}
trap cleanup EXIT

kubectl port-forward --namespace "${NAMESPACE}" "service/${CONTROL_PLANE_SERVICE}" \
  "${LOCAL_PORT}:80" >"${TMPDIR:-/tmp}/kubeloop-impersonation-port-forward.log" 2>&1 &
PORT_FORWARD_PID=$!

CONTROL_PLANE_READY=0
for _ in $(seq 1 60); do
  if curl --silent --show-error --fail "http://127.0.0.1:${LOCAL_PORT}/.well-known/kubeloop" >/dev/null; then
    CONTROL_PLANE_READY=1
    break
  fi
  if ! kill -0 "${PORT_FORWARD_PID}" >/dev/null 2>&1; then
    cat "${TMPDIR:-/tmp}/kubeloop-impersonation-port-forward.log" >&2
    exit 1
  fi
  sleep 1
done
if [[ "${CONTROL_PLANE_READY}" != "1" ]]; then
  echo "Control Plane port-forward did not become ready" >&2
  cat "${TMPDIR:-/tmp}/kubeloop-impersonation-port-forward.log" >&2
  exit 1
fi

LOGIN_RESPONSE="$(curl --silent --show-error --fail \
  --header 'Content-Type: application/json' \
  --data '{"deviceId":"impersonation-audit-device"}' \
  "http://127.0.0.1:${LOCAL_PORT}/auth/anonymous/audit/login")"
ACCESS_TOKEN="$(jq -er '.accessToken' <<<"${LOGIN_RESPONSE}")"
PAYLOAD="$(cut -d. -f2 <<<"${ACCESS_TOKEN}" | tr '_-' '/+')"
case $((${#PAYLOAD} % 4)) in
  2) PAYLOAD="${PAYLOAD}==" ;;
  3) PAYLOAD="${PAYLOAD}=" ;;
esac
PRINCIPAL_ID="$(base64 --decode <<<"${PAYLOAD}" | jq -er '.sub')"
IMPERSONATED_USER="kubeloop:${PRINCIPAL_ID}"

kubectl apply -f - >/dev/null <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: ${RBAC_PREFIX}-identity
rules:
  - apiGroups: [""]
    resources: ["users"]
    verbs: ["impersonate"]
    resourceNames: ["${IMPERSONATED_USER}"]
  - apiGroups: [""]
    resources: ["groups"]
    verbs: ["impersonate"]
    resourceNames: ["${IMPERSONATED_GROUP}"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: ${RBAC_PREFIX}-identity
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: ${RBAC_PREFIX}-identity
subjects:
  - kind: ServiceAccount
    name: ${CONTROL_PLANE_SERVICE_ACCOUNT}
    namespace: ${NAMESPACE}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: ${RBAC_PREFIX}-access
rules:
  - nonResourceURLs: ["/version"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: ${RBAC_PREFIX}-access
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: ${RBAC_PREFIX}-access
subjects:
  - kind: Group
    name: ${IMPERSONATED_GROUP}
    apiGroup: rbac.authorization.k8s.io
EOF

curl --silent --show-error --fail \
  --header "Authorization: Bearer ${ACCESS_TOKEN}" \
  "http://127.0.0.1:${LOCAL_PORT}/api/v2/version" | jq -e . >/dev/null

GATEWAY_IDENTITY="system:serviceaccount:${NAMESPACE}:${CONTROL_PLANE_SERVICE_ACCOUNT}"
audit_event_matches() {
  local audit_events line
  if [[ "${AUDIT_SOURCE}" == "kube-apiserver" ]]; then
    audit_events="$(kubectl logs --namespace kube-system \
      --selector component=kube-apiserver --tail=-1 2>/dev/null || true)"
  else
    audit_events="$(tail -n 1000 "${AUDIT_SOURCE}" 2>/dev/null || true)"
  fi
  while IFS= read -r line; do
    if jq -e \
      --arg gateway "${GATEWAY_IDENTITY}" \
      --arg user "${IMPERSONATED_USER}" \
      --arg group "${IMPERSONATED_GROUP}" \
      --arg unmapped "${UNMAPPED_GROUP}" \
      'select(
        .stage == "ResponseComplete" and
        .verb == "get" and
        (.requestURI == "/version" or (.requestURI | startswith("/version?"))) and
        .responseStatus.code == 200 and
        .user.username == $gateway and
        .impersonatedUser.username == $user and
        ((.impersonatedUser.groups // []) | index($group)) != null and
        ((.impersonatedUser.groups // []) | index($unmapped)) == null
      )' <<<"${line}" >/dev/null 2>&1; then
      return 0
    fi
  done <<<"${audit_events}"
  return 1
}

for _ in $(seq 1 60); do
  if audit_event_matches; then
    echo "Kubernetes audit verified Gateway identity ${GATEWAY_IDENTITY} and final user ${IMPERSONATED_USER}"
    exit 0
  fi
  sleep 1
done

echo "matching Kubernetes impersonation audit event was not found" >&2
if [[ "${AUDIT_SOURCE}" == "kube-apiserver" ]]; then
  kubectl logs --namespace kube-system --selector component=kube-apiserver --tail=100 >&2 || true
else
  tail -n 100 "${AUDIT_SOURCE}" >&2 || true
fi
exit 1
