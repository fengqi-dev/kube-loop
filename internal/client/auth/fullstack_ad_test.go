package auth_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	clientauth "github.com/fengqi-dev/kube-loop/internal/client/auth"
	"github.com/fengqi-dev/kube-loop/internal/controller/authn"
	"github.com/fengqi-dev/kube-loop/internal/controller/authn/ad"
	"github.com/fengqi-dev/kube-loop/internal/controller/authn/httpauth"
	"github.com/fengqi-dev/kube-loop/internal/controller/authn/login"
	"github.com/fengqi-dev/kube-loop/internal/controller/authn/token"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	ber "github.com/go-asn1-ber/asn1-ber"
	"github.com/go-ldap/ldap/v3"
)

const (
	adServiceDN = "CN=reader,DC=example,DC=test"
	adUserDN    = "CN=Ada Lovelace,OU=Users,DC=example,DC=test"
)

type ldapsFixture struct {
	listener net.Listener
	address  string
	roots    *x509.CertPool

	serviceBinds atomic.Int32
	userBinds    atomic.Int32
	searches     atomic.Int32
}

func TestFullStackADLoginUsesRealLDAPSAndIssuesTokens(t *testing.T) {
	directory := startLDAPSFixture(t)
	probe, err := ldap.DialURL("ldaps://"+directory.address, ldap.DialWithTLSConfig(&tls.Config{
		MinVersion: tls.VersionTLS12, RootCAs: directory.roots,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := probe.Bind(adUserDN, "wrong-password"); err == nil {
		probe.Close()
		t.Fatal("LDAPS fixture accepted its direct invalid-password probe")
	}
	probe.Close()
	provider, err := ad.New(context.Background(), ad.Config{
		ID: "legacy-ad", DisplayName: "Legacy AD", DirectoryID: "corp.example",
		URL: "ldaps://" + directory.address, BaseDN: "DC=example,DC=test",
		BindDN: adServiceDN, BindPassword: "reader-secret", RootCAs: directory.roots,
		ConnectTimeout: 2 * time.Second, RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := authn.NewRegistry(provider)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "ad-fullstack.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loginService, err := login.New(registry, store, login.Config{})
	if err != nil {
		t.Fatal(err)
	}
	controllerServer := httptest.NewUnstartedServer(nil)
	controllerURL := "https://" + controllerServer.Listener.Addr().String()
	_, signingKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tokenService, err := token.New(store, token.Config{
		Issuer: controllerURL, KeyID: "ad-controller-e2e", SigningKey: signingKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	authHandler, err := httpauth.New(loginService, tokenService)
	if err != nil {
		t.Fatal(err)
	}
	controllerServer.Config.Handler = authHandler
	controllerServer.StartTLS()
	defer controllerServer.Close()

	client := clientauth.New(clientauth.Config{HTTPClient: controllerServer.Client(), RequestTimeout: 5 * time.Second})
	password := []byte("user-password")
	credential, err := client.LoginAD(
		context.Background(), controllerURL, "legacy-ad", "ada", password, "desktop-ad-e2e",
	)
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccessToken == "" || credential.RefreshToken == "" || credential.DeviceID != "desktop-ad-e2e" {
		t.Fatalf("AD credential = %#v", credential)
	}
	for index, value := range password {
		if value != 0 {
			t.Fatalf("password byte %d was not cleared", index)
		}
	}
	principals, err := store.Principals().List(context.Background(), storage.PrincipalListFilter{Limit: 10})
	if err != nil || len(principals) != 1 || principals[0].Provider != "legacy-ad" ||
		principals[0].DisplayName != "Ada Lovelace" || len(principals[0].Groups) != 1 || principals[0].Groups[0] != "Developers" {
		t.Fatalf("AD principals = %#v err=%v", principals, err)
	}
	if directory.serviceBinds.Load() < 2 || directory.userBinds.Load() != 1 || directory.searches.Load() != 1 {
		t.Fatalf("LDAPS operations: service binds=%d user binds=%d searches=%d",
			directory.serviceBinds.Load(), directory.userBinds.Load(), directory.searches.Load())
	}
	wrongPassword := []byte("wrong-password")
	if _, err := client.LoginAD(
		context.Background(), controllerURL, "legacy-ad", "ada", wrongPassword, "desktop-ad-e2e",
	); err == nil {
		t.Fatalf("invalid LDAPS password was accepted: service binds=%d user binds=%d searches=%d",
			directory.serviceBinds.Load(), directory.userBinds.Load(), directory.searches.Load())
	}
	for index, value := range wrongPassword {
		if value != 0 {
			t.Fatalf("rejected password byte %d was not cleared", index)
		}
	}
}

func startLDAPSFixture(t *testing.T) *ldapsFixture {
	t.Helper()
	certificate, roots := newLDAPCertificate(t)
	listener, err := tls.Listen("tcp4", "127.0.0.1:0", &tls.Config{
		MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &ldapsFixture{listener: listener, address: listener.Addr().String(), roots: roots}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go fixture.serve(connection)
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})
	return fixture
}

func (fixture *ldapsFixture) serve(connection net.Conn) {
	defer connection.Close()
	for {
		request, err := ber.ReadPacket(connection)
		if err != nil || len(request.Children) < 2 {
			return
		}
		messageID, ok := request.Children[0].Value.(int64)
		if !ok {
			return
		}
		operation := request.Children[1]
		switch operation.Tag {
		case ldap.ApplicationBindRequest:
			fixture.bind(connection, messageID, operation)
		case ldap.ApplicationSearchRequest:
			fixture.searches.Add(1)
			fixture.search(connection, messageID)
		case ldap.ApplicationUnbindRequest:
			return
		default:
			return
		}
	}
}

func (fixture *ldapsFixture) bind(connection net.Conn, messageID int64, request *ber.Packet) {
	resultCode := int64(ldap.LDAPResultInvalidCredentials)
	if len(request.Children) >= 3 {
		username, _ := request.Children[1].Value.(string)
		password := string(request.Children[2].Data.Bytes())
		switch {
		case username == adServiceDN && password == "reader-secret":
			fixture.serviceBinds.Add(1)
			resultCode = ldap.LDAPResultSuccess
		case username == adUserDN && password == "user-password":
			fixture.userBinds.Add(1)
			resultCode = ldap.LDAPResultSuccess
		}
	}
	response := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ldap.ApplicationBindResponse, nil, "Bind Response")
	appendLDAPResult(response, resultCode)
	writeLDAPResponse(connection, messageID, response)
}

func (fixture *ldapsFixture) search(connection net.Conn, messageID int64) {
	entry := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ldap.ApplicationSearchResultEntry, nil, "Search Result Entry")
	entry.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, adUserDN, "Object Name"))
	attributes := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "Attributes")
	objectGUID := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	appendLDAPAttribute(attributes, "objectGUID", string(objectGUID))
	appendLDAPAttribute(attributes, "displayName", "Ada Lovelace")
	appendLDAPAttribute(attributes, "mail", "ada@example.test")
	appendLDAPAttribute(attributes, "memberOf", "CN=Developers,OU=Groups,DC=example,DC=test")
	appendLDAPAttribute(attributes, "userAccountControl", "512")
	appendLDAPAttribute(attributes, "lockoutTime", "0")
	appendLDAPAttribute(attributes, "accountExpires", "0")
	appendLDAPAttribute(attributes, "pwdLastSet", "133700000000000000")
	entry.AppendChild(attributes)
	writeLDAPResponse(connection, messageID, entry)
	done := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ldap.ApplicationSearchResultDone, nil, "Search Result Done")
	appendLDAPResult(done, ldap.LDAPResultSuccess)
	writeLDAPResponse(connection, messageID, done)
}

func appendLDAPAttribute(attributes *ber.Packet, name string, values ...string) {
	attribute := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "Attribute")
	attribute.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, name, "Attribute Name"))
	set := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "Attribute Values")
	for _, value := range values {
		set.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, value, "Attribute Value"))
	}
	attribute.AppendChild(set)
	attributes.AppendChild(attribute)
}

func appendLDAPResult(response *ber.Packet, resultCode int64) {
	// go-ldap decodes LDAPResult resultCode through its integer path.
	response.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, resultCode, "resultCode"))
	response.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "matchedDN"))
	response.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "diagnosticMessage"))
}

func writeLDAPResponse(connection net.Conn, messageID int64, operation *ber.Packet) {
	response := ber.NewSequence("LDAP Response")
	response.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, messageID, "MessageID"))
	response.AppendChild(operation)
	_, _ = connection.Write(response.Bytes())
}

func newLDAPCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "KubeLoop LDAPS E2E"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true,
		BasicConstraintsValid: true,
	}
	encoded, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: encoded})
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	certificate, err := tls.X509KeyPair(certificatePEM, privatePEM)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificatePEM) {
		t.Fatal("append LDAPS test certificate")
	}
	return certificate, roots
}
