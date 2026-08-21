package helper

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

func dialHelper(ctx context.Context) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", SocketPath())
}

func listenHelper(ownerSID string) (net.Listener, error) {
	path := SocketPath()
	//nolint:gosec // The system socket directory must be traversable; socket access is authenticated separately.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	_ = os.Remove(path)
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen helper socket: %w", err)
	}
	if err := configureHelperSocketAccess(path, ownerSID); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("configure helper socket access: %w", err)
	}
	return listener, nil
}

func withDialTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, 3*time.Second)
}
