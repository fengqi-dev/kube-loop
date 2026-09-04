//go:build !darwin && !linux && !windows

package platform

import (
	"os"

	"github.com/fengqi-dev/kube-loop/internal/protocol/sessionspec"
)

func ApplyDNS(string, sessionspec.DNSMeta) error     { return nil }
func RestoreDNS(string, sessionspec.DNSMeta) error   { return nil }
func ApplyLinkDNS(string, sessionspec.DNSMeta) error { return nil }
func RestoreLinkDNS(string) error                    { return nil }
func CleanupRoutes([]string)                         {}
func StopManagedProcess(process *os.Process) error   { return process.Kill() }
