package fileapi

import "testing"

func TestConfigFromEnv(t *testing.T) {
	t.Setenv(maximumBytesEnv, "1048576")
	t.Setenv(allowedRootsEnv, `["/workspace","/tmp"]`)
	config, err := ConfigFromEnv()
	if err != nil || config.MaximumBytes != 1048576 || len(config.AllowedPathRoots) != 2 {
		t.Fatalf("config = %#v err = %v", config, err)
	}
	t.Setenv(allowedRootsEnv, `[]`)
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("empty allowed root configuration was accepted")
	}
}
