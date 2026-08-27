package options

import (
	"strings"

	"github.com/spf13/viper"
)

func ConfigPath(config *viper.Viper) string {
	return strings.TrimSpace(config.GetString("control-plane.config-file"))
}

func ApplyOverrides(
	config *viper.Viper,
	loaded Config,
) (Config, error) {
	config.SetDefault("control-plane.api.listen", loaded.Document.API.Listen)
	config.SetDefault("control-plane.api.public-url", loaded.Document.API.PublicURL)
	config.SetDefault("control-plane.logging.level", loaded.Document.Logging.Level)
	document := loaded.Document
	document.API.Listen = config.GetString("control-plane.api.listen")
	document.API.PublicURL = config.GetString("control-plane.api.public-url")
	document.Logging.Level = config.GetString("control-plane.logging.level")
	return normalize(document)
}
