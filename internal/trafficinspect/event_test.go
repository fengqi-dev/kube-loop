package trafficinspect

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"testing"
)

func TestClassifyProtocol(t *testing.T) {
	tests := []struct {
		name        string
		scheme      string
		protoMajor  int
		contentType string
		isTLS       bool
		expected    Protocol
	}{
		{name: "http", scheme: "http", protoMajor: 1, expected: ProtocolHTTP},
		{name: "https", scheme: "https", protoMajor: 2, isTLS: true, expected: ProtocolHTTPS},
		{name: "grpc", scheme: "http", protoMajor: 2, contentType: "application/grpc", expected: ProtocolGRPC},
		{
			name:        "grpcs",
			scheme:      "https",
			protoMajor:  2,
			contentType: "application/grpc+proto",
			isTLS:       true,
			expected:    ProtocolGRPCS,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := &http.Request{
				ProtoMajor: test.protoMajor,
				Header:     make(http.Header),
				URL:        &url.URL{Scheme: test.scheme},
			}
			request.Header.Set("Content-Type", test.contentType)
			if test.isTLS {
				request.TLS = new(tls.ConnectionState)
			}
			if got := classifyProtocol(request); got != test.expected {
				t.Fatalf("classifyProtocol() = %q, want %q", got, test.expected)
			}
		})
	}
}

func TestSplitGRPCPath(t *testing.T) {
	service, method := splitGRPCPath("/grpcbin.GRPCBin/DummyUnary")
	if service != "grpcbin.GRPCBin" || method != "DummyUnary" {
		t.Fatalf("splitGRPCPath() = %q, %q", service, method)
	}
	service, method = splitGRPCPath("/invalid")
	if service != "" || method != "" {
		t.Fatalf("invalid path = %q, %q", service, method)
	}
}
