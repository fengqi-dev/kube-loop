package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

type AuditRecord struct {
	RequestID    string
	IdentityID   string
	SessionID    string
	Operation    string
	Namespace    string
	ResourceKind string
	ResourceName string
	Outcome      string
	HTTPStatus   int
	Duration     time.Duration
}

type AuditSink interface {
	Record(context.Context, AuditRecord) error
}

func recordAudit(
	ctx context.Context,
	sink AuditSink,
	logger *slog.Logger,
	requestID string,
	identity controlplaneapi.Identity,
	state *auditContextState,
	request authorization.Request,
	status int,
	duration time.Duration,
) {
	if sink == nil {
		return
	}
	record := AuditRecord{
		RequestID: requestID, IdentityID: identity.Subject, SessionID: state.sessionID,
		Operation: request.Operation, Namespace: request.Namespace,
		ResourceKind: request.ResourceKind, ResourceName: request.ResourceName,
		Outcome: auditOutcome(status), HTTPStatus: status, Duration: duration,
	}
	if err := sink.Record(ctx, record); err != nil {
		logger.ErrorContext(
			ctx,
			"append API audit event failed",
			"request_id",
			requestID,
		)
	}
}

func auditOutcome(status int) string {
	switch {
	case status == 0 || status >= 200 && status < 300:
		return "success"
	case status == http.StatusUnauthorized:
		return "unauthenticated"
	case status == http.StatusForbidden:
		return "denied"
	default:
		return "error"
	}
}
