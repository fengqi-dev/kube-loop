package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
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
	Admin          adminConfig          `json:"admin"`
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
	OAuth oauthConfig `json:"oauth"`
}

type oauthConfig struct {
	OIDCSigningKeyFile string `json:"oidcSigningKeyFile"`
	HMACSecretFile     string `json:"hmacSecretFile"`
	KeyID              string `json:"keyID"`
	AccessTTL          string `json:"accessTTL"`
	RefreshTTL         string `json:"refreshTTL"`
}

type adminConfig struct {
	Listen    string               `json:"listen"`
	PublicURL string               `json:"publicURL"`
	Bootstrap adminBootstrapConfig `json:"bootstrap"`
}

type adminBootstrapConfig struct {
	Enabled      bool   `json:"enabled"`
	Username     string `json:"username"`
	PasswordFile string `json:"passwordFile,omitempty"`
	DisplayName  string `json:"displayName"`
	Email        string `json:"email,omitempty"`
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

type kubeloopConfigDocument struct {
	ControlPlane *controlPlaneConfigDocument `json:"controlPlane"`
	Gateway      any                         `json:"gateway"`
}

type loadedControlPlaneConfig struct {
	Document            controlPlaneConfigDocument
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
	var root kubeloopConfigDocument
	if err := yaml.UnmarshalStrict(raw, &root); err != nil {
		return loadedControlPlaneConfig{}, fmt.Errorf("decode Control Plane YAML: %w", err)
	}
	if root.ControlPlane == nil || root.Gateway == nil {
		return loadedControlPlaneConfig{}, errors.New("unified configuration requires controlPlane and gateway")
	}
	return normalizeControlPlaneConfig(*root.ControlPlane)
}

func normalizeControlPlaneConfig(document controlPlaneConfigDocument) (loadedControlPlaneConfig, error) {
	applyControlPlaneDefaults(&document)
	result := loadedControlPlaneConfig{Document: document}
	var err error
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
		"authentication.oauth.accessTTL":  {document.Authentication.OAuth.AccessTTL, &result.AccessTokenTTL},
		"authentication.oauth.refreshTTL": {document.Authentication.OAuth.RefreshTTL, &result.RefreshTokenTTL},
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
	if strings.TrimSpace(document.Authentication.OAuth.OIDCSigningKeyFile) == "" ||
		strings.TrimSpace(document.Authentication.OAuth.HMACSecretFile) == "" {
		return loadedControlPlaneConfig{}, errors.New("authentication OAuth key files are required")
	}
	if document.Admin.Bootstrap.Enabled &&
		(strings.TrimSpace(document.Admin.Bootstrap.Username) == "" ||
			strings.TrimSpace(document.Admin.Bootstrap.DisplayName) == "") {
		return loadedControlPlaneConfig{}, errors.New("admin bootstrap identity and organization are required")
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
	if document.Admin.Listen == "" {
		document.Admin.Listen = ":8081"
	}
	if document.Admin.PublicURL == "" {
		document.Admin.PublicURL = "http://127.0.0.1:8081"
	}
	if document.Admin.Bootstrap.Username == "" {
		document.Admin.Bootstrap.Username = "admin"
	}
	if document.Admin.Bootstrap.DisplayName == "" {
		document.Admin.Bootstrap.DisplayName = "KubeLoop Administrator"
	}
	if document.Authentication.OAuth.KeyID == "" {
		document.Authentication.OAuth.KeyID = "primary"
	}
	if document.Authentication.OAuth.AccessTTL == "" {
		document.Authentication.OAuth.AccessTTL = (5 * time.Minute).String()
	}
	if document.Authentication.OAuth.RefreshTTL == "" {
		document.Authentication.OAuth.RefreshTTL = (30 * 24 * time.Hour).String()
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
