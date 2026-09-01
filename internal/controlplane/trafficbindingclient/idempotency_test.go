package trafficbindingclient

import "testing"

func TestTaskIDForIdempotency(t *testing.T) {
	t.Parallel()

	first := TaskIDForIdempotency("session-a", "mirror", "identity-a", "request-a")
	if first != TaskIDForIdempotency("session-a", "mirror", "identity-a", "request-a") {
		t.Fatal("the same Session create request produced different IDs")
	}
	tests := []struct {
		name string
		id   string
	}{
		{name: "session", id: TaskIDForIdempotency("session-b", "mirror", "identity-a", "request-a")},
		{name: "type", id: TaskIDForIdempotency("session-a", "exchange", "identity-a", "request-a")},
		{name: "identity", id: TaskIDForIdempotency("session-a", "mirror", "identity-b", "request-a")},
		{name: "key", id: TaskIDForIdempotency("session-a", "mirror", "identity-a", "request-b")},
	}
	for _, test := range tests {
		if test.id == first {
			t.Errorf("changing %s did not change the Session ID", test.name)
		}
	}
}
