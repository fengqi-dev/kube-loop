package localuser

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

func (service *Service) Authenticate(
	ctx context.Context,
	username string,
	password []byte,
	requestIDs ...string,
) (User, error) {
	requestID := ""
	if len(requestIDs) > 0 {
		requestID = strings.TrimSpace(requestIDs[0])
	}
	identityID := ""
	succeeded := false
	defer func() {
		if requestID == "" || succeeded {
			return
		}
		metadata, _ := json.Marshal(
			map[string]string{"authenticationType": "local"},
		)
		_ = service.store.Audit().
			Append(ctx, storage.AuditEvent{ID: service.newID(), IdentityID: identityID,
				Action: "admin.session.local.exchange", ResourceType: "admin-session", Outcome: "failure",
				RequestID: requestID, Metadata: metadata, CreatedAt: service.now().UTC()})
	}()
	stored, err := service.store.Credentials().
		GetPasswordByUsername(ctx, utils.NormalizeUsername(username))
	hash := service.dummyHash
	if err == nil {
		hash, identityID = stored.PasswordHash, stored.IdentityID
	}
	if !verifyPassword(password, hash) || err != nil {
		return User{}, ErrAuthenticationFailed
	}
	if !stored.Enabled {
		return User{}, ErrDisabled
	}
	user, err := service.user(ctx, stored)
	succeeded = err == nil
	return user, err
}
