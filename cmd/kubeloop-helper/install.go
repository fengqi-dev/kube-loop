package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

type installOptions struct {
	source   string
	token    string
	uid      int
	version  string
	home     string
	ownerSID string
	singBox  string
}

func newInstallCommand(dependencies commandDependencies, commandVersion string) *cobra.Command {
	options := installOptions{}
	command := &cobra.Command{
		Use:   "install",
		Short: "Install or upgrade the privileged helper service",
		Args:  usageArgs(cobra.NoArgs),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if err := requireValues(
				requiredValue{name: "source", value: options.source},
				requiredValue{name: "token", value: options.token},
				requiredValue{name: "home", value: options.home},
			); err != nil {
				return err
			}
			if options.uid < 0 {
				return &usageError{err: fmt.Errorf("--uid must not be negative")}
			}
			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			if dependencies.install == nil {
				return unavailable("install")
			}
			return dependencies.install(options)
		},
	}
	command.Flags().StringVar(&options.source, "source", "", "path to helper binary to install")
	command.Flags().StringVar(&options.token, "token", "", "IPC authentication token")
	command.Flags().IntVar(&options.uid, "uid", 0, "UID allowed to access the helper socket")
	command.Flags().StringVar(&options.version, "version", commandVersion, "helper version")
	command.Flags().StringVar(&options.home, "home", "", "user home directory for the session allowlist")
	command.Flags().StringVar(&options.ownerSID, "sid", "", "Windows SID allowed to access the helper socket")
	command.Flags().StringVar(&options.singBox, "sing-box", "", "path to the packaged sing-box binary")
	markFlagsRequired(command, "source", "token", "home")
	return command
}
