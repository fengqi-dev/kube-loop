package relayagent

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type ClientTLSConfig struct {
	CertificateFile string
	PrivateKeyFile  string
	ServerCAFile    string
	ServerName      string
}

func NewHTTPClient(config ClientTLSConfig) (*http.Client, error) {
	certificateFile := strings.TrimSpace(config.CertificateFile)
	privateKeyFile := strings.TrimSpace(config.PrivateKeyFile)
	if (certificateFile == "") != (privateKeyFile == "") {
		return nil, errors.New("relay agent client certificate and private key must be configured together")
	}
	var certificates []tls.Certificate
	if certificateFile != "" {
		certificate, err := tls.LoadX509KeyPair(certificateFile, privateKeyFile)
		if err != nil {
			return nil, errors.New("load Relay Agent client certificate")
		}
		certificates = []tls.Certificate{certificate}
	}
	caPEM, err := os.ReadFile(strings.TrimSpace(config.ServerCAFile))
	if err != nil {
		return nil, errors.New("read Relay Agent server CA")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("parse Relay Agent server CA")
	}
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("default HTTP transport is not configurable")
	}
	transport := defaultTransport.Clone()
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: certificates,
		RootCAs: roots, ServerName: strings.TrimSpace(config.ServerName),
	}
	return &http.Client{Transport: transport, Timeout: 15 * time.Second}, nil
}

func readBearerToken(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("read Relay Agent bearer token")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	closeErr := file.Close()
	token := strings.TrimSpace(string(raw))
	if err != nil || closeErr != nil || len(raw) == 0 || len(raw) > 16<<10 ||
		token == "" || strings.ContainsAny(token, "\r\n \t") {
		return "", errors.New("relay agent bearer token is invalid")
	}
	return token, nil
}
