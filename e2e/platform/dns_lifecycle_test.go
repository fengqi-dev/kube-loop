//go:build e2e

package platform

import (
	"os"
	"testing"

	helperplatform "github.com/fengqi-dev/kube-loop/internal/helper/platform"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

const platformE2EDomain = "kubeloop-e2e.test"

func TestPlatformDNSApplyAndRestore(t *testing.T) {
	if os.Getenv("KUBELOOP_PLATFORM_E2E") != "1" {
		t.Skip("set KUBELOOP_PLATFORM_E2E=1 to modify real platform DNS")
	}
	workDir := t.TempDir()
	dns := singbox.DNSMeta{
		Listen:  "127.0.0.1",
		Port:    53,
		Domains: []string{platformE2EDomain},
		Search:  []string{platformE2EDomain},
		Ndots:   5,
	}
	t.Cleanup(func() {
		if err := helperplatform.RestoreDNS(workDir, dns); err != nil {
			t.Errorf("cleanup platform DNS: %v", err)
		}
	})

	if err := helperplatform.ApplyDNS(workDir, dns); err != nil {
		t.Fatalf("apply platform DNS: %v", err)
	}
	assertPlatformE2EDNS(t, true)
	if err := helperplatform.RestoreDNS(workDir, dns); err != nil {
		t.Fatalf("restore platform DNS: %v", err)
	}
	assertPlatformE2EDNS(t, false)
}
