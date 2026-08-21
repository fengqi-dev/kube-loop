package exchangeapi

import (
	"context"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

func TestTrafficSessionAcceptsAuthenticatedOlderGeneration(t *testing.T) {
	const currentGeneration = uint64(7)
	handler := &Service{
		sessions: exchangeTestSessions{session: sessionapi.ActiveSession{
			ID: "33333333-3333-4333-8333-333333333333", Namespace: "development", Generation: currentGeneration,
		}},
	}

	tests := []struct {
		name             string
		streamGeneration uint64
		wantConflict     bool
	}{
		{name: "current generation", streamGeneration: currentGeneration},
		{
			name:             "older authenticated generation",
			streamGeneration: currentGeneration - 1,
		},
		{
			name:             "future generation",
			streamGeneration: currentGeneration + 1,
			wantConflict:     true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, apiError := handler.trafficSession(
				context.Background(),
				trafficcontrol.Identity{
					IdentityID:        "11111111-1111-4111-8111-111111111111",
					DeviceID:          "22222222-2222-4222-8222-222222222222",
					SessionID:         "33333333-3333-4333-8333-333333333333",
					SessionGeneration: test.streamGeneration,
					Namespace:         "development",
				},
			)
			if test.wantConflict {
				if apiError == nil ||
					apiError.Code != controlplaneapi.CodeConflict {
					t.Fatalf(
						"trafficSession() error = %#v, want conflict",
						apiError,
					)
				}
				return
			}
			if apiError != nil {
				t.Fatalf("trafficSession() error = %#v", apiError)
			}
		})
	}
}
