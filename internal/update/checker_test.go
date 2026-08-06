package update

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

func TestCheckerFindsNewRelease(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Accept") != "application/vnd.github+json" {
			t.Fatalf("unexpected Accept header %q", request.Header.Get("Accept"))
		}
		body := `{
			"tag_name": "v0.2.0",
			"html_url": "https://github.com/fengqi-dev/kube-loop/releases/tag/v0.2.0",
			"published_at": "2026-07-28T09:00:00Z"
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(body)),
			Request:    request,
		}, nil
	})}
	checker := &Checker{
		CurrentVersion: "v0.1.0",
		HTTPClient:     client,
		LatestURL:      "https://example.invalid/releases/latest",
	}
	info, err := checker.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !info.Available || info.LatestVersion != "v0.2.0" {
		t.Fatalf("unexpected update info: %#v", info)
	}
}

func TestCheckerDoesNotTreatDevelopmentBuildAsOutdated(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body: io.NopCloser(bytes.NewBufferString(
				`{"tag_name":"v9.0.0","html_url":"https://github.com/fengqi-dev/kube-loop/releases/tag/v9.0.0"}`,
			)),
			Request: request,
		}, nil
	})}
	info, err := (&Checker{
		CurrentVersion: "dev",
		HTTPClient:     client,
		LatestURL:      "https://example.invalid/releases/latest",
	}).Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Available {
		t.Fatalf("development build should not be marked outdated: %#v", info)
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		left  string
		right string
		want  int
	}{
		{left: "v1.2.0", right: "v1.1.9", want: 1},
		{left: "v1.2.0", right: "1.2.0", want: 0},
		{left: "v1.2.0-beta.2", right: "v1.2.0-beta.1", want: 1},
		{left: "v1.2.0", right: "v1.2.0-rc.1", want: 1},
		{left: "v1.1.9", right: "v1.2.0", want: -1},
		{left: "v1.2.0+build.2", right: "v1.2.0+build.1", want: 0},
		{left: " v1.2.0 ", right: "1.2", want: 0},
	}
	for _, test := range tests {
		if got := compareVersions(test.left, test.right); got != test.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
