package httpapi

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v5"

	adminauthentication "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authentication"
	adminsession "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/session"
)

type requestContextKey int

const (
	subjectContextKey requestContextKey = iota
	sessionContextKey
	requestIDContextKey
)

func (api *readAPI) authenticate(next echo.HandlerFunc) echo.HandlerFunc {
	return func(echoContext *echo.Context) error {
		request := echoContext.Request()
		requestID := ensureRequestID(echoContext)
		if request.Header.Get("Authorization") != "" ||
			request.Header.Get("Sec-Fetch-Site") == "cross-site" ||
			(request.Header.Get("Origin") != "" && request.Header.Get("Origin") != api.handler.origin) {
			return writeError(
				echoContext,
				http.StatusUnauthorized,
				"unauthenticated",
				"management authentication failed",
				requestID,
			)
		}
		var token string
		cookieCount := 0
		for _, cookie := range request.Cookies() {
			if cookie.Name == api.handler.sessionCookieName {
				cookieCount++
				token = cookie.Value
			}
		}
		if cookieCount != 1 {
			return writeError(
				echoContext,
				http.StatusUnauthorized,
				"unauthenticated",
				"management authentication failed",
				requestID,
			)
		}
		stored, subject, err := api.handler.sessions.AuthenticateSubject(
			request.Context(),
			token,
		)
		if err != nil {
			return writeError(
				echoContext,
				http.StatusUnauthorized,
				"unauthenticated",
				"management authentication failed",
				requestID,
			)
		}
		if request.Method != http.MethodGet &&
			request.Method != http.MethodHead &&
			request.Method != http.MethodOptions {
			if err := adminsession.VerifyCSRF(stored, request.Header.Get(CSRFHeaderName)); err != nil {
				return writeError(
					echoContext,
					http.StatusForbidden,
					"csrf_failed",
					"management request was rejected",
					requestID,
				)
			}
		}
		requestContext := context.WithValue(
			request.Context(),
			subjectContextKey,
			subject,
		)
		requestContext = context.WithValue(
			requestContext,
			sessionContextKey,
			stored,
		)
		requestContext = context.WithValue(
			requestContext,
			requestIDContextKey,
			requestID,
		)
		echoContext.SetRequest(request.WithContext(requestContext))
		return next(echoContext)
	}
}

func requestID(request *http.Request) string {
	value, _ := request.Context().Value(requestIDContextKey).(string)
	return value
}

func subjectFromRequest(request *http.Request) adminauthentication.Subject {
	value, _ := request.Context().Value(subjectContextKey).(adminauthentication.Subject)
	return value
}
