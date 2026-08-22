package main

import (
	"errors"
	"strings"

	"github.com/spf13/viper"

	internalcli "github.com/fengqi-dev/kube-loop/internal/cli"
)

type gatewayEnvironment struct {
	ConfigFile string
	PodName    string
	PodUID     string
	PodIP      string
}

func loadGatewayEnvironment() (gatewayEnvironment, error) {
	return loadGatewayEnvironmentFrom(newGatewayConfigResolver())
}

func newGatewayConfigResolver() *viper.Viper {
	config := internalcli.NewViper()
	for _, key := range []string{"gateway.config-file", "pod.name", "pod.uid", "pod.ip"} {
		config.SetDefault(key, "")
	}
	return config
}

func loadGatewayEnvironmentFrom(config *viper.Viper) (gatewayEnvironment, error) {
	environment := gatewayEnvironment{
		ConfigFile: config.GetString("gateway.config-file"),
		PodName:    config.GetString("pod.name"),
		PodUID:     config.GetString("pod.uid"),
		PodIP:      config.GetString("pod.ip"),
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
