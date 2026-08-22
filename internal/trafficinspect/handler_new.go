package trafficinspect

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/elazarl/goproxy"
)

func New(config Config) (*Handler, error) {
	if config.CA == nil {
		return nil, errors.New("traffic inspection CA is required")
	}
	if config.DialContext == nil {
		return nil, errors.New("traffic inspection dialer is required")
	}

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2", alpnHTTP1},
	}
	if config.TLSConfig != nil {
		tlsConfig = config.TLSConfig.Clone()
	}
	proxy := goproxy.NewProxyHttpServer()
	proxy.AllowHTTP2 = config.AllowHTTP2
	proxy.ConnectDialWithReq = func(request *http.Request, network, _ string) (net.Conn, error) {
		connection, _ := request.Context().Value(inspectionConnectionKey{}).(*inspectionConnection)
		if connection == nil || connection.destination == "" {
			return nil, errors.New("traffic inspection original destination is unavailable")
		}
		return config.DialContext(request.Context(), network, connection.destination)
	}

	mitmTLSConfig := goproxy.TLSConfigFromCA(config.CA)
	mitm := &goproxy.ConnectAction{
		Action: goproxy.ConnectMitm,
		TLSConfig: func(host string, proxyContext *goproxy.ProxyCtx) (*tls.Config, error) {
			fallback, err := mitmTLSConfig(host, proxyContext)
			if err != nil {
				return nil, err
			}
			if len(fallback.Certificates) == 0 {
				return nil, errors.New("traffic inspection generated no fallback certificate")
			}
			fallbackCertificate := fallback.Certificates[0]
			fallback.Certificates = nil
			fallback.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				serverName := strings.TrimSpace(hello.ServerName)
				if serverName == "" {
					return &fallbackCertificate, nil
				}
				dynamic, dynamicErr := mitmTLSConfig(serverName, proxyContext)
				if dynamicErr != nil {
					return nil, dynamicErr
				}
				if len(dynamic.Certificates) == 0 {
					return nil, errors.New("traffic inspection generated no certificate")
				}
				return &dynamic.Certificates[0], nil
			}
			return fallback, nil
		},
	}
	proxy.OnRequest().HandleConnectFunc(
		func(host string, proxyContext *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
			connection, _ := proxyContext.Req.Context().Value(inspectionConnectionKey{}).(*inspectionConnection)
			proxyContext.UserData = connection
			return mitm, host
		},
	)
	proxy.OnRequest().
		DoFunc(func(request *http.Request, proxyContext *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			connection, _ := proxyContext.UserData.(*inspectionConnection)
			if connection == nil || connection.roundTripper == nil || connection.destination == "" {
				return request, goproxy.NewResponse(
					request,
					"text/plain",
					http.StatusBadGateway,
					"traffic inspection original destination is unavailable",
				)
			}
			proxyContext.RoundTripper = connection.roundTripper
			if request.URL != nil && request.Host != "" {
				// The dialer is pinned to the trusted original destination. Preserve
				// the HTTP authority here so TLS uses the client's SNI and virtual
				// hosts continue to work without letting Host select a dial target.
				request.URL.Host = request.Host
			}
			protocol := classifyProtocol(request)
			trace := requestTrace{
				flowID:      newEventID(),
				started:     time.Now(),
				protocol:    protocol,
				destination: connection.destination,
			}
			request = request.WithContext(context.WithValue(request.Context(), requestTraceKey{}, trace))
			wrapRequestBody(request, trace, config)
			emitEvent(request.Context(), config, buildRequestEvent(request, trace))
			if config.OnRequest != nil {
				config.OnRequest(request)
			}
			return request, nil
		})
	proxy.OnResponse().DoFunc(func(response *http.Response, _ *goproxy.ProxyCtx) *http.Response {
		if response != nil && response.Request != nil {
			trace := responseTrace(response)
			wrapResponseBody(response, trace, config)
			emitEvent(response.Request.Context(), config, buildResponseEvent(response, trace))
		}
		if response != nil && config.OnResponse != nil {
			config.OnResponse(response)
		}
		return response
	})

	return &Handler{
		proxy: proxy, dialContext: config.DialContext, tlsConfig: tlsConfig,
		allowHTTP2: config.AllowHTTP2, enabled: config.Enabled,
	}, nil
}
