package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"sigs.k8s.io/yaml"
)

const maximumControlPlaneConfigBytes = 4 << 20

func LoadConfig(path string) (Config, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Config{}, errors.New("--config is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open Control Plane config: %w", err)
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maximumControlPlaneConfigBytes+1))
	if err != nil {
		return Config{}, errors.New("read Control Plane config")
	}
	if len(raw) > maximumControlPlaneConfigBytes {
		return Config{}, errors.New("control plane config exceeds 4 MiB")
	}
	var root kubeloopDocument
	if err := yaml.UnmarshalStrict(raw, &root); err != nil {
		return Config{}, fmt.Errorf("decode Control Plane YAML: %w", err)
	}
	if root.ControlPlane == nil || root.Gateway == nil {
		return Config{}, errors.New("unified configuration requires controlPlane and gateway")
	}
	return normalize(*root.ControlPlane)
}
