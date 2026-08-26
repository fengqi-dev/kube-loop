//go:build darwin

package supervisor

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/supervisor"
)

type Client struct {
	Config Config
	Token  string
	Dial   func(context.Context, string) (*net.UnixConn, error)
}

func (c *Client) Status(ctx context.Context) (supervisor.Response, error) {
	return c.roundTrip(ctx, supervisor.Request{
		Protocol: supervisor.Version,
		Op:       supervisor.OpStatus,
	}, nil)
}

func (c *Client) RestartWorker(ctx context.Context) (supervisor.Response, error) {
	return c.roundTrip(ctx, supervisor.Request{
		Protocol: supervisor.Version,
		Op:       supervisor.OpRestartWorker,
	}, nil)
}

func (c *Client) UpdateWorker(
	ctx context.Context,
	manifest supervisor.UpdateManifest,
	source string,
) (supervisor.Response, error) {
	file, err := os.Open(source)
	if err != nil {
		return supervisor.Response{}, fmt.Errorf("open worker update: %w", err)
	}
	defer func() { _ = file.Close() }()
	return c.roundTrip(ctx, supervisor.Request{
		Protocol: supervisor.Version,
		Op:       supervisor.OpUpdateWorker,
		Manifest: &manifest,
	}, file)
}

func (c *Client) roundTrip(
	ctx context.Context,
	request supervisor.Request,
	body io.Reader,
) (supervisor.Response, error) {
	if c.Token == "" {
		return supervisor.Response{}, fmt.Errorf("supervisor token is required")
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
		return supervisor.Response{}, fmt.Errorf("connect supervisor: %w", err)
	}
	defer func() { _ = connection.Close() }()
	deadline := time.Now().Add(3 * time.Minute)
	if value, ok := ctx.Deadline(); ok {
		deadline = value
	}
	_ = connection.SetDeadline(deadline)
	if err := supervisor.WriteFrame(connection, request, supervisor.MaxRequestBytes); err != nil {
		return supervisor.Response{}, fmt.Errorf("write supervisor request: %w", err)
	}
	if body != nil {
		if _, err := io.Copy(connection, body); err != nil {
			if response, responseErr := readSupervisorResponse(connection); responseErr == nil && !response.OK {
				return response, supervisorResponseError(response)
			}
			return supervisor.Response{}, fmt.Errorf("write worker payload: %w", err)
		}
	}
	if err := connection.CloseWrite(); err != nil {
		return supervisor.Response{}, fmt.Errorf("finish supervisor request: %w", err)
	}
	response, err := readSupervisorResponse(connection)
	if err != nil {
		return supervisor.Response{}, fmt.Errorf("read supervisor response: %w", err)
	}
	return response, supervisorResponseError(response)
}

func readSupervisorResponse(connection io.Reader) (supervisor.Response, error) {
	var response supervisor.Response
	if err := supervisor.ReadFrame(connection, &response, supervisor.MaxResponseBytes); err != nil {
		return supervisor.Response{}, err
	}
	return response, nil
}

func supervisorResponseError(response supervisor.Response) error {
	if !response.OK {
		if response.Error == "" {
			response.Error = "supervisor request failed"
		}
		return fmt.Errorf("%s", response.Error)
	}
	return nil
}
