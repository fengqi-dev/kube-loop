//go:build darwin

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperinstall "github.com/fengqi-dev/kube-loop/internal/helper/install"
	"github.com/fengqi-dev/kube-loop/internal/supervisor"
)

var version = "dev"

func main() {
	if version != "" && version != "dev" {
		helper.Version = version
		supervisor.Version = version
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: kubeloop-supervisor <run|install|version>")
	}
	switch args[0] {
	case "run":
		if len(args) != 1 {
			return fmt.Errorf("run does not accept arguments")
		}
		config := supervisor.CurrentConfig()
		auth, err := supervisor.ReadAuth(config)
		if err != nil {
			return err
		}
		return supervisor.NewServer(config, auth, nil).Serve(ctx)
	case "version":
		if len(args) != 1 {
			return fmt.Errorf("version does not accept arguments")
		}
		fmt.Println(version)
		return nil
	case "install":
		return runInstall(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runInstall(args []string) error {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	source := flags.String("source", "", "supervisor source")
	sha := flags.String("sha256", "", "supervisor SHA-256")
	worker := flags.String("worker", "", "worker source")
	workerSHA := flags.String("worker-sha256", "", "worker SHA-256")
	workerVersion := flags.String("worker-version", "", "worker version")
	token := flags.String("token", "", "IPC token")
	uid := flags.Int("uid", -1, "authorized UID")
	home := flags.String("home", "", "authorized home")
	singBox := flags.String("sing-box", "", "sing-box path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *source == "" || *sha == "" || *worker == "" || *workerSHA == "" || *workerVersion == "" || *token == "" || *uid < 0 || *home == "" || *singBox == "" {
		return fmt.Errorf("install requires source, sha256, worker, worker-sha256, worker-version, token, uid, home, and sing-box")
	}
	actualWorkerSHA, err := hashFile(*worker)
	if err != nil {
		return err
	}
	if actualWorkerSHA != *workerSHA {
		return fmt.Errorf("worker SHA-256 mismatch")
	}
	if err := helperinstall.InstallFromCLI(*worker, *token, *uid, *workerVersion, *home, "", *singBox); err != nil {
		return err
	}
	return supervisor.Install(*source, *sha, *token, *uid)
}
