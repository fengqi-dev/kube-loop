package relayregistry

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

func handleInternalRequest[Request any, Response interface{ Validate(time.Time) error }](
	handler *HTTPHandler,
	ctx *echo.Context,
	invalidBodyMessage string,
	invalidDocumentMessage string,
	status int,
	decode func([]byte, time.Time) (Request, error),
	mutate func(relaycontrol.PeerIdentity, Request) (Response, error),
) error {
	writer, request := ctx.Response(), ctx.Request()
	identity, err := handler.authenticator.Authenticate(request)
	if err != nil {
		writeInternalError(
			writer,
			http.StatusUnauthorized,
			"unauthenticated",
			"Relay workload identity is invalid",
		)
		return nil //nolint:nilerr // The authenticated internal API writes its protocol error response directly.
	}
	raw, err := readInternalBody(request)
	if err != nil {
		writeInternalError(
			writer,
			http.StatusBadRequest,
			"invalid_argument",
			invalidBodyMessage,
		)
		return nil //nolint:nilerr // The authenticated internal API writes its protocol error response directly.
	}
	now := handler.registry.config.Now().UTC()
	document, err := decode(raw, now)
	if err != nil {
		writeInternalError(
			writer,
			http.StatusBadRequest,
			"invalid_argument",
			invalidDocumentMessage,
		)
		return nil //nolint:nilerr // The authenticated internal API writes its protocol error response directly.
	}
	response, err := mutate(identity, document)
	if err != nil {
		writeRegistryError(writer, err)
		return nil
	}
	writeInternalDocument(writer, status, response, now)
	return nil
}

func readInternalBody(request *http.Request) ([]byte, error) {
	if request.Header.Get("Content-Type") != "application/json" {
		return nil, errors.New("content type must be application/json")
	}
	defer func() { _ = request.Body.Close() }()
	raw, err := io.ReadAll(
		io.LimitReader(request.Body, relaycontrol.MaximumBodyBytes+1),
	)
	if err != nil || len(raw) == 0 || len(raw) > relaycontrol.MaximumBodyBytes {
		return nil, errors.New("relay control body size is invalid")
	}
	return raw, nil
}

func writeInternalDocument[T interface {
	Validate(time.Time) error
}](writer http.ResponseWriter, status int, document T, now time.Time) {
	raw, err := relaycontrol.Encode(document, now)
	if err != nil {
		writeInternalError(
			writer,
			http.StatusInternalServerError,
			"internal",
			"Relay control response failed",
		)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_, _ = writer.Write(raw)
}

func writeRegistryError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeInternalError(
			writer,
			http.StatusNotFound,
			"not_found",
			"Relay lease was not found",
		)
	case errors.Is(err, ErrConflict):
		writeInternalError(
			writer,
			http.StatusConflict,
			"conflict",
			"Relay lease does not match",
		)
	case errors.Is(err, ErrUnavailable),
		errors.Is(err, ErrAssignedRelayUnavailable):
		writeInternalError(
			writer,
			http.StatusServiceUnavailable,
			"unavailable",
			"Relay is unavailable",
		)
	default:
		writeInternalError(
			writer,
			http.StatusBadRequest,
			"invalid_argument",
			"Relay control request is invalid",
		)
	}
}

func writeInternalError(
	writer http.ResponseWriter,
	status int,
	code, message string,
) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{Error: struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: code, Message: message}})
}
