package authorization

import "testing"

func TestAuthenticatedAuthorizeRequiresNonBlankSubject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		subject string
		allowed bool
	}{
		{name: "empty subject", subject: ""},
		{name: "blank subject", subject: " \t\n"},
		{name: "authenticated subject", subject: "identity-1", allowed: true},
	}
	authorizer := NewAuthenticated()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			decision := authorizer.Authorize(
				t.Context(),
				Subject{ID: test.subject},
				Request{},
			)
			if decision.Allowed != test.allowed {
				t.Fatalf("allowed = %t, want %t", decision.Allowed, test.allowed)
			}
		})
	}
}
