//go:build linux

package terminal

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

const shellLoginFlag = "-lc"

func open(command string) error {
	script := "exec " + command
	candidates := []struct {
		name string
		args []string
	}{
		{name: "x-terminal-emulator", args: []string{"-e", "sh", shellLoginFlag, script}},
		{name: "gnome-terminal", args: []string{"--", "sh", shellLoginFlag, script}},
		{name: "konsole", args: []string{"-e", "sh", shellLoginFlag, script}},
	}
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate.name)
		if err != nil {
			continue
		}
		if err := exec.CommandContext(context.Background(), path, candidate.args...).Start(); err != nil {
			return fmt.Errorf("open %s: %w", candidate.name, err)
		}
		return nil
	}
	return errors.New("no supported terminal emulator was found")
}
