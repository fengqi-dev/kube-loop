package previewapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
)

func TestPreviewTrafficControlLifecycle(t *testing.T) {
	ctx := context.Background()
	stateStore, owner, active := previewTestStore(t)
	now := time.Date(2026, 8, 23, 14, 0, 0, 0, time.UTC)
	taskID := uuid.NewString()
	specJSON, err := json.Marshal(storedSpec{
		Name:  "local-api",
		Ports: []trafficmodel.Port{{Name: "http", ServicePort: 80, Protocol: "tcp"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Tasks().Create(ctx, storage.Task{
		ID: taskID, IdentityID: owner.Subject, SessionID: active.ID, Type: TaskType,
		State: remotetask.Pending, Spec: specJSON, IdempotencyKey: "preview-traffic",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	resources := &recordingPreviewResources{}
	handler, err := New(
		stateStore,
		previewTestSessions{session: active},
		resources,
		Config{Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	identity := trafficcontrol.Identity{
		IdentityID: owner.Subject, DeviceID: owner.DeviceID, SessionID: active.ID,
		SessionGeneration: active.Generation, Namespace: active.Namespace,
	}
	const relayID = "relay-1"

	claim, apiError := handler.Claim(ctx, relayID, trafficcontrol.ClaimRequest{
		Mode: trafficcontrol.ModePreview, TaskID: taskID, Identity: identity,
	})
	if apiError != nil || claim.Mode != trafficcontrol.ModePreview || claim.Service != "local-api" ||
		len(claim.Ports) != 1 {
		t.Fatalf("claim = %#v error = %#v", claim, apiError)
	}
	claimed, err := stateStore.Tasks().GetByID(ctx, taskID)
	if err != nil || claimed.State != remotetask.Starting || !trafficOwnedBy(claimed, relayID) {
		t.Fatalf("claimed task = %#v error = %v", claimed, err)
	}

	prepared, apiError := handler.Prepare(ctx, relayID, trafficcontrol.PrepareRequest{
		Mode: trafficcontrol.ModePreview, TaskID: taskID, Identity: identity, RelayID: relayID,
		GatewayIP: "10.96.0.99",
		Ports: []trafficcontrol.ListenerPort{{
			Name: "http", ServicePort: 80, ListenPort: 15080, Protocol: "tcp",
		}},
	})
	if apiError != nil || prepared.ClusterIP != "10.96.0.40" || resources.createdID != taskID {
		t.Fatalf("prepare = %#v created = %q error = %#v", prepared, resources.createdID, apiError)
	}
	heartbeat, apiError := handler.Heartbeat(ctx, relayID, trafficcontrol.HeartbeatRequest{
		Mode: trafficcontrol.ModePreview, TaskID: taskID, RelayID: relayID,
	})
	if apiError != nil || heartbeat.Stop {
		t.Fatalf("running heartbeat = %#v error = %#v", heartbeat, apiError)
	}

	path := "/api/sessions/" + active.ID + "/previews/" + taskID + "?namespace=" + active.Namespace
	stopping, apiError := previewRequest(
		handler,
		controlplaneapi.Identity{Subject: owner.Subject, DeviceID: owner.DeviceID},
		http.MethodDelete,
		path,
		nil,
		"",
	)
	if apiError != nil || stopping.Code != http.StatusAccepted {
		t.Fatalf("stop status = %d error = %#v", stopping.Code, apiError)
	}
	heartbeat, apiError = handler.Heartbeat(ctx, relayID, trafficcontrol.HeartbeatRequest{
		Mode: trafficcontrol.ModePreview, TaskID: taskID, RelayID: relayID,
	})
	if apiError != nil || !heartbeat.Stop {
		t.Fatalf("stopping heartbeat = %#v error = %#v", heartbeat, apiError)
	}

	finishContext := context.WithValue(ctx, previewCleanupContextKey{}, "preview-finish")
	finished, apiError := handler.Finish(finishContext, relayID, trafficcontrol.FinishRequest{
		Mode: trafficcontrol.ModePreview, TaskID: taskID, RelayID: relayID,
	})
	if apiError != nil || finished.State != string(remotetask.Stopped) ||
		resources.deletedID != taskID || resources.deleteCalls != 1 ||
		resources.deleteValue != "preview-finish" {
		t.Fatalf(
			"finish = %#v deleted = %q calls = %d error = %#v",
			finished,
			resources.deletedID,
			resources.deleteCalls,
			apiError,
		)
	}
}
