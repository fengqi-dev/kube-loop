package mcp

import (
	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/filemanager"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/intercept"
	"github.com/fengqi-dev/kube-loop/internal/portfwd"
	"github.com/fengqi-dev/kube-loop/internal/session"
	"github.com/fengqi-dev/kube-loop/internal/store"
)

type helperStatusOut struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Version   string `json:"version,omitempty"`
	Expected  string `json:"expected"`
	Socket    string `json:"socket"`
	Error     string `json:"error,omitempty"`
}

type manageHelperOut struct {
	Action string          `json:"action"`
	Status helperStatusOut `json:"status"`
}

type manageNetworkOut struct {
	Action        string                 `json:"action"`
	Type          string                 `json:"type"`
	Context       string                 `json:"context"`
	ManualNetwork *cluster.ManualNetwork `json:"manualNetwork,omitempty"`
	HostAliases   []store.HostAliasSpec  `json:"hostAliases,omitempty"`
}

type manageClusterOut struct {
	Action     string                    `json:"action"`
	Type       string                    `json:"type"`
	Inventory  *cluster.ClusterInventory `json:"inventory,omitempty"`
	Probe      *cluster.ProbeResult      `json:"probe,omitempty"`
	Namespaces []string                  `json:"namespaces,omitempty"`
	Services   []cluster.ServiceInfo     `json:"services,omitempty"`
	Pods       []cluster.PodInfo         `json:"pods,omitempty"`
}

type manageConnectionOut struct {
	Action string         `json:"action"`
	State  *session.State `json:"state,omitempty"`
	Config map[string]any `json:"config,omitempty"`
}

type singBoxDNSConfigOut struct {
	DNS map[string]any `json:"dns"`
}

type manageFileTransferOut struct {
	Action string                     `json:"action"`
	ID     string                     `json:"id,omitempty"`
	Task   *filemanager.TransferTask  `json:"task,omitempty"`
	Items  []filemanager.TransferTask `json:"items,omitempty"`
}

type manageTrafficOut struct {
	Action string        `json:"action"`
	Type   string        `json:"type,omitempty"`
	ID     string        `json:"id,omitempty"`
	Item   *trafficItem  `json:"item,omitempty"`
	Items  []trafficItem `json:"items,omitempty"`
}

type trafficItem struct {
	Type        string          `json:"type"`
	Intercept   *intercept.Info `json:"intercept,omitempty"`
	PortForward *portfwd.Info   `json:"portForward,omitempty"`
}

func toHelperStatusOut(st helper.Status) helperStatusOut {
	return helperStatusOut{
		Installed: st.Installed,
		Running:   st.Running,
		Version:   st.Version,
		Expected:  st.Expected,
		Socket:    st.Socket,
		Error:     st.Error,
	}
}
