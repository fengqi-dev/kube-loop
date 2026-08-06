package app

import (
	"io/fs"
	"path"
	"strings"

	helperinstall "github.com/fengqi-dev/kube-loop/internal/helper/install"
)

func registerEmbeddedHelpers(embeddedHelperFiles fs.FS) {
	if embeddedHelperFiles == nil {
		return
	}
	entries, err := fs.ReadDir(embeddedHelperFiles, "build/embedded")
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "README.md" || strings.HasPrefix(name, ".") {
			continue
		}
		content, readErr := fs.ReadFile(embeddedHelperFiles, path.Join("build/embedded", name))
		if readErr != nil || len(content) == 0 {
			continue
		}
		helperinstall.SetBundledFile(name, content)
	}
}
