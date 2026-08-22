package cli

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
)

func TestExecuteUsesStandardExitCodesAndStreams(t *testing.T) {
	tests := []struct {
		name       string
		command    *cobra.Command
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name: "success",
			command: &cobra.Command{Use: "example", RunE: func(command *cobra.Command, _ []string) error {
				_, err := command.OutOrStdout().Write([]byte("ok\n"))
				return err
			}},
			wantCode: ExitSuccess, wantStdout: "ok\n",
		},
		{
			name: "usage",
			command: &cobra.Command{
				Use: "example", Args: NoArgs,
				RunE: func(*cobra.Command, []string) error { return nil },
			},
			args:       []string{"extra"},
			wantCode:   ExitUsage,
			wantStderr: "Error: unknown command \"extra\" for \"example\"\n",
		},
		{
			name: "runtime",
			command: &cobra.Command{
				Use:  "example",
				RunE: func(*cobra.Command, []string) error { return errors.New("failed") },
			},
			wantCode: ExitFailure, wantStderr: "Error: failed\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ConfigureRoot(test.command, "example")
			var stdout, stderr bytes.Buffer
			got := Execute(context.Background(), test.command, test.args, &stdout, &stderr)
			if got != test.wantCode || stdout.String() != test.wantStdout || stderr.String() != test.wantStderr {
				t.Fatalf("execute = %d, stdout=%q stderr=%q", got, stdout.String(), stderr.String())
			}
		})
	}
}
