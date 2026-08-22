package fileopsapi

import (
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

func TestNormalizeAppliesActionSpecificPathContracts(t *testing.T) {
	handler := &Service{allowedRoots: []string{"/workspace"}}
	tests := []struct {
		name          string
		spec          Spec
		expectedField string
	}{
		{
			name: "list root",
			spec: Spec{Action: "list", Pod: "api-0", Container: "api", Path: "/workspace"},
		},
		{
			name: "create file",
			spec: Spec{
				Action: ActionCreate, Pod: "api-0", Container: "api",
				Path: "/workspace/report.txt", Kind: " FILE ",
			},
		},
		{
			name: "rename",
			spec: Spec{
				Action: ActionRename, Pod: "api-0", Path: "/workspace/old.txt",
				Destination: "/workspace/new.txt",
			},
		},
		{
			name: "recursive delete",
			spec: Spec{Action: ActionDelete, Pod: "api-0", Path: "/workspace/archive", Recursive: true},
		},
		{
			name:          "invalid pod",
			spec:          Spec{Action: "list", Pod: "Bad_Pod", Path: "/workspace"},
			expectedField: "pod",
		},
		{
			name:          "invalid container",
			spec:          Spec{Action: "list", Pod: "api-0", Container: "Bad_Container", Path: "/workspace"},
			expectedField: "container",
		},
		{
			name:          "list rejects mutation fields",
			spec:          Spec{Action: "list", Pod: "api-0", Path: "/workspace", Recursive: true},
			expectedField: "path",
		},
		{
			name:          "create rejects allowed root",
			spec:          Spec{Action: ActionCreate, Pod: "api-0", Path: "/workspace", Kind: KindDirectory},
			expectedField: "path",
		},
		{
			name: "rename rejects same path",
			spec: Spec{
				Action: ActionRename, Pod: "api-0", Path: "/workspace/report.txt",
				Destination: "/workspace/report.txt",
			},
			expectedField: "destination",
		},
		{
			name: "delete rejects destination",
			spec: Spec{
				Action: ActionDelete, Pod: "api-0", Path: "/workspace/report.txt",
				Destination: "/workspace/other.txt",
			},
			expectedField: "destination",
		},
		{
			name:          "invalid action",
			spec:          Spec{Action: "copy", Pod: "api-0", Path: "/workspace/report.txt"},
			expectedField: "action",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := test.spec
			apiError := handler.normalize(&spec)
			if test.expectedField == "" {
				if apiError != nil {
					t.Fatalf("normalize error = %#v", apiError)
				}
				if spec.AllowedRoot != "/workspace" {
					t.Fatalf("allowed root = %q", spec.AllowedRoot)
				}
				if spec.Action == ActionRename && spec.DestinationRoot != "/workspace" {
					t.Fatalf("destination root = %q", spec.DestinationRoot)
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
