package ad

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
	"github.com/go-ldap/ldap/v3"
)

const (
	accountDisabledFlag  = 0x0002
	accountLockoutFlag   = 0x0010
	passwordExpiredFlag  = 0x800000
	windowsUnixEpochDiff = int64(11644473600)
)

var (
	ErrInvalidCredentials   = errors.New("invalid directory credentials")
	ErrAccountUnavailable   = errors.New("directory account is unavailable")
	ErrDirectoryUnavailable = errors.New("directory service is unavailable")
)

type directoryClient interface {
	Bind(username, password string) error
	Search(*ldap.SearchRequest) (*ldap.SearchResult, error)
	Close()
}

type ldapConnection struct {
	connection *ldap.Conn
}

func (connection *ldapConnection) Bind(username, password string) error {
	return connection.connection.Bind(username, password)
}
func (connection *ldapConnection) Search(request *ldap.SearchRequest) (*ldap.SearchResult, error) {
	return connection.connection.Search(request)
}
func (connection *ldapConnection) Close() { connection.connection.Close() }

type Provider struct {
	config Config
}

var _ authn.PasswordProvider = (*Provider)(nil)

func New(ctx context.Context, config Config) (*Provider, error) {
	normalized, err := config.normalized()
	if err != nil {
		return nil, err
	}
	provider := &Provider{config: normalized}
	connection, err := provider.connectAndServiceBind(ctx)
	if err != nil {
		return nil, err
	}
	connection.Close()
	return provider, nil
}

func (provider *Provider) Descriptor() authn.Descriptor {
	return authn.Descriptor{
		ID: provider.config.ID, Type: authn.ProviderAD,
		DisplayName: provider.config.DisplayName, Interaction: authn.InteractionPassword,
	}
}

// Check is intentionally local after New has performed a fail-closed startup
// connection. Readiness must not amplify a directory outage with a new LDAP
// bind on every probe; actual login requests retain strict timeouts.
func (provider *Provider) Check(context.Context) error {
	if provider == nil || provider.config.dial == nil {
		return ErrDirectoryUnavailable
	}
	return nil
}

func (provider *Provider) AuthenticatePassword(
	ctx context.Context,
	credentials authn.PasswordCredentials,
) (authn.Identity, error) {
	username := strings.TrimSpace(credentials.Username)
	if username == "" || len(username) > 256 || len(credentials.Password) == 0 || len(credentials.Password) > 4096 {
		zero(credentials.Password)
		return authn.Identity{}, ErrInvalidCredentials
	}
	defer zero(credentials.Password)

	searchConnection, err := provider.connectAndServiceBind(ctx)
	if err != nil {
		return authn.Identity{}, ErrDirectoryUnavailable
	}
	defer searchConnection.Close()
	request := ldap.NewSearchRequest(
		provider.config.BaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
		2, durationSeconds(provider.config.RequestTimeout), false,
		provider.config.userSearchFilter(username), provider.config.attributes(), nil,
	)
	result, err := searchConnection.Search(request)
	if err != nil {
		return authn.Identity{}, ErrDirectoryUnavailable
	}
	if len(result.Entries) != 1 {
		return authn.Identity{}, ErrInvalidCredentials
	}
	entry := result.Entries[0]
	if err := provider.validateAccount(entry); err != nil {
		return authn.Identity{}, err
	}
	objectID, err := immutableObjectID(entry, provider.config.ObjectIDAttribute)
	if err != nil {
		return authn.Identity{}, ErrAccountUnavailable
	}

	// Authenticate on a fresh TLS connection so the service-bind connection is
	// never left carrying user credentials or authorization state.
	userConnection, err := provider.config.dial(ctx, provider.config)
	if err != nil {
		return authn.Identity{}, ErrDirectoryUnavailable
	}
	defer userConnection.Close()
	password := string(credentials.Password)
	if err := userConnection.Bind(entry.DN, password); err != nil {
		return authn.Identity{}, ErrInvalidCredentials
	}
	groups, err := provider.expandGroups(searchConnection, entry.GetAttributeValues(provider.config.GroupsAttribute))
	if err != nil {
		return authn.Identity{}, ErrDirectoryUnavailable
	}
	return authn.Identity{
		ProviderID:  provider.config.ID,
		DirectoryID: provider.config.DirectoryID,
		ObjectID:    objectID,
		DisplayName: strings.TrimSpace(entry.GetAttributeValue(provider.config.DisplayNameAttribute)),
		Email:       strings.TrimSpace(entry.GetAttributeValue(provider.config.EmailAttribute)),
		Groups:      groups,
	}, nil
}

func (provider *Provider) connectAndServiceBind(ctx context.Context) (directoryClient, error) {
	connection, err := provider.config.dial(ctx, provider.config)
	if err != nil {
		return nil, ErrDirectoryUnavailable
	}
	if provider.config.BindDN != "" {
		if err := connection.Bind(provider.config.BindDN, provider.config.BindPassword); err != nil {
			connection.Close()
			return nil, ErrDirectoryUnavailable
		}
	}
	return connection, nil
}

func (provider *Provider) validateAccount(entry *ldap.Entry) error {
	controlRaw := strings.TrimSpace(entry.GetAttributeValue(provider.config.UserAccountControlAttribute))
	control, err := strconv.ParseUint(controlRaw, 10, 32)
	if err != nil {
		return ErrAccountUnavailable
	}
	if control&(accountDisabledFlag|accountLockoutFlag|passwordExpiredFlag) != 0 {
		return ErrAccountUnavailable
	}
	lockoutRaw := strings.TrimSpace(entry.GetAttributeValue(provider.config.LockoutTimeAttribute))
	if lockoutRaw != "" {
		lockout, err := strconv.ParseInt(lockoutRaw, 10, 64)
		if err != nil || lockout < 0 {
			return ErrAccountUnavailable
		}
		if lockout > 0 {
			return ErrAccountUnavailable
		}
	}
	passwordLastSet, err := strconv.ParseInt(strings.TrimSpace(entry.GetAttributeValue(provider.config.PasswordLastSetAttribute)), 10, 64)
	if err != nil || passwordLastSet <= 0 {
		return ErrAccountUnavailable
	}
	expiresRaw := strings.TrimSpace(entry.GetAttributeValue(provider.config.AccountExpiresAttribute))
	if expiresRaw != "" && expiresRaw != "0" && expiresRaw != "9223372036854775807" {
		fileTime, err := strconv.ParseInt(expiresRaw, 10, 64)
		if err != nil || fileTime <= 0 {
			return ErrAccountUnavailable
		}
		seconds := fileTime/10_000_000 - windowsUnixEpochDiff
		if !time.Unix(seconds, 0).After(provider.config.Now()) {
			return ErrAccountUnavailable
		}
	}
	return nil
}

func (provider *Provider) expandGroups(connection directoryClient, directDNs []string) ([]string, error) {
	type queuedGroup struct {
		dn    string
		depth int
	}
	queue := make([]queuedGroup, 0, len(directDNs))
	for _, dn := range directDNs {
		if strings.TrimSpace(dn) != "" {
			queue = append(queue, queuedGroup{dn: strings.TrimSpace(dn)})
		}
	}
	seenDN := make(map[string]struct{}, len(queue))
	seenName := make(map[string]struct{}, len(queue))
	groups := make([]string, 0, len(queue))
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		key := strings.ToLower(current.dn)
		if _, exists := seenDN[key]; exists {
			continue
		}
		seenDN[key] = struct{}{}
		name := groupNameFromDN(current.dn)
		var parents []string
		if current.depth < provider.config.NestedGroupDepth {
			result, err := connection.Search(ldap.NewSearchRequest(
				current.dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases,
				1, durationSeconds(provider.config.RequestTimeout), false,
				"(objectClass=group)",
				[]string{provider.config.GroupNameAttribute, provider.config.GroupsAttribute}, nil,
			))
			if err != nil || len(result.Entries) != 1 {
				return nil, ErrDirectoryUnavailable
			}
			if configuredName := strings.TrimSpace(result.Entries[0].GetAttributeValue(provider.config.GroupNameAttribute)); configuredName != "" {
				name = configuredName
			}
			parents = result.Entries[0].GetAttributeValues(provider.config.GroupsAttribute)
		}
		if name != "" {
			nameKey := strings.ToLower(name)
			if _, exists := seenName[nameKey]; !exists {
				seenName[nameKey] = struct{}{}
				groups = append(groups, name)
				if len(groups) > provider.config.MaxGroups {
					return nil, ErrAccountUnavailable
				}
			}
		}
		for _, parent := range parents {
			queue = append(queue, queuedGroup{dn: parent, depth: current.depth + 1})
		}
	}
	return groups, nil
}

func immutableObjectID(entry *ldap.Entry, attribute string) (string, error) {
	raw := entry.GetRawAttributeValue(attribute)
	if len(raw) == 0 {
		return "", errors.New("missing immutable directory object ID")
	}
	if attribute == "objectGUID" {
		if len(raw) != 16 {
			return "", errors.New("invalid objectGUID")
		}
		return fmt.Sprintf("%08x-%04x-%04x-%02x%02x-%x",
			binary.LittleEndian.Uint32(raw[0:4]),
			binary.LittleEndian.Uint16(raw[4:6]),
			binary.LittleEndian.Uint16(raw[6:8]),
			raw[8], raw[9], raw[10:16],
		), nil
	}
	return sidString(raw)
}

func sidString(raw []byte) (string, error) {
	if len(raw) < 8 {
		return "", errors.New("invalid objectSid")
	}
	count := int(raw[1])
	if len(raw) != 8+4*count {
		return "", errors.New("invalid objectSid")
	}
	authority := uint64(0)
	for _, value := range raw[2:8] {
		authority = authority<<8 | uint64(value)
	}
	var builder strings.Builder
	_, _ = fmt.Fprintf(&builder, "S-%d-%d", raw[0], authority)
	for index := range count {
		_, _ = fmt.Fprintf(&builder, "-%d", binary.LittleEndian.Uint32(raw[8+index*4:12+index*4]))
	}
	return builder.String(), nil
}

func groupNameFromDN(value string) string {
	dn, err := ldap.ParseDN(value)
	if err != nil {
		return ""
	}
	for _, rdn := range dn.RDNs {
		for _, attribute := range rdn.Attributes {
			if strings.EqualFold(attribute.Type, "CN") {
				return strings.TrimSpace(attribute.Value)
			}
		}
	}
	return ""
}

func durationSeconds(value time.Duration) int {
	seconds := int(value.Round(time.Second) / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
