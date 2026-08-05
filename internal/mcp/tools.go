package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/filemanager"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/intercept"
	"github.com/fengqi-dev/kube-loop/internal/portfwd"
	"github.com/fengqi-dev/kube-loop/internal/session"
	"github.com/fengqi-dev/kube-loop/internal/store"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type manageClusterIn struct {
	Action    string `json:"action" jsonschema:"reload, probe, or list"`
	Type      string `json:"type" jsonschema:"context, namespace, service, or pod"`
	Context   string `json:"context,omitempty" jsonschema:"Kubernetes context name; required except when reloading contexts"`
	Namespace string `json:"namespace,omitempty" jsonschema:"Kubernetes namespace; required when listing Services or Pods"`
}

type manageConnectionIn struct {
	Action    string `json:"action" jsonschema:"status, connect, disconnect, or config"`
	Context   string `json:"context,omitempty" jsonschema:"Kubernetes context name required when connecting"`
	Namespace string `json:"namespace,omitempty" jsonschema:"Kubernetes namespace used when connecting"`
}

type manageNetworkIn struct {
	Action         string      `json:"action" jsonschema:"get or set"`
	Type           string      `json:"type" jsonschema:"manual_network or host_aliases"`
	Context        string      `json:"context" jsonschema:"Kubernetes context name"`
	PodCIDRs       []string    `json:"podCIDRs,omitempty" jsonschema:"Pod CIDR list"`
	ServiceCIDRs   []string    `json:"serviceCIDRs,omitempty" jsonschema:"Service CIDR list"`
	DNSServer      string      `json:"dnsServer,omitempty" jsonschema:"CoreDNS / cluster DNS IP"`
	ClusterDomains []string    `json:"clusterDomains,omitempty" jsonschema:"Cluster DNS domains; always includes cluster.local"`
	DNSNamespace   string      `json:"dnsNamespace,omitempty" jsonschema:"Namespace used for short-name DNS search"`
	Items          []hostAlias `json:"items,omitempty" jsonschema:"Host aliases to set; an empty list clears them"`
}

type hostAlias struct {
	Domain string `json:"domain" jsonschema:"DNS name"`
	IP     string `json:"ip" jsonschema:"IPv4 address"`
}

type manageHelperIn struct {
	Action string `json:"action" jsonschema:"status, install, or uninstall"`
}

type portMappingIn struct {
	ServicePort int32  `json:"servicePort" jsonschema:"Service port number"`
	Protocol    string `json:"protocol,omitempty" jsonschema:"tcp or udp"`
	LocalHost   string `json:"localHost,omitempty" jsonschema:"Local bind host"`
	LocalPort   int    `json:"localPort" jsonschema:"Local listen port"`
}

type manageTrafficIn struct {
	Action     string          `json:"action" jsonschema:"start, stop, or list"`
	Type       string          `json:"type,omitempty" jsonschema:"exchange, mirror, preview, or port_forward; optional only when listing all"`
	ID         string          `json:"id,omitempty" jsonschema:"Traffic session id; required when stopping"`
	Context    string          `json:"context,omitempty" jsonschema:"Kubernetes context for port_forward; defaults to active session"`
	Namespace  string          `json:"namespace,omitempty" jsonschema:"Kubernetes namespace"`
	Service    string          `json:"service,omitempty" jsonschema:"Service name for exchange or mirror"`
	Name       string          `json:"name,omitempty" jsonschema:"Preview Service name"`
	TargetKind string          `json:"targetKind,omitempty" jsonschema:"Required for port_forward; must be pod or service"`
	TargetName string          `json:"targetName,omitempty" jsonschema:"Required for port_forward; Pod name when targetKind=pod, Service name when targetKind=service"`
	Ports      []portMappingIn `json:"ports,omitempty" jsonschema:"Local port mappings for exchange, mirror, or preview"`
	Protocol   string          `json:"protocol,omitempty" jsonschema:"tcp or udp for port_forward"`
	RemotePort uint16          `json:"remotePort,omitempty" jsonschema:"Remote container or service port for port_forward"`
	LocalPort  uint16          `json:"localPort,omitempty" jsonschema:"Local port for port_forward; 0 allocates"`
}

type podCommandIn struct {
	Context        string `json:"context" jsonschema:"Kubernetes context name"`
	Namespace      string `json:"namespace" jsonschema:"Kubernetes namespace"`
	Pod            string `json:"pod" jsonschema:"Pod name"`
	PodUID         string `json:"podUID,omitempty" jsonschema:"Pod UID; rejects a replaced Pod when set"`
	Container      string `json:"container,omitempty" jsonschema:"Container name; defaults to the first regular container"`
	Command        string `json:"command" jsonschema:"Shell command executed with /bin/sh -c"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"Command timeout in seconds; defaults to 30 and max is 300"`
}

type manageFileTransferIn struct {
	Action         string                `json:"action" jsonschema:"start, list, or cancel"`
	ID             string                `json:"id,omitempty" jsonschema:"Transfer task id required when cancelling"`
	Direction      filemanager.Direction `json:"direction,omitempty" jsonschema:"Required when starting; upload copies local sourcePath to the Pod, download copies Pod sourcePath to the local destinationDir"`
	Context        string                `json:"context,omitempty" jsonschema:"Kubernetes context name required when starting"`
	Namespace      string                `json:"namespace,omitempty" jsonschema:"Kubernetes namespace required when starting"`
	Pod            string                `json:"pod,omitempty" jsonschema:"Pod name required when starting"`
	PodUID         string                `json:"podUID,omitempty" jsonschema:"Pod UID; rejects a replaced Pod when set"`
	Container      string                `json:"container,omitempty" jsonschema:"Container name used when starting"`
	SourcePath     string                `json:"sourcePath,omitempty" jsonschema:"Absolute source path on the local machine for upload or in the container for download"`
	DestinationDir string                `json:"destinationDir,omitempty" jsonschema:"Absolute destination directory in the container for upload or on the local machine for download"`
	Overwrite      bool                  `json:"overwrite,omitempty" jsonschema:"Replace an existing destination path"`
}

func registerTools(server *mcpsdk.Server, backend Backend) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "manage_cluster",
		Description: "Reload or probe Kubernetes contexts and list cluster resources. " +
			"Use action=reload,type=context; action=probe,type=context; or " +
			"action=list,type=namespace|service|pod.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in manageClusterIn) (*mcpsdk.CallToolResult, manageClusterOut, error) {
		out, err := manageCluster(ctx, backend, in)
		return nil, out, err
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "manage_connection",
		Description: "Inspect or control the KubeLoop cluster connection. " +
			"Use action=status|connect|disconnect|config.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in manageConnectionIn) (*mcpsdk.CallToolResult, manageConnectionOut, error) {
		out, err := manageConnection(ctx, backend, in)
		return nil, out, err
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "manage_network",
		Description: "Get or set manual Pod/Service network overrides and local tunnel DNS host aliases. " +
			"Use action=get|set and type=manual_network|host_aliases.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in manageNetworkIn) (*mcpsdk.CallToolResult, manageNetworkOut, error) {
		if in.Context == "" {
			return nil, manageNetworkOut{}, fmt.Errorf("context is required")
		}
		out := manageNetworkOut{Action: in.Action, Type: in.Type, Context: in.Context}
		switch in.Type {
		case "manual_network":
			if in.Action == "set" {
				if err := backend.SetManualNetwork(in.Context, cluster.ManualNetwork{
					PodCIDRs:       in.PodCIDRs,
					ServiceCIDRs:   in.ServiceCIDRs,
					DNSServer:      in.DNSServer,
					ClusterDomains: in.ClusterDomains,
					DNSNamespace:   in.DNSNamespace,
				}); err != nil {
					return nil, manageNetworkOut{}, err
				}
			} else if in.Action != "get" {
				return nil, manageNetworkOut{}, fmt.Errorf("action must be get or set")
			}
			network := backend.GetManualNetwork(in.Context)
			out.ManualNetwork = &network
		case "host_aliases":
			if in.Action == "set" {
				items := make([]store.HostAliasSpec, 0, len(in.Items))
				for _, item := range in.Items {
					items = append(items, store.HostAliasSpec{Domain: item.Domain, IP: item.IP})
				}
				if err := backend.SetHostAliases(in.Context, items); err != nil {
					return nil, manageNetworkOut{}, err
				}
			} else if in.Action != "get" {
				return nil, manageNetworkOut{}, fmt.Errorf("action must be get or set")
			}
			out.HostAliases = backend.GetHostAliases(in.Context)
		default:
			return nil, manageNetworkOut{}, fmt.Errorf(
				"type must be manual_network or host_aliases",
			)
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "manage_traffic",
		Description: "Start, stop, or list KubeLoop traffic sessions. " +
			"Supported types are exchange, mirror, preview, and port_forward. " +
			"Type may be omitted only with action=list to return every traffic session.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in manageTrafficIn) (*mcpsdk.CallToolResult, manageTrafficOut, error) {
		out, err := manageTraffic(ctx, backend, in)
		return nil, out, err
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "manage_helper",
		Description: "Get status, install, or uninstall the privileged virtual network helper. " +
			"Install and uninstall may prompt for OS elevation.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in manageHelperIn) (*mcpsdk.CallToolResult, manageHelperOut, error) {
		switch in.Action {
		case "status":
		case "install":
			if err := backend.InstallHelper(ctx); err != nil {
				return nil, manageHelperOut{}, err
			}
		case "uninstall":
			if err := backend.UninstallHelper(ctx); err != nil {
				return nil, manageHelperOut{}, err
			}
		default:
			return nil, manageHelperOut{}, fmt.Errorf(
				"action must be status, install, or uninstall",
			)
		}
		return nil, manageHelperOut{
			Action: in.Action,
			Status: toHelperStatusOut(backend.HelperStatus(ctx)),
		}, nil
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "exec_pod_command",
		Description: "Execute a shell command in a Pod container through the Kubernetes exec backend used by Pod SSH. " +
			"Returns stdout, stderr, and exitCode; non-zero command exits are returned as structured results.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in podCommandIn) (*mcpsdk.CallToolResult, PodCommandResult, error) {
		out, err := backend.ExecPodCommand(ctx, PodCommandRequest{
			Context: in.Context, Namespace: in.Namespace, Pod: in.Pod,
			PodUID: in.PodUID, Container: in.Container, Command: in.Command,
			TimeoutSeconds: in.TimeoutSeconds,
		})
		return nil, out, err
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "manage_file_transfer",
		Description: "Start, list, or cancel asynchronous file and directory transfers between the local machine and a Pod. " +
			"Use action=start|list|cancel; start supports direction=upload|download.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in manageFileTransferIn) (*mcpsdk.CallToolResult, manageFileTransferOut, error) {
		out, err := manageFileTransfer(ctx, backend, in)
		return nil, out, err
	})
}

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
	Action string          `json:"action"`
	State  *session.State  `json:"state,omitempty"`
	Config json.RawMessage `json:"config,omitempty"`
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

func manageCluster(
	ctx context.Context,
	backend Backend,
	in manageClusterIn,
) (manageClusterOut, error) {
	out := manageClusterOut{Action: in.Action, Type: in.Type}
	switch {
	case in.Action == "reload" && in.Type == "context":
		inventory, err := backend.ReloadContexts()
		if err != nil {
			return manageClusterOut{}, err
		}
		out.Inventory = &inventory
	case in.Action == "probe" && in.Type == "context":
		if in.Context == "" {
			return manageClusterOut{}, fmt.Errorf("context is required when probing")
		}
		probe, err := backend.ProbeContext(ctx, in.Context)
		if err != nil {
			return manageClusterOut{}, err
		}
		out.Probe = &probe
	case in.Action == "list" && in.Type == "namespace":
		if in.Context == "" {
			return manageClusterOut{}, fmt.Errorf("context is required when listing namespaces")
		}
		items, err := backend.Namespaces(ctx, in.Context)
		if err != nil {
			return manageClusterOut{}, err
		}
		out.Namespaces = items
	case in.Action == "list" && (in.Type == "service" || in.Type == "pod"):
		if in.Context == "" {
			return manageClusterOut{}, fmt.Errorf("context is required when listing %ss", in.Type)
		}
		if in.Namespace == "" {
			return manageClusterOut{}, fmt.Errorf("namespace is required when listing %ss", in.Type)
		}
		if in.Type == "service" {
			items, err := backend.ListServices(ctx, in.Context, in.Namespace)
			if err != nil {
				return manageClusterOut{}, err
			}
			out.Services = items
		} else {
			items, err := backend.ListPods(ctx, in.Context, in.Namespace)
			if err != nil {
				return manageClusterOut{}, err
			}
			out.Pods = items
		}
	default:
		return manageClusterOut{}, fmt.Errorf(
			"supported combinations are reload/context, probe/context, and list/namespace|service|pod",
		)
	}
	return out, nil
}

func manageConnection(
	ctx context.Context,
	backend Backend,
	in manageConnectionIn,
) (manageConnectionOut, error) {
	out := manageConnectionOut{Action: in.Action}
	switch in.Action {
	case "status":
	case "connect":
		if in.Context == "" {
			return manageConnectionOut{}, fmt.Errorf("context is required when connecting")
		}
		if err := backend.Connect(ctx, in.Context, in.Namespace); err != nil {
			return manageConnectionOut{}, err
		}
	case "disconnect":
		if err := backend.Disconnect(); err != nil {
			return manageConnectionOut{}, err
		}
	case "config":
		raw, err := backend.SingBoxConfig()
		if err != nil {
			return manageConnectionOut{}, err
		}
		out.Config = json.RawMessage(raw)
	default:
		return manageConnectionOut{}, fmt.Errorf(
			"action must be status, connect, disconnect, or config",
		)
	}
	state := backend.SessionState()
	out.State = &state
	return out, nil
}

func manageFileTransfer(
	ctx context.Context,
	backend Backend,
	in manageFileTransferIn,
) (manageFileTransferOut, error) {
	out := manageFileTransferOut{Action: in.Action}
	switch in.Action {
	case "start":
		task, err := backend.StartFileTransfer(ctx, filemanager.TransferRequest{
			Direction: in.Direction,
			Target: filemanager.Target{
				Context: in.Context, Namespace: in.Namespace, Pod: in.Pod,
				PodUID: in.PodUID, Container: in.Container,
			},
			SourcePath: in.SourcePath, DestinationDir: in.DestinationDir,
			Overwrite: in.Overwrite,
		})
		if err != nil {
			return manageFileTransferOut{}, err
		}
		out.ID = task.ID
		out.Task = &task
	case "list":
		out.Items = backend.ListFileTransfers()
	case "cancel":
		if in.ID == "" {
			return manageFileTransferOut{}, fmt.Errorf("id is required when cancelling a file transfer")
		}
		if err := backend.CancelFileTransfer(in.ID); err != nil {
			return manageFileTransferOut{}, err
		}
		out.ID = in.ID
	default:
		return manageFileTransferOut{}, fmt.Errorf("action must be start, list, or cancel")
	}
	return out, nil
}

func manageTraffic(
	ctx context.Context,
	backend Backend,
	in manageTrafficIn,
) (manageTrafficOut, error) {
	switch in.Action {
	case "start":
		return startTraffic(ctx, backend, in)
	case "stop":
		if in.ID == "" {
			return manageTrafficOut{}, fmt.Errorf("id is required when stopping traffic")
		}
		var err error
		switch in.Type {
		case "exchange", "mirror":
			err = backend.StopIntercept(ctx, in.ID)
		case "preview":
			err = backend.StopPreview(ctx, in.ID)
		case "port_forward":
			err = backend.StopPortForward(in.ID)
		default:
			return manageTrafficOut{}, trafficTypeError(false)
		}
		if err != nil {
			return manageTrafficOut{}, err
		}
		return manageTrafficOut{Action: in.Action, Type: in.Type, ID: in.ID}, nil
	case "list":
		if in.Type != "" && !validTrafficType(in.Type) {
			return manageTrafficOut{}, trafficTypeError(true)
		}
		return manageTrafficOut{
			Action: in.Action,
			Type:   in.Type,
			Items:  listTraffic(backend, in.Type),
		}, nil
	default:
		return manageTrafficOut{}, fmt.Errorf("action must be start, stop, or list")
	}
}

func startTraffic(
	ctx context.Context,
	backend Backend,
	in manageTrafficIn,
) (manageTrafficOut, error) {
	var item trafficItem
	switch in.Type {
	case "exchange", "mirror":
		mapping := intercept.Mapping{
			Namespace: in.Namespace,
			Service:   in.Service,
			Ports:     toPortMappings(in.Ports),
		}
		var (
			info intercept.Info
			err  error
		)
		if in.Type == "exchange" {
			info, err = backend.StartIntercept(ctx, mapping)
		} else {
			info, err = backend.StartMirror(ctx, mapping)
		}
		if err != nil {
			return manageTrafficOut{}, err
		}
		item = trafficItem{Type: in.Type, Intercept: &info}
	case "preview":
		info, err := backend.StartPreview(ctx, intercept.PreviewRequest{
			Namespace: in.Namespace,
			Name:      in.Name,
			Ports:     toPortMappings(in.Ports),
		})
		if err != nil {
			return manageTrafficOut{}, err
		}
		item = trafficItem{Type: in.Type, Intercept: &info}
	case "port_forward":
		if in.TargetKind != portfwd.KindPod && in.TargetKind != portfwd.KindService {
			return manageTrafficOut{}, fmt.Errorf(
				"targetKind must be %q or %q for port_forward",
				portfwd.KindPod,
				portfwd.KindService,
			)
		}
		if in.TargetName == "" {
			return manageTrafficOut{}, fmt.Errorf("targetName is required for port_forward")
		}
		info, err := backend.StartPortForward(ctx, portfwd.Request{
			Context: in.Context, Namespace: in.Namespace,
			Kind: in.TargetKind, Name: in.TargetName, Protocol: in.Protocol,
			RemotePort: in.RemotePort, LocalPort: in.LocalPort,
		})
		if err != nil {
			return manageTrafficOut{}, err
		}
		item = trafficItem{Type: in.Type, PortForward: &info}
	default:
		return manageTrafficOut{}, trafficTypeError(false)
	}
	return manageTrafficOut{Action: in.Action, Type: in.Type, Item: &item}, nil
}

func listTraffic(backend Backend, trafficType string) []trafficItem {
	items := make([]trafficItem, 0)
	if trafficType == "" || trafficType == "exchange" {
		for _, info := range backend.ListIntercepts() {
			copy := info
			items = append(items, trafficItem{Type: "exchange", Intercept: &copy})
		}
	}
	if trafficType == "" || trafficType == "mirror" {
		for _, info := range backend.ListMirrors() {
			copy := info
			items = append(items, trafficItem{Type: "mirror", Intercept: &copy})
		}
	}
	if trafficType == "" || trafficType == "preview" {
		for _, info := range backend.ListPreviews() {
			copy := info
			items = append(items, trafficItem{Type: "preview", Intercept: &copy})
		}
	}
	if trafficType == "" || trafficType == "port_forward" {
		for _, info := range backend.ListPortForwards() {
			copy := info
			items = append(items, trafficItem{Type: "port_forward", PortForward: &copy})
		}
	}
	return items
}

func validTrafficType(value string) bool {
	switch value {
	case "exchange", "mirror", "preview", "port_forward":
		return true
	default:
		return false
	}
}

func trafficTypeError(optional bool) error {
	message := "type must be exchange, mirror, preview, or port_forward"
	if optional {
		message += " when provided"
	}
	return fmt.Errorf("%s", message)
}

func toPortMappings(ports []portMappingIn) []intercept.PortMapping {
	out := make([]intercept.PortMapping, 0, len(ports))
	for _, port := range ports {
		out = append(out, intercept.PortMapping{
			ServicePort: port.ServicePort,
			Protocol:    port.Protocol,
			LocalHost:   port.LocalHost,
			LocalPort:   port.LocalPort,
		})
	}
	return out
}
