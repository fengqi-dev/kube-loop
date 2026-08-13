package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	adminsession "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/session"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func FuzzManagementEntryBoundedRedactedAndFailClosed(f *testing.F) {
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(f.TempDir(), "management-fuzz.db"),
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { _ = store.Close() })
	sessions, err := adminsession.New(store)
	if err != nil {
		f.Fatal(err)
	}
	handler, err := New(Config{
		PublicURL: "https://gateway.example", MaxRequestBodyBytes: 512,
		GlobalAttempts: 100000, SourceAttempts: 100000,
	}, sessions)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(`{"credential":"valid"}`, "application/json", "https://gateway.example", "same-origin", false)
	f.Add(`{"credential":"wrong","unknown":true}`, "text/plain", "https://attacker.example", "cross-site", true)
	f.Fuzz(func(t *testing.T, body, contentType, origin, fetchSite string, authorization bool) {
		if len(body) > 4096 || len(contentType) > 256 || len(origin) > 512 || len(fetchSite) > 64 {
			t.Skip()
		}
		const secret = "management-fuzz-secret-marker"
		request := httptest.NewRequest(http.MethodPost, "/sessions/break-glass", strings.NewReader(body+secret))
		request.RemoteAddr = "192.0.2.20:43210"
		request.Header.Set("Content-Type", contentType)
		request.Header.Set("Origin", origin)
		request.Header.Set("Sec-Fetch-Site", fetchSite)
		if authorization {
			request.Header.Set("Authorization", "Bearer "+secret)
		}
		response := httptest.NewRecorder()
		serveHTTP(handler, response, request)
		if response.Body.Len() > 16<<10 {
			t.Fatalf("management response exceeded bound: %d", response.Body.Len())
		}
		if strings.Contains(response.Body.String(), secret) || strings.Contains(response.Body.String(), body) && len(body) > 32 {
			t.Fatal("management request content leaked into response")
		}
		if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("management security headers were omitted")
		}
	})
}
