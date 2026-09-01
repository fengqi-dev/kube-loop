#!/usr/bin/env bash
set -Eeuo pipefail

namespace="${DEMO_NAMESPACE:-kubeloop-demo}"
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
http_test_pod="kubeloop-http-test-$$"
grpc_test_pod="kubeloop-grpc-test-$$"

cleanup() {
  kubectl -n "$namespace" delete pod \
    "$http_test_pod" "$grpc_test_pod" \
    --ignore-not-found \
    --wait=false \
    >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

if ! command -v kubectl >/dev/null 2>&1; then
  echo "required command not found: kubectl" >&2
  exit 1
fi

if [[ "${SKIP_APPLY:-false}" != "true" ]]; then
  echo "Applying demo resources..."
  kubectl apply -f "$script_dir/demo.yaml"
fi

echo "Waiting for demo workloads..."
kubectl -n "$namespace" rollout status deployment/go-httpbin --timeout=120s
kubectl -n "$namespace" rollout status deployment/grpcbin --timeout=120s

http_service="go-httpbin.${namespace}.svc:8080"
grpc_service="grpcbin.${namespace}.svc:9000"

echo "Testing HTTP service http://$http_service..."
http_response=$(kubectl -n "$namespace" run "$http_test_pod" \
  --image="${CURL_IMAGE:-curlimages/curl:latest}" \
  --image-pull-policy=IfNotPresent \
  --restart=Never \
  --rm \
  --attach \
  --quiet \
  -- \
  --fail \
  --show-error \
  --silent \
  "http://$http_service/get?source=kubeloop-demo")

if [[ "$http_response" != *"kubeloop-demo"* ]]; then
  echo "go-httpbin response did not contain the expected query value" >&2
  exit 1
fi

echo "Testing gRPC service $grpc_service..."
grpc_response=$(kubectl -n "$namespace" run "$grpc_test_pod" \
  --image="${GRPCURL_IMAGE:-fullstorydev/grpcurl:latest}" \
  --image-pull-policy=IfNotPresent \
  --restart=Never \
  --rm \
  --attach \
  --quiet \
  -- \
  -plaintext \
  -d '{"f_string":"kubeloop-demo","f_int32":42}' \
  "$grpc_service" \
  grpcbin.GRPCBin/DummyUnary)

if [[ "$grpc_response" != *"kubeloop-demo"* ]]; then
  echo "grpcbin did not echo the expected payload" >&2
  exit 1
fi

echo "PASS: go-httpbin and grpcbin Kubernetes Services returned expected responses."
