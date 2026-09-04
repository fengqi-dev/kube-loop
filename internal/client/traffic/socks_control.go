package traffic

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/fengqi-dev/kube-loop/internal/utils"
)

func (d Dialer) openControl(ctx context.Context) (net.Conn, *bufio.Reader, error) {
	if d.Endpoint.Address == "" {
		return nil, nil, errors.New("sOCKS endpoint address is required")
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", d.Endpoint.Address)
	if err != nil {
		return nil, nil, fmt.Errorf("connect SOCKS endpoint: %w", err)
	}
	reader := bufio.NewReader(conn)
	methods := []byte{socksMethodNone}
	if d.Endpoint.Username != "" || d.Endpoint.Password != "" {
		methods = []byte{socksMethodPassword}
	}
	if err := utils.WriteAll(conn, append([]byte{socksVersion, 1}, methods...)); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(reader, response); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if response[0] != socksVersion {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("unexpected SOCKS version %d", response[0])
	}
	switch response[1] {
	case socksMethodNone:
		if d.Endpoint.Username != "" || d.Endpoint.Password != "" {
			_ = conn.Close()
			return nil, nil, errors.New("sOCKS endpoint skipped required authentication")
		}
	case socksMethodPassword:
		if err := authenticate(conn, reader, d.Endpoint.Username, d.Endpoint.Password); err != nil {
			_ = conn.Close()
			return nil, nil, err
		}
	default:
		_ = conn.Close()
		return nil, nil, fmt.Errorf("sOCKS authentication method %d rejected", response[1])
	}
	return conn, reader, nil
}

func authenticate(conn net.Conn, reader *bufio.Reader, username, password string) error {
	if len(username) == 0 || len(username) > 255 || len(password) == 0 || len(password) > 255 {
		return errors.New("invalid SOCKS username or password length")
	}
	request := make([]byte, 0, len(username)+len(password)+3)
	//nolint:gosec // The length validation above bounds both values to one-byte SOCKS fields.
	request = append(request, 1, byte(len(username)))
	request = append(request, username...)
	//nolint:gosec // The length validation above bounds both values to one-byte SOCKS fields.
	request = append(request, byte(len(password)))
	request = append(request, password...)
	if err := utils.WriteAll(conn, request); err != nil {
		return err
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(reader, response); err != nil {
		return err
	}
	if response[0] != 1 || response[1] != 0 {
		return errors.New("sOCKS authentication failed")
	}
	return nil
}
