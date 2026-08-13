package main

import (
	"errors"
	"fmt"
	"strings"

	env "github.com/Netflix/go-env"
)

type gatewayEnvironment struct {
	ConfigFile string `env:"KUBELOOP_GATEWAY_CONFIG_FILE,required=true"`
	PodName    string `env:"KUBELOOP_POD_NAME"`
	PodUID     string `env:"KUBELOOP_POD_UID"`
	PodIP      string `env:"KUBELOOP_POD_IP"`
}

func loadGatewayEnvironment() (gatewayEnvironment, error) {
	var environment gatewayEnvironment
	if _, err := env.UnmarshalFromEnviron(&environment); err != nil {
		return gatewayEnvironment{}, fmt.Errorf("decode Gateway environment: %w", err)
	}
	environment.ConfigFile = strings.TrimSpace(environment.ConfigFile)
	environment.PodName = strings.TrimSpace(environment.PodName)
	environment.PodUID = strings.TrimSpace(environment.PodUID)
	environment.PodIP = strings.TrimSpace(environment.PodIP)
	if environment.ConfigFile == "" {
		return gatewayEnvironment{}, errors.New("KUBELOOP_GATEWAY_CONFIG_FILE is required")
	}
	return environment, nil
}
