package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/helperrpc"
	"github.com/fengqi-dev/kube-loop/internal/protocol/sessionspec"
)

// Client talks to the privileged helper over a local socket/pipe.
type Client struct {
	Token string
	Dial  func(context.Context) (net.Conn, error)
}

func NewClient() (*Client, error) {
	token, err := ReadUserToken()
	if err != nil {
		return nil, err
	}
	return &Client{Token: token, Dial: dialHelper}, nil
}

func (c *Client) Ping(ctx context.Context) (helperrpc.Response, error) {
	return c.roundTrip(ctx, helperrpc.Request{Op: helperrpc.OpPing})
}

func (c *Client) Status(ctx context.Context) (helperrpc.Response, error) {
	return c.roundTrip(ctx, helperrpc.Request{Op: helperrpc.OpStatus})
}

func (c *Client) Start(ctx context.Context, session sessionspec.Spec) (helperrpc.Response, error) {
	return c.roundTrip(ctx, helperrpc.Request{Op: helperrpc.OpStart, Session: &session})
}

func (c *Client) Stop(ctx context.Context, sessionID string) (helperrpc.Response, error) {
	return c.roundTrip(ctx, helperrpc.Request{Op: helperrpc.OpStop, SessionID: sessionID})
}

func (c *Client) StopAll(ctx context.Context) (helperrpc.Response, error) {
	return c.roundTrip(ctx, helperrpc.Request{Op: helperrpc.OpStopAll})
}

func (c *Client) UpdateDNS(
	ctx context.Context,
	sessionID string,
	dns sessionspec.DNSMeta,
) (helperrpc.Response, error) {
	return c.roundTrip(ctx, helperrpc.Request{
		Op: helperrpc.OpUpdateDNS, SessionID: sessionID, DNS: &dns,
	})
}

func (c *Client) ReadLogs(ctx context.Context, sessionID string, offset int64) (helperrpc.Response, error) {
	return c.roundTrip(
		ctx,
		helperrpc.Request{Op: helperrpc.OpReadLogs, SessionID: sessionID, LogOffset: offset},
	)
}

func (c *Client) roundTrip(ctx context.Context, request helperrpc.Request) (helperrpc.Response, error) {
	if c.Token == "" {
		return helperrpc.Response{}, fmt.Errorf("helper token is required")
	}
	request.Token = c.Token
	dial := c.Dial
	if dial == nil {
		dial = dialHelper
	}
	conn, err := dial(ctx)
	if err != nil {
		return helperrpc.Response{}, err
	}
	defer func() { _ = conn.Close() }()
	timeout := 30 * time.Second
	if request.Op == helperrpc.OpStart {
		timeout = 3 * time.Minute
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(request); err != nil {
		return helperrpc.Response{}, fmt.Errorf("write helper request: %w", err)
	}
	reader := bufio.NewReader(conn)
	decoder := json.NewDecoder(reader)
	var response helperrpc.Response
	if err := decoder.Decode(&response); err != nil {
		return helperrpc.Response{}, fmt.Errorf("read helper response: %w", err)
	}
	if !response.OK {
		if response.Error == "" {
			response.Error = "helper request failed"
		}
		return response, fmt.Errorf("%s", response.Error)
	}
	return response, nil
}
