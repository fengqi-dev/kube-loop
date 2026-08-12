package fileapi

import "testing"

func TestAllowedRootsAreCanonicalAndMayIncludeContainerRoot(t *testing.T) {
	roots, err := normalizeRoots([]string{"/workspace/", "/", "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 || roots[0] != "/" || roots[1] != "/workspace" {
		t.Fatalf("roots = %#v", roots)
	}
	if _, err := normalizeRoots([]string{"relative"}); err == nil {
		t.Fatal("relative allowed root was accepted")
	}
}
