package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

type commandDependencies struct {
	install   func(installOptions) error
	uninstall func() error
	run       func(context.Context) error
	elevated  func(elevatedOptions) error
}

type usageError struct {
	err error
}

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

func execute(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	dependencies commandDependencies,
	commandVersion string,
) int {
	command := newRootCommand(dependencies, commandVersion)
	command.SetArgs(args)
	command.SetOut(stdout)
	command.SetErr(stderr)
	if err := command.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		if isUsageError(err) {
			return 2
		}
		return 1
	}
	return 0
}

func newRootCommand(dependencies commandDependencies, commandVersion string) *cobra.Command {
	root := &cobra.Command{
		Use:           "kubeloop-helper",
		Short:         "KubeLoop privileged helper",
		Version:       commandVersion,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          usageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &usageError{err: err}
	})
	root.AddCommand(
		newInstallCommand(dependencies, commandVersion),
		newUninstallCommand(dependencies),
		newRunCommand(dependencies),
		newElevatedCommand(dependencies),
		newVersionCommand(commandVersion),
	)
	return root
}

func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(command *cobra.Command, args []string) error {
		if err := validate(command, args); err != nil {
			return &usageError{err: err}
		}
		return nil
	}
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
	return &usageError{err: fmt.Errorf("required flag(s) %s not set", strings.Join(missing, ", "))}
}

func markFlagsRequired(command *cobra.Command, names ...string) {
	for _, name := range names {
		if err := command.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
}

func isUsageError(err error) bool {
	var target *usageError
	if errors.As(err, &target) {
		return true
	}
	message := err.Error()
	return strings.HasPrefix(message, "unknown command ") ||
		strings.HasPrefix(message, "unknown shorthand flag: ")
}

func unavailable(command string) error {
	return fmt.Errorf("%s command is unavailable", command)
}
