package main

import (
	"bytes"
	"context"
	"testing"
)

func TestTUIVersionCommands(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var stdout, stderr bytes.Buffer
		if code := executeTUI(context.Background(), args, &stdout, &stderr); code != 0 {
			t.Fatalf("args=%v exit=%d stderr=%q", args, code, stderr.String())
		}
		if stdout.String() != "kubeloop "+version+"\n" || stderr.Len() != 0 {
			t.Fatalf("args=%v stdout=%q stderr=%q", args, stdout.String(), stderr.String())
		}
	}
}

func TestTUIInvalidArgumentsUseUsageExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := executeTUI(context.Background(), []string{"unexpected"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
