package main

import (
	"os"

	"k8s.io/component-base/cli"

	"github.com/fengqi-dev/kube-loop/cmd/kubeloop-helper/app"
)

var buildMarker = ""

func main() {
	// buildMarker lets E2E produce a byte-distinct binary without changing the
	// dev service identity used for isolated test installs.
	_ = buildMarker
	os.Exit(cli.Run(app.NewHelperCommand()))
}
