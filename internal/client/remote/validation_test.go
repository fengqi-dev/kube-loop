package remote

import (
	"strings"
	"testing"
)

func TestValidateNamespace(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		wantError bool
	}{
		{name: "simple", namespace: "default"},
		{name: "hyphenated", namespace: "team-2"},
		{name: "empty", wantError: true},
		{name: "too long", namespace: strings.Repeat("a", 64), wantError: true},
		{name: "leading hyphen", namespace: "-team", wantError: true},
		{name: "trailing hyphen", namespace: "team-", wantError: true},
		{name: "uppercase", namespace: "Team", wantError: true},
		{name: "underscore", namespace: "team_dev", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateNamespace(test.namespace)
			if (err != nil) != test.wantError {
				t.Fatalf("validateNamespace(%q) error = %v, wantError = %v", test.namespace, err, test.wantError)
			}
		})
	}
}

func TestValidDigest(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "lowercase sha256", value: strings.Repeat("0a", 32), want: true},
		{name: "too short", value: strings.Repeat("a", 63)},
		{name: "uppercase", value: strings.Repeat("A", 64)},
		{name: "non hexadecimal", value: strings.Repeat("g", 64)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validDigest(test.value); got != test.want {
				t.Fatalf("validDigest(%q) = %v, want %v", test.value, got, test.want)
			}
		})
	}
}

func TestValidDNSSubdomain(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "single label", value: "api-0", want: true},
		{name: "multiple labels", value: "api.default", want: true},
		{name: "empty"},
		{name: "too long", value: strings.Repeat("a", 254)},
		{name: "empty label", value: "api..default"},
		{name: "label too long", value: strings.Repeat("a", 64) + ".default"},
		{name: "leading hyphen", value: "-api"},
		{name: "trailing hyphen", value: "api-"},
		{name: "uppercase", value: "API"},
		{name: "underscore", value: "api_server"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validDNSSubdomain(test.value); got != test.want {
				t.Fatalf("validDNSSubdomain(%q) = %v, want %v", test.value, got, test.want)
			}
		})
	}
}

func TestValidateServiceSpec(t *testing.T) {
	service := " api.default "
	ports := []servicePortValue{
		{name: " http ", servicePort: 80, protocol: " TCP "},
		{name: "dns", servicePort: 53, protocol: "udp"},
	}
	if err := validateServiceSpec(&service, ports, "Test"); err != nil {
		t.Fatalf("valid service spec: %v", err)
	}
	if service != "api.default" || ports[0].name != "http" || ports[0].protocol != remoteProtocolTCP {
		t.Fatalf("service spec was not normalized: service=%q ports=%#v", service, ports)
	}

	tests := []struct {
		name  string
		ports []servicePortValue
	}{
		{name: "no ports"},
		{name: "too many ports", ports: make([]servicePortValue, 65)},
		{name: "zero port", ports: []servicePortValue{{servicePort: 0, protocol: remoteProtocolTCP}}},
		{name: "high port", ports: []servicePortValue{{servicePort: 65536, protocol: remoteProtocolTCP}}},
		{name: "invalid protocol", ports: []servicePortValue{{servicePort: 80, protocol: "sctp"}}},
		{name: "duplicate port", ports: []servicePortValue{
			{servicePort: 80, protocol: remoteProtocolTCP},
			{servicePort: 80, protocol: remoteProtocolTCP},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := "api"
			if err := validateServiceSpec(&service, test.ports, "Test"); err == nil {
				t.Fatal("invalid service spec was accepted")
			}
		})
	}
}

func TestValidateExecSpec(t *testing.T) {
	tests := []struct {
		name      string
		spec      ExecSpec
		wantError bool
	}{
		{name: "valid", spec: ExecSpec{Pod: "api-0", Container: "api", Command: []string{"sh", "-c", "echo ok"}}},
		{name: "invalid pod", spec: ExecSpec{Pod: "API", Command: []string{"true"}}, wantError: true},
		{
			name:      "invalid container",
			spec:      ExecSpec{Pod: "api", Container: "API", Command: []string{"true"}},
			wantError: true,
		},
		{name: "empty command", spec: ExecSpec{Pod: "api"}, wantError: true},
		{name: "too many arguments", spec: ExecSpec{Pod: "api", Command: make([]string, 65)}, wantError: true},
		{name: "empty argument", spec: ExecSpec{Pod: "api", Command: []string{""}}, wantError: true},
		{
			name:      "oversized argument",
			spec:      ExecSpec{Pod: "api", Command: []string{strings.Repeat("a", 4097)}},
			wantError: true,
		},
		{name: "null byte", spec: ExecSpec{Pod: "api", Command: []string{"echo\x00bad"}}, wantError: true},
		{name: "oversized command", spec: ExecSpec{Pod: "api", Command: []string{
			strings.Repeat("a", 4096), strings.Repeat("b", 4096), strings.Repeat("c", 4096),
			strings.Repeat("d", 4096), "x",
		}}, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateExecSpec(test.spec)
			if (err != nil) != test.wantError {
				t.Fatalf("validateExecSpec(%#v) error = %v, wantError = %v", test.spec, err, test.wantError)
			}
		})
	}
}

func TestValidateRemotePaths(t *testing.T) {
	tests := []struct {
		name         string
		value        string
		allowRoot    bool
		wantTransfer bool
		wantPodFile  bool
	}{
		{name: "absolute", value: "/workspace/file", wantTransfer: true, wantPodFile: true},
		{name: "root list", value: "/", allowRoot: true, wantPodFile: true},
		{name: "root transfer", value: "/"},
		{name: "relative", value: "workspace/file"},
		{name: "unclean", value: "/workspace/../etc"},
		{name: "backslash", value: "/workspace\\file"},
		{name: "control character", value: "/workspace/\x1f"},
		{name: "delete character", value: "/workspace/\x7f"},
		{name: "too long", value: "/" + strings.Repeat("a", 4096)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validateRemotePath(test.value) == nil; got != test.wantTransfer {
				t.Fatalf("validateRemotePath(%q) success = %v, want %v", test.value, got, test.wantTransfer)
			}
			if got := validatePodFilePath(test.value, test.allowRoot) == nil; got != test.wantPodFile {
				t.Fatalf(
					"validatePodFilePath(%q, %v) success = %v, want %v",
					test.value,
					test.allowRoot,
					got,
					test.wantPodFile,
				)
			}
		})
	}
}
