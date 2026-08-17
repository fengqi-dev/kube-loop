//go:build darwin

package supervisor

import "testing"

func TestAuthAuthorize(t *testing.T) {
	t.Parallel()
	token := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	auth, err := NewAuth(token, 501)
	if err != nil {
		t.Fatalf("NewAuth: %v", err)
	}
	if !auth.Authorize(token, 501) {
		t.Fatal("Authorize rejected matching token and UID")
	}
	if auth.Authorize(token, 502) || auth.Authorize(token+"x", 501) {
		t.Fatal("Authorize accepted mismatched credentials")
	}
}
