package main

import (
	"os"

	"k8s.io/component-base/cli"

	"github.com/fengqi-dev/kube-loop/cmd/kubeloop-gateway/app"
)

func main() {
	os.Exit(cli.Run(app.NewGatewayCommand()))
}
