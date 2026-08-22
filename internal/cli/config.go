// Package cli provides shared command-line configuration and exit contracts.
package cli

import (
	"strings"

	"github.com/spf13/viper"
)

// NewViper returns an isolated configuration resolver. Keys such as
// gateway.config-file map to KUBELOOP_GATEWAY_CONFIG_FILE.
func NewViper() *viper.Viper {
	config := viper.New()
	config.SetEnvPrefix("KUBELOOP")
	config.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	config.AutomaticEnv()
	return config
}
