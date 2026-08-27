package main

import (
	"os"

	"k8s.io/component-base/cli"

	"github.com/fengqi-dev/kube-loop/cmd/kubeloop-tui/app"
)

func main() {
	command := app.NewTUICommand()
	code := cli.Run(command)
	os.Exit(code)
}
