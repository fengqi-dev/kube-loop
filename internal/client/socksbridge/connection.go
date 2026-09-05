package socksbridge

import (
	"context"
	"io"
	"net"
)

// remoteResolver preserves domain names so the cluster-side proxy resolves
// Kubernetes service names instead of the workstation.
type remoteResolver struct{}

func (remoteResolver) Resolve(ctx context.Context, _ string) (context.Context, net.IP, error) {
	return ctx, nil, nil
}

type bufferedConn struct {
	net.Conn

	reader io.Reader
}

func (connection *bufferedConn) Read(value []byte) (int, error) {
	return connection.reader.Read(value)
}

func (connection *bufferedConn) CloseWrite() error {
	if writer, ok := connection.Conn.(interface{ CloseWrite() error }); ok {
		return writer.CloseWrite()
	}
	return connection.Close()
}
