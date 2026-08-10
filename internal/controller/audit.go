package controller

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/google/uuid"
)

type AuditRecord struct {
	RequestID    string
	PrincipalID  string
	SessionID    string
	Operation    string
	Namespace    string
	ResourceKind string
	ResourceName string
	Outcome      string
	PolicyRuleID string
	HTTPStatus   int
	Duration     time.Duration
}

type AuditSink interface {
	Record(context.Context, AuditRecord) error
}

type storageAuditSink struct {
	repository storage.AuditRepository
}

func NewStorageAuditSink(repository storage.AuditRepository) (AuditSink, error) {
	if repository == nil {
		return nil, errors.New("audit repository is required")
	}
	return &storageAuditSink{repository: repository}, nil
}

func (sink *storageAuditSink) Record(ctx context.Context, record AuditRecord) error {
	metadata, err := json.Marshal(struct {
		SessionID    string `json:"sessionId,omitempty"`
		Namespace    string `json:"namespace,omitempty"`
		PolicyRuleID string `json:"policyRuleId,omitempty"`
		HTTPStatus   int    `json:"httpStatus"`
		LatencyMS    int64  `json:"latencyMs"`
	}{
		SessionID: record.SessionID, Namespace: record.Namespace, PolicyRuleID: record.PolicyRuleID,
		HTTPStatus: record.HTTPStatus, LatencyMS: record.Duration.Milliseconds(),
	})
	if err != nil {
		return errors.New("encode audit metadata")
	}
	return sink.repository.Append(ctx, storage.AuditEvent{
		ID: uuid.NewString(), PrincipalID: record.PrincipalID, Action: record.Operation,
		ResourceType: record.ResourceKind, ResourceID: record.ResourceName,
		Outcome: record.Outcome, RequestID: record.RequestID, Metadata: metadata,
		CreatedAt: time.Now().UTC(),
	})
}

func auditOutcome(status int) string {
	switch {
	case status == 0 || status >= 200 && status < 300:
		return "success"
	case status == 401:
		return "unauthenticated"
	case status == 403:
		return "denied"
	default:
		return "error"
	}
}
