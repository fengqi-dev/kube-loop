//go:build darwin

package main

import (
	"os"

	"k8s.io/component-base/cli"

	"github.com/fengqi-dev/kube-loop/cmd/kubeloop-supervisor/app"
)

func main() {
	command := app.NewSupervisorCommand()
	code := cli.Run(command)
	os.Exit(code)
}
