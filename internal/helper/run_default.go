//go:build !windows

package helper

import "context"

// RunService starts the helper RPC server until the context is cancelled.
func RunService(ctx context.Context, server *Server) error {
	return server.Serve(ctx)
}
