package distribution

import (
	"fmt"
	"strings"

	env "github.com/Netflix/go-env"
)

type installerEnvironment struct {
	SingBoxPath string `env:"KUBELOOP_SINGBOX_PATH"`
}

func loadInstallerEnvironment() (installerEnvironment, error) {
	var environment installerEnvironment
	if _, err := env.UnmarshalFromEnviron(&environment); err != nil {
		return installerEnvironment{}, fmt.Errorf("decode sing-box installer environment: %w", err)
	}
	environment.SingBoxPath = strings.TrimSpace(environment.SingBoxPath)
	return environment, nil
}
