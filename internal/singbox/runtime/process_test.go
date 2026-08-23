package runtime

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

func TestProcessReadLogsReturnsSessionHistory(t *testing.T) {
	responses := []struct {
		data   string
		offset int64
	}{
		{data: "first line\npartial", offset: 18},
		{data: " line\nsecond line\n", offset: 36},
		{offset: 36},
	}
	readIndex := 0
	process := &Process{
		spec: singbox.SessionSpec{ID: "session-1"},
		readLogs: func(context.Context, string, int64) (string, int64, error) {
			response := responses[readIndex]
			readIndex++
			return response.data, response.offset, nil
		},
	}

	tests := []struct {
		name string
		want []string
	}{
		{name: "buffers incomplete line", want: []string{"first line"}},
		{name: "appends newly completed lines", want: []string{"first line", "partial line", "second line"}},
		{name: "keeps history when no new data arrives", want: []string{"first line", "partial line", "second line"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := process.ReadLogs(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("logs = %#v, want %#v", got, test.want)
			}
			if test.name == "appends newly completed lines" {
				got[0] = "mutated by caller"
			}
		})
	}
}

func TestProcessCloseReportsHelperStopFailure(t *testing.T) {
	stopErr := errors.New("helper stop failed")
	process := &Process{
		done:   make(chan struct{}),
		stopCh: make(chan struct{}),
		helperStop: func(context.Context) error {
			return stopErr
		},
	}
	go process.wait()
	if err := process.Close(); !errors.Is(err, stopErr) {
		t.Fatalf("Close error = %v, want %v", err, stopErr)
	}
	select {
	case <-process.Done():
	default:
		t.Fatal("Process watcher did not stop")
	}
	if err := process.Close(); !errors.Is(err, stopErr) {
		t.Fatalf("repeated Close error = %v, want %v", err, stopErr)
	}
}
