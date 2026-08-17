package main

import (
	"fmt"
	"strings"

	env "github.com/Netflix/go-env"
)

type controlPlaneEnvironment struct {
	PodName string `env:"KUBELOOP_POD_NAME"`
}

func loadControlPlaneEnvironment() (controlPlaneEnvironment, error) {
	var environment controlPlaneEnvironment
	if _, err := env.UnmarshalFromEnviron(&environment); err != nil {
		return controlPlaneEnvironment{}, fmt.Errorf("decode Control Plane environment: %w", err)
	}
	environment.PodName = strings.TrimSpace(environment.PodName)
	return environment, nil
}
