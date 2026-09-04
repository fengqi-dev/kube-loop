//go:build !windows

package runtime

import "github.com/fengqi-dev/kube-loop/internal/utils"

func selectDNSPort() (int, error) {
	return utils.FreeTCPPort()
}
