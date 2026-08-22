package trafficinspect

import (
	"context"
	"net/http"
	"time"
)

func buildRequestEvent(request *http.Request, trace requestTrace) Event {
	event := Event{
		SchemaVersion: EventSchemaVersion,
		ID:            newEventID(),
		FlowID:        trace.flowID,
		Timestamp:     trace.started,
		Type:          EventTypeRequest,
		Protocol:      trace.protocol,
		TLS:           isTLSProtocol(trace.protocol),
		Destination:   trace.destination,
		HTTP: &HTTPEvent{
			Version:        request.Proto,
			Method:         request.Method,
			Host:           request.Host,
			Path:           request.URL.RequestURI(),
			RequestHeaders: request.Header.Clone(),
		},
		Raw: rawHTTPHeader(request, nil, directionRequest),
	}
	if trace.protocol == ProtocolGRPC || trace.protocol == ProtocolGRPCS {
		service, method := splitGRPCPath(request.URL.Path)
		event.GRPC = &GRPCEvent{Service: service, Method: method, Path: request.URL.Path}
	}
	return event
}

func responseTrace(response *http.Response) requestTrace {
	now := time.Now()
	trace, found := response.Request.Context().Value(requestTraceKey{}).(requestTrace)
	if !found {
		trace = requestTrace{
			flowID:      newEventID(),
			started:     now,
			protocol:    classifyProtocol(response.Request),
			destination: canonicalAuthority(response.Request.Host, response.Request.URL.Scheme),
		}
	}
	return trace
}

func buildResponseEvent(response *http.Response, trace requestTrace) Event {
	now := time.Now()
	event := Event{
		SchemaVersion: EventSchemaVersion,
		ID:            newEventID(),
		FlowID:        trace.flowID,
		Timestamp:     now,
		Type:          EventTypeResponse,
		Protocol:      trace.protocol,
		TLS:           isTLSProtocol(trace.protocol),
		Destination:   trace.destination,
		Duration:      now.Sub(trace.started).Milliseconds(),
		HTTP: &HTTPEvent{
			Version:         response.Proto,
			Method:          response.Request.Method,
			Host:            response.Request.Host,
			Path:            response.Request.URL.RequestURI(),
			Status:          response.StatusCode,
			ResponseHeaders: response.Header.Clone(),
		},
		Raw: rawHTTPHeader(nil, response, directionResponse),
	}
	if trace.protocol == ProtocolGRPC || trace.protocol == ProtocolGRPCS {
		service, method := splitGRPCPath(response.Request.URL.Path)
		grpcStatus := response.Header.Get("Grpc-Status")
		if grpcStatus == "" {
			grpcStatus = response.Trailer.Get("Grpc-Status")
		}
		event.GRPC = &GRPCEvent{
			Service: service,
			Method:  method,
			Path:    response.Request.URL.Path,
			Status:  grpcStatus,
		}
	}
	return event
}

func emitEvent(ctx context.Context, config Config, event Event) {
	if config.Sink == nil {
		return
	}
	if err := config.Sink.Emit(ctx, event); err != nil && config.OnSinkError != nil {
		config.OnSinkError(err)
	}
}
