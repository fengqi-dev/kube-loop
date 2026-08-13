package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	managementconfig "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/config"
	authconfig "github.com/fengqi-dev/kube-loop/internal/controlplane/authn/config"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/fileapi"
	controlplanekubernetes "github.com/fengqi-dev/kube-loop/internal/controlplane/kubernetes"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/maintenance"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
	"sigs.k8s.io/yaml"
)

const maximumControlPlaneConfigBytes = 4 << 20

type controlPlaneConfigDocument struct {
	API            apiConfig            `json:"api"`
	Authentication authenticationConfig `json:"authentication"`
	Authorization  authorization.Policy `json:"authorization"`
	Management     managementConfig     `json:"management"`
	Kubernetes     kubernetesConfig     `json:"kubernetes"`
	Relay          relayConfig          `json:"relay"`
	Sessions       sessionsConfig       `json:"sessions"`
	Storage        storageConfig        `json:"storage"`
	Maintenance    maintenanceConfig    `json:"maintenance"`
	Files          filesConfig          `json:"files"`
	Logging        loggingConfig        `json:"logging"`
}

type apiConfig struct {
	Listen              string `json:"listen"`
	PublicURL           string `json:"publicURL"`
	ServiceID           string `json:"serviceID"`
	TunnelPath          string `json:"tunnelPath"`
	MinClientVersion    string `json:"minClientVersion,omitempty"`
	ShutdownTimeout     string `json:"shutdownTimeout"`
	RequestTimeout      string `json:"requestTimeout"`
	MaxRequestBodyBytes int64  `json:"maxRequestBodyBytes"`
}

type authenticationConfig struct {
	Providers []authconfig.Provider `json:"providers"`
	Token     tokenConfig           `json:"token"`
}

type tokenConfig struct {
	SigningKeyFile string `json:"signingKeyFile"`
	KeyID          string `json:"keyID"`
	AccessTTL      string `json:"accessTTL"`
	RefreshTTL     string `json:"refreshTTL"`
}

type managementConfig struct {
	Listen                string                                 `json:"listen"`
	PublicURL             string                                 `json:"publicURL"`
	InitialAdmin          initialAdminConfig                     `json:"initialAdmin"`
	Bootstrap             managementconfig.BootstrapConfig       `json:"bootstrap"`
	BreakGlass            managementconfig.BreakGlassConfig      `json:"breakGlass"`
	ProviderSecretAliases managementconfig.ProviderSecretAliases `json:"providerSecretAliases,omitempty"`
}

type initialAdminConfig struct {
	UsernameFile         string `json:"usernameFile,omitempty"`
	PasswordFile         string `json:"passwordFile,omitempty"`
	MFAEncryptionKeyFile string `json:"mfaEncryptionKeyFile,omitempty"`
}

type kubernetesConfig struct {
	Timeout       string                                     `json:"timeout"`
	QPS           float32                                    `json:"qps"`
	Burst         int                                        `json:"burst"`
	UserAgent     string                                     `json:"userAgent,omitempty"`
	Impersonation controlplanekubernetes.ImpersonationConfig `json:"impersonation"`
}

type relayConfig struct {
	Ticket   relayTicketConfig   `json:"ticket"`
	Registry relayRegistryConfig `json:"registry"`
}

type relayTicketConfig struct {
	SigningKeyFile string `json:"signingKeyFile"`
	KeyID          string `json:"keyID"`
	TTL            string `json:"ttl"`
}

type relayRegistryConfig struct {
	Listen               string `json:"listen"`
	CertificateFile      string `json:"certificateFile"`
	PrivateKeyFile       string `json:"privateKeyFile"`
	ClientCAFile         string `json:"clientCAFile,omitempty"`
	Authentication       string `json:"authentication"`
	TokenAudience        string `json:"tokenAudience"`
	TrustDomain          string `json:"trustDomain"`
	Namespace            string `json:"namespace"`
	ServiceAccount       string `json:"serviceAccount"`
	EndpointAllowedHosts string `json:"endpointAllowedHosts,omitempty"`
	LeaseDuration        string `json:"leaseDuration"`
	HeartbeatAfter       string `json:"heartbeatAfter"`
	KeyGeneration        uint64 `json:"keyGeneration"`
	KeyValidity          string `json:"keyValidity"`
}

type sessionsConfig struct {
	TTL         string `json:"ttl"`
	MaxLifetime string `json:"maxLifetime"`
}

type storageConfig struct {
	DatasourceURL           string       `json:"datasourceURL,omitempty"`
	DatasourceURLFile       string       `json:"datasourceURLFile,omitempty"`
	Replicas                int          `json:"replicas"`
	SQLite                  sqliteConfig `json:"sqlite"`
	ConnectTimeout          string       `json:"connectTimeout"`
	QueryTimeout            string       `json:"queryTimeout"`
	MaxOpenConnections      int          `json:"maxOpenConnections"`
	MaxIdleConnections      int          `json:"maxIdleConnections"`
	ConnectionMaxLifetime   string       `json:"connectionMaxLifetime"`
	TransactionMaxRetries   int          `json:"transactionMaxRetries"`
	TransactionRetryBackoff string       `json:"transactionRetryBackoff"`
	AllowInsecureDatasource bool         `json:"allowInsecureDatasource,omitempty"`
}

type sqliteConfig struct {
	Path        string `json:"path"`
	BusyTimeout string `json:"busyTimeout,omitempty"`
}

type maintenanceConfig struct {
	Interval  string `json:"interval"`
	BatchSize int    `json:"batchSize"`
}

type filesConfig struct {
	MaxBytes     uint64   `json:"maxBytes"`
	AllowedRoots []string `json:"allowedRoots"`
}

type loggingConfig struct {
	Level string `json:"level"`
}

type loadedControlPlaneConfig struct {
	Document            controlPlaneConfigDocument
	Authentication      authconfig.File
	Management          managementconfig.File
	Policy              authorization.Policy
	Kubernetes          controlplanekubernetes.Config
	Storage             controlplanestorage.Config
	Files               fileapi.Config
	AccessTokenTTL      time.Duration
	RefreshTokenTTL     time.Duration
	SessionTTL          time.Duration
	SessionMaxLifetime  time.Duration
	RelayTicketTTL      time.Duration
	RelayLeaseDuration  time.Duration
	RelayHeartbeatAfter time.Duration
	RelayKeyValidity    time.Duration
	MaintenanceInterval time.Duration
	ShutdownTimeout     time.Duration
	APIRequestTimeout   time.Duration
}

func loadControlPlaneConfig(path string) (loadedControlPlaneConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return loadedControlPlaneConfig{}, errors.New("--config is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return loadedControlPlaneConfig{}, fmt.Errorf("open Control Plane config: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maximumControlPlaneConfigBytes+1))
	if err != nil {
		return loadedControlPlaneConfig{}, errors.New("read Control Plane config")
	}
	if len(raw) > maximumControlPlaneConfigBytes {
		return loadedControlPlaneConfig{}, errors.New("Control Plane config exceeds 4 MiB")
	}
	var document controlPlaneConfigDocument
	if err := yaml.UnmarshalStrict(raw, &document); err != nil {
		return loadedControlPlaneConfig{}, fmt.Errorf("decode Control Plane YAML: %w", err)
	}
	return normalizeControlPlaneConfig(document)
}

func normalizeControlPlaneConfig(document controlPlaneConfigDocument) (loadedControlPlaneConfig, error) {
	applyControlPlaneDefaults(&document)
	result := loadedControlPlaneConfig{Document: document}
	result.Authentication = authconfig.File{Providers: document.Authentication.Providers}
	var err error
	result.Management, err = managementconfig.Normalize(managementconfig.File{
		Bootstrap: document.Management.Bootstrap, BreakGlass: document.Management.BreakGlass,
		ProviderSecretAliases: document.Management.ProviderSecretAliases,
	})
	if err != nil {
		return loadedControlPlaneConfig{}, err
	}
	result.Policy, err = authorization.Normalize(document.Authorization)
	if err != nil {
		return loadedControlPlaneConfig{}, err
	}
	result.Kubernetes, err = normalizeKubernetesConfig(document.Kubernetes)
	if err != nil {
		return loadedControlPlaneConfig{}, err
	}
	result.Storage, err = normalizeStorageConfig(document.Storage)
	if err != nil {
		return loadedControlPlaneConfig{}, err
	}
	result.Files = fileapi.Config{MaximumBytes: document.Files.MaxBytes, AllowedPathRoots: append([]string(nil), document.Files.AllowedRoots...)}
	for label, input := range map[string]struct {
		raw    string
		target *time.Duration
	}{
		"authentication.token.accessTTL":  {document.Authentication.Token.AccessTTL, &result.AccessTokenTTL},
		"authentication.token.refreshTTL": {document.Authentication.Token.RefreshTTL, &result.RefreshTokenTTL},
		"sessions.ttl":                    {document.Sessions.TTL, &result.SessionTTL},
		"sessions.maxLifetime":            {document.Sessions.MaxLifetime, &result.SessionMaxLifetime},
		"relay.ticket.ttl":                {document.Relay.Ticket.TTL, &result.RelayTicketTTL},
		"relay.registry.leaseDuration":    {document.Relay.Registry.LeaseDuration, &result.RelayLeaseDuration},
		"relay.registry.heartbeatAfter":   {document.Relay.Registry.HeartbeatAfter, &result.RelayHeartbeatAfter},
		"relay.registry.keyValidity":      {document.Relay.Registry.KeyValidity, &result.RelayKeyValidity},
		"maintenance.interval":            {document.Maintenance.Interval, &result.MaintenanceInterval},
		"api.shutdownTimeout":             {document.API.ShutdownTimeout, &result.ShutdownTimeout},
		"api.requestTimeout":              {document.API.RequestTimeout, &result.APIRequestTimeout},
	} {
		*input.target, err = positiveDuration(label, input.raw)
		if err != nil {
			return loadedControlPlaneConfig{}, err
		}
	}
	return result, nil
}

func applyControlPlaneDefaults(document *controlPlaneConfigDocument) {
	if document.API.Listen == "" {
		document.API.Listen = controlplane.DefaultListenAddress
	}
	if document.API.ServiceID == "" {
		document.API.ServiceID = controlplane.DefaultServiceID
	}
	if document.API.TunnelPath == "" {
		document.API.TunnelPath = controlplane.DefaultTunnelPath
	}
	if document.API.ShutdownTimeout == "" {
		document.API.ShutdownTimeout = controlplane.DefaultShutdownTimeout.String()
	}
	if document.API.RequestTimeout == "" {
		document.API.RequestTimeout = controlplane.DefaultAPIRequestTimeout.String()
	}
	if document.API.MaxRequestBodyBytes == 0 {
		document.API.MaxRequestBodyBytes = controlplane.DefaultMaxRequestBodyBytes
	}
	if document.Management.Listen == "" {
		document.Management.Listen = ":8081"
	}
	if document.Management.PublicURL == "" {
		document.Management.PublicURL = "http://127.0.0.1:8081"
	}
	if document.Authentication.Token.KeyID == "" {
		document.Authentication.Token.KeyID = "primary"
	}
	if document.Authentication.Token.AccessTTL == "" {
		document.Authentication.Token.AccessTTL = (5 * time.Minute).String()
	}
	if document.Authentication.Token.RefreshTTL == "" {
		document.Authentication.Token.RefreshTTL = (30 * 24 * time.Hour).String()
	}
	if document.Sessions.TTL == "" {
		document.Sessions.TTL = sessionapi.DefaultSessionTTL.String()
	}
	if document.Sessions.MaxLifetime == "" {
		document.Sessions.MaxLifetime = sessionapi.DefaultMaxLifetime.String()
	}
	if document.Relay.Ticket.KeyID == "" {
		document.Relay.Ticket.KeyID = "primary"
	}
	if document.Relay.Ticket.TTL == "" {
		document.Relay.Ticket.TTL = relayticket.DefaultLifetime.String()
	}
	if document.Relay.Registry.Authentication == "" {
		document.Relay.Registry.Authentication = "mtls"
	}
	if document.Relay.Registry.TokenAudience == "" {
		document.Relay.Registry.TokenAudience = "kubeloop-relay"
	}
	if document.Relay.Registry.TrustDomain == "" {
		document.Relay.Registry.TrustDomain = "cluster.local"
	}
	if document.Relay.Registry.LeaseDuration == "" {
		document.Relay.Registry.LeaseDuration = "45s"
	}
	if document.Relay.Registry.HeartbeatAfter == "" {
		document.Relay.Registry.HeartbeatAfter = "10s"
	}
	if document.Relay.Registry.KeyGeneration == 0 {
		document.Relay.Registry.KeyGeneration = 1
	}
	if document.Relay.Registry.KeyValidity == "" {
		document.Relay.Registry.KeyValidity = (365 * 24 * time.Hour).String()
	}
	if document.Maintenance.Interval == "" {
		document.Maintenance.Interval = maintenance.DefaultInterval.String()
	}
	if document.Maintenance.BatchSize == 0 {
		document.Maintenance.BatchSize = maintenance.DefaultBatchSize
	}
	if document.Logging.Level == "" {
		document.Logging.Level = "info"
	}
}

func normalizeKubernetesConfig(document kubernetesConfig) (controlplanekubernetes.Config, error) {
	timeout := time.Duration(0)
	var err error
	if document.Timeout != "" {
		timeout, err = positiveDuration("kubernetes.timeout", document.Timeout)
		if err != nil {
			return controlplanekubernetes.Config{}, err
		}
	}
	return controlplanekubernetes.Normalize(controlplanekubernetes.Config{
		Timeout: timeout, QPS: document.QPS, Burst: document.Burst, UserAgent: document.UserAgent,
		Impersonation: document.Impersonation,
	})
}

func normalizeStorageConfig(document storageConfig) (controlplanestorage.Config, error) {
	config := controlplanestorage.Config{
		SQLitePath: document.SQLite.Path, DatasourceURL: strings.TrimSpace(document.DatasourceURL),
		ControlPlaneReplicas: document.Replicas, MaxOpenConnections: document.MaxOpenConnections,
		MaxIdleConnections: document.MaxIdleConnections, TransactionMaxRetries: document.TransactionMaxRetries,
		AllowInsecureDatasource: document.AllowInsecureDatasource,
	}
	var err error
	for label, input := range map[string]struct {
		raw    string
		target *time.Duration
	}{
		"storage.sqlite.busyTimeout":      {document.SQLite.BusyTimeout, &config.BusyTimeout},
		"storage.connectTimeout":          {document.ConnectTimeout, &config.ConnectTimeout},
		"storage.queryTimeout":            {document.QueryTimeout, &config.QueryTimeout},
		"storage.connectionMaxLifetime":   {document.ConnectionMaxLifetime, &config.ConnectionMaxLifetime},
		"storage.transactionRetryBackoff": {document.TransactionRetryBackoff, &config.TransactionRetryBackoff},
	} {
		if input.raw == "" {
			continue
		}
		*input.target, err = positiveDuration(label, input.raw)
		if err != nil {
			return controlplanestorage.Config{}, err
		}
	}
	if file := strings.TrimSpace(document.DatasourceURLFile); file != "" {
		if config.DatasourceURL != "" {
			return controlplanestorage.Config{}, errors.New("storage datasourceURL and datasourceURLFile are mutually exclusive")
		}
		raw, readErr := os.ReadFile(file)
		if readErr != nil {
			return controlplanestorage.Config{}, errors.New("read storage datasource URL file")
		}
		config.DatasourceURL = strings.TrimSpace(string(raw))
	}
	return controlplanestorage.Normalize(config)
}

func positiveDuration(label, raw string) (time.Duration, error) {
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", label)
	}
	return value, nil
}
