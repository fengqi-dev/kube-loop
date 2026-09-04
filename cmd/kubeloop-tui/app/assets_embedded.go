//go:build kubeloop_embed

package app

import (
	"embed"
	"fmt"
	"io/fs"

	"github.com/fengqi-dev/kube-loop/internal/helperinstall"
)

//go:embed embedded/*
var embeddedRuntimeFiles embed.FS

func registerBundledResources() error {
	entries, err := fs.ReadDir(embeddedRuntimeFiles, "embedded")
	if err != nil {
		return fmt.Errorf("read embedded runtime files: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := fs.ReadFile(embeddedRuntimeFiles, "embedded/"+entry.Name())
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", entry.Name(), err)
		}
		helperinstall.SetBundledFile(entry.Name(), content)
	}
	return nil
}
