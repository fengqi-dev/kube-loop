package main

import (
	"strings"

	"github.com/spf13/viper"

	internalcli "github.com/fengqi-dev/kube-loop/internal/cli"
)

type controlPlaneEnvironment struct {
	PodName string
}

func loadControlPlaneEnvironment() controlPlaneEnvironment {
	return loadControlPlaneEnvironmentFrom(newControlPlaneConfigResolver())
}

func newControlPlaneConfigResolver() *viper.Viper {
	config := internalcli.NewViper()
	config.SetDefault("control-plane.config-file", "")
	config.SetDefault("pod.name", "")
	return config
}

func loadControlPlaneEnvironmentFrom(config *viper.Viper) controlPlaneEnvironment {
	environment := controlPlaneEnvironment{PodName: config.GetString("pod.name")}
	environment.PodName = strings.TrimSpace(environment.PodName)
	return environment
}
