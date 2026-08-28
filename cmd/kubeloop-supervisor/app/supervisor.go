//go:build darwin

package app

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/fengqi-dev/kube-loop/internal/buildinfo"
	supervisorapp "github.com/fengqi-dev/kube-loop/internal/supervisorapp"
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
	channel := supervisorapp.ChannelRelease
	command := &cobra.Command{
		Use:   "run",
		Short: "Run the supervisor service",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			signalContext, stopSignals := signal.NotifyContext(
				command.Context(),
				os.Interrupt,
				syscall.SIGTERM,
			)
			defer stopSignals()
			return supervisorapp.Run(signalContext, channel)
		},
	}
	command.Flags().StringVar(&channel, "channel", supervisorapp.ChannelRelease, "installation channel")
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
			return supervisorapp.InstallRelease(supervisorapp.InstallOptions{
				Source: source, SHA256: sha, Worker: worker, WorkerSHA256: workerSHA,
				WorkerVersion: workerVersion, Channel: channel, Token: token,
				UID: uid, Home: home, SingBox: singBox,
			})
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
