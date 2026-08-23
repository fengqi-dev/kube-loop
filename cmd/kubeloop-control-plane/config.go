package main

import controlplanekubernetes "github.com/fengqi-dev/kube-loop/internal/controlplane/kubernetes"

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
