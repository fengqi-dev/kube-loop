package taskapi

import (
	"net/http"
	"testing"
)

func TestIdempotencyKeyContract(t *testing.T) {
	for _, valid := range []string{"task-1", "client.request_2", "exec:550e8400-e29b-41d4-a716-446655440000"} {
		request, _ := http.NewRequest(http.MethodPost, "/", nil)
		request.Header.Set("Idempotency-Key", valid)
		if key, apiError := IdempotencyKey(request); apiError != nil || key != valid {
			t.Fatalf("valid key %q = %q, %#v", valid, key, apiError)
		}
	}
	for _, invalid := range []string{"", "has space", "slash/value", "ümlaut"} {
		request, _ := http.NewRequest(http.MethodPost, "/", nil)
		request.Header.Set("Idempotency-Key", invalid)
		if _, apiError := IdempotencyKey(request); apiError == nil {
			t.Fatalf("invalid key %q was accepted", invalid)
		}
	}
	request, _ := http.NewRequest(http.MethodPost, "/", nil)
	request.Header.Add("Idempotency-Key", "one")
	request.Header.Add("Idempotency-Key", "two")
	if _, apiError := IdempotencyKey(request); apiError == nil {
		t.Fatal("duplicate Idempotency-Key was accepted")
	}
}

func TestRequestHashBindsSessionNamespaceAndCanonicalJSON(t *testing.T) {
	spec := struct {
		Pod string `json:"pod"`
	}{Pod: "api-0"}
	first, err := RequestHash("session-a", "development", spec)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := RequestHash("session-a", "development", spec)
	otherSession, _ := RequestHash("session-b", "development", spec)
	otherNamespace, _ := RequestHash("session-a", "production", spec)
	const stableHash = "3f43b1b0ec02f2513edd0839d05865e1691b9cb93c5ce4aec9ed41bb9d919a8f"
	if first != stableHash || first != second || first == otherSession || first == otherNamespace || len(first) != 64 {
		t.Fatalf("hashes first=%q second=%q session=%q namespace=%q", first, second, otherSession, otherNamespace)
	}
	if Scope("pod-exec", "identity") != "task:pod-exec:identity" {
		t.Fatal("Task scope is unstable")
	}
}
