package gateway

import (
	"net/http"
	"slices"

	"github.com/gorilla/websocket"

	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
)

// NewTunnelHandler lets the control and Trojan transports share the public
// /tunnel endpoint. The control client always requests KubeLoop's multiplexing
// subprotocol; sing-box's Trojan/WebSocket transport does not request one.
func NewTunnelHandler(control, forward http.Handler) http.Handler {
	if forward == nil {
		return control
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if slices.Contains(websocket.Subprotocols(request), websocketmux.Subprotocol) {
			control.ServeHTTP(writer, request)
			return
		}
		forward.ServeHTTP(writer, request)
	})
}
