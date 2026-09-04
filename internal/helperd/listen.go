package helperd

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/fengqi-dev/kube-loop/internal/helper"
)

func listenHelper(ownerSID string) (net.Listener, error) {
	path := helper.SocketPath()
	//nolint:gosec // The system socket directory must be traversable; socket access is authenticated separately.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	_ = os.Remove(path)
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen helper socket: %w", err)
	}
	if err := helper.ConfigureSocketAccess(path, ownerSID); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("configure helper socket access: %w", err)
	}
	return listener, nil
}
