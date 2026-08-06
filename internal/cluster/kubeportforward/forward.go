package kubeportforward

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

type Forwarder struct {
	localPort uint16
	stop      chan struct{}
	done      chan error
	once      sync.Once
}

func (f *Forwarder) Address() string {
	return fmt.Sprintf("127.0.0.1:%d", f.localPort)
}

func (f *Forwarder) Close() error {
	f.once.Do(func() { close(f.stop) })
	select {
	case err := <-f.done:
		return err
	case <-time.After(5 * time.Second):
		return errors.New("timed out stopping Kubernetes port-forward")
	}
}

// Start forwards a loopback TCP port through the Kubernetes API server to a
// Pod. A local port of zero requests an ephemeral port from the OS.
func Start(
	ctx context.Context,
	config *rest.Config,
	namespace, podName string,
	localPort, remotePort uint16,
) (*Forwarder, error) {
	if config == nil {
		return nil, errors.New("Kubernetes REST config is required")
	}
	if namespace == "" {
		return nil, errors.New("namespace is required")
	}
	if podName == "" {
		return nil, errors.New("pod name is required")
	}
	if remotePort == 0 {
		return nil, errors.New("remote port is required")
	}
	transport, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		return nil, fmt.Errorf("create port-forward transport: %w", err)
	}
	serverURL, err := url.Parse(config.Host)
	if err != nil {
		return nil, fmt.Errorf("parse API server URL: %w", err)
	}
	serverURL.Path = fmt.Sprintf(
		"/api/v1/namespaces/%s/pods/%s/portforward", namespace, podName,
	)
	dialer := spdy.NewDialer(
		upgrader,
		&http.Client{Transport: transport},
		http.MethodPost,
		serverURL,
	)
	stop := make(chan struct{})
	ready := make(chan struct{})
	done := make(chan error, 1)
	errorsOutput := &lockedBuffer{}
	forward, err := portforward.NewOnAddresses(
		dialer,
		[]string{"127.0.0.1"},
		[]string{fmt.Sprintf("%d:%d", localPort, remotePort)},
		stop, ready, io.Discard, errorsOutput,
	)
	if err != nil {
		return nil, fmt.Errorf("create port-forward: %w", err)
	}
	go func() { done <- forward.ForwardPorts() }()
	select {
	case <-ready:
	case err := <-done:
		return nil, fmt.Errorf("start port-forward: %w: %s", err, errorsOutput.String())
	case <-ctx.Done():
		close(stop)
		return nil, ctx.Err()
	}
	ports, err := forward.GetPorts()
	if err != nil || len(ports) != 1 {
		close(stop)
		return nil, fmt.Errorf("get local port: %w", err)
	}
	return &Forwarder{localPort: ports[0].Local, stop: stop, done: done}, nil
}

type lockedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(value)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}
