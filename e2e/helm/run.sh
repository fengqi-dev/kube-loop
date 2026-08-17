#!/usr/bin/env bash

set -euo pipefail

if [[ "${KUBELOOP_HELM_E2E:-}" != "1" ]]; then
  echo "KUBELOOP_HELM_E2E=1 is required; this test creates and removes cluster resources" >&2
  exit 2
fi

EXPECTED_CONTEXT="${KUBELOOP_HELM_E2E_CONTEXT:-kubeloop-helm-e2e}"
CURRENT_CONTEXT="$(kubectl config current-context)"
if [[ "${CURRENT_CONTEXT}" != "${EXPECTED_CONTEXT}" ]]; then
  echo "refusing to run against context ${CURRENT_CONTEXT}; expected ${EXPECTED_CONTEXT}" >&2
  exit 2
fi

for command in helm kubectl openssl jq; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "${command} is required" >&2
    exit 2
  fi
done

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHART="${ROOT}/charts/kubeloop"
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/kubeloop-helm-e2e.XXXXXX")"

SQLITE_RELEASE="sqlite"
SQLITE_NAMESPACE="kubeloop-helm-sqlite"
POSTGRES_RELEASE="postgresql"
POSTGRES_NAMESPACE="kubeloop-helm-postgresql"
CRD="trafficbindings.traffic.kubeloop.io"

CONTROL_PLANE_IMAGE="${KUBELOOP_HELM_E2E_CONTROL_PLANE_IMAGE:-kubeloop/control-plane:e2e}"
DATA_PLANE_IMAGE="${KUBELOOP_HELM_E2E_DATA_PLANE_IMAGE:-kubeloop/gateway:e2e}"
OPERATOR_IMAGE="${KUBELOOP_HELM_E2E_OPERATOR_IMAGE:-kubeloop/operator:e2e}"
POSTGRES_IMAGE="${KUBELOOP_HELM_E2E_POSTGRES_IMAGE:-postgres:17-alpine}"
BUSYBOX_IMAGE="${KUBELOOP_HELM_E2E_BUSYBOX_IMAGE:-busybox:1.36.1}"

CRD_OWNED=0

log() {
  printf '\n==> %s\n' "$*"
}

cleanup() {
  local status=$?
  set +e
  if [[ "${status}" != "0" && "${KUBELOOP_HELM_E2E_KEEP_ON_FAILURE:-}" == "1" ]]; then
    echo "Helm E2E resources retained for failure diagnostics" >&2
    case "${WORK_DIR}" in
      "${TMPDIR:-/tmp}"/kubeloop-helm-e2e.*) rm -rf -- "${WORK_DIR}" ;;
    esac
    exit "${status}"
  fi
  helm uninstall "${SQLITE_RELEASE}" --namespace "${SQLITE_NAMESPACE}" --wait >/dev/null 2>&1
  helm uninstall "${POSTGRES_RELEASE}" --namespace "${POSTGRES_NAMESPACE}" --wait >/dev/null 2>&1
  kubectl delete namespace "${SQLITE_NAMESPACE}" "${POSTGRES_NAMESPACE}" --ignore-not-found --wait=true >/dev/null 2>&1
  if [[ "${CRD_OWNED}" == "1" ]]; then
    kubectl delete crd "${CRD}" --ignore-not-found --wait=true >/dev/null 2>&1
  fi
  case "${WORK_DIR}" in
    "${TMPDIR:-/tmp}"/kubeloop-helm-e2e.*) rm -rf -- "${WORK_DIR}" ;;
  esac
  exit "${status}"
}
trap cleanup EXIT

if kubectl get crd "${CRD}" >/dev/null 2>&1; then
  echo "${CRD} already exists; use the dedicated empty Helm E2E cluster" >&2
  exit 2
fi
# The chart installs this CRD before Helm creates namespaced resources. Mark it
# as test-owned before the first install so a failed or interrupted install is
# still cleaned up.
CRD_OWNED=1

create_relay_material() {
  local namespace=$1
  local release=$2
  local directory="${WORK_DIR}/${release}"
  mkdir -p "${directory}"

  openssl genpkey -algorithm ED25519 -out "${directory}/signing-key.pem" >/dev/null 2>&1
  local -a secret_args=(
    create secret generic "${release}-relay"
    --namespace "${namespace}"
    --from-file="signing-key.pem=${directory}/signing-key.pem"
  )
  local registry_name="${release}-kubeloop-control-plane-relay.${namespace}.svc"
  openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
    -keyout "${directory}/tls.key" -out "${directory}/tls.crt" \
    -subj "/CN=kubeloop-relay-registry" \
    -addext "subjectAltName=DNS:${registry_name}" >/dev/null 2>&1
  cp "${directory}/tls.crt" "${directory}/ca.crt"
  secret_args+=(
    --from-file="tls.crt=${directory}/tls.crt"
    --from-file="tls.key=${directory}/tls.key"
    --from-file="ca.crt=${directory}/ca.crt"
  )
  kubectl "${secret_args[@]}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

  openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 \
    -out "${directory}/oidc-signing-key.pem" >/dev/null 2>&1
  openssl rand -hex 16 >"${directory}/hmac-secret"
  kubectl create secret generic "${release}-auth" \
    --namespace "${namespace}" \
    --from-file="oidc-signing-key.pem=${directory}/oidc-signing-key.pem" \
    --from-file="hmac-secret=${directory}/hmac-secret" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
}

image_repository() {
  printf '%s\n' "${1%:*}"
}

image_tag() {
  printf '%s\n' "${1##*:}"
}

helm_apply() {
  local mode=$1
  local release namespace
  shift
  if [[ "${mode}" == "sqlite" ]]; then
    release="${SQLITE_RELEASE}"
    namespace="${SQLITE_NAMESPACE}"
  else
    release="${POSTGRES_RELEASE}"
    namespace="${POSTGRES_NAMESPACE}"
  fi

  local -a args=(
    upgrade --install "${release}" "${CHART}"
    --namespace "${namespace}"
    --wait --timeout 5m --history-max 10
    --set-string publicURL=https://kubeloop.e2e.invalid
    --set-string "controlPlane.relay.existingSecret=${release}-relay"
    --set-string "controlPlane.auth.oauth.existingSecret=${release}-auth"
    --set-string "controlPlane.image.repository=$(image_repository "${CONTROL_PLANE_IMAGE}")"
    --set-string "controlPlane.image.tag=$(image_tag "${CONTROL_PLANE_IMAGE}")"
    --set-string controlPlane.image.pullPolicy=IfNotPresent
    --set-string "dataPlane.image.repository=$(image_repository "${DATA_PLANE_IMAGE}")"
    --set-string "dataPlane.image.tag=$(image_tag "${DATA_PLANE_IMAGE}")"
    --set-string dataPlane.image.pullPolicy=IfNotPresent
    --set-string "operator.image.repository=$(image_repository "${OPERATOR_IMAGE}")"
    --set-string "operator.image.tag=$(image_tag "${OPERATOR_IMAGE}")"
    --set-string operator.image.pullPolicy=IfNotPresent
  )
  if [[ "${mode}" == "sqlite" ]]; then
    args+=(
      --set-string controlPlane.relayRegistry.endpointAllowedHosts=.kubeloop.e2e.invalid
      --set-string 'dataPlane.relayRegistry.endpoint=wss://{podName}.kubeloop.e2e.invalid/tunnel'
    )
  else
    args+=(
      --set-string "controlPlane.storage.datasource.existingSecret=${release}-postgresql"
    )
  fi
  helm "${args[@]}" "$@"
}

rollout_all() {
  local namespace=$1
  local release=$2
  for component in control-plane gateway operator; do
    kubectl rollout status "deployment/${release}-kubeloop-${component}" \
      --namespace "${namespace}" --timeout=5m
  done
}

pod_uids() {
  local namespace=$1
  local release=$2
  local component=$3
  kubectl get pods --namespace "${namespace}" \
    --selector "app.kubernetes.io/instance=${release},app.kubernetes.io/component=${component}" \
    -o json | jq -r '.items[] | select(.metadata.deletionTimestamp == null) | .metadata.uid' | sort
}

assert_equal() {
  local actual=$1
  local expected=$2
  local description=$3
  if [[ "${actual}" != "${expected}" ]]; then
    echo "${description}: got ${actual}, want ${expected}" >&2
    exit 1
  fi
}

assert_replicas() {
  local namespace=$1
  local deployment=$2
  local expected=$3
  local available
  available="$(kubectl get deployment "${deployment}" --namespace "${namespace}" -o jsonpath='{.status.availableReplicas}')"
  assert_equal "${available:-0}" "${expected}" "${namespace}/${deployment} available replicas"
}

namespaced_kubeloop_resources() {
  local selector='app.kubernetes.io/part-of=kubeloop,app.kubernetes.io/instance in (sqlite,postgresql)'
  kubectl get all,configmap,serviceaccount,role,rolebinding,pvc,networkpolicy,pdb \
    --all-namespaces --selector "${selector}" -o name 2>/dev/null
}

cluster_kubeloop_resources() {
  local selector='app.kubernetes.io/part-of=kubeloop,app.kubernetes.io/instance in (sqlite,postgresql)'
  kubectl get clusterrole,clusterrolebinding \
    --selector "${selector}" -o name 2>/dev/null
}

wait_for_helm_cleanup() {
  local deadline=$((SECONDS + 120))
  local namespaced cluster
  while (( SECONDS < deadline )); do
    namespaced="$(namespaced_kubeloop_resources)"
    cluster="$(cluster_kubeloop_resources)"
    if [[ -z "${namespaced}" && -z "${cluster}" ]]; then
      return 0
    fi
    sleep 2
  done
  echo "namespaced KubeLoop resources after uninstall:" >&2
  namespaced_kubeloop_resources >&2
  echo "cluster-scoped KubeLoop RBAC after uninstall:" >&2
  cluster_kubeloop_resources >&2
  return 1
}

restart_component() {
  local namespace=$1
  local release=$2
  local component=$3
  local deployment=$4
  local old_uids new_uids
  old_uids="$(pod_uids "${namespace}" "${release}" "${component}")"
  kubectl delete pods --namespace "${namespace}" \
    --selector "app.kubernetes.io/instance=${release},app.kubernetes.io/component=${component}" \
    --wait=true >/dev/null
  kubectl rollout status "deployment/${deployment}" --namespace "${namespace}" --timeout=5m
  new_uids="$(pod_uids "${namespace}" "${release}" "${component}")"
  if [[ -z "${new_uids}" || "${new_uids}" == "${old_uids}" ]]; then
    echo "${component} Pods did not recover with new identities" >&2
    exit 1
  fi
}

sqlite_probe() {
  local operation=$1
  kubectl delete pod sqlite-retention-probe --namespace "${SQLITE_NAMESPACE}" \
    --ignore-not-found --wait=true >/dev/null
  local command
  if [[ "${operation}" == "write" ]]; then
    command='test -s /data/kubeloop.db && printf %s helm-retained > /data/helm-e2e-marker'
  else
    command='test -s /data/kubeloop.db && test "$(cat /data/helm-e2e-marker)" = helm-retained'
  fi
  kubectl apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: sqlite-retention-probe
  namespace: ${SQLITE_NAMESPACE}
spec:
  restartPolicy: Never
  containers:
    - name: probe
      image: ${BUSYBOX_IMAGE}
      imagePullPolicy: IfNotPresent
      command: ["sh", "-c", '${command}']
      volumeMounts:
        - name: data
          mountPath: /data
  volumes:
    - name: data
      persistentVolumeClaim:
        claimName: ${SQLITE_RELEASE}-kubeloop-control-plane-data
EOF
  if ! kubectl wait pod/sqlite-retention-probe --namespace "${SQLITE_NAMESPACE}" \
    --for=jsonpath='{.status.phase}'=Succeeded --timeout=2m; then
    kubectl describe pod/sqlite-retention-probe --namespace "${SQLITE_NAMESPACE}" >&2
    kubectl logs pod/sqlite-retention-probe --namespace "${SQLITE_NAMESPACE}" >&2 || true
    exit 1
  fi
  kubectl delete pod sqlite-retention-probe --namespace "${SQLITE_NAMESPACE}" --wait=true >/dev/null
}

install_postgresql() {
  local directory="${WORK_DIR}/${POSTGRES_RELEASE}"
  local server_name="postgresql.${POSTGRES_NAMESPACE}.svc"
  openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
    -keyout "${directory}/postgresql-tls.key" -out "${directory}/postgresql-tls.crt" \
    -subj "/CN=${server_name}" \
    -addext "subjectAltName=DNS:${server_name},DNS:postgresql.${POSTGRES_NAMESPACE},DNS:postgresql" >/dev/null 2>&1
  kubectl create secret generic postgresql-tls \
    --namespace "${POSTGRES_NAMESPACE}" \
    --from-file="tls.crt=${directory}/postgresql-tls.crt" \
    --from-file="tls.key=${directory}/postgresql-tls.key" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null

  kubectl apply -f - >/dev/null <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: postgresql
  namespace: ${POSTGRES_NAMESPACE}
spec:
  replicas: 1
  selector:
    matchLabels: {app: postgresql}
  template:
    metadata:
      labels: {app: postgresql}
    spec:
      initContainers:
        - name: prepare-tls
          image: ${POSTGRES_IMAGE}
          imagePullPolicy: IfNotPresent
          command: ["sh", "-c", "cp /source/tls.crt /tls/tls.crt && cp /source/tls.key /tls/tls.key && chown 70:70 /tls/tls.crt /tls/tls.key && chmod 600 /tls/tls.key"]
          securityContext:
            runAsUser: 0
          volumeMounts:
            - {name: tls-source, mountPath: /source, readOnly: true}
            - {name: tls, mountPath: /tls}
      containers:
        - name: postgresql
          image: ${POSTGRES_IMAGE}
          imagePullPolicy: IfNotPresent
          args: ["-c", "ssl=on", "-c", "ssl_cert_file=/var/run/postgresql-tls/tls.crt", "-c", "ssl_key_file=/var/run/postgresql-tls/tls.key"]
          env:
            - {name: POSTGRES_DB, value: kubeloop}
            - {name: POSTGRES_USER, value: kubeloop}
            - {name: POSTGRES_PASSWORD, value: kubeloop-e2e}
          ports:
            - {name: postgres, containerPort: 5432}
          readinessProbe:
            exec:
              command: ["pg_isready", "-U", "kubeloop", "-d", "kubeloop"]
            periodSeconds: 2
          volumeMounts:
            - {name: tls, mountPath: /var/run/postgresql-tls, readOnly: true}
      volumes:
        - name: tls-source
          secret:
            secretName: postgresql-tls
        - name: tls
          emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: postgresql
  namespace: ${POSTGRES_NAMESPACE}
spec:
  selector: {app: postgresql}
  ports:
    - {name: postgres, port: 5432, targetPort: postgres}
EOF
  kubectl rollout status deployment/postgresql --namespace "${POSTGRES_NAMESPACE}" --timeout=5m
  kubectl create secret generic "${POSTGRES_RELEASE}-postgresql" \
    --namespace "${POSTGRES_NAMESPACE}" \
    --from-literal="datasource-url=postgres://kubeloop:kubeloop-e2e@postgresql.${POSTGRES_NAMESPACE}.svc:5432/kubeloop?sslmode=require" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
}

postgres_exec() {
  kubectl exec --namespace "${POSTGRES_NAMESPACE}" deployment/postgresql -- \
    psql -U kubeloop -d kubeloop -v ON_ERROR_STOP=1 "$@"
}

log "Create isolated namespaces and runtime secrets"
kubectl create namespace "${SQLITE_NAMESPACE}" >/dev/null
kubectl create namespace "${POSTGRES_NAMESPACE}" >/dev/null
create_relay_material "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}"
create_relay_material "${POSTGRES_NAMESPACE}" "${POSTGRES_RELEASE}"
install_postgresql

log "Install SQLite release with dynamic Relay Registry"
helm_apply sqlite \
  --set-string 'controlPlane.podAnnotations.e2e\.kubeloop\.io/revision=v1' \
  --set-string 'dataPlane.podAnnotations.e2e\.kubeloop\.io/revision=v1' \
  --set-string 'operator.podAnnotations.e2e\.kubeloop\.io/revision=v1'
rollout_all "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}"
CRD_UID="$(kubectl get crd "${CRD}" -o jsonpath='{.metadata.uid}')"
kubectl explain trafficbinding.spec.mode >/dev/null
SQLITE_PVC_UID="$(kubectl get pvc "${SQLITE_RELEASE}-kubeloop-control-plane-data" \
  --namespace "${SQLITE_NAMESPACE}" -o jsonpath='{.metadata.uid}')"
sqlite_probe write

log "Upgrade Control Plane without restarting Data Plane or Operator"
DATA_PLANE_UIDS="$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" data-plane)"
OPERATOR_UIDS="$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" operator)"
helm_apply sqlite \
  --set-string 'controlPlane.podAnnotations.e2e\.kubeloop\.io/revision=v2' \
  --set-string 'dataPlane.podAnnotations.e2e\.kubeloop\.io/revision=v1' \
  --set-string 'operator.podAnnotations.e2e\.kubeloop\.io/revision=v1'
assert_equal "$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" data-plane)" "${DATA_PLANE_UIDS}" "Data Plane changed during Control Plane upgrade"
assert_equal "$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" operator)" "${OPERATOR_UIDS}" "Operator changed during Control Plane upgrade"

log "Upgrade Data Plane without restarting Control Plane or Operator"
CONTROL_PLANE_UIDS="$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" control-plane)"
OPERATOR_UIDS="$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" operator)"
helm_apply sqlite \
  --set-string 'controlPlane.podAnnotations.e2e\.kubeloop\.io/revision=v2' \
  --set-string 'dataPlane.podAnnotations.e2e\.kubeloop\.io/revision=v2' \
  --set-string 'operator.podAnnotations.e2e\.kubeloop\.io/revision=v1'
assert_equal "$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" control-plane)" "${CONTROL_PLANE_UIDS}" "Control Plane changed during Data Plane upgrade"
assert_equal "$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" operator)" "${OPERATOR_UIDS}" "Operator changed during Data Plane upgrade"

log "Upgrade Operator without restarting Control Plane or Data Plane"
CONTROL_PLANE_UIDS="$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" control-plane)"
DATA_PLANE_UIDS="$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" data-plane)"
helm_apply sqlite \
  --set-string 'controlPlane.podAnnotations.e2e\.kubeloop\.io/revision=v2' \
  --set-string 'dataPlane.podAnnotations.e2e\.kubeloop\.io/revision=v2' \
  --set-string 'operator.podAnnotations.e2e\.kubeloop\.io/revision=v2'
assert_equal "$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" control-plane)" "${CONTROL_PLANE_UIDS}" "Control Plane changed during Operator upgrade"
assert_equal "$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" data-plane)" "${DATA_PLANE_UIDS}" "Data Plane changed during Operator upgrade"

log "Rollback SQLite release and verify persistent data"
helm rollback "${SQLITE_RELEASE}" 1 --namespace "${SQLITE_NAMESPACE}" --wait --timeout 5m
rollout_all "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}"
assert_equal "$(kubectl get pvc "${SQLITE_RELEASE}-kubeloop-control-plane-data" --namespace "${SQLITE_NAMESPACE}" -o jsonpath='{.metadata.uid}')" "${SQLITE_PVC_UID}" "SQLite PVC identity after rollback"
sqlite_probe read

log "Scale Data Plane without restarting Control Plane or Operator"
CONTROL_PLANE_UIDS="$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" control-plane)"
OPERATOR_UIDS="$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" operator)"
helm_apply sqlite \
  --set dataPlane.replicas=2 \
  --set-string 'controlPlane.podAnnotations.e2e\.kubeloop\.io/revision=v1' \
  --set-string 'dataPlane.podAnnotations.e2e\.kubeloop\.io/revision=v1' \
  --set-string 'operator.podAnnotations.e2e\.kubeloop\.io/revision=v1' \
  --set-string 'dataPlane.relayRegistry.endpoint=wss://{podName}.kubeloop.e2e.invalid/tunnel'
assert_replicas "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}-kubeloop-gateway" 2
assert_equal "$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" control-plane)" "${CONTROL_PLANE_UIDS}" "Control Plane changed during Data Plane scale"
assert_equal "$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" operator)" "${OPERATOR_UIDS}" "Operator changed during Data Plane scale"
helm_apply sqlite \
  --set dataPlane.replicas=1 \
  --set-string 'controlPlane.podAnnotations.e2e\.kubeloop\.io/revision=v1' \
  --set-string 'dataPlane.podAnnotations.e2e\.kubeloop\.io/revision=v1' \
  --set-string 'operator.podAnnotations.e2e\.kubeloop\.io/revision=v1'

log "Scale Operator without restarting Control Plane or Data Plane"
CONTROL_PLANE_UIDS="$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" control-plane)"
DATA_PLANE_UIDS="$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" data-plane)"
helm_apply sqlite \
  --set operator.replicas=2 \
  --set-string 'controlPlane.podAnnotations.e2e\.kubeloop\.io/revision=v1' \
  --set-string 'dataPlane.podAnnotations.e2e\.kubeloop\.io/revision=v1' \
  --set-string 'operator.podAnnotations.e2e\.kubeloop\.io/revision=v1'
assert_replicas "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}-kubeloop-operator" 2
assert_equal "$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" control-plane)" "${CONTROL_PLANE_UIDS}" "Control Plane changed during Operator scale"
assert_equal "$(pod_uids "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" data-plane)" "${DATA_PLANE_UIDS}" "Data Plane changed during Operator scale"
helm_apply sqlite \
  --set operator.replicas=1 \
  --set-string 'controlPlane.podAnnotations.e2e\.kubeloop\.io/revision=v1' \
  --set-string 'dataPlane.podAnnotations.e2e\.kubeloop\.io/revision=v1' \
  --set-string 'operator.podAnnotations.e2e\.kubeloop\.io/revision=v1'
rollout_all "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}"

log "Recover all SQLite-mode components from Pod failure"
restart_component "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" control-plane "${SQLITE_RELEASE}-kubeloop-control-plane"
restart_component "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" data-plane "${SQLITE_RELEASE}-kubeloop-gateway"
restart_component "${SQLITE_NAMESPACE}" "${SQLITE_RELEASE}" operator "${SQLITE_RELEASE}-kubeloop-operator"
sqlite_probe read

log "Install PostgreSQL release with externally managed database"
helm_apply postgresql
rollout_all "${POSTGRES_NAMESPACE}" "${POSTGRES_RELEASE}"
assert_equal "$(kubectl get crd "${CRD}" -o jsonpath='{.metadata.uid}')" "${CRD_UID}" "CRD identity after second Helm install"
postgres_exec -c 'CREATE TABLE helm_e2e_retention (value text PRIMARY KEY); INSERT INTO helm_e2e_retention VALUES ('"'"'retained'"'"');' >/dev/null

log "Rollback PostgreSQL release and verify database retention"
helm rollback "${POSTGRES_RELEASE}" 1 --namespace "${POSTGRES_NAMESPACE}" --wait --timeout 5m
rollout_all "${POSTGRES_NAMESPACE}" "${POSTGRES_RELEASE}"
assert_replicas "${POSTGRES_NAMESPACE}" "${POSTGRES_RELEASE}-kubeloop-control-plane" 1
assert_equal "$(postgres_exec -Atc 'SELECT value FROM helm_e2e_retention;')" "retained" "PostgreSQL marker after rollback"
restart_component "${POSTGRES_NAMESPACE}" "${POSTGRES_RELEASE}" control-plane "${POSTGRES_RELEASE}-kubeloop-control-plane"
assert_equal "$(postgres_exec -Atc 'SELECT value FROM helm_e2e_retention;')" "retained" "PostgreSQL marker after Control Plane restart"

log "Uninstall both releases and verify cleanup plus explicit CRD retention"
helm uninstall "${SQLITE_RELEASE}" --namespace "${SQLITE_NAMESPACE}" --wait
helm uninstall "${POSTGRES_RELEASE}" --namespace "${POSTGRES_NAMESPACE}" --wait
if ! wait_for_helm_cleanup; then
  exit 1
fi
assert_equal "$(kubectl get crd "${CRD}" -o jsonpath='{.metadata.uid}')" "${CRD_UID}" "Helm-managed CRD retention"
if [[ -n "$(kubectl get trafficbindings --all-namespaces --no-headers 2>/dev/null)" ]]; then
  echo "TrafficBinding business resources remain after uninstall" >&2
  exit 1
fi

kubectl delete crd "${CRD}" --wait=true >/dev/null
CRD_OWNED=0
kubectl delete namespace "${SQLITE_NAMESPACE}" "${POSTGRES_NAMESPACE}" --wait=true >/dev/null

log "Helm lifecycle E2E passed"
