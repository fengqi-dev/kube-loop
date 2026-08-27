package options

import (
	"errors"
	"strings"

	"github.com/spf13/viper"
)

// ExpandRelayEndpoint resolves Downward API placeholders in a Relay endpoint.
func ExpandRelayEndpoint(template string, environment Environment) (string, error) {
	result := strings.TrimSpace(template)
	for placeholder, metadata := range map[string]struct {
		name  string
		value string
	}{
		"{podName}": {name: "KUBELOOP_POD_NAME", value: environment.PodName},
		"{podUID}":  {name: "KUBELOOP_POD_UID", value: environment.PodUID},
	} {
		if !strings.Contains(result, placeholder) {
			continue
		}
		if metadata.value == "" {
			return "", errors.New(metadata.name + " is required by the Relay endpoint template")
		}
		result = strings.ReplaceAll(result, placeholder, metadata.value)
	}
	if strings.ContainsAny(result, "{}") {
		return "", errors.New("relay endpoint contains an unknown template placeholder")
	}
	return result, nil
}

type Environment struct {
	ConfigFile string
	PodName    string
	PodUID     string
	PodIP      string
}

func NewConfigResolver() *viper.Viper {
	config := viper.New()
	config.SetEnvPrefix("KUBELOOP")
	config.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	config.AutomaticEnv()
	for _, key := range []string{"gateway.config-file", "pod.name", "pod.uid", "pod.ip"} {
		config.SetDefault(key, "")
	}
	return config
}

func LoadEnvironmentFrom(config *viper.Viper) (Environment, error) {
	environment := Environment{
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
		return Environment{}, errors.New("KUBELOOP_GATEWAY_CONFIG_FILE is required")
	}
	return environment, nil
}

// ApplyOverrides applies CLI and environment values over a loaded config.
func ApplyOverrides(config *viper.Viper, loaded Config) (Config, error) {
	config.SetDefault("gateway.http.listen", loaded.HTTP.Listen)
	config.SetDefault("gateway.relay.control-plane-url", loaded.Relay.ControlPlaneURL)
	config.SetDefault("gateway.relay.endpoint", loaded.Relay.Endpoint)
	config.SetDefault("gateway.log-level", loaded.LogLevel)
	loaded.HTTP.Listen = config.GetString("gateway.http.listen")
	loaded.Relay.ControlPlaneURL = config.GetString("gateway.relay.control-plane-url")
	loaded.Relay.Endpoint = config.GetString("gateway.relay.endpoint")
	loaded.LogLevel = config.GetString("gateway.log-level")
	if err := loaded.normalizeAndValidate(); err != nil {
		return Config{}, err
	}
	return loaded, nil
}
