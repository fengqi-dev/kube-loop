package helper

import (
	"context"
	"net"
	"time"
)

func dialHelper(ctx context.Context) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", SocketPath())
}

func withDialTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, 3*time.Second)
}
