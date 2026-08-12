#!/usr/bin/env bash

set -euo pipefail

if [[ "${KUBELOOP_OIDC_E2E:-}" != "1" ]]; then
  echo "KUBELOOP_OIDC_E2E=1 is required" >&2
  exit 2
fi

EXPECTED_CONTEXT="${KUBELOOP_OIDC_E2E_CONTEXT:-kubeloop-oidc-e2e}"
if [[ "$(kubectl config current-context)" != "${EXPECTED_CONTEXT}" ]]; then
  echo "refusing to run against a context other than ${EXPECTED_CONTEXT}" >&2
  exit 2
fi

for command in go helm kubectl minikube openssl jq node; do
  command -v "${command}" >/dev/null 2>&1 || { echo "${command} is required" >&2; exit 2; }
done

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/kubeloop-oidc-e2e.XXXXXX")"
ARTIFACTS="${KUBELOOP_OIDC_E2E_ARTIFACTS:-${ROOT}/build/oidc-e2e-artifacts}"
KEYCLOAK_NAMESPACE="kubeloop-oidc-provider"
KUBELOOP_NAMESPACE="kubeloop-oidc"
RELEASE="oidc"
KEYCLOAK_IMAGE="${KUBELOOP_OIDC_E2E_KEYCLOAK_IMAGE:-quay.io/keycloak/keycloak:26.3.5}"
CONTROL_PLANE_IMAGE="${KUBELOOP_OIDC_E2E_CONTROL_PLANE_IMAGE:-kubeloop/control-plane:e2e}"
DATA_PLANE_IMAGE="${KUBELOOP_OIDC_E2E_DATA_PLANE_IMAGE:-kubeloop/gateway:e2e}"
OPERATOR_IMAGE="${KUBELOOP_OIDC_E2E_OPERATOR_IMAGE:-kubeloop/operator:e2e}"
CLIENT_ID="kubeloop-e2e"
CLIENT_SECRET="keycloak-client-secret"
USERNAME="oidc-e2e-user"
PASSWORD="oidc-e2e-password"
BACKEND_URL="http://127.0.0.1:18080"
PUBLIC_URL="https://127.0.0.1:18443"
PORT_FORWARD_PID=""
TLS_PROXY_PID=""

mkdir -p "${ARTIFACTS}"
rm -f "${ARTIFACTS}"/*

cleanup() {
  local status=$?
  set +e
  if [[ -n "${PORT_FORWARD_PID}" ]]; then
    kill "${PORT_FORWARD_PID}" >/dev/null 2>&1 || true
    wait "${PORT_FORWARD_PID}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${TLS_PROXY_PID}" ]]; then
    kill "${TLS_PROXY_PID}" >/dev/null 2>&1 || true
    wait "${TLS_PROXY_PID}" >/dev/null 2>&1 || true
  fi
  if [[ "${status}" == "0" || "${KUBELOOP_OIDC_E2E_KEEP_ON_FAILURE:-}" != "1" ]]; then
    helm uninstall "${RELEASE}" --namespace "${KUBELOOP_NAMESPACE}" --wait >/dev/null 2>&1 || true
    kubectl delete namespace "${KUBELOOP_NAMESPACE}" "${KEYCLOAK_NAMESPACE}" --ignore-not-found --wait=true >/dev/null 2>&1 || true
  fi
  case "${WORK_DIR}" in
    "${TMPDIR:-/tmp}"/kubeloop-oidc-e2e.*) rm -rf -- "${WORK_DIR}" ;;
  esac
  exit "${status}"
}
trap cleanup EXIT

MINIKUBE_IP="$(minikube --profile "${EXPECTED_CONTEXT}" ip)"
KEYCLOAK_HOST="${MINIKUBE_IP}.nip.io"
KEYCLOAK_URL="https://${KEYCLOAK_HOST}:32443"
ISSUER="${KEYCLOAK_URL}/realms/kubeloop-e2e"

openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
  -keyout "${WORK_DIR}/tls.key" -out "${WORK_DIR}/tls.crt" \
  -subj "/CN=${KEYCLOAK_HOST}" \
  -addext "subjectAltName=DNS:${KEYCLOAK_HOST}" >/dev/null 2>&1
REGISTRY_HOST="${RELEASE}-kubeloop-control-plane-relay.${KUBELOOP_NAMESPACE}.svc"
openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
  -keyout "${WORK_DIR}/registry-tls.key" -out "${WORK_DIR}/registry-tls.crt" \
  -subj "/CN=kubeloop-relay-registry" \
  -addext "subjectAltName=DNS:${REGISTRY_HOST}" >/dev/null 2>&1
openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
  -keyout "${WORK_DIR}/public-ca.key" -out "${WORK_DIR}/public-ca.crt" \
  -subj "/CN=KubeLoop OIDC E2E CA" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign" >/dev/null 2>&1
openssl req -new -newkey rsa:2048 -nodes \
  -keyout "${WORK_DIR}/public-tls.key" -out "${WORK_DIR}/public-tls.csr" \
  -subj "/CN=127.0.0.1" \
  -addext "subjectAltName=IP:127.0.0.1" >/dev/null 2>&1
openssl x509 -req -days 2 \
  -in "${WORK_DIR}/public-tls.csr" \
  -CA "${WORK_DIR}/public-ca.crt" -CAkey "${WORK_DIR}/public-ca.key" -CAcreateserial \
  -copy_extensions copy -out "${WORK_DIR}/public-tls.crt" >/dev/null 2>&1
openssl genpkey -algorithm ED25519 -out "${WORK_DIR}/relay-signing-key.pem" >/dev/null 2>&1
openssl genpkey -algorithm ED25519 -out "${WORK_DIR}/token-signing-key.pem" >/dev/null 2>&1

jq -n \
  --arg clientID "${CLIENT_ID}" \
  --arg clientSecret "${CLIENT_SECRET}" \
  --arg redirectURI "${PUBLIC_URL}/oauth2/callback/keycloak" \
  --arg username "${USERNAME}" \
  --arg password "${PASSWORD}" \
  '{
    realm: "kubeloop-e2e",
    enabled: true,
    sslRequired: "external",
    registrationAllowed: false,
    loginWithEmailAllowed: true,
    roles: {realm: [{name: "kubeloop-e2e-user"}]},
    clients: [{
      clientId: $clientID,
      name: "KubeLoop E2E",
      enabled: true,
      publicClient: false,
      secret: $clientSecret,
      standardFlowEnabled: true,
      directAccessGrantsEnabled: false,
      redirectUris: [$redirectURI],
      webOrigins: [],
      attributes: {"pkce.code.challenge.method": "S256"},
      defaultClientScopes: ["web-origins", "acr", "roles", "profile", "email"]
    }],
    users: [{
      username: $username,
      enabled: true,
      emailVerified: true,
      email: "oidc-e2e@example.test",
      firstName: "OIDC",
      lastName: "E2E",
      realmRoles: ["kubeloop-e2e-user"],
      credentials: [{type: "password", value: $password, temporary: false}]
    }]
  }' >"${WORK_DIR}/realm.json"

kubectl create namespace "${KEYCLOAK_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl create configmap keycloak-realm --namespace "${KEYCLOAK_NAMESPACE}" \
  --from-file="kubeloop-e2e-realm.json=${WORK_DIR}/realm.json" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl create secret tls keycloak-tls --namespace "${KEYCLOAK_NAMESPACE}" \
  --cert="${WORK_DIR}/tls.crt" --key="${WORK_DIR}/tls.key" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

cat <<EOF | kubectl apply -f - >/dev/null
apiVersion: apps/v1
kind: Deployment
metadata:
  name: keycloak
  namespace: ${KEYCLOAK_NAMESPACE}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: keycloak
  template:
    metadata:
      labels:
        app: keycloak
    spec:
      securityContext:
        fsGroup: 1000
      containers:
        - name: keycloak
          image: ${KEYCLOAK_IMAGE}
          imagePullPolicy: IfNotPresent
          args:
            - start-dev
            - --import-realm
            - --http-enabled=false
            - --https-port=8443
            - --https-certificate-file=/etc/keycloak/tls/tls.crt
            - --https-certificate-key-file=/etc/keycloak/tls/tls.key
            - --hostname=${KEYCLOAK_URL}
            - --hostname-strict=true
          env:
            - name: KC_BOOTSTRAP_ADMIN_USERNAME
              value: admin
            - name: KC_BOOTSTRAP_ADMIN_PASSWORD
              value: admin-e2e-password
          ports:
            - name: https
              containerPort: 8443
          readinessProbe:
            tcpSocket:
              port: https
            initialDelaySeconds: 10
            periodSeconds: 3
          resources:
            requests:
              cpu: 250m
              memory: 512Mi
            limits:
              memory: 1Gi
          volumeMounts:
            - name: realm
              mountPath: /opt/keycloak/data/import
              readOnly: true
            - name: tls
              mountPath: /etc/keycloak/tls
              readOnly: true
      volumes:
        - name: realm
          configMap:
            name: keycloak-realm
        - name: tls
          secret:
            secretName: keycloak-tls
---
apiVersion: v1
kind: Service
metadata:
  name: keycloak
  namespace: ${KEYCLOAK_NAMESPACE}
spec:
  type: NodePort
  selector:
    app: keycloak
  ports:
    - name: https
      port: 8443
      targetPort: https
      nodePort: 32443
EOF

kubectl rollout status deployment/keycloak --namespace "${KEYCLOAK_NAMESPACE}" --timeout=5m
for _ in $(seq 1 60); do
  if curl --silent --show-error --fail --cacert "${WORK_DIR}/tls.crt" \
    "${ISSUER}/.well-known/openid-configuration" >/dev/null; then
    break
  fi
  sleep 2
done
curl --silent --show-error --fail --cacert "${WORK_DIR}/tls.crt" \
  "${ISSUER}/.well-known/openid-configuration" >/dev/null

kubectl create namespace "${KUBELOOP_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl create secret generic oidc-credentials --namespace "${KUBELOOP_NAMESPACE}" \
  --from-literal="client-secret=${CLIENT_SECRET}" --from-file="ca.crt=${WORK_DIR}/tls.crt" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl create secret generic oidc-signing --namespace "${KUBELOOP_NAMESPACE}" \
  --from-file="relay-signing-key.pem=${WORK_DIR}/relay-signing-key.pem" \
  --from-file="tls.crt=${WORK_DIR}/registry-tls.crt" \
  --from-file="tls.key=${WORK_DIR}/registry-tls.key" \
  --from-file="ca.crt=${WORK_DIR}/registry-tls.crt" \
  --from-file="token-signing-key.pem=${WORK_DIR}/token-signing-key.pem" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

image_repository() { printf '%s\n' "${1%:*}"; }
image_tag() { printf '%s\n' "${1##*:}"; }

helm upgrade --install "${RELEASE}" "${ROOT}/charts/kubeloop" \
  --namespace "${KUBELOOP_NAMESPACE}" --wait --timeout 7m --history-max 2 \
  --set-string "publicURL=${PUBLIC_URL}" \
  --set-string "controlPlane.image.repository=$(image_repository "${CONTROL_PLANE_IMAGE}")" \
  --set-string "controlPlane.image.tag=$(image_tag "${CONTROL_PLANE_IMAGE}")" \
  --set-string controlPlane.image.pullPolicy=IfNotPresent \
  --set-string "dataPlane.image.repository=$(image_repository "${DATA_PLANE_IMAGE}")" \
  --set-string "dataPlane.image.tag=$(image_tag "${DATA_PLANE_IMAGE}")" \
  --set-string dataPlane.image.pullPolicy=IfNotPresent \
  --set-string "operator.image.repository=$(image_repository "${OPERATOR_IMAGE}")" \
  --set-string "operator.image.tag=$(image_tag "${OPERATOR_IMAGE}")" \
  --set-string operator.image.pullPolicy=IfNotPresent \
  --set controlPlane.storage.sqlite.persistence.enabled=false \
  --set controlPlane.management.initialAdmin.enabled=false \
  --set-string controlPlane.relay.existingSecret=oidc-signing \
  --set-string controlPlane.relay.signingKeyKey=relay-signing-key.pem \
  --set-string controlPlane.auth.token.existingSecret=oidc-signing \
  --set-string controlPlane.auth.token.signingKeyKey=token-signing-key.pem \
  --set-string controlPlane.auth.providers[0].id=keycloak \
  --set-string controlPlane.auth.providers[0].type=oidc \
  --set-string controlPlane.auth.providers[0].displayName='Keycloak E2E' \
  --set-string "controlPlane.auth.providers[0].oidc.issuer=${ISSUER}" \
  --set-string "controlPlane.auth.providers[0].oidc.clientID=${CLIENT_ID}" \
  --set-string controlPlane.auth.providers[0].oidc.existingSecret=oidc-credentials \
  --set-string controlPlane.auth.providers[0].oidc.clientSecretKey=client-secret \
  --set-string controlPlane.auth.providers[0].oidc.caKey=ca.crt \
  --set-string controlPlane.auth.providers[0].oidc.claims.displayName=preferred_username \
  --set-string controlPlane.auth.providers[0].oidc.claims.email=email \
  --set-string controlPlane.auth.providers[0].oidc.claims.groups=realm_access.roles \
  --set-string controlPlane.policy.rules[0].id=oidc-e2e-namespaces \
  --set-string controlPlane.policy.rules[0].groups[0]=kubeloop-e2e-user \
  --set-string 'controlPlane.policy.rules[0].namespaces[0]=$cluster' \
  --set-string controlPlane.policy.rules[0].operations[0]=list \
  --set-string controlPlane.policy.rules[0].resourceKinds[0]=namespaces

kubectl port-forward --namespace "${KUBELOOP_NAMESPACE}" \
  "service/${RELEASE}-kubeloop-control-plane" 18080:80 >"${ARTIFACTS}/port-forward.log" 2>&1 &
PORT_FORWARD_PID=$!
for _ in $(seq 1 30); do
  if curl --silent --show-error --fail "${BACKEND_URL}/health/ready" >/dev/null; then
    break
  fi
  sleep 1
done
curl --silent --show-error --fail "${BACKEND_URL}/health/ready" >/dev/null

go build -o "${WORK_DIR}/tls-proxy" ./e2e/oidc/tlsproxy
"${WORK_DIR}/tls-proxy" \
  --listen 127.0.0.1:18443 --target "${BACKEND_URL}" \
  --cert "${WORK_DIR}/public-tls.crt" --key "${WORK_DIR}/public-tls.key" \
  >"${ARTIFACTS}/tls-proxy.log" 2>&1 &
TLS_PROXY_PID=$!
for _ in $(seq 1 30); do
  if curl --silent --show-error --fail --cacert "${WORK_DIR}/public-ca.crt" \
    "${PUBLIC_URL}/health/ready" >/dev/null; then
    break
  fi
  sleep 1
done
curl --silent --show-error --fail --cacert "${WORK_DIR}/public-ca.crt" \
  "${PUBLIC_URL}/health/ready" >/dev/null

cd "${ROOT}"
KUBELOOP_OIDC_E2E=1 \
KUBELOOP_OIDC_E2E_BASE_URL="${PUBLIC_URL}" \
KUBELOOP_OIDC_E2E_CA_FILE="${WORK_DIR}/public-ca.crt" \
KUBELOOP_OIDC_E2E_USERNAME="${USERNAME}" \
KUBELOOP_OIDC_E2E_PASSWORD="${PASSWORD}" \
KUBELOOP_OIDC_E2E_ARTIFACTS="${ARTIFACTS}" \
  go test -tags=e2e ./e2e/oidc -count=1 -v -timeout=2m
