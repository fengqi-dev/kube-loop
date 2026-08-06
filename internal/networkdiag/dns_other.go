//go:build !windows

package networkdiag

func inspectDNSPort() *Issue {
	return nil
}
