package httpauth

import (
	"net/url"
	"strings"
	"testing"
)

func TestBrowserLoginErrorURL(t *testing.T) {
	t.Parallel()

	returnTo := "?transaction=transaction-1&csrf=csrf-1&client=Management&provider=local%00Local&provider=oidc%00OIDC"
	target := browserLoginErrorURL(url.Values{
		"transaction": {"transaction-1"},
		"csrf":        {"csrf-1"},
		"return_to":   {returnTo},
	}, "authentication_failed")
	if !strings.HasPrefix(target, oauthPath+"/ui/?") {
		t.Fatalf("unexpected redirect target %q", target)
	}
	query, err := url.ParseQuery(strings.TrimPrefix(target, oauthPath+"/ui/?"))
	if err != nil {
		t.Fatalf("parse redirect query: %v", err)
	}
	if got := query.Get("error"); got != "authentication_failed" {
		t.Fatalf("error = %q, want authentication_failed", got)
	}
	if got := query["provider"]; len(got) != 2 {
		t.Fatalf("provider count = %d, want 2", len(got))
	}
}

func TestBrowserLoginErrorURLRejectsTamperedReturnTarget(t *testing.T) {
	t.Parallel()

	target := browserLoginErrorURL(url.Values{
		"transaction": {"transaction-1"},
		"csrf":        {"csrf-1"},
		"return_to":   {"?transaction=other&csrf=csrf-1"},
	}, "authentication_failed")
	if target != "" {
		t.Fatalf("target = %q, want empty", target)
	}
}
