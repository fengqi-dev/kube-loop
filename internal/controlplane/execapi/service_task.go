package execapi

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func owned(
	task storage.Task,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) bool {
	return task.Type == TaskType && task.IdentityID == identity.Subject &&
		task.SessionID == session.ID
}

func specFromTask(task storage.Task) (Spec, error) {
	var spec Spec
	if err := json.Unmarshal(task.Spec, &spec); err != nil {
		return Spec{}, errors.New("decode Pod exec Task")
	}
	if apiError := normalizeSpec(&spec); apiError != nil {
		return Spec{}, errors.New("stored Pod exec Task is invalid")
	}
	return spec, nil
}

func documentFromTask(task storage.Task, namespace string) Document {
	document, _ := decodeTask(task, namespace)
	return document
}

func decodeTask(task storage.Task, namespace string) (Document, error) {
	spec, err := specFromTask(task)
	if err != nil {
		return Document{}, err
	}
	expiresAt := time.Time{}
	if task.ExpiresAt != nil {
		expiresAt = *task.ExpiresAt
	}
	return Document{
		ID:        task.ID,
		SessionID: task.SessionID,
		Namespace: namespace,
		State:     task.State,
		Pod:       spec.Pod,
		Container: spec.Container,
		TTY:       spec.TTY,
		CreatedAt: task.CreatedAt,
		UpdatedAt: task.UpdatedAt,
		ExpiresAt: expiresAt,
	}, nil
}
