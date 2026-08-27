package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fengqi-dev/kube-loop/internal/buildinfo"
	"github.com/fengqi-dev/kube-loop/internal/helper"
)

const (
	installCommandName = "install"
	runCommandName     = "run"
	versionCommandName = "version"
)

type commandDependencies struct {
	install          func(installOptions) error
	uninstall        func() error
	run              func(context.Context) error
	elevated         func(elevatedOptions) error
	trustCertificate func(context.Context, trustCertificateOptions) error
}

// NewHelperCommand returns the kubeloop-helper cobra command.
func NewHelperCommand() *cobra.Command {
	info := buildinfo.Get()
	if info.Version != "dev" {
		helper.Version = info.Version
	}
	return newHelperCommand(productionDependencies(), info)
}

func newHelperCommand(dependencies commandDependencies, info buildinfo.Info) *cobra.Command {
	commandVersion := info.Version
	root := &cobra.Command{
		Use:     "kubeloop-helper",
		Short:   "KubeLoop privileged helper",
		Version: commandVersion,
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.SetVersionTemplate("kubeloop-helper {{.Version}}\n")
	versionCommand := &cobra.Command{
		Use:   versionCommandName,
		Short: "Print the helper version",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(command.OutOrStdout(), "kubeloop-helper", commandVersion)
			return err
		},
	}
	root.AddCommand(
		newInstallCommand(dependencies, commandVersion),
		newUninstallCommand(dependencies),
		newRunCommand(dependencies),
		newElevatedCommand(dependencies),
		newTrustCertificateCommand(dependencies),
		versionCommand,
		newIdentityCommand(commandVersion),
	)
	return root
}

type requiredValue struct {
	name  string
	value string
}

func requireValues(values ...requiredValue) error {
	missing := make([]string, 0, len(values))
	for _, item := range values {
		if item.value == "" {
			missing = append(missing, "--"+item.name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("required flag(s) %s not set", strings.Join(missing, ", "))
}

func markFlagsRequired(command *cobra.Command, names ...string) {
	for _, name := range names {
		if err := command.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
}

func unavailable(command string) error {
	return fmt.Errorf("%s command is unavailable", command)
}
