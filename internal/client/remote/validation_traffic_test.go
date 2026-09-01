package remote

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidateExchangeTaskNormalizesRestorableLocalTargets(t *testing.T) {
	now := time.Now().UTC()
	session := Session{ID: uuid.NewString(), Namespace: "development", State: remoteSessionActive}
	task := ExchangeTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "stopped", Service: "api", ClusterIP: "10.96.0.20",
		Ports:        []ExchangePort{{ServicePort: 80, Protocol: "TCP"}},
		LocalTargets: []LocalTarget{{ServicePort: 80, Protocol: " TCP ", LocalPort: 8080}},
		CreatedAt:    now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}

	validated, err := validateExchangeTask(task, session)
	if err != nil {
		t.Fatal(err)
	}
	if len(validated.LocalTargets) != 1 || validated.LocalTargets[0].Protocol != "tcp" ||
		validated.LocalTargets[0].LocalHost != remoteLoopbackHost {
		t.Fatalf("normalized local targets = %#v", validated.LocalTargets)
	}
}

func TestValidateExchangeTaskRejectsMismatchedRestorableLocalTargets(t *testing.T) {
	now := time.Now().UTC()
	session := Session{ID: uuid.NewString(), Namespace: "development", State: remoteSessionActive}
	task := ExchangeTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "stopped", Service: "api", ClusterIP: "10.96.0.20",
		Ports:        []ExchangePort{{ServicePort: 80, Protocol: "tcp"}},
		LocalTargets: []LocalTarget{{ServicePort: 81, Protocol: "tcp", LocalPort: 8080}},
		CreatedAt:    now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if _, err := validateExchangeTask(task, session); err == nil {
		t.Fatal("mismatched local target was accepted")
	}
}
