package config

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
	oidcprovider "github.com/fengqi-dev/kube-loop/internal/controlplane/authn/oidc"
)

const maxConfigBytes = 1 << 20

type File struct {
	Providers []Provider `json:"providers"`
}

type Provider struct {
	ID          string      `json:"id"`
	Type        string      `json:"type"`
	DisplayName string      `json:"displayName,omitempty"`
	OIDC        *OIDCConfig `json:"oidc,omitempty"`
}

type OIDCConfig struct {
	Issuer             string                    `json:"issuer"`
	ClientID           string                    `json:"clientId"`
	ClientSecret       string                    `json:"clientSecret,omitempty"`
	ClientSecretFile   string                    `json:"clientSecretFile"`
	RedirectURL        string                    `json:"redirectUrl"`
	Scopes             []string                  `json:"scopes,omitempty"`
	AllowedSigningAlgs []string                  `json:"allowedSigningAlgs,omitempty"`
	RequiredClaims     []string                  `json:"requiredClaims,omitempty"`
	Claims             oidcprovider.ClaimMapping `json:"claims"`
	CAPEM              string                    `json:"caPem,omitempty"`
	HTTPTimeout        string                    `json:"httpTimeout,omitempty"`
}

func Load(path string) (File, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return File{}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return File{}, errors.New("open authentication config")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return File{}, errors.New("read authentication config")
	}
	if len(data) > maxConfigBytes {
		return File{}, errors.New("authentication config exceeds 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var config File
	if err := decoder.Decode(&config); err != nil {
		return File{}, errors.New("decode authentication config")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return File{}, err
	}
	return config, nil
}

func Build(ctx context.Context, config File) (*authn.Registry, error) {
	providers := make([]authn.Provider, 0, len(config.Providers))
	for index, item := range config.Providers {
		providerType := strings.ToLower(strings.TrimSpace(item.Type))
		switch providerType {
		case "oidc":
			if item.OIDC == nil {
				return nil, fmt.Errorf("auth provider %d requires oidc configuration", index)
			}
			provider, err := buildOIDC(ctx, item)
			if err != nil {
				return nil, fmt.Errorf("initialize auth provider %q: %w", item.ID, err)
			}
			providers = append(providers, provider)
		default:
			return nil, fmt.Errorf("auth provider %d has unsupported type %q", index, item.Type)
		}
	}
	return authn.NewRegistry(providers...)
}

func buildOIDC(ctx context.Context, item Provider) (*oidcprovider.Provider, error) {
	configuration := item.OIDC
	var timeout time.Duration
	var err error
	if strings.TrimSpace(configuration.HTTPTimeout) != "" {
		timeout, err = time.ParseDuration(configuration.HTTPTimeout)
		if err != nil || timeout <= 0 {
			return nil, errors.New("OIDC HTTP timeout must be a positive duration")
		}
	}
	roots, err := loadRoots([]byte(configuration.CAPEM), "OIDC")
	if err != nil {
		return nil, err
	}
	return oidcprovider.New(ctx, oidcprovider.Config{
		ID: item.ID, DisplayName: item.DisplayName,
		Issuer: configuration.Issuer, ClientID: configuration.ClientID,
		ClientSecret: configuration.ClientSecret, ClientSecretFile: configuration.ClientSecretFile, RedirectURL: configuration.RedirectURL,
		Scopes: configuration.Scopes, AllowedSigningAlgs: configuration.AllowedSigningAlgs,
		RequiredClaims: configuration.RequiredClaims, Claims: configuration.Claims,
		HTTPTimeout: timeout, RootCAs: roots,
	})
}

func loadRoots(pem []byte, label string) (*x509.CertPool, error) {
	if len(bytes.TrimSpace(pem)) == 0 {
		return nil, nil
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s CA file contains no certificates", label)
	}
	return roots, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	}
	return errors.New("authentication config must contain exactly one JSON document")
}
