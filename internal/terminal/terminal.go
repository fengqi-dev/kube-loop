package terminal

import (
	"errors"
	"strings"
)

// Open starts the platform terminal and runs a trusted application-generated
// command in a new interactive window.
func Open(command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return errors.New("terminal command is required")
	}
	return open(command)
}
