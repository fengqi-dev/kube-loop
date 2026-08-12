package listener

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

const connectivityTestTimeout = 3 * time.Second

// Test verifies that an active TCP port-forward accepts a connection on its
// local endpoint. UDP requires an application-specific payload and response,
// so it cannot be tested generically.
func (m *Manager) Test(parent context.Context, id string) error {
	m.mu.Lock()
	runtime := m.active[id]
	m.mu.Unlock()
	if runtime == nil {
		return fmt.Errorf("port-forward %q not found", id)
	}
	if strings.ToLower(runtime.info.Protocol) != "tcp" {
		return errors.New("generic connectivity tests are only supported for TCP sessions")
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, connectivityTestTimeout)
	defer cancel()

	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", runtime.info.Address)
	if err != nil {
		return fmt.Errorf("dial %s: %w", runtime.info.Address, err)
	}
	return connection.Close()
}
