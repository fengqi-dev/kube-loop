package platform

import (
	"reflect"
	"strings"
	"testing"
)

func TestKubernetesSearchRoots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		domains []string
		want    []string
	}{
		{
			name: "extracts and deduplicates cluster root",
			domains: []string{
				"default.svc.cluster.local",
				"svc.cluster.local",
				"cluster.local",
			},
			want: []string{"cluster.local"},
		},
		{
			name:    "normalizes case and trailing dot",
			domains: []string{"Team.SVC.Example.Local.", "svc.EXAMPLE.local."},
			want:    []string{"example.local"},
		},
		{
			name:    "ignores unrelated domains",
			domains: []string{"corp.example", "dev.example"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := kubernetesSearchRoots(tt.domains); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("kubernetesSearchRoots() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWindowsSearchDomainMergeScript(t *testing.T) {
	t.Parallel()

	script := windowsSearchDomainMergeScript([]string{
		"default.svc.cluster.local",
		"svc.cluster.local",
		"cluster.local",
	})
	wants := []string{
		"$want=@('default.svc.cluster.local','svc.cluster.local','cluster.local')",
		"$roots=@('cluster.local')",
		"$value -eq ('svc.'+$root)",
		"$value.EndsWith('.svc.'+$root)",
		"$preserved += $item",
		"Set-DnsClientGlobalSetting -SuffixSearchList $merged",
	}
	for _, want := range wants {
		if !strings.Contains(script, want) {
			t.Errorf("windowsSearchDomainMergeScript() missing %q in %q", want, script)
		}
	}
}

func TestPowerShellLiteral(t *testing.T) {
	t.Parallel()

	if got, want := powershellLiteral("team's.example"), "'team''s.example'"; got != want {
		t.Fatalf("powershellLiteral() = %q, want %q", got, want)
	}
}
