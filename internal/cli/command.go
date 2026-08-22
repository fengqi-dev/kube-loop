package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

const (
	ExitSuccess = 0
	ExitFailure = 1
	ExitUsage   = 2
)

type UsageError struct{ Err error }

func (err *UsageError) Error() string { return err.Err.Error() }
func (err *UsageError) Unwrap() error { return err.Err }

func Usage(err error) error {
	if err == nil {
		return nil
	}
	return &UsageError{Err: err}
}

func NoArgs(command *cobra.Command, args []string) error {
	return Usage(cobra.NoArgs(command, args))
}

func ConfigureRoot(command *cobra.Command, versionLabel string) {
	command.SilenceErrors = true
	command.SilenceUsage = true
	command.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return Usage(err) })
	if command.Version != "" {
		command.SetVersionTemplate(versionLabel + " {{.Version}}\n")
	}
}

func AddVersionCommand(root *cobra.Command, versionLabel, version string) {
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(command.OutOrStdout(), "%s %s\n", versionLabel, version)
			return err
		},
	})
}

func Execute(
	ctx context.Context,
	command *cobra.Command,
	args []string,
	stdout, stderr io.Writer,
) int {
	command.SetArgs(args)
	command.SetOut(stdout)
	command.SetErr(stderr)
	if err := command.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		if IsUsageError(err) {
			return ExitUsage
		}
		return ExitFailure
	}
	return ExitSuccess
}

func IsUsageError(err error) bool {
	if _, ok := errors.AsType[*UsageError](err); ok {
		return true
	}
	message := err.Error()
	return strings.HasPrefix(message, "unknown command ") ||
		strings.HasPrefix(message, "unknown flag: ") ||
		strings.HasPrefix(message, "unknown shorthand flag: ")
}
