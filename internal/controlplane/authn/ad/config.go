package ad

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

const (
	defaultConnectTimeout = 5 * time.Second
	defaultRequestTimeout = 5 * time.Second
	defaultMaxGroups      = 200
)

type Config struct {
	ID                          string
	DisplayName                 string
	DirectoryID                 string
	URL                         string
	StartTLS                    bool
	BaseDN                      string
	UserFilter                  string
	BindDN                      string
	BindPassword                string
	ObjectIDAttribute           string
	DisplayNameAttribute        string
	EmailAttribute              string
	GroupsAttribute             string
	UserAccountControlAttribute string
	LockoutTimeAttribute        string
	AccountExpiresAttribute     string
	PasswordLastSetAttribute    string
	GroupNameAttribute          string
	NestedGroupDepth            int
	MaxGroups                   int
	ConnectTimeout              time.Duration
	RequestTimeout              time.Duration
	RootCAs                     *x509.CertPool
	Now                         func() time.Time
	dial                        func(context.Context, Config) (directoryClient, error)
}

func (config Config) normalized() (Config, error) {
	config.ID = strings.TrimSpace(config.ID)
	config.DisplayName = strings.TrimSpace(config.DisplayName)
	config.DirectoryID = strings.TrimSpace(config.DirectoryID)
	config.URL = strings.TrimSpace(config.URL)
	config.BaseDN = strings.TrimSpace(config.BaseDN)
	config.UserFilter = strings.TrimSpace(config.UserFilter)
	config.BindDN = strings.TrimSpace(config.BindDN)
	if config.ID == "" || config.DirectoryID == "" || config.BaseDN == "" {
		return Config{}, errors.New("AD provider ID, directory ID and Base DN are required")
	}
	parsed, err := url.Parse(config.URL)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return Config{}, errors.New("AD URL must be an absolute LDAP URL without credentials, path, query or fragment")
	}
	switch parsed.Scheme {
	case "ldaps":
		if config.StartTLS {
			return Config{}, errors.New("StartTLS must not be enabled for an LDAPS URL")
		}
	case "ldap":
		if !config.StartTLS {
			return Config{}, errors.New("plain LDAP is forbidden; enable StartTLS or use LDAPS")
		}
	default:
		return Config{}, errors.New("AD URL scheme must be ldaps or ldap with StartTLS")
	}
	if net.ParseIP(parsed.Hostname()) != nil && config.RootCAs == nil {
		// IP certificates can be valid, but public trust roots almost never issue
		// them. Requiring an explicit CA avoids accidental endpoint substitution.
		return Config{}, errors.New("AD IP endpoints require an explicit CA bundle")
	}
	if config.UserFilter == "" {
		config.UserFilter = "(&(objectClass=user)(sAMAccountName={username}))"
	}
	if strings.Count(config.UserFilter, "{username}") != 1 || len(config.UserFilter) > 2048 {
		return Config{}, errors.New("AD user filter must contain exactly one {username} placeholder")
	}
	if (config.BindDN == "") != (config.BindPassword == "") {
		return Config{}, errors.New("AD Bind DN and password must be configured together")
	}
	config.ObjectIDAttribute = defaultValue(config.ObjectIDAttribute, "objectGUID")
	if config.ObjectIDAttribute != "objectGUID" && config.ObjectIDAttribute != "objectSid" {
		return Config{}, errors.New("AD object ID attribute must be objectGUID or objectSid")
	}
	config.DisplayNameAttribute = defaultValue(config.DisplayNameAttribute, "displayName")
	config.EmailAttribute = defaultValue(config.EmailAttribute, "mail")
	config.GroupsAttribute = defaultValue(config.GroupsAttribute, "memberOf")
	config.UserAccountControlAttribute = defaultValue(config.UserAccountControlAttribute, "userAccountControl")
	config.LockoutTimeAttribute = defaultValue(config.LockoutTimeAttribute, "lockoutTime")
	config.AccountExpiresAttribute = defaultValue(config.AccountExpiresAttribute, "accountExpires")
	config.PasswordLastSetAttribute = defaultValue(config.PasswordLastSetAttribute, "pwdLastSet")
	config.GroupNameAttribute = defaultValue(config.GroupNameAttribute, "cn")
	if config.NestedGroupDepth < 0 || config.NestedGroupDepth > 5 {
		return Config{}, errors.New("AD nested group depth must be between 0 and 5")
	}
	if config.MaxGroups <= 0 {
		config.MaxGroups = defaultMaxGroups
	}
	if config.MaxGroups > 1000 {
		return Config{}, errors.New("AD maximum groups must not exceed 1000")
	}
	if config.ConnectTimeout <= 0 {
		config.ConnectTimeout = defaultConnectTimeout
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = defaultRequestTimeout
	}
	if config.ConnectTimeout > 30*time.Second || config.RequestTimeout > 30*time.Second {
		return Config{}, errors.New("AD timeout must not exceed 30 seconds")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.dial == nil {
		config.dial = dialDirectory
	}
	return config, nil
}

func dialDirectory(_ context.Context, config Config) (directoryClient, error) {
	parsed, err := url.Parse(config.URL)
	if err != nil {
		return nil, errors.New("parse AD URL")
	}
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: parsed.Hostname(),
		RootCAs:    config.RootCAs,
	}
	connection, err := ldap.DialURL(config.URL,
		ldap.DialWithDialer(&net.Dialer{Timeout: config.ConnectTimeout}),
		ldap.DialWithTLSConfig(tlsConfig),
	)
	if err != nil {
		return nil, errors.New("connect to AD directory")
	}
	connection.SetTimeout(config.RequestTimeout)
	if config.StartTLS {
		if err := connection.StartTLS(tlsConfig); err != nil {
			connection.Close()
			return nil, errors.New("establish AD StartTLS")
		}
	}
	return &ldapConnection{connection: connection}, nil
}

func defaultValue(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

func (config Config) attributes() []string {
	return []string{
		config.ObjectIDAttribute, config.DisplayNameAttribute, config.EmailAttribute,
		config.GroupsAttribute, config.UserAccountControlAttribute,
		config.LockoutTimeAttribute, config.AccountExpiresAttribute,
		config.PasswordLastSetAttribute,
	}
}

func (config Config) userSearchFilter(username string) string {
	return strings.Replace(config.UserFilter, "{username}", ldap.EscapeFilter(username), 1)
}

func (config Config) String() string {
	return fmt.Sprintf("AD provider %s", config.ID)
}
