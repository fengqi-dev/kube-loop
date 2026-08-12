package authorization

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestPolicyDefaultsToDenyAndMatchesEveryDimension(t *testing.T) {
	engine := NewDenyAll()
	request := Request{Operation: "list", Namespace: "payments", ResourceKind: "pods"}
	if decision := engine.Authorize(context.Background(), Subject{ID: "user-1", Groups: []string{"developers"}}, request); decision.Allowed {
		t.Fatalf("deny-all decision = %#v", decision)
	}
	if err := engine.Update(Policy{Version: CurrentVersion, Rules: []Rule{{
		ID: "payments-read", Groups: []string{"developers"}, Namespaces: []string{"payments"},
		Operations: []string{"get", "list", "watch"}, ResourceKinds: []string{"pods", "services"},
	}}}); err != nil {
		t.Fatal(err)
	}
	if decision := engine.Authorize(context.Background(), Subject{ID: "user-1", Groups: []string{"developers"}}, request); !decision.Allowed || decision.RuleID != "payments-read" {
		t.Fatalf("allowed decision = %#v", decision)
	}
	denied := []struct {
		subject Subject
		request Request
	}{
		{subject: Subject{ID: "user-1", Groups: []string{"other"}}, request: request},
		{subject: Subject{ID: "user-1", Groups: []string{"developers"}}, request: Request{Operation: "delete", Namespace: "payments", ResourceKind: "pods"}},
		{subject: Subject{ID: "user-1", Groups: []string{"developers"}}, request: Request{Operation: "list", Namespace: "secret", ResourceKind: "pods"}},
		{subject: Subject{ID: "user-1", Groups: []string{"developers"}}, request: Request{Operation: "list", Namespace: "payments", ResourceKind: "secrets"}},
	}
	for index, test := range denied {
		if decision := engine.Authorize(context.Background(), test.subject, test.request); decision.Allowed {
			t.Fatalf("denied case %d = %#v", index, decision)
		}
	}
}

func TestPolicyRequiresExplicitWildcardAndClusterScope(t *testing.T) {
	engine, err := New(Policy{Rules: []Rule{{
		ID: "cluster-read", Subjects: []string{"admin"}, Namespaces: []string{"$cluster"},
		Operations: []string{"list"}, ResourceKinds: []string{"namespaces"},
	}, {
		ID: "all-namespaces", Groups: []string{"platform"}, Namespaces: []string{"*"},
		Operations: []string{"*"}, ResourceKinds: []string{"pods"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if !engine.Authorize(context.Background(), Subject{ID: "admin"}, Request{Operation: "list", ResourceKind: "namespaces"}).Allowed {
		t.Fatal("cluster-scoped request was denied")
	}
	if engine.Authorize(context.Background(), Subject{ID: "admin"}, Request{Operation: "list", Namespace: "default", ResourceKind: "namespaces"}).Allowed {
		t.Fatal("namespaced request matched cluster-only rule")
	}
	if !engine.Authorize(context.Background(), Subject{ID: "user", Groups: []string{"platform"}}, Request{Operation: "delete", Namespace: "default", ResourceKind: "pods"}).Allowed {
		t.Fatal("explicit wildcard rule was denied")
	}
}

func TestInvalidUpdateDoesNotReplaceActivePolicy(t *testing.T) {
	engine, err := New(Policy{Rules: []Rule{{
		ID: "read", Subjects: []string{"user"}, Namespaces: []string{"default"},
		Operations: []string{"get"}, ResourceKinds: []string{"pods"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Update(Policy{Rules: []Rule{{ID: "bad", Groups: []string{"*"}, Operations: []string{"*"}, ResourceKinds: []string{"*"}}}}); err == nil {
		t.Fatal("invalid policy update succeeded")
	}
	if !engine.Authorize(context.Background(), Subject{ID: "user"}, Request{Operation: "get", Namespace: "default", ResourceKind: "pods"}).Allowed {
		t.Fatal("invalid update replaced active policy")
	}
}

func TestPolicyConcurrentUpdateAndAuthorization(t *testing.T) {
	allowed := Policy{Rules: []Rule{{
		ID: "allow", Groups: []string{"developers"}, Namespaces: []string{"default"},
		Operations: []string{"list"}, ResourceKinds: []string{"pods"},
	}}}
	engine, err := New(allowed)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for range 8 {
		wait.Go(func() {
			for range 500 {
				engine.Authorize(context.Background(), Subject{ID: "user", Groups: []string{"developers"}}, Request{Operation: "list", Namespace: "default", ResourceKind: "pods"})
			}
		})
	}
	for range 100 {
		if err := engine.Update(Policy{Version: CurrentVersion, Rules: []Rule{}}); err != nil {
			t.Fatal(err)
		}
		if err := engine.Update(allowed); err != nil {
			t.Fatal(err)
		}
	}
	wait.Wait()
}

func TestLoadPolicyIsStrictAndBounded(t *testing.T) {
	directory := t.TempDir()
	valid := filepath.Join(directory, "policy.json")
	if err := os.WriteFile(valid, []byte(`{"version":1,"rules":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err := Load(valid)
	if err != nil || policy.Version != CurrentVersion || policy.Rules == nil {
		t.Fatalf("policy = %#v, error = %v", policy, err)
	}
	unknown := filepath.Join(directory, "unknown.json")
	if err := os.WriteFile(unknown, []byte(`{"version":1,"rules":[],"clientSecret":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(unknown); err == nil {
		t.Fatal("policy accepted an unknown secret field")
	}
	oversized := filepath.Join(directory, "oversized.json")
	if err := os.WriteFile(oversized, make([]byte, maxPolicyBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(oversized); err == nil {
		t.Fatal("oversized policy was accepted")
	}
}
