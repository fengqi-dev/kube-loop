//go:build e2e

package oidc_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	clientauth "github.com/fengqi-dev/kube-loop/internal/client/auth"
)

func TestKeycloakBrowserLoginRefreshAndRevoke(t *testing.T) {
	if os.Getenv("KUBELOOP_OIDC_E2E") != "1" {
		t.Skip("set KUBELOOP_OIDC_E2E=1 to run the Keycloak browser E2E test")
	}
	baseURL := requiredEnvironment(t, "KUBELOOP_OIDC_E2E_BASE_URL")
	username := requiredEnvironment(t, "KUBELOOP_OIDC_E2E_USERNAME")
	password := requiredEnvironment(t, "KUBELOOP_OIDC_E2E_PASSWORD")
	artifacts := requiredEnvironment(t, "KUBELOOP_OIDC_E2E_ARTIFACTS")
	if err := os.MkdirAll(artifacts, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	client := clientauth.New(clientauth.Config{
		RequestTimeout: 10 * time.Second,
		LoginTimeout:   60 * time.Second,
		OpenBrowser: func(target string) error {
			logFile, err := os.Create(filepath.Join(artifacts, "playwright.log"))
			if err != nil {
				return err
			}
			command := exec.CommandContext(ctx, "node", "e2e/oidc/browser.mjs", target, username, password, artifacts)
			command.Stdout = logFile
			command.Stderr = logFile
			err = command.Run()
			if closeErr := logFile.Close(); err == nil {
				err = closeErr
			}
			return err
		},
	})

	credential, err := client.LoginOIDC(ctx, baseURL, "keycloak", "github-actions-e2e")
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccessToken == "" || credential.RefreshToken == "" {
		t.Fatal("OIDC login returned incomplete credentials")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/kubeloop/api/namespaces", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authorized namespace request returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	refreshed, err := client.Refresh(ctx, baseURL, credential)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken == credential.AccessToken || refreshed.RefreshToken == credential.RefreshToken {
		t.Fatal("refresh did not rotate both tokens")
	}
	if err := client.Revoke(ctx, baseURL, refreshed.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Refresh(ctx, baseURL, refreshed); err == nil {
		t.Fatal("revoked token family was refreshed")
	}
}

func requiredEnvironment(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatal(fmt.Sprintf("%s is required", name))
	}
	return value
}
