package remotetask

import "testing"

func TestStatesAreStableAndTransitionsRejectRegression(t *testing.T) {
	want := []State{Pending, Starting, Running, Recovering, Failed, Stopping, Stopped, Deleted}
	got := States()
	if len(got) != len(want) {
		t.Fatalf("states=%v", got)
	}
	for index := range want {
		if got[index] != want[index] || !got[index].Valid() {
			t.Fatalf("states=%v", got)
		}
	}
	for _, transition := range [][2]State{
		{Pending, Starting}, {Starting, Running}, {Running, Stopping}, {Stopping, Stopped},
		{Starting, Recovering}, {Running, Recovering}, {Recovering, Failed},
		{Failed, Stopped},
		{Stopped, Deleted},
		{Starting, Starting}, {Running, Running}, {Recovering, Recovering},
	} {
		if err := ValidateTransition(transition[0], transition[1]); err != nil {
			t.Fatalf("valid transition %q -> %q: %v", transition[0], transition[1], err)
		}
	}
	for _, transition := range [][2]State{
		{Stopped, Running}, {Failed, Starting}, {Running, Starting}, {Stopping, Running},
		{Pending, Pending}, {Stopped, Stopped},
		{Deleted, Pending},
	} {
		if err := ValidateTransition(transition[0], transition[1]); err == nil {
			t.Fatalf("invalid transition %q -> %q was accepted", transition[0], transition[1])
		}
	}
}
