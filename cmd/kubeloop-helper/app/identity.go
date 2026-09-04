package app

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/fengqi-dev/kube-loop/internal/protocol/helperrpc"
)

type helperIdentity struct {
	Kind     string `json:"kind"`
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
}

func newIdentityCommand(commandVersion string) *cobra.Command {
	return &cobra.Command{
		Use:    "identity",
		Short:  "Print the machine-readable worker identity",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return json.NewEncoder(command.OutOrStdout()).Encode(helperIdentity{
				Kind: "kubeloop-helper", Version: commandVersion, Protocol: helperrpc.Version,
			})
		},
	}
}
