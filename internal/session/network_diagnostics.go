package session

import (
	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/networkdiag"
)

type NetworkDiagnosticSeverity string

const (
	NetworkDiagnosticInfo    NetworkDiagnosticSeverity = "info"
	NetworkDiagnosticWarning NetworkDiagnosticSeverity = "warning"
)

type NetworkDiagnostic struct {
	Code      string                    `json:"code"`
	Severity  NetworkDiagnosticSeverity `json:"severity"`
	Message   string                    `json:"message"`
	Target    string                    `json:"target,omitempty"`
	Conflict  string                    `json:"conflict,omitempty"`
	Interface string                    `json:"interface,omitempty"`
}

type NetworkDiagnostics struct {
	RoutingMode string              `json:"routingMode"`
	StrictRoute bool                `json:"strictRoute"`
	Issues      []NetworkDiagnostic `json:"issues,omitempty"`
}

func inspectNetwork(discovery cluster.Discovery) *NetworkDiagnostics {
	result := networkdiag.Inspect(discovery)
	diagnostics := &NetworkDiagnostics{
		RoutingMode: result.RoutingMode,
		StrictRoute: result.StrictRoute,
	}
	if len(result.Issues) == 0 {
		return diagnostics
	}
	diagnostics.Issues = make([]NetworkDiagnostic, 0, len(result.Issues))
	for _, issue := range result.Issues {
		diagnostics.Issues = append(diagnostics.Issues, NetworkDiagnostic{
			Code:      issue.Code,
			Severity:  NetworkDiagnosticSeverity(issue.Severity),
			Message:   issue.Message,
			Target:    issue.Target,
			Conflict:  issue.Conflict,
			Interface: issue.Interface,
		})
	}
	return diagnostics
}
