package app

import (
	"context"
	"errors"
	"testing"
)

func TestCancelServerLoginCancelsActiveAttemptAndAllowsRetry(t *testing.T) {
	application := &App{}
	loginContext, finish, err := application.beginServerLogin()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := application.beginServerLogin(); err == nil {
		t.Fatal("concurrent browser login was allowed")
	}

	application.CancelServerLogin()
	select {
	case <-loginContext.Done():
		if !errors.Is(loginContext.Err(), context.Canceled) {
			t.Fatalf("login context error = %v", loginContext.Err())
		}
	default:
		t.Fatal("active browser login was not cancelled")
	}
	finish()

	_, retryFinish, err := application.beginServerLogin()
	if err != nil {
		t.Fatalf("start browser login after cancellation: %v", err)
	}
	retryFinish()
	application.CancelServerLogin()
}
