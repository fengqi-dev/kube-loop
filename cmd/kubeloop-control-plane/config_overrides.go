package main

import (
	"strings"

	"github.com/spf13/viper"
)

func controlPlaneOptionsFrom(config *viper.Viper) string {
	return strings.TrimSpace(config.GetString("control-plane.config-file"))
}

func applyControlPlaneOverrides(
	config *viper.Viper,
	loaded loadedControlPlaneConfig,
) (loadedControlPlaneConfig, error) {
	config.SetDefault("control-plane.api.listen", loaded.Document.API.Listen)
	config.SetDefault("control-plane.api.public-url", loaded.Document.API.PublicURL)
	config.SetDefault("control-plane.logging.level", loaded.Document.Logging.Level)
	document := loaded.Document
	document.API.Listen = config.GetString("control-plane.api.listen")
	document.API.PublicURL = config.GetString("control-plane.api.public-url")
	document.Logging.Level = config.GetString("control-plane.logging.level")
	return normalizeControlPlaneConfig(document)
}
