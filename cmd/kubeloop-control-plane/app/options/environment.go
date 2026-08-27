package options

import (
	"strings"

	"github.com/spf13/viper"
)

type Environment struct {
	PodName string
}

func NewConfigResolver() *viper.Viper {
	config := viper.New()
	config.SetEnvPrefix("KUBELOOP")
	config.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	config.AutomaticEnv()
	config.SetDefault("control-plane.config-file", "")
	config.SetDefault("pod.name", "")
	return config
}

func LoadEnvironmentFrom(config *viper.Viper) Environment {
	environment := Environment{PodName: config.GetString("pod.name")}
	environment.PodName = strings.TrimSpace(environment.PodName)
	return environment
}
