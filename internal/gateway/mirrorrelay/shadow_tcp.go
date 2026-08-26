package mirrorrelay

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/entity"
	"github.com/fengqi-dev/kube-loop/internal/gateway/trafficlistener"
	"github.com/fengqi-dev/kube-loop/internal/protocol/mirrorstream"
)

type tcpPrimaryStream struct {
	client  net.Conn
	primary net.Conn
}

func (relay *mirrorRelay) acceptTCP(ctx context.Context, binding trafficlistener.TCPBinding) error {
	for {
		client, err := binding.Listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil //nolint:nilerr // A closed listener is the normal relay shutdown signal.
			}
			return err
		}
		relay.streams.Go(func() {
			relay.serveTCP(ctx, binding.Port, client)
		})
	}
}

func (relay *mirrorRelay) serveTCP(ctx context.Context, port entity.Port, client net.Conn) {
	dialContext, cancel := context.WithTimeout(ctx, relay.config.PrimaryDialTimeout)
	primary, err := relay.primaries.Dial(dialContext, "tcp", port.ServicePort)
	cancel()
	if err != nil {
		_ = client.Close()
		return
	}
	id := relay.nextStreamID()
	relay.mu.Lock()
	relay.tcp[id] = &tcpPrimaryStream{client: client, primary: primary}
	relay.mu.Unlock()
	shadow := relay.emit(mirrorstream.Frame{
		Type:     mirrorstream.Open,
		StreamID: id,
		// BindListeners validates ServicePort as a positive 16-bit port.
		ServicePort: uint32(port.ServicePort), //nolint:gosec // Validated as a positive 16-bit port.
		Protocol:    mirrorstream.ProtocolTCP,
	})

	requestDone := make(chan struct{})
	responseDone := make(chan struct{})
	var copies sync.WaitGroup
	copies.Add(2)
	go func() {
		defer copies.Done()
		defer close(requestDone)
		buffer := make([]byte, 32<<10)
		for {
			count, readErr := client.Read(buffer)
			if count > 0 {
				payload := buffer[:count]
				if writeErr := writePrimary(primary, payload); writeErr != nil {
					return
				}
				if shadow {
					shadow = relay.emit(mirrorstream.Frame{
						Type: mirrorstream.Data, StreamID: id, Payload: append([]byte(nil), payload...),
					})
				}
			}
			if readErr == nil {
				continue
			}
			if errors.Is(readErr, io.EOF) {
				closeWrite(primary)
				if shadow {
					relay.emit(mirrorstream.Frame{Type: mirrorstream.CloseWrite, StreamID: id})
				}
			}
			return
		}
	}()
	go func() {
		defer copies.Done()
		defer close(responseDone)
		_, _ = io.Copy(client, primary)
		closeWrite(client)
	}()

	select {
	case <-ctx.Done():
	case <-responseDone:
	case <-requestDone:
		select {
		case <-ctx.Done():
		case <-responseDone:
		}
	}
	relay.removeTCP(id)
	copies.Wait()
	relay.emit(mirrorstream.Frame{Type: mirrorstream.Close, StreamID: id})
	relay.clearDropped(id)
}
