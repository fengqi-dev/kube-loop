package main

import (
	"os"

	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/component-base/cli"

	"github.com/fengqi-dev/kube-loop/cmd/kubeloop-operator/app"
)

func main() {
	os.Exit(cli.Run(app.NewOperatorCommand()))
}
