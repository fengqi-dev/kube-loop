package app

import (
	"fmt"

	"github.com/spf13/cobra"
)

const trustCertificateCommandName = "trust-certificate"

type trustCertificateOptions struct {
	operation   string
	certificate string
}

func newTrustCertificateCommand(dependencies commandDependencies) *cobra.Command {
	options := trustCertificateOptions{}
	command := &cobra.Command{
		Use:    trustCertificateCommandName,
		Short:  "Manage the traffic inspection certificate from an elevated process",
		Hidden: true,
		Args:   cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if err := requireValues(
				requiredValue{name: "operation", value: options.operation},
				requiredValue{name: "certificate", value: options.certificate},
			); err != nil {
				return err
			}
			if options.operation != "install" && options.operation != "uninstall" {
				return fmt.Errorf("--operation must be install or uninstall")
			}
			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			if dependencies.trustCertificate == nil {
				return unavailable(trustCertificateCommandName)
			}
			return dependencies.trustCertificate(command.Context(), options)
		},
	}
	command.Flags().StringVar(&options.operation, "operation", "", "install or uninstall")
	command.Flags().StringVar(&options.certificate, "certificate", "", "public certificate PEM path")
	markFlagsRequired(command, "operation", "certificate")
	return command
}
