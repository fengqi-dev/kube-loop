package trafficinspect

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"

	"golang.org/x/net/http2"
)

// Close implements the handler lifecycle. ServeConn owns and closes each
// connection-scoped upstream transport.
func (h *Handler) Close() error {
	return nil
}

func (h *Handler) newRoundTripper(target string) *upstreamRoundTripper {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return h.dialContext(ctx, network, target)
		},
		ForceAttemptHTTP2: h.allowHTTP2,
		TLSClientConfig:   h.tlsConfig.Clone(),
	}
	h2cTransport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, _ string, _ *tls.Config) (net.Conn, error) {
			return h.dialContext(ctx, network, target)
		},
	}
	return &upstreamRoundTripper{http: transport, h2c: h2cTransport}
}

// ServeConn takes ownership of connection and transparently inspects traffic
// addressed to target. It returns after the connection closes or ctx is
// canceled.
func (h *Handler) ServeConn(ctx context.Context, connection net.Conn, target string) error {
	if connection == nil {
		return errors.New("traffic inspection connection is required")
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		_ = connection.Close()
		return fmt.Errorf("parse traffic inspection target: %w", err)
	}
	if h.enabled != nil && !h.enabled() {
		return h.relayUninspected(ctx, connection, target)
	}
	reader := bufio.NewReader(connection)
	if !sniffInspectableProtocol(connection, reader) {
		return h.relayUninspected(ctx, &bufferedConn{Conn: connection, reader: reader}, target)
	}
	roundTripper := h.newRoundTripper(target)
	defer roundTripper.Close()
	inspection := &inspectionConnection{
		destination:  canonicalAuthority(target, "https"),
		roundTripper: roundTripper,
	}
	ctx = context.WithValue(ctx, inspectionConnectionKey{}, inspection)

	tracked := newTransparentConn(&bufferedConn{Conn: connection, reader: reader})
	request := (&http.Request{
		Method: http.MethodConnect,
		URL: &url.URL{
			Host:   target,
			Opaque: target,
		},
		Host:       target,
		Header:     make(http.Header),
		RemoteAddr: connection.RemoteAddr().String(),
	}).WithContext(ctx)
	writer := &connectionResponseWriter{
		connection: tracked,
		header:     make(http.Header),
	}

	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		h.proxy.ServeHTTP(writer, request)
	}()
	defer func() {
		_ = tracked.Close()
		<-serveDone
	}()

	select {
	case <-tracked.done:
		return nil
	case <-ctx.Done():
		if err := tracked.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			return fmt.Errorf("close canceled traffic inspection connection: %w", err)
		}
		return context.Cause(ctx)
	case <-serveDone:
		select {
		case <-tracked.done:
			return nil
		case <-ctx.Done():
			if err := tracked.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				return fmt.Errorf("close canceled traffic inspection connection: %w", err)
			}
			return context.Cause(ctx)
		}
	}
}
