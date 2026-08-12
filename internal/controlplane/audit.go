package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
)

type AuditRecord = controlplanemiddleware.AuditRecord
type AuditSink = controlplanemiddleware.AuditSink

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
