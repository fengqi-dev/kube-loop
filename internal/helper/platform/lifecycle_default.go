//go:build !darwin && !linux && !windows

package platform

import (
	"os"

	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

func ApplyDNS(string, singbox.DNSMeta) error       { return nil }
func RestoreDNS(string, singbox.DNSMeta) error     { return nil }
func ApplyLinkDNS(string, singbox.DNSMeta) error   { return nil }
func RestoreLinkDNS(string) error                  { return nil }
func CleanupRoutes([]string)                       {}
func StopManagedProcess(process *os.Process) error { return process.Kill() }
