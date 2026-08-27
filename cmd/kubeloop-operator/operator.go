package main

import (
	"os"

	"k8s.io/component-base/cli"

	"github.com/fengqi-dev/kube-loop/cmd/kubeloop-operator/app"
)

func main() {
	command := app.NewOperatorCommand()
	code := cli.Run(command)
	os.Exit(code)
}
