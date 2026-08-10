package capability

import (
	"slices"
	"testing"
)

func TestNormalizeValidatesBindingsAndDeduplicates(t *testing.T) {
	got, err := Normalize(Snapshot{
		SchemaVersion: SchemaVersion, PrincipalID: "oidc:user-1", Namespace: "development",
		GatewayVersion: "v2-test", Capabilities: []string{"pods.list", "cluster.tunnel", "pods.list"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Capabilities, []string{"pods.list", "cluster.tunnel"}) {
		t.Fatalf("capabilities = %v", got.Capabilities)
	}
	for name, mutate := range map[string]func(*Snapshot){
		"schema":     func(value *Snapshot) { value.SchemaVersion++ },
		"principal":  func(value *Snapshot) { value.PrincipalID = " principal" },
		"namespace":  func(value *Snapshot) { value.Namespace = "Bad_Name" },
		"gateway":    func(value *Snapshot) { value.GatewayVersion = "v2\nforged" },
		"capability": func(value *Snapshot) { value.Capabilities = []string{"Pods.List"} },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := got
			mutate(&candidate)
			if _, err := Normalize(candidate); err == nil {
				t.Fatal("invalid snapshot was accepted")
			}
		})
	}
}
