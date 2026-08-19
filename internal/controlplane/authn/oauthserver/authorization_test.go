package oauthserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDesktopAuthorizationLocale(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		formLocale   string
		cookieLocale string
		accept       string
		want         string
	}{
		{name: "form overrides saved preference", formLocale: "zh-CN", cookieLocale: "en-US", accept: "en-US", want: "zh-CN"},
		{name: "saved preference overrides browser", cookieLocale: "en-US", accept: "zh-CN,zh;q=0.9", want: "en-US"},
		{name: "browser Chinese", accept: "zh-TW,zh;q=0.9,en;q=0.8", want: "zh-CN"},
		{name: "browser English", accept: "en-US,en;q=0.9", want: "en-US"},
		{name: "default English", want: "en-US"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "https://issuer.example/oauth2/authorize", nil)
			if test.formLocale != "" {
				form := url.Values{"locale": {test.formLocale}}
				request = httptest.NewRequest(http.MethodPost, "https://issuer.example/oauth2/login/local", strings.NewReader(form.Encode()))
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			if test.cookieLocale != "" {
				request.AddCookie(&http.Cookie{Name: "kubeloop.locale", Value: test.cookieLocale})
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
		{name: "Chinese only", locale: "zh-CN", heading: "登录完成", forbidden: "Login complete"},
		{name: "English only", locale: "en-US", heading: "Login complete", forbidden: "登录完成"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			messages := desktopAuthorizationMessagesFor(test.locale)
			content := strings.Join([]string{
				messages.title, messages.badge, messages.heading, messages.description,
				messages.returnToApp, messages.logout, messages.logoutNote,
			}, " ")
			if messages.heading != test.heading || strings.Contains(content, test.forbidden) {
				t.Fatalf("unexpected localized messages: %#v", messages)
			}
		})
	}
}
