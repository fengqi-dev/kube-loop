package websocketmux

import (
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

func newWebSocketDialer(transport *http.Transport) websocket.Dialer {
	dialer := websocket.Dialer{
		Proxy:             transport.Proxy,
		NetDialContext:    transport.DialContext,
		NetDialTLSContext: transport.DialTLSContext,
	}
	if transport.TLSClientConfig != nil {
		dialer.TLSClientConfig = transport.TLSClientConfig.Clone()
		dialer.TLSClientConfig.NextProtos = []string{"http/1.1"}
	}
	return dialer
}

func closeWebSocket(connection *websocket.Conn, code int, reason string) error {
	writeErr := connection.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason),
		time.Now().Add(5*time.Second),
	)
	return errors.Join(writeErr, connection.Close())
}
