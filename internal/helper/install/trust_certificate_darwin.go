//go:build darwin

package install

import (
	"context"
	"fmt"

	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
)

// ManageDarwinTrustFromCLI runs inside the already elevated helper process so
// service and certificate lifecycle changes share one administrator approval.
func ManageDarwinTrustFromCLI(ctx context.Context, operation, certificatePath string) error {
	switch operation {
	case "install":
		return trafficinspect.InstallSystemTrustFromCertificateFile(ctx, certificatePath)
	case "uninstall":
		return trafficinspect.UninstallSystemTrustFromCertificateFile(ctx, certificatePath)
	default:
		return fmt.Errorf("unsupported certificate trust operation %q", operation)
	}
}
