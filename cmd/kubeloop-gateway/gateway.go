package main

import (
	"os"

	"k8s.io/component-base/cli"

	"github.com/fengqi-dev/kube-loop/cmd/kubeloop-gateway/app"
)

func main() {
	command := app.NewGatewayCommand()
	code := cli.Run(command)
	os.Exit(code)
}
