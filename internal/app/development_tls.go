package app

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
)

func developmentGatewayHTTPClient(embeddedFiles fs.FS) (*http.Client, *tls.Config, error) {
	if embeddedFiles == nil {
		return nil, nil, errors.New("embedded files are unavailable")
	}
	certificate, err := fs.ReadFile(
		embeddedFiles,
		path.Join("build", "embedded", embeddedDevelopmentCA),
	)
	if err != nil {
		return nil, nil, errors.New("read embedded development CA")
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(certificate) {
		return nil, nil, errors.New("parse embedded development CA")
	}
	tlsConfig := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, nil, fmt.Errorf("default HTTP transport %T is unsupported", http.DefaultTransport)
	}
	transport := defaultTransport.Clone()
	transport.TLSClientConfig = tlsConfig.Clone()
	return &http.Client{Transport: transport}, tlsConfig, nil
}
