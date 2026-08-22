package fileopsapi

import (
	"encoding/json"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func decodeTask(task storage.Task, namespace string) (Document, error) {
	var spec Spec
	if err := json.Unmarshal(task.Spec, &spec); err != nil {
		return Document{}, err
	}
	result := Result{}
	if len(task.Result) > 0 {
		if err := json.Unmarshal(task.Result, &result); err != nil {
			return Document{}, err
		}
	}
	expiresAt := time.Time{}
	if task.ExpiresAt != nil {
		expiresAt = task.ExpiresAt.UTC()
	}
	return Document{
		ID:          task.ID,
		SessionID:   task.SessionID,
		Namespace:   namespace,
		State:       task.State,
		Action:      spec.Action,
		Pod:         spec.Pod,
		Container:   spec.Container,
		Path:        spec.Path,
		Destination: spec.Destination,
		Kind:        spec.Kind,
		Recursive:   spec.Recursive,
		Result:      result,
		CreatedAt:   task.CreatedAt,
		UpdatedAt:   task.UpdatedAt,
		ExpiresAt:   expiresAt,
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
