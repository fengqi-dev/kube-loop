package httpapi

import (
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
)

type responseError struct {
	status  int
	code    string
	message string
}

func (responseErr *responseError) write(ctx *echo.Context) error {
	return writeError(
		ctx,
		responseErr.status,
		responseErr.code,
		responseErr.message,
		requestID(ctx.Request()),
	)
}

func bindJSON(ctx *echo.Context, destination any) *responseError {
	mediaType, _, err := mime.ParseMediaType(
		ctx.Request().Header.Get("Content-Type"),
	)
	if err != nil || mediaType != "application/json" {
		return &responseError{
			status:  http.StatusUnsupportedMediaType,
			code:    "invalid_content_type",
			message: "application/json is required",
		}
	}
	if err := ctx.Bind(destination); err != nil {
		return &responseError{
			status:  http.StatusBadRequest,
			code:    invalidRequestCode,
			message: "request body is invalid",
		}
	}
	return nil
}

func validChangeReason(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 8 && len(value) <= 512 &&
		!strings.ContainsAny(value, "\x00\r\n")
}

func (api *readAPI) invalidIAMMutation(ctx *echo.Context) error {
	return writeError(
		ctx,
		http.StatusBadRequest,
		invalidRequestCode,
		"structured request and an 8-512 character reason are required",
		requestID(ctx.Request()),
	)
}

func iamETag(
	updatedAt time.Time,
) string {
	return fmt.Sprintf(`"%x"`, updatedAt.UTC().UnixNano())
}

func requireIAMETag(ctx *echo.Context, updatedAt time.Time) *responseError {
	want := iamETag(updatedAt)
	if strings.TrimSpace(ctx.Request().Header.Get("If-Match")) == want {
		return nil
	}
	ctx.Response().Header().Set("ETag", want)
	return &responseError{
		status:  http.StatusPreconditionFailed,
		code:    "etag_mismatch",
		message: "resource changed; reload before updating",
	}
}
