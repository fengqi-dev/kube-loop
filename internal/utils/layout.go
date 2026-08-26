package utils

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	releaseDirectory = ".kubeloop"
	devDirectory     = ".kubeloop-dev"
)

type Layout struct {
	root string
}

func Default() (Layout, error) {
	return forDirectory(releaseDirectory)
}

func ForVersion(version string) (Layout, error) {
	version = strings.TrimSpace(version)
	if version == "" || version == "dev" {
		return forDirectory(devDirectory)
	}
	return Default()
}

func New(root string) (Layout, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return Layout{}, errors.New("KubeLoop user root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return Layout{}, errors.New("resolve KubeLoop user root")
	}
	return Layout{root: filepath.Clean(absolute)}, nil
}

func forDirectory(name string) (Layout, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Layout{}, errors.New("find user home directory")
	}
	return New(filepath.Join(home, name))
}

func (layout Layout) Root() string      { return layout.root }
func (layout Layout) ConfigDir() string { return filepath.Join(layout.root, "config") }
func (layout Layout) DataDir() string   { return filepath.Join(layout.root, "data") }
func (layout Layout) StateDir() string  { return filepath.Join(layout.root, "state") }
func (layout Layout) SecretsDir() string {
	return filepath.Join(layout.root, "secrets")
}
func (layout Layout) CacheDir() string { return filepath.Join(layout.root, "cache") }

func (layout Layout) Ensure() error {
	for _, directory := range []string{
		layout.Root(), layout.ConfigDir(), layout.DataDir(), layout.StateDir(), layout.SecretsDir(), layout.CacheDir(),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
		if runtime.GOOS != "windows" {
			//nolint:gosec // Private application directories need owner execute permission for traversal.
			if err := os.Chmod(directory, 0o700); err != nil {
				return err
			}
		}
	}
	return nil
}
