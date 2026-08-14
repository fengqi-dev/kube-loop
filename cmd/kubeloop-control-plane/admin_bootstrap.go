package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	adminconfig "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/managementconfig"
	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
)

func initializeLocalUsers(
	ctx context.Context,
	store *controlplanestorage.Store,
	issuer, usernameFile, passwordFile, mfaKeyFile string,
) (*adminlocaluser.Service, adminlocaluser.User, error) {
	usernameFile, passwordFile, mfaKeyFile = strings.TrimSpace(usernameFile), strings.TrimSpace(passwordFile), strings.TrimSpace(mfaKeyFile)
	if usernameFile == "" && passwordFile == "" && mfaKeyFile == "" {
		return nil, adminlocaluser.User{}, nil
	}
	if usernameFile == "" || passwordFile == "" || mfaKeyFile == "" {
		return nil, adminlocaluser.User{}, errors.New("all initial administrator Secret files are required")
	}
	username, err := readSecretFile(usernameFile, 256)
	if err != nil {
		return nil, adminlocaluser.User{}, err
	}
	defer clear(username)
	password, err := readSecretFile(passwordFile, 1024)
	if err != nil {
		return nil, adminlocaluser.User{}, err
	}
	defer clear(password)
	mfaKey, err := readBinarySecretFile(mfaKeyFile, 32)
	if err != nil {
		return nil, adminlocaluser.User{}, err
	}
	defer clear(mfaKey)
	service, err := adminlocaluser.New(store, mfaKey, issuer)
	if err != nil {
		return nil, adminlocaluser.User{}, err
	}
	user, _, err := service.EnsureInitial(ctx, adminlocaluser.CreateRequest{
		Username: string(username), Password: password, DisplayName: "KubeLoop Administrator",
	})
	if err != nil {
		return nil, adminlocaluser.User{}, err
	}
	return service, user, nil
}

func readSecretFile(path string, maximum int) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("read Management Plane administrator Secret")
	}
	value := bytes.Clone(bytes.TrimSpace(raw))
	clear(raw)
	if len(value) == 0 || len(value) > maximum {
		clear(value)
		return nil, errors.New("Management Plane administrator Secret value is invalid")
	}
	return value, nil
}

func readBinarySecretFile(path string, exactLength int) ([]byte, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("read Management Plane administrator Secret")
	}
	if len(value) != exactLength {
		clear(value)
		return nil, fmt.Errorf("Management Plane MFA key must contain exactly %d bytes", exactLength)
	}
	return value, nil
}

func ensureInitialAdminPolicy(ctx context.Context, service *adminconfig.Service, principalID string) error {
	if _, err := uuid.Parse(principalID); err != nil {
		return errors.New("initial administrator principal ID is invalid")
	}
	state, err := service.CurrentPolicy(ctx)
	if err != nil {
		return err
	}
	for _, binding := range state.Snapshot.Bindings {
		if binding.RoleID != adminauthorization.RolePlatformAdmin {
			continue
		}
		if binding.Subject.Type == adminauthorization.SubjectPrincipal && binding.Subject.PrincipalID == principalID {
			return nil
		}
	}
	snapshot := state.Snapshot
	snapshot.Bindings = append(snapshot.Bindings, adminauthorization.Binding{
		ID:      uuid.NewSHA1(uuid.NameSpaceURL, []byte("kubeloop:initial-admin:"+principalID)).String(),
		Subject: adminauthorization.SubjectRef{Type: adminauthorization.SubjectPrincipal, PrincipalID: principalID},
		RoleID:  adminauthorization.RolePlatformAdmin, Scope: adminauthorization.BindingScope{Type: adminauthorization.ScopePlatform},
		ManagedBy: adminauthorization.ManagedByPlatform, CreatedBy: principalID,
	})
	idempotencyKey := fmt.Sprintf("initial-admin-policy-%s-%s", principalID, state.Pointer.ObjectID)
	requestID := uuid.NewString()
	actor := adminconfig.Actor{PrincipalID: principalID, Authentication: adminauthorization.AuthenticationBootstrap}
	draft, err := service.CreatePolicyDraft(ctx, adminconfig.PolicyDraftRequest{
		Snapshot: snapshot, IdempotencyKey: idempotencyKey, Reason: "initialize Helm administrator",
		RequestID: requestID, Actor: actor,
	})
	if err != nil {
		return err
	}
	_, err = service.PublishPolicy(ctx, adminconfig.ActivateRequest{
		ChangeID: draft.Change.ID, IdempotencyKey: idempotencyKey,
		Reason: "initialize Helm administrator", RequestID: requestID, Actor: actor,
	})
	return err
}
