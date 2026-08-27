//go:build darwin

package app

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/fengqi-dev/kube-loop/internal/buildinfo"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperinstall "github.com/fengqi-dev/kube-loop/internal/helper/install"
	"github.com/fengqi-dev/kube-loop/internal/supervisor"
)

const (
	channelDev     = "dev"
	channelRelease = "release"
)

// NewSupervisorCommand returns the kubeloop-supervisor cobra command.
func NewSupervisorCommand() *cobra.Command {
	return newSupervisorCommand(buildinfo.Get())
}

func newSupervisorCommand(info buildinfo.Info) *cobra.Command {
	root := &cobra.Command{
		Use:     "kubeloop-supervisor",
		Short:   "Manage the privileged KubeLoop worker",
		Version: info.Version,
	}
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.SetVersionTemplate("kubeloop-supervisor {{.Version}}\n")
	root.AddCommand(
		&cobra.Command{
			Use:   "version",
			Short: "Print the version",
			Args:  cobra.NoArgs,
			RunE: func(command *cobra.Command, _ []string) error {
				_, err := fmt.Fprintf(command.OutOrStdout(), "kubeloop-supervisor %s\n", info.Version)
				return err
			},
		},
	)
	root.AddCommand(newSupervisorRunCommand(), newSupervisorInstallCommand())
	return root
}

func newSupervisorRunCommand() *cobra.Command {
	channel := channelRelease
	command := &cobra.Command{
		Use:   "run",
		Short: "Run the supervisor service",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := configureChannel(channel, ""); err != nil {
				return err
			}
			config := supervisor.CurrentConfig()
			auth, err := supervisor.ReadAuth(config)
			if err != nil {
				return err
			}
			signalContext, stopSignals := signal.NotifyContext(
				command.Context(),
				os.Interrupt,
				syscall.SIGTERM,
			)
			defer stopSignals()
			return supervisor.NewServer(config, auth, nil).Serve(signalContext)
		},
	}
	command.Flags().StringVar(&channel, "channel", channelRelease, "installation channel")
	return command
}

func newSupervisorInstallCommand() *cobra.Command {
	var source, sha, worker, workerSHA, workerVersion, channel, token, home, singBox string
	uid := -1
	command := &cobra.Command{
		Use:    "install",
		Short:  "Install or upgrade the supervisor and worker",
		Args:   cobra.NoArgs,
		Hidden: true,
		PreRunE: func(*cobra.Command, []string) error {
			missingValue := source == "" || sha == "" || worker == "" || workerSHA == "" ||
				workerVersion == "" || channel == "" || token == "" || uid < 0 || home == "" || singBox == ""
			if missingValue {
				return fmt.Errorf(
					"install requires source, sha256, worker, worker-sha256, worker-version, channel, token, uid, home, and sing-box",
				)
			}
			return nil
		},
		RunE: func(*cobra.Command, []string) error {
			if err := configureChannel(channel, workerVersion); err != nil {
				return err
			}
			if err := supervisor.VerifyFileSHA256(worker, workerSHA); err != nil {
				return fmt.Errorf("worker: %w", err)
			}
			if err := helperinstall.InstallFromCLI(worker, token, uid, workerVersion, home, "", singBox); err != nil {
				return err
			}
			return supervisor.Install(source, sha, token, uid)
		},
	}
	command.Flags().StringVar(&source, "source", "", "supervisor source")
	command.Flags().StringVar(&sha, "sha256", "", "supervisor SHA-256")
	command.Flags().StringVar(&worker, "worker", "", "worker source")
	command.Flags().StringVar(&workerSHA, "worker-sha256", "", "worker SHA-256")
	command.Flags().StringVar(&workerVersion, "worker-version", "", "worker version")
	command.Flags().StringVar(&channel, "channel", "", "installation channel")
	command.Flags().StringVar(&token, "token", "", "IPC token")
	command.Flags().IntVar(&uid, "uid", -1, "authorized UID")
	command.Flags().StringVar(&home, "home", "", "authorized home")
	command.Flags().StringVar(&singBox, "sing-box", "", "sing-box path")
	return command
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
