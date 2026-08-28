package supervisorapp

import (
	"context"
	"fmt"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperinstall "github.com/fengqi-dev/kube-loop/internal/helper/install"
	"github.com/fengqi-dev/kube-loop/internal/supervisor"
)

const (
	ChannelDev     = "dev"
	ChannelRelease = "release"
)

type InstallOptions struct {
	Source        string
	SHA256        string
	Worker        string
	WorkerSHA256  string
	WorkerVersion string
	Channel       string
	Token         string
	UID           int
	Home          string
	SingBox       string
}

func Run(ctx context.Context, channel string) error {
	if err := ConfigureChannel(channel, ""); err != nil {
		return err
	}
	config := supervisor.CurrentConfig()
	auth, err := supervisor.ReadAuth(config)
	if err != nil {
		return err
	}
	return supervisor.NewServer(config, auth, nil).Serve(ctx)
}

func InstallRelease(options InstallOptions) error {
	if err := ConfigureChannel(options.Channel, options.WorkerVersion); err != nil {
		return err
	}
	if err := supervisor.VerifyFileSHA256(options.Worker, options.WorkerSHA256); err != nil {
		return fmt.Errorf("worker: %w", err)
	}
	if err := helperinstall.InstallFromCLI(
		options.Worker,
		options.Token,
		options.UID,
		options.WorkerVersion,
		options.Home,
		"",
		options.SingBox,
	); err != nil {
		return err
	}
	return supervisor.Install(options.Source, options.SHA256, options.Token, options.UID)
}

func ConfigureChannel(channel, workerVersion string) error {
	switch channel {
	case ChannelDev:
		if workerVersion != "" && workerVersion != ChannelDev {
			return fmt.Errorf("dev channel requires a dev worker, got %q", workerVersion)
		}
		helper.Version = ChannelDev
		supervisor.Version = ChannelDev
	case ChannelRelease:
		if workerVersion == ChannelDev {
			return fmt.Errorf("release channel cannot install a dev worker")
		}
		helper.Version = workerVersion
		if helper.Version == "" {
			helper.Version = ChannelRelease
		}
		supervisor.Version = ChannelRelease
	default:
		return fmt.Errorf("unsupported supervisor channel %q", channel)
	}
	return nil
}
