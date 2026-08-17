//go:build darwin

package platform

import (
	"reflect"
	"testing"
)

func TestMergeSearchDomains(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		desired  []string
		existing []string
		want     []string
	}{
		{
			name: "drops stale Kubernetes domains and preserves unrelated domains",
			desired: []string{
				"default.svc.cluster.local",
				"svc.cluster.local",
				"cluster.local",
			},
			existing: []string{
				"kube-system.svc.cluster.local",
				"kubeloop-system.svc.cluster.local",
				"svc.cluster.local",
				"corp.example",
			},
			want: []string{
				"default.svc.cluster.local",
				"svc.cluster.local",
				"cluster.local",
				"corp.example",
			},
		},
		{
			name:     "preserves existing domains when no cluster root is present",
			desired:  []string{"dev.example"},
			existing: []string{"corp.example", "dev.example"},
			want:     []string{"dev.example", "corp.example"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			existingBefore := append([]string(nil), tt.existing...)
			got := mergeSearchDomains(tt.desired, tt.existing)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("mergeSearchDomains() = %v, want %v", got, tt.want)
			}
			if !reflect.DeepEqual(tt.existing, existingBefore) {
				t.Fatalf("mergeSearchDomains() mutated existing domains: got %v, want %v", tt.existing, existingBefore)
			}
		})
	}
}
