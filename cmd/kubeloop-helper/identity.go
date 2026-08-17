package main

import (
	"encoding/json"

	helperprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/helper"
	"github.com/spf13/cobra"
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
		Args:   usageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return json.NewEncoder(command.OutOrStdout()).Encode(helperIdentity{
				Kind: "kubeloop-helper", Version: commandVersion, Protocol: helperprotocol.Version,
			})
		},
	}
}
