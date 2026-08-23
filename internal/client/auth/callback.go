package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"errors"
)

func (client *Client) beginCallback(state string) (*pendingCallback, error) {
	client.callbackMu.Lock()
	defer client.callbackMu.Unlock()
	if client.pendingCallback != nil {
		return nil, errors.New("browser login is already in progress")
	}
	pending := &pendingCallback{state: state, result: make(chan callbackResult, 1)}
	client.pendingCallback = pending
	return pending, nil
}

func (client *Client) endCallback(pending *pendingCallback) {
	client.callbackMu.Lock()
	defer client.callbackMu.Unlock()
	if client.pendingCallback == pending {
		client.pendingCallback = nil
	}
}

func (client *Client) startLoopbackCallback(ctx context.Context) (string, func(), error) {
	redirect, err := url.Parse(client.redirectURI)
	if err != nil || redirect.Scheme != "http" || redirect.Hostname() != "127.0.0.1" ||
		redirect.Port() != "" || redirect.Path != "/callback" || redirect.User != nil ||
		redirect.RawQuery != "" || redirect.Fragment != "" {
		return "", nil, errors.New("loopback login redirect URI is invalid")
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", net.JoinHostPort(redirect.Hostname(), "0"))
	if err != nil {
		return "", nil, fmt.Errorf("listen for browser login callback: %w", err)
	}
	actualRedirect := *redirect
	actualRedirect.Host = listener.Addr().String()
	mux := http.NewServeMux()
	mux.HandleFunc(redirect.Path, func(rw http.ResponseWriter, request *http.Request) {
		rw.Header().Set("Cache-Control", "no-store")
		rw.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		if request.Method != http.MethodGet || request.Host != actualRedirect.Host {
			http.Error(rw, "Invalid login callback.", http.StatusBadRequest)
			return
		}
		callbackURL := actualRedirect
		callbackURL.RawQuery = request.URL.RawQuery
		if err := client.HandleCallbackURL(callbackURL.String()); err != nil {
			http.Error(rw, "Login callback was rejected. Return to the terminal and try again.", http.StatusBadRequest)
			return
		}
		const loginCompleteHTML = "<!doctype html><title>KubeLoop login complete</title>" +
			"<style>body{font-family:sans-serif;max-width:40rem;margin:15vh auto;padding:2rem}" +
			"h1{color:#087f5b}</style><h1>Login complete</h1>" +
			"<p>You can close this window and return to KubeLoop TUI.</p>"
		_, _ = io.WriteString(rw, loginCompleteHTML)
	})
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       5 * time.Second,
	}
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = server.Serve(listener)
	}()
	var stopOnce sync.Once
	return actualRedirect.String(), func() {
		stopOnce.Do(func() {
			_ = listener.Close()
			_ = server.Close()
			<-serveDone
		})
	}, nil
}

// HandleCallbackURL completes the active browser login from the desktop URL
// protocol handler or the TUI loopback listener. Invalid or stale URLs never
// consume the pending login.
func (client *Client) HandleCallbackURL(rawURL string) error {
	callbackURL, err := url.Parse(rawURL)
	redirectURL, redirectErr := url.Parse(client.redirectURI)
	if err != nil || redirectErr != nil || !client.matchesCallbackTarget(callbackURL, redirectURL) {
		return errors.New("login callback URL is invalid")
	}
	query := callbackURL.Query()
	states := query["state"]
	if len(states) != 1 {
		return errors.New("login callback state is invalid")
	}

	client.callbackMu.Lock()
	defer client.callbackMu.Unlock()
	pending := client.pendingCallback
	if pending == nil {
		return errors.New("no browser login is in progress")
	}
	state := states[0]
	if len(state) != len(pending.state) || subtle.ConstantTimeCompare([]byte(state), []byte(pending.state)) != 1 {
		return errors.New("login callback state is invalid")
	}
	if pending.delivered {
		return errors.New("login callback was already consumed")
	}

	var result callbackResult
	callbackErrors := query["error"]
	if len(callbackErrors) > 1 {
		return errors.New("login callback error is invalid")
	}
	if len(callbackErrors) == 1 && callbackErrors[0] != "" {
		result.err = errors.New("identity provider rejected the login request")
	} else {
		codes := query["code"]
		if len(codes) != 1 || len(codes[0]) < 32 || len(codes[0]) > 512 {
			return errors.New("login callback code is invalid")
		}
		result.code = codes[0]
	}
	pending.result <- result
	pending.delivered = true
	return nil
}

func (client *Client) matchesCallbackTarget(callbackURL, redirectURL *url.URL) bool {
	if callbackURL == nil || redirectURL == nil || callbackURL.User != nil || callbackURL.Fragment != "" ||
		callbackURL.Scheme != redirectURL.Scheme || callbackURL.Path != redirectURL.Path {
		return false
	}
	if callbackURL.Host == redirectURL.Host {
		return true
	}
	validLoopback := client.loopbackCallback && callbackURL.Scheme == "http" && callbackURL.Port() != ""
	sameHost := callbackURL.Hostname() == "127.0.0.1" && redirectURL.Hostname() == callbackURL.Hostname()
	return validLoopback && sameHost && redirectURL.Port() == ""
}
