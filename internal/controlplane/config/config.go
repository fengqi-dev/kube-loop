package config

import controlplanekubernetes "github.com/fengqi-dev/kube-loop/internal/controlplane/kubernetes"

type Document struct {
	API            APIConfig            `json:"api"`
	Authentication AuthenticationConfig `json:"authentication"`
	Admin          AdminConfig          `json:"admin"`
	Kubernetes     KubernetesConfig     `json:"kubernetes"`
	Relay          RelayConfig          `json:"relay"`
	Sessions       SessionsConfig       `json:"sessions"`
	Storage        StorageConfig        `json:"storage"`
	Maintenance    MaintenanceConfig    `json:"maintenance"`
	Files          FilesConfig          `json:"files"`
	Logging        LoggingConfig        `json:"logging"`
}

type APIConfig struct {
	Listen              string `json:"listen"`
	PublicURL           string `json:"publicURL"`
	ServiceID           string `json:"serviceID"`
	TunnelPath          string `json:"tunnelPath"`
	MinClientVersion    string `json:"minClientVersion,omitempty"`
	ShutdownTimeout     string `json:"shutdownTimeout"`
	RequestTimeout      string `json:"requestTimeout"`
	MaxRequestBodyBytes int64  `json:"maxRequestBodyBytes"`
}

type AuthenticationConfig struct {
	OAuth OAuthConfig `json:"oauth"`
}

type OAuthConfig struct {
	OIDCSigningKeyFile string `json:"oidcSigningKeyFile"`
	HMACSecretFile     string `json:"hmacSecretFile"`
	KeyID              string `json:"keyID"`
	AccessTTL          string `json:"accessTTL"`
	RefreshTTL         string `json:"refreshTTL"`
}

type AdminConfig struct {
	Bootstrap AdminBootstrapConfig `json:"bootstrap"`
}

type AdminBootstrapConfig struct {
	Enabled      bool   `json:"enabled"`
	Username     string `json:"username"`
	PasswordFile string `json:"passwordFile,omitempty"`
	DisplayName  string `json:"displayName"`
	Email        string `json:"email,omitempty"`
}

type KubernetesConfig struct {
	Timeout       string                                     `json:"timeout"`
	QPS           float32                                    `json:"qps"`
	Burst         int                                        `json:"burst"`
	UserAgent     string                                     `json:"userAgent,omitempty"`
	Impersonation controlplanekubernetes.ImpersonationConfig `json:"impersonation"`
}

type RelayConfig struct {
	Ticket   RelayTicketConfig   `json:"ticket"`
	Registry RelayRegistryConfig `json:"registry"`
}

type RelayTicketConfig struct {
	SigningKeyFile    string `json:"signingKeyFile"`
	KeyID             string `json:"keyID"`
	TTL               string `json:"ttl"`
	TrafficEncryption *bool  `json:"trafficEncryption"`
}

type RelayRegistryConfig struct {
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

type SessionsConfig struct {
	TTL         string `json:"ttl"`
	MaxLifetime string `json:"maxLifetime"`
}

type StorageConfig struct {
	DatasourceURL           string       `json:"datasourceURL,omitempty"`
	DatasourceURLFile       string       `json:"datasourceURLFile,omitempty"`
	Replicas                int          `json:"replicas"`
	SQLite                  SQLiteConfig `json:"sqlite"`
	ConnectTimeout          string       `json:"connectTimeout"`
	QueryTimeout            string       `json:"queryTimeout"`
	MaxOpenConnections      int          `json:"maxOpenConnections"`
	MaxIdleConnections      int          `json:"maxIdleConnections"`
	ConnectionMaxLifetime   string       `json:"connectionMaxLifetime"`
	TransactionMaxRetries   int          `json:"transactionMaxRetries"`
	TransactionRetryBackoff string       `json:"transactionRetryBackoff"`
	AllowInsecureDatasource bool         `json:"allowInsecureDatasource,omitempty"`
}

type SQLiteConfig struct {
	Path        string `json:"path"`
	BusyTimeout string `json:"busyTimeout,omitempty"`
}

type MaintenanceConfig struct {
	Interval  string `json:"interval"`
	BatchSize int    `json:"batchSize"`
}

type FilesConfig struct {
	MaxBytes     uint64   `json:"maxBytes"`
	AllowedRoots []string `json:"allowedRoots"`
}

type LoggingConfig struct {
	Level string `json:"level"`
}

type kubeloopDocument struct {
	ControlPlane *Document `json:"controlPlane"`
	Gateway      any       `json:"gateway"`
}
