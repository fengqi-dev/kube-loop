package relayregistry

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
	"strings"
)

type ServerTLSConfig struct {
	CertificateFile          string
	PrivateKeyFile           string
	ClientCAFile             string
	RequireClientCertificate bool
}

func LoadServerTLS(config ServerTLSConfig) (*tls.Config, error) {
	config.CertificateFile = strings.TrimSpace(config.CertificateFile)
	config.PrivateKeyFile = strings.TrimSpace(config.PrivateKeyFile)
	config.ClientCAFile = strings.TrimSpace(config.ClientCAFile)
	if config.CertificateFile == "" || config.PrivateKeyFile == "" ||
		(config.RequireClientCertificate && config.ClientCAFile == "") {
		return nil, errors.New(
			"relay registry server certificate configuration is incomplete",
		)
	}
	certificate, err := tls.LoadX509KeyPair(
		config.CertificateFile,
		config.PrivateKeyFile,
	)
	if err != nil {
		return nil, errors.New("load Relay Registry server certificate")
	}
	var clientCAs *x509.CertPool
	clientAuth := tls.NoClientCert
	if config.RequireClientCertificate {
		caPEM, err := os.ReadFile(config.ClientCAFile)
		if err != nil {
			return nil, errors.New("read Relay Registry client CA")
		}
		clientCAs = x509.NewCertPool()
		if !clientCAs.AppendCertsFromPEM(caPEM) {
			return nil, errors.New("parse Relay Registry client CA")
		}
		clientAuth = tls.RequireAndVerifyClientCert
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		ClientAuth:   clientAuth,
		ClientCAs:    clientCAs,
		NextProtos:   []string{"h2", "http/1.1"},
	}, nil
}
