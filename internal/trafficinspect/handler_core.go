// Package trafficinspect contains the proof-of-concept transparent HTTP
// inspection engine used to evaluate an in-process sing-box integration.
package trafficinspect

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"github.com/elazarl/goproxy"
	"golang.org/x/net/http2"
)

const (
	connectEstablished   = "HTTP/1.0 200 OK\r\n\r\n"
	alpnHTTP1            = "http/1.1"
	protocolSniffTimeout = 500 * time.Millisecond
)

var inspectableCleartextPrefaces = [][]byte{
	[]byte("CONNECT "),
	[]byte("DELETE "),
	[]byte("GET "),
	[]byte("HEAD "),
	[]byte("OPTIONS "),
	[]byte("PATCH "),
	[]byte("POST "),
	[]byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"),
	[]byte("PUT "),
	[]byte("TRACE "),
}

// DialContextFunc opens an upstream connection. The production implementation
// is expected to use the KubeLoop relay rather than the host network.
type DialContextFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Config configures a transparent inspection handler.
type Config struct {
	CA          *tls.Certificate
	DialContext DialContextFunc
	Enabled     func() bool
	OnRequest   func(*http.Request)
	OnResponse  func(*http.Response)
	Sink        Sink
	Policy      CapturePolicy
	Protobuf    *ProtobufDecoder
	OnSinkError func(error)
	TLSConfig   *tls.Config
	AllowHTTP2  bool
}

// Handler adapts goproxy's explicit CONNECT API to an already accepted
// transparent connection whose original destination is known by sing-box.
type Handler struct {
	proxy       *goproxy.ProxyHttpServer
	dialContext DialContextFunc
	tlsConfig   *tls.Config
	allowHTTP2  bool
	enabled     func() bool
}

type upstreamRoundTripper struct {
	http *http.Transport
	h2c  *http2.Transport
}

type inspectionConnection struct {
	destination  string
	roundTripper *upstreamRoundTripper
}

type inspectionConnectionKey struct{}

func (t upstreamRoundTripper) RoundTrip(
	request *http.Request,
	_ *goproxy.ProxyCtx,
) (*http.Response, error) {
	if request.URL.Scheme == string(ProtocolHTTP) && request.ProtoMajor == 2 {
		return t.h2c.RoundTrip(request)
	}
	return t.http.RoundTrip(request)
}

func (t upstreamRoundTripper) Close() {
	t.http.CloseIdleConnections()
	t.h2c.CloseIdleConnections()
}
