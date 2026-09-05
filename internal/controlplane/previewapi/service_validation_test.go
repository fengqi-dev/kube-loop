package previewapi

import (
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/protocol/servicemodel"
)

func TestNormalizeRequestPreviewNames(t *testing.T) {
	spec := Spec{Name: " api ", Ports: []servicemodel.Port{
		{ServicePort: 80, Protocol: " TCP "},
		{Name: " dns ", ServicePort: 53, Protocol: " UDP "},
	}}
	if err := normalizeRequest(&spec); err != nil {
		t.Fatal(err)
	}
	if spec.Name != "api" || spec.Ports[0].Name != "dns" || spec.Ports[1].Name != "tcp-80" {
		t.Fatalf("unexpected normalized spec: %#v", spec)
	}
	for _, tt := range []struct {
		name           string
		spec           Spec
		field, message string
	}{
		{"name before ports", Spec{Name: "api.example"}, "name", "Service name is invalid"},
		{"invalid port name", Spec{Name: "api", Ports: []servicemodel.Port{{Name: "bad.name", ServicePort: 80, Protocol: "tcp"}}}, "ports", "Service port name is invalid"},
		{"duplicate port names", Spec{Name: "api", Ports: []servicemodel.Port{{Name: "http", ServicePort: 80, Protocol: "tcp"}, {Name: "http", ServicePort: 81, Protocol: "tcp"}}}, "ports", "Service port names must be unique"},
		{"generated name collision", Spec{Name: "api", Ports: []servicemodel.Port{{ServicePort: 80, Protocol: "tcp"}, {Name: "tcp-80", ServicePort: 81, Protocol: "tcp"}}}, "ports", "Service port names must be unique"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := normalizeRequest(&tt.spec)
			if err == nil || err.Field != tt.field || err.Message != tt.message {
				t.Fatalf("error=%#v, want %s: %s", err, tt.field, tt.message)
			}
		})
	}
}
