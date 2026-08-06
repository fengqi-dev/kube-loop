//go:build windows

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperinstall "github.com/fengqi-dev/kube-loop/internal/helper/install"
)

var version = "dev"

func main() {
	if version != "" && version != "dev" {
		helper.Version = version
	}
	fs := flag.NewFlagSet("kubeloop-helper-uninstall", flag.ExitOnError)
	request := fs.String("request", "", "elevated request file")
	result := fs.String("result", "", "elevated result file")
	_ = fs.Bool("quiet", false, "suppress non-error output")
	_ = fs.Parse(os.Args[1:])

	if *request != "" || *result != "" {
		if *request == "" || *result == "" {
			fmt.Fprintln(os.Stderr, "--request and --result are required together")
			os.Exit(2)
		}
		if err := helperinstall.RunElevatedRequest("uninstall", *request, *result); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := helperinstall.UninstallFromCLI(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
