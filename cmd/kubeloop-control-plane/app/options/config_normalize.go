package options

import (
	"errors"
	"fmt"
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
)

type Config struct {
	Document            Document
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

func normalize(document Document) (Config, error) {
	applyDefaults(&document)
	result := Config{Document: document}
	var err error
	result.Kubernetes, err = normalizeKubernetesConfig(document.Kubernetes)
	if err != nil {
		return Config{}, err
	}
	result.Storage, err = normalizeStorageConfig(document.Storage)
	if err != nil {
		return Config{}, err
	}
	result.Files = fileapi.Config{
		MaximumBytes:     document.Files.MaxBytes,
		AllowedPathRoots: append([]string(nil), document.Files.AllowedRoots...),
	}
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
			return Config{}, err
		}
	}
	if strings.TrimSpace(document.Authentication.OAuth.OIDCSigningKeyFile) == "" ||
		strings.TrimSpace(document.Authentication.OAuth.HMACSecretFile) == "" {
		return Config{}, errors.New("authentication OAuth key files are required")
	}
	if document.Admin.Bootstrap.Enabled &&
		(strings.TrimSpace(document.Admin.Bootstrap.Username) == "" ||
			strings.TrimSpace(document.Admin.Bootstrap.DisplayName) == "") {
		return Config{}, errors.New("admin bootstrap identity and organization are required")
	}
	return result, nil
}

func applyDefaults(document *Document) {
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
		document.Authentication.OAuth.AccessTTL = (24 * time.Hour).String()
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

func normalizeKubernetesConfig(document KubernetesConfig) (controlplanekubernetes.Config, error) {
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

func normalizeStorageConfig(document StorageConfig) (controlplanestorage.Config, error) {
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
			return controlplanestorage.Config{}, errors.New(
				"storage datasourceURL and datasourceURLFile are mutually exclusive",
			)
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
