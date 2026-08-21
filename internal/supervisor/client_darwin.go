//go:build darwin

package supervisor

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	supervisorprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/supervisor"
)

type Client struct {
	Config Config
	Token  string
	Dial   func(context.Context, string) (*net.UnixConn, error)
}

func (c *Client) Status(ctx context.Context) (supervisorprotocol.Response, error) {
	return c.roundTrip(ctx, supervisorprotocol.Request{
		Protocol: supervisorprotocol.Version,
		Op:       supervisorprotocol.OpStatus,
	}, nil)
}

func (c *Client) RestartWorker(ctx context.Context) (supervisorprotocol.Response, error) {
	return c.roundTrip(ctx, supervisorprotocol.Request{
		Protocol: supervisorprotocol.Version,
		Op:       supervisorprotocol.OpRestartWorker,
	}, nil)
}

func (c *Client) UpdateWorker(
	ctx context.Context,
	manifest supervisorprotocol.UpdateManifest,
	source string,
) (supervisorprotocol.Response, error) {
	file, err := os.Open(source)
	if err != nil {
		return supervisorprotocol.Response{}, fmt.Errorf("open worker update: %w", err)
	}
	defer func() { _ = file.Close() }()
	return c.roundTrip(ctx, supervisorprotocol.Request{
		Protocol: supervisorprotocol.Version,
		Op:       supervisorprotocol.OpUpdateWorker,
		Manifest: &manifest,
	}, file)
}

func (c *Client) roundTrip(
	ctx context.Context,
	request supervisorprotocol.Request,
	body io.Reader,
) (supervisorprotocol.Response, error) {
	if c.Token == "" {
		return supervisorprotocol.Response{}, fmt.Errorf("supervisor token is required")
	}
	request.Token = c.Token
	dial := c.Dial
	if dial == nil {
		dial = func(ctx context.Context, path string) (*net.UnixConn, error) {
			var dialer net.Dialer
			connection, err := dialer.DialContext(ctx, "unix", path)
			if err != nil {
				return nil, err
			}
			unixConnection, ok := connection.(*net.UnixConn)
			if !ok {
				_ = connection.Close()
				return nil, fmt.Errorf("unexpected supervisor connection type %T", connection)
			}
			return unixConnection, nil
		}
	}
	connection, err := dial(ctx, c.Config.SocketPath)
	if err != nil {
		return supervisorprotocol.Response{}, fmt.Errorf("connect supervisor: %w", err)
	}
	defer func() { _ = connection.Close() }()
	deadline := time.Now().Add(3 * time.Minute)
	if value, ok := ctx.Deadline(); ok {
		deadline = value
	}
	_ = connection.SetDeadline(deadline)
	if err := supervisorprotocol.WriteFrame(connection, request, supervisorprotocol.MaxRequestBytes); err != nil {
		return supervisorprotocol.Response{}, fmt.Errorf("write supervisor request: %w", err)
	}
	if body != nil {
		if _, err := io.Copy(connection, body); err != nil {
			if response, responseErr := readSupervisorResponse(connection); responseErr == nil && !response.OK {
				return response, supervisorResponseError(response)
			}
			return supervisorprotocol.Response{}, fmt.Errorf("write worker payload: %w", err)
		}
	}
	if err := connection.CloseWrite(); err != nil {
		return supervisorprotocol.Response{}, fmt.Errorf("finish supervisor request: %w", err)
	}
	response, err := readSupervisorResponse(connection)
	if err != nil {
		return supervisorprotocol.Response{}, fmt.Errorf("read supervisor response: %w", err)
	}
	return response, supervisorResponseError(response)
}

func readSupervisorResponse(connection io.Reader) (supervisorprotocol.Response, error) {
	var response supervisorprotocol.Response
	if err := supervisorprotocol.ReadFrame(connection, &response, supervisorprotocol.MaxResponseBytes); err != nil {
		return supervisorprotocol.Response{}, err
	}
	return response, nil
}

func supervisorResponseError(response supervisorprotocol.Response) error {
	if !response.OK {
		if response.Error == "" {
			response.Error = "supervisor request failed"
		}
		return fmt.Errorf("%s", response.Error)
	}
	return nil
}
