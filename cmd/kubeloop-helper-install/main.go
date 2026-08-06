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
	fs := flag.NewFlagSet("kubeloop-helper-install", flag.ExitOnError)
	request := fs.String("request", "", "elevated request file")
	result := fs.String("result", "", "elevated result file")
	source := fs.String("source", "", "path to helper service binary to install")
	token := fs.String("token", "", "IPC token")
	uid := fs.Int("uid", 0, "owner uid")
	ver := fs.String("version", helper.Version, "helper version")
	home := fs.String("home", "", "user home directory for session allowlist")
	ownerSID := fs.String("sid", "", "Windows SID allowed to access the helper socket")
	singBox := fs.String("sing-box", "", "path to packaged sing-box binary")
	_ = fs.Parse(os.Args[1:])

	if *request != "" || *result != "" {
		if *request == "" || *result == "" {
			fmt.Fprintln(os.Stderr, "--request and --result are required together")
			os.Exit(2)
		}
		if err := helperinstall.RunElevatedRequest("install", *request, *result); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := helperinstall.InstallFromCLI(*source, *token, *uid, *ver, *home, *ownerSID, *singBox); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
