//go:build e2e

package dataplane

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

// temporaryKubernetesOutage injects a real client-go transport failure without
// disturbing the Kubernetes client used by the assertions. Providers created
// with WrapTransport keep this gate for their lifetime, so a test can take the
// API down for one lifecycle phase and restore it for reconciliation.
type temporaryKubernetesOutage struct {
	active   atomic.Bool
	requests atomic.Int64
}

func (outage *temporaryKubernetesOutage) Enable() {
	outage.requests.Store(0)
	outage.active.Store(true)
}

func (outage *temporaryKubernetesOutage) Disable() {
	outage.active.Store(false)
}

func (outage *temporaryKubernetesOutage) RequestCount() int64 {
	return outage.requests.Load()
}

func (outage *temporaryKubernetesOutage) WrapTransport(delegate http.RoundTripper) http.RoundTripper {
	return roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if !outage.active.Load() {
			return delegate.RoundTrip(request)
		}
		outage.requests.Add(1)
		if request.Body != nil {
			_ = request.Body.Close()
		}
		body := `{"kind":"Status","apiVersion":"v1","status":"Failure","message":"simulated temporary Kubernetes API outage","reason":"ServiceUnavailable","code":503}`
		return &http.Response{
			Status:        "503 Service Unavailable",
			StatusCode:    http.StatusServiceUnavailable,
			Proto:         "HTTP/1.1",
			ProtoMajor:    1,
			ProtoMinor:    1,
			Header:        http.Header{"Content-Type": []string{"application/json"}},
			Body:          io.NopCloser(strings.NewReader(body)),
			ContentLength: int64(len(body)),
			Request:       request,
		}, nil
	})
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
