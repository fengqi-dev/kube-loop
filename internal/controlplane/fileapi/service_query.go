package fileapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func (handler *Service) get(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) *controlplaneapi.Error {
	request := ctx.Request()
	if _, err := uuid.Parse(taskID); err != nil {
		return notFound()
	}
	task, err := handler.storage.Tasks().GetByID(request.Context(), taskID)
	if err != nil || !owned(task, identity, session) {
		return notFound()
	}
	document, err := handler.decodeTask(task, session.Namespace)
	if err != nil {
		return internalError(err)
	}
	writeJSON(ctx, http.StatusOK, document)
	return nil
}
func (handler *Service) specFromTask(task storage.Task) (Spec, error) {
	var spec Spec
	if err := json.Unmarshal(task.Spec, &spec); err != nil {
		return Spec{}, errors.New("decode file transfer Task")
	}
	if apiError := handler.normalizeSpec(&spec); apiError != nil ||
		spec.Container == "" {
		return Spec{}, errors.New("stored file transfer Task is invalid")
	}
	return spec, nil
}

func (handler *Service) documentFromTask(
	task storage.Task,
	namespace string,
) Document {
	document, _ := handler.decodeTask(task, namespace)
	return document
}

func (handler *Service) decodeTask(
	task storage.Task,
	namespace string,
) (Document, error) {
	spec, err := handler.specFromTask(task)
	if err != nil {
		return Document{}, err
	}
	expiresAt := time.Time{}
	if task.ExpiresAt != nil {
		expiresAt = task.ExpiresAt.UTC()
	}
	return Document{
		ID: task.ID, SessionID: task.SessionID, Namespace: namespace, State: task.State,
		Direction: spec.Direction, Kind: spec.Kind, Pod: spec.Pod, Container: spec.Container,
		RemotePath: spec.RemotePath, Size: spec.Size, Offset: spec.Offset, Checksum: spec.Checksum,
		Overwrite: spec.Overwrite, ResumeID: spec.ResumeID,
		CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt, ExpiresAt: expiresAt,
	}, nil
}

func owned(
	task storage.Task,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) bool {
	return task.Type == TaskType && task.IdentityID == identity.Subject &&
		task.SessionID == session.ID
}
