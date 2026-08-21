package dataplane

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"time"
)

const connectivityTestTimeout = 5 * time.Second

// TestConnectivity verifies the complete local SOCKS -> Gateway -> cluster
// path by opening a TCP connection to the cluster DNS service.
func (runtime *Runtime) TestConnectivity(ctx context.Context) (resultErr error) {
	if ctx == nil {
		return errors.New("connectivity test context is required")
	}
	runtime.stateMu.Lock()
	status := runtime.status
	dnsServer := runtime.session.NetworkSpec.DNSServer
	runtime.stateMu.Unlock()
	if status.State != dataplaneConnected || status.SOCKSAddress == "" {
		return errors.New("data Plane is not connected")
	}
	target, err := netip.ParseAddr(dnsServer)
	if err != nil {
		return fmt.Errorf("cluster DNS address is unavailable: %w", err)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, connectivityTestTimeout)
		defer cancel()
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", status.SOCKSAddress)
	if err != nil {
		return fmt.Errorf("connect local SOCKS listener: %w", err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close connectivity probe: %w", err))
		}
	}()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if err := socksConnect(connection, target, 53); err != nil {
		return fmt.Errorf("connect cluster DNS through Gateway: %w", err)
	}
	return nil
}

func socksConnect(connection net.Conn, target netip.Addr, port uint16) error {
	if _, err := connection.Write([]byte{5, 1, 0}); err != nil {
		return fmt.Errorf("write SOCKS greeting: %w", err)
	}
	var greeting [2]byte
	if _, err := io.ReadFull(connection, greeting[:]); err != nil {
		return fmt.Errorf("read SOCKS greeting: %w", err)
	}
	if greeting != [2]byte{5, 0} {
		return fmt.Errorf("sOCKS listener rejected authentication method: %v", greeting)
	}
	request := []byte{5, 1, 0}
	if target.Is4() {
		request = append(request, 1)
		request = append(request, target.AsSlice()...)
	} else {
		request = append(request, 4)
		request = append(request, target.AsSlice()...)
	}
	request = binary.BigEndian.AppendUint16(request, port)
	if _, err := connection.Write(request); err != nil {
		return fmt.Errorf("write SOCKS connect request: %w", err)
	}
	var response [4]byte
	if _, err := io.ReadFull(connection, response[:]); err != nil {
		return fmt.Errorf("read SOCKS connect response: %w", err)
	}
	if response[0] != 5 || response[1] != 0 {
		return fmt.Errorf("sOCKS target connection failed with status %d", response[1])
	}
	var addressLength int
	switch response[3] {
	case 1:
		addressLength = 4
	case 4:
		addressLength = 16
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(connection, length[:]); err != nil {
			return fmt.Errorf("read SOCKS response address length: %w", err)
		}
		addressLength = int(length[0])
	default:
		return fmt.Errorf("sOCKS response used unsupported address type %d", response[3])
	}
	_, err := io.CopyN(io.Discard, connection, int64(addressLength+2))
	return err
}
