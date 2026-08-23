package session

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

// Revoke atomically invalidates the current Management Session and records the
// logout without persisting its Cookie or CSRF plaintext values.
func (service *Service) Revoke(
	ctx context.Context,
	stored storage.AdminSession,
	requestID string,
) error {
	requestID = strings.TrimSpace(requestID)
	if len(stored.IDHash) != sha256.Size || requestID == "" {
		return ErrSessionInvalid
	}
	now := service.now().UTC()
	identityID := stored.IdentityID
	metadata, err := json.Marshal(
		map[string]string{"authenticationType": stored.AuthenticationType},
	)
	if err != nil {
		return ErrSessionInvalid
	}
	err = service.store.WithinTransaction(
		ctx,
		func(repositories storage.Repositories) error {
			if err := repositories.AdminSessions().Revoke(ctx, stored.IDHash, now); err != nil {
				return err
			}
			return repositories.Audit().Append(ctx, storage.AuditEvent{
				ID: service.newID(), IdentityID: identityID, Action: sessionRevokeAudit,
				ResourceType: "admin-session", Outcome: "success", RequestID: requestID,
				Metadata: metadata, CreatedAt: now,
			})
		},
	)
	if err != nil {
		return ErrSessionInvalid
	}
	return nil
}
