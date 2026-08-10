package mirrorapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/servicebinding"
)

type primaryPool struct {
	dial func(context.Context, string, string) (net.Conn, error)

	mu      sync.Mutex
	targets map[string][]servicebinding.BackendTarget
	next    map[string]int
}

func newPrimaryPool(
	sets []servicebinding.BackendSet,
	dial func(context.Context, string, string) (net.Conn, error),
) (*primaryPool, error) {
	if len(sets) == 0 {
		return nil, errors.New("Mirror requires original Service backends")
	}
	if dial == nil {
		dialer := &net.Dialer{}
		dial = dialer.DialContext
	}
	pool := &primaryPool{
		dial: dial, targets: make(map[string][]servicebinding.BackendTarget, len(sets)),
		next: make(map[string]int, len(sets)),
	}
	for _, set := range sets {
		key := primaryKey(strings.ToLower(string(set.Protocol)), set.ServicePort)
		if _, exists := pool.targets[key]; exists || len(set.Targets) == 0 {
			return nil, errors.New("Mirror backend sets are invalid")
		}
		pool.targets[key] = append([]servicebinding.BackendTarget(nil), set.Targets...)
	}
	return pool, nil
}

func (pool *primaryPool) Dial(ctx context.Context, protocol string, servicePort int32) (net.Conn, error) {
	key := primaryKey(protocol, servicePort)
	pool.mu.Lock()
	targets := append([]servicebinding.BackendTarget(nil), pool.targets[key]...)
	start := pool.next[key]
	if len(targets) > 0 {
		pool.next[key] = (start + 1) % len(targets)
	}
	pool.mu.Unlock()
	if len(targets) == 0 {
		return nil, errors.New("Mirror primary Service port is unavailable")
	}
	var result error
	for offset := range targets {
		target := targets[(start+offset)%len(targets)]
		connection, err := pool.dial(ctx, protocol, net.JoinHostPort(target.Address, strconv.Itoa(int(target.Port))))
		if err == nil {
			return connection, nil
		}
		result = errors.Join(result, err)
	}
	return nil, fmt.Errorf("dial Mirror primary: %w", result)
}

func primaryKey(protocol string, servicePort int32) string {
	return strings.ToLower(strings.TrimSpace(protocol)) + "/" + strconv.Itoa(int(servicePort))
}
