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

const (
	channelDev     = "dev"
	channelRelease = "release"
)

var version = channelDev

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		cancel()
		os.Exit(1)
	}
	cancel()
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: kubeloop-supervisor <run|install|version>")
	}
	switch args[0] {
	case "run":
		flags := flag.NewFlagSet("run", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		channel := flags.String("channel", channelRelease, "installation channel")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("run does not accept positional arguments")
		}
		if err := configureChannel(*channel, ""); err != nil {
			return err
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
	channel := flags.String("channel", "", "installation channel")
	token := flags.String("token", "", "IPC token")
	uid := flags.Int("uid", -1, "authorized UID")
	home := flags.String("home", "", "authorized home")
	singBox := flags.String("sing-box", "", "sing-box path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	missingValue := *source == "" || *sha == "" || *worker == "" || *workerSHA == "" ||
		*workerVersion == "" || *channel == "" || *token == "" || *uid < 0 || *home == "" || *singBox == ""
	if flags.NArg() != 0 || missingValue {
		return fmt.Errorf(
			"install requires source, sha256, worker, worker-sha256, worker-version, " +
				"channel, token, uid, home, and sing-box",
		)
	}
	if err := configureChannel(*channel, *workerVersion); err != nil {
		return err
	}
	actualWorkerSHA, err := hashFile(*worker)
	if err != nil {
		return err
	}
	if actualWorkerSHA != *workerSHA {
		return fmt.Errorf("worker SHA-256 mismatch")
	}
	if err := helperinstall.InstallFromCLI(
		*worker,
		*token,
		*uid,
		*workerVersion,
		*home,
		"",
		*singBox,
	); err != nil {
		return err
	}
	return supervisor.Install(*source, *sha, *token, *uid)
}

func configureChannel(channel, workerVersion string) error {
	switch channel {
	case channelDev:
		if workerVersion != "" && workerVersion != channelDev {
			return fmt.Errorf("dev channel requires a dev worker, got %q", workerVersion)
		}
		helper.Version = channelDev
		supervisor.Version = channelDev
	case channelRelease:
		if workerVersion == channelDev {
			return fmt.Errorf("release channel cannot install a dev worker")
		}
		helper.Version = workerVersion
		if helper.Version == "" {
			helper.Version = channelRelease
		}
		supervisor.Version = channelRelease
	default:
		return fmt.Errorf("unsupported supervisor channel %q", channel)
	}
	return nil
}
