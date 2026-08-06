package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperinstall "github.com/fengqi-dev/kube-loop/internal/helper/install"
)

var version = "dev"
var buildMarker = ""

func main() {
	// buildMarker lets E2E produce a byte-distinct binary without changing the
	// dev service identity used for isolated test installs.
	_ = buildMarker
	if version != "" && version != "dev" {
		helper.Version = version
	}
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "install":
		fs := flag.NewFlagSet("install", flag.ExitOnError)
		source := fs.String("source", "", "path to helper binary to install")
		token := fs.String("token", "", "IPC token")
		uid := fs.Int("uid", 0, "owner uid")
		ver := fs.String("version", helper.Version, "helper version")
		home := fs.String("home", "", "user home directory for session allowlist")
		ownerSID := fs.String("sid", "", "Windows SID allowed to access the helper socket")
		singBox := fs.String("sing-box", "", "path to packaged sing-box binary")
		_ = fs.Parse(os.Args[2:])
		if err := helperinstall.InstallFromCLI(*source, *token, *uid, *ver, *home, *ownerSID, *singBox); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "uninstall":
		if err := helperinstall.UninstallFromCLI(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "run":
		auth, err := helper.ReadSystemAuth()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		server := helper.NewServer(auth)
		if err := helper.RunService(server); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "elevated":
		fs := flag.NewFlagSet("elevated", flag.ExitOnError)
		operation := fs.String("operation", "", "elevated operation")
		request := fs.String("request", "", "elevated request file")
		result := fs.String("result", "", "elevated result file")
		_ = fs.Parse(os.Args[2:])
		if *operation == "" || *request == "" || *result == "" {
			fmt.Fprintln(os.Stderr, "--operation, --request, and --result are required")
			os.Exit(2)
		}
		if err := helperinstall.RunElevatedRequest(*operation, *request, *result); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "version", "--version", "-version":
		fmt.Println(helper.Version)
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `KubeLoop privileged helper

Usage:
  kubeloop-helper install --source PATH --token TOKEN [--uid N] [--version V]
  kubeloop-helper uninstall
  kubeloop-helper run
  kubeloop-helper elevated --operation OP --request PATH --result PATH
  kubeloop-helper version
`)
}
