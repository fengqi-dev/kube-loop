package intercept

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/tunnel"
)

const mirrorShadowQueueSize = 16

const (
	ModeExchange = "exchange"
	ModeMirror   = "mirror"
)

// primaryAddress picks the first ready Pod IP:targetPort for a Service port.
func primaryAddress(
	backends []Backend,
	port InterceptPort,
) (string, error) {
	protocol := port.Protocol
	if protocol == "" {
		protocol = ProtocolTCP
	}
	for _, backend := range backends {
		portNum, ok := matchBackendPort(backend.Ports, port, protocol)
		if !ok {
			continue
		}
		if backend.Address != "" {
			return net.JoinHostPort(backend.Address, strconv.Itoa(int(portNum))), nil
		}
	}
	return "", fmt.Errorf(
		"no ready backend for service port %s/%d", port.Name, port.ServicePort,
	)
}

func matchBackendPort(
	ports []BackendPort,
	want InterceptPort,
	protocol string,
) (int32, bool) {
	for _, port := range ports {
		portProtocol := port.Protocol
		if portProtocol == "" {
			portProtocol = ProtocolTCP
		}
		if portProtocol != protocol {
			continue
		}
		if want.Name != "" && port.Name == want.Name {
			return port.Port, true
		}
		if port.Port == want.ServicePort {
			return port.Port, true
		}
	}
	if len(ports) == 1 {
		portProtocol := ports[0].Protocol
		if portProtocol == "" {
			portProtocol = ProtocolTCP
		}
		if portProtocol == protocol {
			return ports[0].Port, true
		}
	}
	return 0, false
}

func buildPrimaryAddrs(
	backends []Backend,
	ports []InterceptPort,
	portKeys map[string]PortMapping,
	interceptID string,
) (map[string]string, error) {
	if len(backends) == 0 {
		return nil, fmt.Errorf("service has no endpoints to mirror")
	}
	out := make(map[string]string, len(portKeys))
	for _, port := range ports {
		network := tunnelNetwork(port.Protocol)
		subID := fmt.Sprintf("%s:%s:%d", interceptID, networkName(network), port.ServicePort)
		if _, ok := portKeys[subID]; !ok {
			continue
		}
		addr, err := primaryAddress(backends, port)
		if err != nil {
			return nil, err
		}
		out[subID] = addr
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no mirror backends resolved")
	}
	return out, nil
}

func tunnelNetwork(protocol string) byte {
	return protocolToNetwork(protocol)
}

func closeWrite(conn net.Conn) {
	if value, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = value.CloseWrite()
	}
}

// mirrorTCP forwards client traffic to primary (response path) and tees
// requests to local (responses discarded). local may be nil.
func mirrorTCP(client, primary net.Conn, local net.Conn) {
	defer client.Close()
	defer primary.Close()
	var shadow *shadowWriter
	if local != nil {
		go func() { _, _ = io.Copy(io.Discard, local) }()
		shadow = newShadowWriter(local)
		defer shadow.Close()
	}

	done := make(chan struct{}, 2)
	go func() {
		dst := io.Writer(primary)
		if shadow != nil {
			// Tee to shadow before primary. MultiWriter stops at the first
			// error and otherwise writes in order; writing primary first let a
			// fast response close the session before the request was queued.
			dst = io.MultiWriter(shadow, primary)
		}
		_, _ = io.Copy(dst, client)
		closeWrite(primary)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, primary)
		closeWrite(client)
		done <- struct{}{}
	}()
	<-done
}

// mirrorUDP forwards client datagrams to primary (response path) and tees
// requests to local (responses discarded). primaryFramed is true when primary
// is a Gateway UDP tunnel that uses length-prefixed datagrams.
func mirrorUDP(client, primary net.Conn, primaryFramed bool, local net.Conn) {
	defer client.Close()
	defer primary.Close()
	var shadow *shadowWriter
	if local != nil {
		go func() {
			buf := make([]byte, tunnel.MaxDatagramSize)
			for {
				if _, err := local.Read(buf); err != nil {
					return
				}
			}
		}()
		shadow = newShadowWriter(local)
		defer shadow.Close()
	}

	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }

	go func() {
		defer stop()
		reader := bufio.NewReader(client)
		var buffer []byte
		for {
			payload, err := tunnel.ReadDatagram(reader, buffer)
			if err != nil {
				return
			}
			buffer = payload[:0]
			// Tee before primary write so a fast response path cannot close the
			// shadow mid-datagram.
			if shadow != nil {
				_, _ = shadow.Write(payload)
			}
			if err := writeMirrorUDP(primary, primaryFramed, payload); err != nil {
				return
			}
		}
	}()

	go func() {
		defer stop()
		if primaryFramed {
			reader := bufio.NewReader(primary)
			var buffer []byte
			for {
				payload, err := tunnel.ReadDatagram(reader, buffer)
				if err != nil {
					return
				}
				buffer = payload[:0]
				if err := tunnel.WriteDatagram(client, payload); err != nil {
					return
				}
			}
		}
		buf := make([]byte, tunnel.MaxDatagramSize)
		for {
			n, err := primary.Read(buf)
			if err != nil {
				return
			}
			if err := tunnel.WriteDatagram(client, buf[:n]); err != nil {
				return
			}
		}
	}()

	<-done
}

// shadowWriter decouples Mirror's best-effort local copy from the primary
// request path. Once its bounded queue fills, it closes the shadow connection
// and silently discards subsequent bytes for that stream; Primary never waits
// for a slow local service.
type shadowWriter struct {
	conn    net.Conn
	queue   chan []byte
	mu      sync.Mutex
	aborted bool
	closed  bool
	done    chan struct{}
}

func newShadowWriter(conn net.Conn) *shadowWriter {
	writer := &shadowWriter{
		conn: conn, queue: make(chan []byte, mirrorShadowQueueSize), done: make(chan struct{}),
	}
	go writer.run()
	return writer
}

func (w *shadowWriter) Write(payload []byte) (int, error) {
	copyOfPayload := append([]byte(nil), payload...)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.aborted || w.closed {
		return len(payload), nil
	}
	select {
	case w.queue <- copyOfPayload:
	default:
		w.aborted = true
		_ = w.conn.Close()
	}
	return len(payload), nil
}

func (w *shadowWriter) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	close(w.queue)
	w.mu.Unlock()
	select {
	case <-w.done:
	case <-time.After(50 * time.Millisecond):
	}
	return w.conn.Close()
}

func (w *shadowWriter) run() {
	defer close(w.done)
	for payload := range w.queue {
		w.mu.Lock()
		aborted := w.aborted
		w.mu.Unlock()
		if aborted {
			continue
		}
		if _, err := w.conn.Write(payload); err != nil {
			w.mu.Lock()
			w.aborted = true
			w.mu.Unlock()
			_ = w.conn.Close()
		}
	}
}

func writeMirrorUDP(primary net.Conn, framed bool, payload []byte) error {
	if framed {
		return tunnel.WriteDatagram(primary, payload)
	}
	_, err := primary.Write(payload)
	return err
}

// hostMirrorUDPConn is the host-TUN view of a mirrored UDP Service: requests
// go to primary (response path) and are teed to the local shadow.
type hostMirrorUDPConn struct {
	primary       net.Conn
	primaryFramed bool
	primaryReader *bufio.Reader
	shadow        *shadowWriter
	closeOnce     sync.Once
}

func newHostMirrorUDPConn(primary net.Conn, primaryFramed bool, local net.Conn) net.Conn {
	conn := &hostMirrorUDPConn{primary: primary, primaryFramed: primaryFramed}
	if primaryFramed {
		conn.primaryReader = bufio.NewReader(primary)
	}
	if local != nil {
		go func() {
			buf := make([]byte, tunnel.MaxDatagramSize)
			for {
				if _, err := local.Read(buf); err != nil {
					return
				}
			}
		}()
		conn.shadow = newShadowWriter(local)
	}
	return conn
}

func (c *hostMirrorUDPConn) Read(buffer []byte) (int, error) {
	if c.primaryFramed {
		payload, err := tunnel.ReadDatagram(c.primaryReader, nil)
		if err != nil {
			return 0, err
		}
		n := copy(buffer, payload)
		if n < len(payload) {
			return n, io.ErrShortBuffer
		}
		return n, nil
	}
	return c.primary.Read(buffer)
}

func (c *hostMirrorUDPConn) Write(payload []byte) (int, error) {
	if c.shadow != nil {
		_, _ = c.shadow.Write(payload)
	}
	if err := writeMirrorUDP(c.primary, c.primaryFramed, payload); err != nil {
		return 0, err
	}
	return len(payload), nil
}

func (c *hostMirrorUDPConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		if c.shadow != nil {
			_ = c.shadow.Close()
		}
		err = c.primary.Close()
	})
	return err
}

func (c *hostMirrorUDPConn) LocalAddr() net.Addr                { return c.primary.LocalAddr() }
func (c *hostMirrorUDPConn) RemoteAddr() net.Addr               { return c.primary.RemoteAddr() }
func (c *hostMirrorUDPConn) SetDeadline(t time.Time) error      { return c.primary.SetDeadline(t) }
func (c *hostMirrorUDPConn) SetReadDeadline(t time.Time) error  { return c.primary.SetReadDeadline(t) }
func (c *hostMirrorUDPConn) SetWriteDeadline(t time.Time) error { return c.primary.SetWriteDeadline(t) }
