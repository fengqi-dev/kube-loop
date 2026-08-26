package supervisor

import "testing"

func TestBinaryVersionHasIndependentLifecycle(t *testing.T) {
	t.Parallel()
	if BinaryVersion != "v1.0.0" {
		t.Fatalf("BinaryVersion = %q, want v1.0.0", BinaryVersion)
	}
}
