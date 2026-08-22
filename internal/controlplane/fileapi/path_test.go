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

func TestExportedPathNormalizationContracts(t *testing.T) {
	roots, err := NormalizeAllowedRoots([]string{" /workspace/ ", "/", "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 || roots[0] != "/" || roots[1] != "/workspace" {
		t.Fatalf("normalized roots = %#v", roots)
	}
	normalized, root, err := NormalizeContainerPath(" /workspace/report.txt ", roots)
	if err != nil || normalized != "/workspace/report.txt" || root != "/workspace" {
		t.Fatalf("normalized path = %q root = %q err = %v", normalized, root, err)
	}
	normalized, root, err = NormalizeContainerPath("/", roots)
	if err != nil || normalized != "/" || root != "/" {
		t.Fatalf("container root = %q root = %q err = %v", normalized, root, err)
	}
	if _, _, err := NormalizeContainerPath("/outside/../secret", roots); err == nil {
		t.Fatal("traversal path was accepted")
	}
}
