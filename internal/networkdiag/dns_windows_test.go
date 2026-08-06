//go:build windows

package networkdiag

import (
	"errors"
	"strings"
	"testing"
)

func TestDNSPortDiagnosticReportsProtocol(t *testing.T) {
	issue := dnsPortDiagnostic(nil, errors.New("access denied"))
	if issue.Code != "dns_port_unavailable" ||
		!strings.Contains(issue.Message, "UDP: access denied") {
		t.Fatalf("unexpected issue: %#v", issue)
	}
}
