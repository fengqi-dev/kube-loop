package trafficapi_test

import (
	"reflect"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/servicemodel"
)

func TestNormalizeServicePorts(t *testing.T) {
	service := " api.example "
	ports := []servicemodel.Port{
		{Name: " dns ", ServicePort: 53, Protocol: " UDP "},
		{Name: " http ", ServicePort: 80, Protocol: " TCP "},
		{Name: " dns-tcp ", ServicePort: 53, Protocol: " TCP "},
	}
	if err := trafficapi.NormalizeServicePorts(&service, ports); err != nil {
		t.Fatal(err)
	}
	want := []servicemodel.Port{
		{Name: "dns-tcp", ServicePort: 53, Protocol: "tcp"},
		{Name: "dns", ServicePort: 53, Protocol: "udp"},
		{Name: "http", ServicePort: 80, Protocol: "tcp"},
	}
	if service != "api.example" || !reflect.DeepEqual(ports, want) {
		t.Fatalf("service=%q ports=%#v", service, ports)
	}
}

func TestNormalizeServicePortsErrors(t *testing.T) {
	tests := []struct {
		name           string
		service        string
		ports          []servicemodel.Port
		field, message string
	}{
		{"service before ports", " Bad Name ", nil, "service", "Service name is invalid"},
		{"no ports", "api", nil, "ports", "one to 64 Service ports are required"},
		{"too many ports", "api", make([]servicemodel.Port, 65), "ports", "one to 64 Service ports are required"},
		{"zero port", "api", []servicemodel.Port{{Protocol: "tcp"}}, "ports", "Service port and protocol are invalid"},
		{
			"overflow port",
			"api",
			[]servicemodel.Port{{ServicePort: 65536, Protocol: "tcp"}},
			"ports",
			"Service port and protocol are invalid",
		},
		{
			"missing protocol",
			"api",
			[]servicemodel.Port{{ServicePort: 80}},
			"ports",
			"Service port and protocol are invalid",
		},
		{
			"duplicate",
			"api",
			[]servicemodel.Port{{ServicePort: 80, Protocol: "tcp"}, {ServicePort: 80, Protocol: " TCP "}},
			"ports",
			"Service ports must be unique",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := trafficapi.NormalizeServicePorts(&tt.service, tt.ports)
			if err == nil || err.Code != controlplaneapi.CodeInvalidArgument || err.Field != tt.field ||
				err.Message != tt.message {
				t.Fatalf("error=%#v, want %s: %s", err, tt.field, tt.message)
			}
		})
	}
}

func TestNormalizeServicePortsKeepsPartialNormalizationOnError(t *testing.T) {
	service := " api "
	ports := []servicemodel.Port{
		{Name: " first ", ServicePort: 80, Protocol: " TCP "},
		{Name: " bad ", ServicePort: 1, Protocol: " SCTP "},
		{Name: " untouched ", ServicePort: 53, Protocol: " UDP "},
	}
	err := trafficapi.NormalizeServicePorts(&service, ports)
	want := []servicemodel.Port{
		{Name: "first", ServicePort: 80, Protocol: "tcp"},
		{Name: "bad", ServicePort: 1, Protocol: "sctp"},
		{Name: " untouched ", ServicePort: 53, Protocol: " UDP "},
	}
	if err == nil || service != "api" || !reflect.DeepEqual(ports, want) {
		t.Fatalf("service=%q ports=%#v error=%v", service, ports, err)
	}
}
