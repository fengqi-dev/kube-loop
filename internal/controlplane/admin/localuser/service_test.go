package localuser

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/pquerna/otp/totp"
)

func TestInitialAdminPasswordIsOneTimeBootstrapInput(t *testing.T) {
	service, closeStore := newTestService(t)
	defer closeStore()
	ctx := context.Background()
	created, first, err := service.EnsureInitial(ctx, CreateRequest{Username: "Admin", Password: []byte("first-password-value")})
	if err != nil || !first {
		t.Fatalf("first EnsureInitial = %#v, %v, %v", created, first, err)
	}
	again, second, err := service.EnsureInitial(ctx, CreateRequest{Username: "admin", Password: []byte("second-password-value")})
	if err != nil || second || again.PrincipalID != created.PrincipalID {
		t.Fatalf("second EnsureInitial = %#v, %v, %v", again, second, err)
	}
	if _, err := service.Authenticate(ctx, "admin", []byte("first-password-value"), ""); err != nil {
		t.Fatalf("original password rejected: %v", err)
	}
	if _, err := service.Authenticate(ctx, "admin", []byte("second-password-value"), ""); err == nil {
		t.Fatal("Helm upgrade input replaced the stored password")
	}
}

func TestTOTPEnrollmentAndRecoveryCodeConsumption(t *testing.T) {
	service, closeStore := newTestService(t)
	defer closeStore()
	ctx := context.Background()
	user, err := service.Create(ctx, CreateRequest{Username: "operator", Password: []byte("correct-horse-battery")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, "operator", []byte("correct-horse-battery"), ""); err != nil {
		t.Fatalf("password-only login before MFA enrollment failed: %v", err)
	}
	enrollment, err := service.StartTOTP(ctx, user.PrincipalID)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	recoveryCodes, err := service.ConfirmTOTP(ctx, user.PrincipalID, enrollment.EnrollmentToken, code)
	if err != nil || len(recoveryCodes) != recoveryCodeCount {
		t.Fatalf("ConfirmTOTP = %d recovery codes, %v", len(recoveryCodes), err)
	}
	if _, err := service.Authenticate(ctx, "operator", []byte("correct-horse-battery"), ""); err == nil {
		t.Fatal("password-only login succeeded after MFA enrollment")
	}
	if _, err := service.Authenticate(ctx, "operator", []byte("correct-horse-battery"), recoveryCodes[0]); err != nil {
		t.Fatalf("recovery-code login failed: %v", err)
	}
	if _, err := service.Authenticate(ctx, "operator", []byte("correct-horse-battery"), recoveryCodes[0]); err == nil {
		t.Fatal("recovery code was accepted twice")
	}
}

func newTestService(t *testing.T) (*Service, func()) {
	t.Helper()
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "state.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store, []byte("0123456789abcdef0123456789abcdef"), "KubeLoop Test")
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	return service, func() { _ = store.Close() }
}
