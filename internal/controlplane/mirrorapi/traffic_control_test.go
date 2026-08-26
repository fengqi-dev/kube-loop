package mirrorapi

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/entity"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

type mirrorTrafficResources struct {
	applied      bool
	restored     bool
	restoreValue any
}

type mirrorCleanupContextKey struct{}

func (*mirrorTrafficResources) Capture(
	_ context.Context,
	_ controlplaneapi.Identity,
	snapshot *servicebinding.ServiceInterceptSnapshot,
) error {
	snapshot.HasEndpoints = true
	snapshot.EndpointsSubsets = []corev1.EndpointSubset{{
		Addresses: []corev1.EndpointAddress{{IP: "10.244.0.20"}},
		Ports: []corev1.EndpointPort{{
			Name: "http", Port: 8080, Protocol: corev1.ProtocolTCP,
		}},
	}}
	return nil
}

func (resources *mirrorTrafficResources) Apply(
	context.Context,
	controlplaneapi.Identity,
	servicebinding.ServiceInterceptSnapshot,
	string,
) error {
	resources.applied = true
	return nil
}

func (resources *mirrorTrafficResources) Restore(
	ctx context.Context,
	_ servicebinding.ServiceInterceptSnapshot,
	_ string,
) error {
	resources.restored = true
	resources.restoreValue = ctx.Value(mirrorCleanupContextKey{})
	return nil
}

type mirrorTrafficFixture struct {
	ctx        context.Context
	store      *storage.Store
	handler    *Service
	resources  *mirrorTrafficResources
	identity   trafficcontrol.Identity
	identityID string
	sessionID  string
	taskID     string
}

func newMirrorTrafficFixture(t *testing.T) mirrorTrafficFixture {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	stateStore, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "mirror-traffic.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })

	identityID, sessionID, taskID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := stateStore.Identities().Create(ctx, storage.Identity{
		ID: identityID, Type: "human", DisplayName: "Traffic Identity", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	network, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{"10.96.0.10"}})
	if err != nil {
		t.Fatal(err)
	}
	networkJSON, err := networkspec.CanonicalJSON(network)
	if err != nil {
		t.Fatal(err)
	}
	networkHash, err := networkspec.Hash(network)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := now.Add(time.Hour)
	if err := stateStore.Sessions().Create(ctx, storage.Session{
		ID: sessionID, IdentityID: identityID, DeviceID: "device", ClusterID: "cluster",
		Namespace: "development", State: "active", Generation: 1,
		NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	specJSON, err := json.Marshal(storedSpec{
		Service: "api", ClusterIP: "10.96.0.20",
		Ports: []entity.Port{{Name: "http", ServicePort: 80, Protocol: "tcp"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Tasks().Create(ctx, storage.Task{
		ID: taskID, IdentityID: identityID, SessionID: sessionID, Type: TaskType,
		State: remotetask.Pending, Spec: specJSON, IdempotencyKey: "mirror-traffic",
		CreatedAt: now, UpdatedAt: now, ExpiresAt: &expiresAt,
	}); err != nil {
		t.Fatal(err)
	}

	resources := &mirrorTrafficResources{}
	handler, err := New(
		stateStore,
		mirrorTestSessions{session: sessionapi.ActiveSession{
			ID: sessionID, Namespace: "development", Generation: 1, ExpiresAt: expiresAt,
		}},
		&mirrorTestServices{},
		resources,
		Config{Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	return mirrorTrafficFixture{
		ctx: ctx, store: stateStore, handler: handler, resources: resources,
		identity: trafficcontrol.Identity{
			IdentityID: identityID, DeviceID: "device", SessionID: sessionID,
			SessionGeneration: 1, Namespace: "development",
		},
		identityID: identityID, sessionID: sessionID, taskID: taskID,
	}
}

func TestMirrorTrafficControlLifecycle(t *testing.T) {
	fixture := newMirrorTrafficFixture(t)
	ctx, stateStore, handler := fixture.ctx, fixture.store, fixture.handler
	resources, identity := fixture.resources, fixture.identity
	identityID, sessionID, taskID := fixture.identityID, fixture.sessionID, fixture.taskID
	const relayID = "relay-1"

	claim, apiError := handler.Claim(ctx, relayID, trafficcontrol.ClaimRequest{
		Mode: trafficcontrol.ModeMirror, TaskID: taskID, Identity: identity,
	})
	if apiError != nil || claim.Mode != trafficcontrol.ModeMirror || claim.Service != "api" ||
		len(claim.Ports) != 1 {
		t.Fatalf("claim = %#v error = %#v", claim, apiError)
	}
	claimed, err := stateStore.Tasks().GetByID(ctx, taskID)
	if err != nil || claimed.State != remotetask.Starting || !trafficOwnedBy(claimed, relayID) {
		t.Fatalf("claimed task = %#v error = %v", claimed, err)
	}

	prepared, apiError := handler.Prepare(ctx, relayID, trafficcontrol.PrepareRequest{
		Mode: trafficcontrol.ModeMirror, TaskID: taskID, Identity: identity, RelayID: relayID,
		GatewayIP: "10.96.0.99",
		Ports: []trafficcontrol.ListenerPort{{
			Name: "http", ServicePort: 80, ListenPort: 15080, Protocol: "tcp",
		}},
	})
	if apiError != nil || len(prepared.Backends) != 1 || len(prepared.Backends[0].Targets) != 1 ||
		prepared.Backends[0].Targets[0].Address != "10.244.0.20" ||
		prepared.Backends[0].Targets[0].Port != 8080 || !resources.applied {
		t.Fatalf("prepare = %#v applied = %t error = %#v", prepared, resources.applied, apiError)
	}

	heartbeat, apiError := handler.Heartbeat(ctx, relayID, trafficcontrol.HeartbeatRequest{
		Mode: trafficcontrol.ModeMirror, TaskID: taskID, RelayID: relayID,
	})
	if apiError != nil || heartbeat.Stop {
		t.Fatalf("running heartbeat = %#v error = %#v", heartbeat, apiError)
	}
	path := "/api/sessions/" + sessionID + "/mirrors/" + taskID + "?namespace=development"
	stopping, apiError := mirrorRequest(
		handler,
		controlplaneapi.Identity{Subject: identityID, DeviceID: "device"},
		http.MethodDelete,
		path,
		nil,
		"",
	)
	if apiError != nil || stopping.Code != http.StatusAccepted {
		t.Fatalf("stop status = %d error = %#v", stopping.Code, apiError)
	}
	heartbeat, apiError = handler.Heartbeat(ctx, relayID, trafficcontrol.HeartbeatRequest{
		Mode: trafficcontrol.ModeMirror, TaskID: taskID, RelayID: relayID,
	})
	if apiError != nil || !heartbeat.Stop {
		t.Fatalf("stopping heartbeat = %#v error = %#v", heartbeat, apiError)
	}

	finishContext := context.WithValue(ctx, mirrorCleanupContextKey{}, "mirror-finish")
	finished, apiError := handler.Finish(finishContext, relayID, trafficcontrol.FinishRequest{
		Mode: trafficcontrol.ModeMirror, TaskID: taskID, RelayID: relayID,
	})
	if apiError != nil || finished.State != string(remotetask.Stopped) || !resources.restored ||
		resources.restoreValue != "mirror-finish" {
		t.Fatalf("finish = %#v restored = %t error = %#v", finished, resources.restored, apiError)
	}
}
