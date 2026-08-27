package main

import (
	"os"

	"k8s.io/component-base/cli"

	"github.com/fengqi-dev/kube-loop/cmd/kubeloop-control-plane/app"
)

func main() {
	command := app.NewControlPlaneCommand()
	code := cli.Run(command)
	os.Exit(code)
}
