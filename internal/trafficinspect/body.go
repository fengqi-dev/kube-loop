package trafficinspect

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"
)

const defaultMaxBodyBytes int64 = 64 * 1024

const (
	directionRequest  = "request"
	directionResponse = "response"
)

type capturedBody struct {
	data      []byte
	size      int64
	truncated bool
}

type observedBody struct {
	source   io.ReadCloser
	limit    int64
	captured capturedBody
	onDone   func(capturedBody)
	done     sync.Once
}

func observeBody(source io.ReadCloser, limit int64, onDone func(capturedBody)) io.ReadCloser {
	return &observedBody{source: source, limit: limit, onDone: onDone}
}

func (b *observedBody) Read(destination []byte) (int, error) {
	n, err := b.source.Read(destination)
	if n > 0 {
		b.capture(destination[:n])
	}
	if err != nil {
		b.finish()
	}
	return n, err
}

func (b *observedBody) Close() error {
	err := b.source.Close()
	b.finish()
	return err
}

func (b *observedBody) capture(payload []byte) {
	b.captured.size += int64(len(payload))
	remaining := b.limit - int64(len(b.captured.data))
	if remaining <= 0 {
		b.captured.truncated = true
		return
	}
	if int64(len(payload)) > remaining {
		payload = payload[:remaining]
		b.captured.truncated = true
	}
	b.captured.data = append(b.captured.data, payload...)
}

func (b *observedBody) finish() {
	b.done.Do(func() {
		if b.onDone != nil {
			b.onDone(b.captured)
		}
	})
}

func bodyLimit(policy CapturePolicy) int64 {
	if policy.MaxBodyBytes > 0 {
		return policy.MaxBodyBytes
	}
	return defaultMaxBodyBytes
}

func wrapRequestBody(request *http.Request, trace requestTrace, config Config) {
	if !config.Policy.CaptureBodies || request.Body == nil || request.Body == http.NoBody {
		return
	}
	contentType := request.Header.Get("Content-Type")
	request.Body = observeBody(request.Body, bodyLimit(config.Policy), func(body capturedBody) {
		emitCapturedBody(request.Context(), config, trace, request, nil, directionRequest, contentType, body)
	})
}

func wrapResponseBody(response *http.Response, trace requestTrace, config Config) {
	if !config.Policy.CaptureBodies || response.Body == nil || response.Body == http.NoBody {
		return
	}
	contentType := response.Header.Get("Content-Type")
	response.Body = observeBody(response.Body, bodyLimit(config.Policy), func(body capturedBody) {
		emitCapturedBody(
			response.Request.Context(), config, trace, response.Request, response,
			directionResponse, contentType, body,
		)
	})
}

func emitCapturedBody(
	ctx context.Context,
	config Config,
	trace requestTrace,
	request *http.Request,
	response *http.Response,
	direction string,
	contentType string,
	body capturedBody,
) {
	if trace.protocol == ProtocolGRPC || trace.protocol == ProtocolGRPCS {
		emitEvent(ctx, config, Event{
			SchemaVersion: EventSchemaVersion,
			ID:            newEventID(),
			FlowID:        trace.flowID,
			Timestamp:     time.Now(),
			Type:          EventTypeBody,
			Protocol:      trace.protocol,
			TLS:           isTLSProtocol(trace.protocol),
			Destination:   trace.destination,
			HTTP:          requestHTTPEvent(request, response),
			GRPC:          requestGRPCEvent(request),
			Raw: &RawEvent{
				Format:    "grpc",
				Direction: direction,
				Encoding:  "base64",
				Data:      base64.StdEncoding.EncodeToString(body.data),
				Size:      body.size,
				Truncated: body.truncated,
			},
		})
		return
	}
	emitEvent(ctx, config, Event{
		SchemaVersion: EventSchemaVersion,
		ID:            newEventID(),
		FlowID:        trace.flowID,
		Timestamp:     time.Now(),
		Type:          EventTypeBody,
		Protocol:      trace.protocol,
		TLS:           isTLSProtocol(trace.protocol),
		Destination:   trace.destination,
		HTTP:          requestHTTPEvent(request, response),
		Raw:           rawHTTPMessage(request, response, direction, contentType, body),
	})
}

func requestHTTPEvent(request *http.Request, response *http.Response) *HTTPEvent {
	event := &HTTPEvent{
		Version: request.Proto,
		Method:  request.Method,
		Host:    request.Host,
		Path:    request.URL.RequestURI(),
	}
	if response != nil {
		event.Version = response.Proto
		event.Status = response.StatusCode
	}
	return event
}

func requestGRPCEvent(request *http.Request) *GRPCEvent {
	service, method := splitGRPCPath(request.URL.Path)
	return &GRPCEvent{Service: service, Method: method, Path: request.URL.Path}
}

func rawHTTPHeader(request *http.Request, response *http.Response, direction string) *RawEvent {
	return rawHTTPMessage(request, response, direction, "", capturedBody{})
}

func rawHTTPMessage(
	request *http.Request,
	response *http.Response,
	direction string,
	_ string,
	body capturedBody,
) *RawEvent {
	var (
		dump []byte
		err  error
	)
	if response == nil {
		cloned := request.Clone(request.Context())
		includeBody := request.Body != nil && request.Body != http.NoBody && (body.size > 0 || body.truncated)
		if includeBody {
			cloned.Body = io.NopCloser(bytes.NewReader(body.data))
			cloned.ContentLength = int64(len(body.data))
		}
		dump, err = httputil.DumpRequest(cloned, includeBody)
	} else {
		cloned := new(http.Response)
		*cloned = *response
		includeBody := response.Body != nil && response.Body != http.NoBody && (body.size > 0 || body.truncated)
		if includeBody {
			cloned.Body = io.NopCloser(bytes.NewReader(body.data))
			cloned.ContentLength = int64(len(body.data))
		}
		dump, err = httputil.DumpResponse(cloned, includeBody)
	}
	if err != nil {
		return nil
	}
	return &RawEvent{
		Format:    "http",
		Direction: direction,
		Encoding:  "base64",
		Data:      base64.StdEncoding.EncodeToString(dump),
		Size:      int64(len(dump)),
		Truncated: body.truncated,
	}
}
