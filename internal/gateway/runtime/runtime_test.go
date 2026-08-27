package runtime

import "testing"

type fakeGateway struct {
	draining bool
	active   int
}

func (state fakeGateway) Draining() bool         { return state.draining }
func (state fakeGateway) ActiveConnections() int { return state.active }

type fakeReadiness bool

func (state fakeReadiness) Ready() bool { return bool(state) }

func TestOperationsState(t *testing.T) {
	state := OperationsState{Gateway: fakeGateway{active: 4}, Agent: fakeReadiness(false)}
	if state.Ready() || state.Draining() || state.ActiveConnections() != 4 {
		t.Fatal("unexpected unavailable state")
	}
	state.Agent = fakeReadiness(true)
	if !state.Ready() {
		t.Fatal("ready gateway reported unavailable")
	}
	state.Gateway = fakeGateway{draining: true, active: 4}
	if state.Ready() || !state.Draining() {
		t.Fatal("draining gateway reported ready")
	}
	zero := OperationsState{}
	if zero.Ready() || zero.Draining() || zero.ActiveConnections() != 0 {
		t.Fatal("invalid zero state")
	}
}
