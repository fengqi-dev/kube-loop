package execapi

import (
	"strings"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

func TestNormalizeSpecValidatesCommandContract(t *testing.T) {
	tests := []struct {
		name          string
		spec          Spec
		expectedField string
	}{
		{
			name: "valid",
			spec: Spec{Pod: " api-0 ", Container: " api ", Command: []string{"sh", "-c", "echo ok"}},
		},
		{name: "invalid pod", spec: Spec{Pod: "Bad_Pod", Command: []string{"true"}}, expectedField: "pod"},
		{
			name:          "invalid container",
			spec:          Spec{Pod: "api-0", Container: "Bad_Container", Command: []string{"true"}},
			expectedField: "container",
		},
		{name: "empty command", spec: Spec{Pod: "api-0"}, expectedField: "command"},
		{
			name:          "too many arguments",
			spec:          Spec{Pod: "api-0", Command: make([]string, 65)},
			expectedField: "command",
		},
		{
			name:          "empty argument",
			spec:          Spec{Pod: "api-0", Command: []string{"sh", ""}},
			expectedField: "command[1]",
		},
		{
			name:          "oversized argument",
			spec:          Spec{Pod: "api-0", Command: []string{strings.Repeat("x", 4097)}},
			expectedField: "command[0]",
		},
		{
			name: "oversized command",
			spec: Spec{Pod: "api-0", Command: []string{
				strings.Repeat("a", 4096), strings.Repeat("b", 4096),
				strings.Repeat("c", 4096), strings.Repeat("d", 4096), "x",
			}},
			expectedField: "command",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := test.spec
			apiError := normalizeSpec(&spec)
			if test.expectedField == "" {
				if apiError != nil || spec.Pod != "api-0" || spec.Container != "api" {
					t.Fatalf("normalized spec = %#v error = %#v", spec, apiError)
				}
				return
			}
			if apiError == nil || apiError.Code != controlplaneapi.CodeInvalidArgument ||
				apiError.Field != test.expectedField {
				t.Fatalf("normalize error = %#v, want field %q", apiError, test.expectedField)
			}
		})
	}
}
