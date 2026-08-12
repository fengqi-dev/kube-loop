package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

type recordingAuditRepository struct {
	events []storage.AuditEvent
}

func (repository *recordingAuditRepository) Append(_ context.Context, event storage.AuditEvent) error {
	repository.events = append(repository.events, event)
	return nil
}

func (*recordingAuditRepository) List(context.Context, storage.AuditFilter) ([]storage.AuditEvent, error) {
	return nil, nil
}

type recordingAuditSink struct {
	records []AuditRecord
}

func (sink *recordingAuditSink) Record(_ context.Context, record AuditRecord) error {
	sink.records = append(sink.records, record)
	return nil
}

func TestStorageAuditSinkPersistsOnlyStructuredMetadata(t *testing.T) {
	repository := &recordingAuditRepository{}
	sink, err := NewStorageAuditSink(repository)
	if err != nil {
		t.Fatal(err)
	}
	err = sink.Record(context.Background(), AuditRecord{
		RequestID: "request-1", PrincipalID: "5d7e7980-33df-4f93-a91f-ff6a48725384",
		SessionID: "session-1", Operation: "list", Namespace: "payments",
		ResourceKind: "pods", Outcome: "success", PolicyRuleID: "payments-read",
		HTTPStatus: http.StatusOK, Duration: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(repository.events) != 1 {
		t.Fatalf("events = %d", len(repository.events))
	}
	event := repository.events[0]
	if event.Action != "list" || event.ResourceType != "pods" || event.RequestID != "request-1" || event.Outcome != "success" {
		t.Fatalf("event = %#v", event)
	}
	var metadata map[string]any
	if err := json.Unmarshal(event.Metadata, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["namespace"] != "payments" || metadata["policyRuleId"] != "payments-read" || metadata["sessionId"] != "session-1" {
		t.Fatalf("metadata = %#v", metadata)
	}
	for _, forbidden := range []string{"token", "claims", "command", "content"} {
		if strings.Contains(strings.ToLower(string(event.Metadata)), forbidden) {
			t.Fatalf("metadata contains forbidden field %q: %s", forbidden, event.Metadata)
		}
	}
}

func TestAPIAuditCapturesAllowedAndDeniedOutcomes(t *testing.T) {
	engine, err := authorization.New(authorization.Policy{Rules: []authorization.Rule{{
		ID: "payments-read", Groups: []string{"developers"}, Namespaces: []string{"payments"},
		Operations: []string{"list"}, ResourceKinds: []string{"pods"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordingAuditSink{}
	server := newAPITestServer(t,
		WithAuthenticator(controlplaneapi.AuthenticatorFunc(func(*http.Request) (controlplaneapi.Principal, *controlplaneapi.Error) {
			return controlplaneapi.Principal{Subject: "5d7e7980-33df-4f93-a91f-ff6a48725384", Groups: []string{"developers"}}, nil
		})),
		WithAuthorizer(engine), WithAuditSink(sink),
		WithAPIRoutes(testEndpoint(func(writer http.ResponseWriter, _ *http.Request, _ controlplaneapi.Principal) *controlplaneapi.Error {
			writeTestJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
			return nil
		})),
	)
	allowed := httptest.NewRecorder()
	server.Handler().ServeHTTP(allowed, httptest.NewRequest(http.MethodGet, APIPathPrefix+"/namespaces/payments/pods", nil))
	denied := httptest.NewRecorder()
	server.Handler().ServeHTTP(denied, httptest.NewRequest(http.MethodDelete, APIPathPrefix+"/namespaces/payments/pods/pod-1", nil))
	if len(sink.records) != 2 {
		t.Fatalf("audit records = %#v", sink.records)
	}
	if sink.records[0].Outcome != "success" || sink.records[0].PolicyRuleID != "payments-read" || sink.records[0].HTTPStatus != http.StatusOK {
		t.Fatalf("allowed audit = %#v", sink.records[0])
	}
	if sink.records[1].Outcome != "denied" || sink.records[1].HTTPStatus != http.StatusForbidden || sink.records[1].ResourceName != "pod-1" {
		t.Fatalf("denied audit = %#v", sink.records[1])
	}
}
