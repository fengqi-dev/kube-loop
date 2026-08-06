package mcp

import (
	"context"
	"fmt"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/filemanager"
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

type getSingBoxDNSConfigIn struct{}

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
		Name:        "get_singbox_dns_config",
		Description: "Get the native DNS object from the active connected session's generated sing-box config.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, _ getSingBoxDNSConfigIn) (*mcpsdk.CallToolResult, singBoxDNSConfigOut, error) {
		out, err := getSingBoxDNSConfig(backend)
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
		out, err := backend.ExecPodCommand(ctx, PodCommandRequest(in))
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
