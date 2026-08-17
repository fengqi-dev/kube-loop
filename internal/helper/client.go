package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	helperprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/helper"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
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

func (c *Client) Ping(ctx context.Context) (helperprotocol.Response, error) {
	return c.roundTrip(ctx, helperprotocol.Request{Op: helperprotocol.OpPing})
}

func (c *Client) Status(ctx context.Context) (helperprotocol.Response, error) {
	return c.roundTrip(ctx, helperprotocol.Request{Op: helperprotocol.OpStatus})
}

func (c *Client) Start(ctx context.Context, session singbox.SessionSpec) (helperprotocol.Response, error) {
	return c.roundTrip(ctx, helperprotocol.Request{Op: helperprotocol.OpStart, Session: &session})
}

func (c *Client) Stop(ctx context.Context, sessionID string) (helperprotocol.Response, error) {
	return c.roundTrip(ctx, helperprotocol.Request{Op: helperprotocol.OpStop, SessionID: sessionID})
}

func (c *Client) StopAll(ctx context.Context) (helperprotocol.Response, error) {
	return c.roundTrip(ctx, helperprotocol.Request{Op: helperprotocol.OpStopAll})
}

func (c *Client) UpdateDNS(ctx context.Context, sessionID string, dns singbox.DNSMeta) (helperprotocol.Response, error) {
	return c.roundTrip(ctx, helperprotocol.Request{Op: helperprotocol.OpUpdateDNS, SessionID: sessionID, DNS: &dns})
}

func (c *Client) ReadLogs(ctx context.Context, sessionID string, offset int64) (helperprotocol.Response, error) {
	return c.roundTrip(ctx, helperprotocol.Request{Op: helperprotocol.OpReadLogs, SessionID: sessionID, LogOffset: offset})
}

func (c *Client) roundTrip(ctx context.Context, request helperprotocol.Request) (helperprotocol.Response, error) {
	if c.Token == "" {
		return helperprotocol.Response{}, fmt.Errorf("helper token is required")
	}
	request.Token = c.Token
	dial := c.Dial
	if dial == nil {
		dial = dialHelper
	}
	conn, err := dial(ctx)
	if err != nil {
		return helperprotocol.Response{}, err
	}
	defer conn.Close()
	timeout := 30 * time.Second
	if request.Op == helperprotocol.OpStart {
		timeout = 3 * time.Minute
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(request); err != nil {
		return helperprotocol.Response{}, fmt.Errorf("write helper request: %w", err)
	}
	reader := bufio.NewReader(conn)
	decoder := json.NewDecoder(reader)
	var response helperprotocol.Response
	if err := decoder.Decode(&response); err != nil {
		return helperprotocol.Response{}, fmt.Errorf("read helper response: %w", err)
	}
	if !response.OK {
		if response.Error == "" {
			response.Error = "helper request failed"
		}
		return response, fmt.Errorf("%s", response.Error)
	}
	return response, nil
}
