package main

import (
	"errors"
	"testing"
)

type testAuthCallbackHandler struct {
	err    error
	called []string
}

func (handler *testAuthCallbackHandler) HandleAuthCallbackURL(rawURL string) error {
	handler.called = append(handler.called, rawURL)
	return handler.err
}

func TestDeliverAuthCallback(t *testing.T) {
	tests := []struct {
		name    string
		handler authCallbackHandler
		want    bool
	}{
		{name: "accepted", handler: &testAuthCallbackHandler{}, want: true},
		{name: "rejected", handler: &testAuthCallbackHandler{err: errors.New("invalid callback")}},
		{name: "unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const callbackURL = "kubeloop://auth/callback?state=test"
			if got := deliverAuthCallback(test.handler, callbackURL); got != test.want {
				t.Fatalf("deliverAuthCallback() = %t, want %t", got, test.want)
			}
			if handler, ok := test.handler.(*testAuthCallbackHandler); ok &&
				(len(handler.called) != 1 || handler.called[0] != callbackURL) {
				t.Fatalf("callback calls = %#v", handler.called)
			}
		})
	}
}
