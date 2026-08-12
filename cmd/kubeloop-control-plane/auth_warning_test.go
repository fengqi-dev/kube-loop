package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
)

func TestAnonymousDevelopmentAuthenticationEmitsHighVisibilityWarning(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	warnDevelopmentAuthentication(logger, []authn.Descriptor{
		{ID: "local", Type: authn.ProviderStaticToken},
		{ID: "guest", Type: authn.ProviderAnonymous},
	})
	logged := output.String()
	for _, want := range []string{
		"SECURITY WARNING", "ANONYMOUS DEVELOPMENT AUTHENTICATION IS ENABLED",
		"provider_id=guest", "production_safe=false",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("development authentication warning missing %q: %s", want, logged)
		}
	}
}
