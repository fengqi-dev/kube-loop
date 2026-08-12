package taskstream_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskstream"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
)

type taskStore struct {
	task storage.Task
}

func (s *taskStore) GetByID(context.Context, string) (storage.Task, error) {
	return s.task, nil
}

func (s *taskStore) UpdateState(
	_ context.Context,
	_ string,
	expected remotetask.State,
	next remotetask.State,
	result json.RawMessage,
	updatedAt time.Time,
) error {
	if s.task.State != expected {
		return storage.ErrConflict
	}
	s.task.State = next
	s.task.Result = append(json.RawMessage(nil), result...)
	s.task.UpdatedAt = updatedAt
	return nil
}

func TestFinishSelectsTerminalAndRecoveryStates(t *testing.T) {
	tests := []struct {
		name            string
		cause           error
		cleanupRequired bool
		cleanupComplete bool
		expected        remotetask.State
	}{
		{name: "clean stop", expected: remotetask.Stopped},
		{name: "unexpected failure", cause: errors.New("relay failed"), expected: remotetask.Failed},
		{
			name: "cleanup pending", cause: errors.New("relay failed"),
			cleanupRequired: true, expected: remotetask.Recovering,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &taskStore{task: storage.Task{ID: "task", State: remotetask.Running}}
			completed := taskstream.Finish(taskstream.FinishConfig{
				Tasks: store, TaskID: "task", Now: time.Now, Cause: test.cause,
				CleanupRequired: test.cleanupRequired, CleanupComplete: test.cleanupComplete,
				Result: func(_ storage.Task, next remotetask.State, cleanupPending bool) json.RawMessage {
					encoded, _ := json.Marshal(struct {
						State          remotetask.State `json:"state"`
						CleanupPending bool             `json:"cleanupPending"`
					}{State: next, CleanupPending: cleanupPending})
					return encoded
				},
			})
			if !completed || store.task.State != test.expected {
				t.Fatalf("completed=%t state=%q, want %q", completed, store.task.State, test.expected)
			}
		})
	}
}

func TestFailedIgnoresCancellationAndExpectedStop(t *testing.T) {
	stopErr := errors.New("stop requested")
	if taskstream.Failed(stopErr, context.Background(), stopErr) {
		t.Fatal("expected stop was classified as a failure")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if taskstream.Failed(errors.New("relay failed"), ctx) {
		t.Fatal("canceled stream was classified as a failure")
	}
	if !taskstream.Failed(errors.New("relay failed"), context.Background()) {
		t.Fatal("unexpected relay error was not classified as a failure")
	}
}
