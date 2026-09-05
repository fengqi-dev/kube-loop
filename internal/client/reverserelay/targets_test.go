package reverserelay

import (
	"reflect"
	"slices"
	"testing"
)

func TestNormalizeTargets(t *testing.T) {
	for _, operation := range []string{"exchange", "mirror", "preview"} {
		t.Run(operation, func(t *testing.T) {
			type testCase struct {
				name    string
				input   []Target
				want    []Target
				message string
			}
			tests := []testCase{
				{name: "nil", message: "requires one to 64 local targets"},
				{name: "empty", input: []Target{}, message: "requires one to 64 local targets"},
				{
					name:  "defaults",
					input: []Target{{ServicePort: 80}},
					want:  []Target{{ServicePort: 80, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: 80}},
				},
				{name: "normalize and preserve order", input: []Target{
					{ServicePort: 65535, Protocol: " UDP ", LocalHost: " ::1 ", LocalPort: 53},
					{ServicePort: 1, Protocol: " TCP ", LocalHost: " localhost "},
				}, want: []Target{
					{ServicePort: 65535, Protocol: "udp", LocalHost: "::1", LocalPort: 53},
					{ServicePort: 1, Protocol: "tcp", LocalHost: "localhost", LocalPort: 1},
				}},
				{name: "zero port", input: []Target{{ServicePort: 0}}, message: "local target is invalid"},
				{
					name:    "negative port",
					input:   []Target{{ServicePort: -1, LocalPort: 80}},
					message: "local target is invalid",
				},
				{name: "overflow port", input: []Target{{ServicePort: 65536}}, message: "local target is invalid"},
				{
					name:    "unsupported protocol",
					input:   []Target{{ServicePort: 80, Protocol: "sctp"}},
					message: "local target is invalid",
				},
				{
					name:    "unspecified host",
					input:   []Target{{ServicePort: 80, LocalHost: "0.0.0.0"}},
					message: "local target is invalid",
				},
				{
					name:    "multicast host",
					input:   []Target{{ServicePort: 80, LocalHost: "224.0.0.1"}},
					message: "local target is invalid",
				},
				{
					name:    "invalid host",
					input:   []Target{{ServicePort: 80, LocalHost: "bad host"}},
					message: "local target is invalid",
				},
				{
					name:    "normalized duplicate",
					input:   []Target{{ServicePort: 80}, {ServicePort: 80, Protocol: " TCP "}},
					message: "Service ports must be unique",
				},
				{
					name:    "invalid before duplicate",
					input:   []Target{{ServicePort: 80}, {ServicePort: 80, LocalHost: "bad host"}},
					message: "local target is invalid",
				},
				{
					name:  "same port different protocols",
					input: []Target{{ServicePort: 53}, {ServicePort: 53, Protocol: "udp"}},
					want: []Target{
						{ServicePort: 53, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: 53},
						{ServicePort: 53, Protocol: "udp", LocalHost: "127.0.0.1", LocalPort: 53},
					},
				},
			}
			for _, count := range []int{64, 65} {
				input := make([]Target, count)
				want := make([]Target, count)
				for i := range input {
					input[i] = Target{ServicePort: int32(i + 1)}
					want[i] = Target{
						ServicePort: int32(i + 1),
						Protocol:    "tcp",
						LocalHost:   "127.0.0.1",
						LocalPort:   uint16(i + 1),
					}
				}
				if count == 64 {
					tests = append(tests, testCase{"64 targets", input, want, ""})
				} else {
					tests = append(tests, testCase{"65 targets", input, nil, "requires one to 64 local targets"})
				}
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					before := slices.Clone(tt.input)
					got, err := NormalizeTargets(tt.input, operation)
					if !reflect.DeepEqual(tt.input, before) {
						t.Fatal("input was modified")
					}
					if tt.message != "" {
						if err == nil || err.Error() != operation+" "+tt.message || got != nil {
							t.Fatalf("got %v, %v; want nil, %q", got, err, operation+" "+tt.message)
						}
						return
					}
					if err != nil || !reflect.DeepEqual(got, tt.want) {
						t.Fatalf("got %#v, %v; want %#v", got, err, tt.want)
					}
					got[0].LocalHost = "changed"
					if !reflect.DeepEqual(tt.input, before) {
						t.Fatal("result aliases input")
					}
				})
			}
		})
	}
}
