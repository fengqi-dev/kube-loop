package oauthserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	controlstorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func TestConsentRequiredUsesTrustAndExactScopeGrant(t *testing.T) {
	endpoints, store, identity := newTestEndpoints(t)
	client := controlstorage.OAuthClient{
		ID: "external-client", Name: "External", Public: true,
		RedirectURIs: []string{"https://client.example/callback"},
		GrantTypes:   []string{"authorization_code"},
		Scopes:       []string{"openid", "profile"}, Enabled: true,
	}
	createTestClient(t, store, client, "")
	challenge := AuthorizationChallenge{Client: client, Scopes: []string{"profile", "openid"}}
	required, err := endpoints.ConsentRequired(t.Context(), challenge, identity.ID)
	if err != nil || !required {
		t.Fatalf("consent required = %t err = %v", required, err)
	}
	now := time.Now().UTC()
	if err := store.OAuthConsents().Grant(t.Context(), controlstorage.OAuthConsent{
		IdentityID: identity.ID,
		ClientID:   client.ID,
		ScopeHash:  exactScopeHash([]string{"openid", "profile"}),
		Scopes:     []string{"openid", "profile"},
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatal(err)
	}
	required, err = endpoints.ConsentRequired(t.Context(), challenge, identity.ID)
	if err != nil || required {
		t.Fatalf("granted consent required = %t err = %v", required, err)
	}
	challenge.Trusted = true
	challenge.Scopes = []string{"ungranted"}
	required, err = endpoints.ConsentRequired(t.Context(), challenge, identity.ID)
	if err != nil || required {
		t.Fatalf("trusted consent required = %t err = %v", required, err)
	}
}

func TestAuthenticateLocalBindsProviderAndRequestContext(t *testing.T) {
	endpoints, _, identity := newTestEndpoints(t)
	type call struct {
		username  string
		password  string
		requestID string
	}
	var received call
	endpoints.SetLocalAuthenticator(func(
		_ context.Context,
		username string,
		password []byte,
		requestID string,
	) (controlstorage.Identity, error) {
		received = call{username: username, password: string(password), requestID: requestID}
		return identity, nil
	})
	before := time.Now().UTC()
	browserIdentity, err := endpoints.AuthenticateLocal(
		t.Context(),
		"administrator",
		[]byte("secret"),
		"request-123",
	)
	if err != nil {
		t.Fatal(err)
	}
	if received != (call{username: "administrator", password: "secret", requestID: "request-123"}) ||
		browserIdentity.Identity.ID != identity.ID || browserIdentity.ProviderID != providerLocal ||
		browserIdentity.AuthTime.Before(before) || browserIdentity.AuthTime.After(time.Now().UTC()) {
		t.Fatalf("received = %#v browser identity = %#v", received, browserIdentity)
	}
}

func TestAuthorizationDenialConsumesChallengeAndReturnsOAuthError(t *testing.T) {
	endpoints, store, _ := newTestEndpoints(t)
	client := controlstorage.OAuthClient{
		ID: "browser-client", Name: "Browser", Public: true,
		RedirectURIs: []string{"https://client.example/callback"},
		GrantTypes:   []string{"authorization_code"},
		Scopes:       []string{"openid"}, Enabled: true,
	}
	createTestClient(t, store, client, "")
	tests := []struct {
		name     string
		code     string
		cancel   bool
		expected string
	}{
		{name: "cancel", cancel: true, expected: "access_denied"},
		{name: "login required", code: "login_required", expected: "login_required"},
		{name: "consent required", code: "consent_required", expected: "consent_required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authorize := testAuthorizationRequest(client)
			challenge, err := endpoints.BeginAuthorization(authorize.Context(), authorize)
			if err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "https://issuer.example/oauth2/login/local", nil)
			if test.cancel {
				err = endpoints.CancelAuthorization(
					recorder,
					request,
					challenge.Transaction,
					challenge.CSRF,
				)
			} else {
				err = endpoints.DenyAuthorization(
					recorder,
					request,
					challenge.Transaction,
					challenge.CSRF,
					test.code,
				)
			}
			if err != nil {
				t.Fatal(err)
			}
			location, err := url.Parse(recorder.Header().Get("Location"))
			if err != nil || location.Query().Get("error") != test.expected {
				t.Fatalf("status = %d location = %q err = %v", recorder.Code, location, err)
			}
			if err := endpoints.DenyAuthorization(
				httptest.NewRecorder(),
				request,
				challenge.Transaction,
				challenge.CSRF,
				test.code,
			); err == nil {
				t.Fatal("consumed authorization challenge was accepted again")
			}
		})
	}
}

func testAuthorizationRequest(client controlstorage.OAuthClient) *http.Request {
	query := url.Values{
		"response_type":         {responseTypeCode},
		"client_id":             {client.ID},
		"redirect_uri":          {client.RedirectURIs[0]},
		"scope":                 {strings.Join(client.Scopes, " ")},
		"state":                 {strings.Repeat("s", 32)},
		"nonce":                 {strings.Repeat("n", 32)},
		"code_challenge":        {strings.Repeat("c", 43)},
		"code_challenge_method": {"S256"},
	}
	return httptest.NewRequest(
		http.MethodGet,
		"https://issuer.example/oauth2/authorize?"+query.Encode(),
		nil,
	)
}

func TestDesktopAuthorizationLocale(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		formLocale   string
		cookieLocale string
		accept       string
		want         string
	}{
		{
			name:         "form overrides saved preference",
			formLocale:   localeChineseSimplified,
			cookieLocale: localeEnglishUS,
			accept:       localeEnglishUS,
			want:         localeChineseSimplified,
		},
		{
			name:         "saved preference overrides browser",
			cookieLocale: localeEnglishUS,
			accept:       "zh-CN,zh;q=0.9",
			want:         localeEnglishUS,
		},
		{
			name:   "browser Chinese",
			accept: "zh-TW,zh;q=0.9,en;q=0.8",
			want:   localeChineseSimplified,
		},
		{
			name:   "browser English",
			accept: "en-US,en;q=0.9",
			want:   localeEnglishUS,
		},
		{name: "default English", want: localeEnglishUS},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(
				http.MethodGet,
				"https://issuer.example/oauth2/authorize",
				nil,
			)
			if test.formLocale != "" {
				form := url.Values{"locale": {test.formLocale}}
				request = httptest.NewRequest(
					http.MethodPost,
					"https://issuer.example/oauth2/login/local",
					strings.NewReader(form.Encode()),
				)
				request.Header.Set(
					"Content-Type",
					"application/x-www-form-urlencoded",
				)
			}
			if test.cookieLocale != "" {
				request.AddCookie(
					&http.Cookie{
						Name:  "kubeloop.locale",
						Value: test.cookieLocale,
					},
				)
			}
			request.Header.Set("Accept-Language", test.accept)
			if got := desktopAuthorizationLocale(request); got != test.want {
				t.Fatalf("locale = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDesktopAuthorizationMessagesFor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		locale    string
		heading   string
		forbidden string
	}{
		{
			name:      "Chinese only",
			locale:    localeChineseSimplified,
			heading:   loginCompleteTitleChinese,
			forbidden: loginCompleteTitle,
		},
		{
			name:      "English only",
			locale:    localeEnglishUS,
			heading:   loginCompleteTitle,
			forbidden: loginCompleteTitleChinese,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			messages := desktopAuthorizationMessagesFor(test.locale)
			content := strings.Join([]string{
				messages.title, messages.badge, messages.heading, messages.description,
				messages.returnToApp, messages.logout, messages.logoutNote,
			}, " ")
			if messages.heading != test.heading ||
				strings.Contains(content, test.forbidden) {
				t.Fatalf("unexpected localized messages: %#v", messages)
			}
		})
	}
}
