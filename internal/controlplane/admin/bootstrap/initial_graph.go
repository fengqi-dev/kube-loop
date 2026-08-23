package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

type initialGraphRequest struct {
	Username    string
	Password    []byte
	DisplayName string
	Email       string
	RequestID   string
	AuditAction string
	Now         time.Time
}

func (service *Service) createInitialGraph(
	ctx context.Context,
	repositories storage.Repositories,
	request initialGraphRequest,
) (Result, error) {
	identity, err := service.localUsers.CreateWithRepositories(
		ctx,
		repositories,
		adminlocaluser.CreateRequest{
			Username:    request.Username,
			Password:    request.Password,
			DisplayName: request.DisplayName,
			Email:       request.Email,
		},
	)
	if err != nil {
		return Result{}, err
	}
	metadata, err := json.Marshal(
		map[string]string{"authenticationType": "local"},
	)
	if err != nil {
		return Result{}, errors.New("encode IAM bootstrap audit metadata")
	}
	if err := repositories.Audit().Append(ctx, storage.AuditEvent{ID: service.newID(),
		IdentityID: identity.IdentityID, Action: request.AuditAction, ResourceType: "identity",
		ResourceID: identity.IdentityID, Outcome: "success", RequestID: request.RequestID, Metadata: metadata,
		CreatedAt: request.Now}); err != nil {
		return Result{}, err
	}
	return Result{Identity: identity}, nil
}
