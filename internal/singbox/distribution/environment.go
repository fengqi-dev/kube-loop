package distribution

import (
	"os"
	"strings"
)

type installerEnvironment struct{ SingBoxPath string }

func loadInstallerEnvironment() installerEnvironment {
	return installerEnvironment{
		SingBoxPath: strings.TrimSpace(os.Getenv("KUBELOOP_SINGBOX_PATH")),
	}
}
