package mcp

import (
	"context"
	"fmt"

	"github.com/fengqi-dev/kube-loop/internal/intercept"
	"github.com/fengqi-dev/kube-loop/internal/portfwd"
)

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
