package store

func cloneState(state State) State {
	out := State{
		Version: state.Version,
		UI: UIState{
			LastContext:     state.UI.LastContext,
			LastNamespace:   state.UI.LastNamespace,
			KubeconfigFiles: cloneStrings(state.UI.KubeconfigFiles),
		},
		MCP:      normalizeMCP(state.MCP),
		Clusters: make(map[string]*ClusterState, len(state.Clusters)),
	}
	for name, item := range state.Clusters {
		if item == nil {
			continue
		}
		copyItem := cloneCluster(*item)
		out.Clusters[name] = &copyItem
	}
	return out
}

func cloneStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, len(items))
	copy(out, items)
	return out
}

func cloneCluster(item ClusterState) ClusterState {
	out := ClusterState{
		Namespace:      item.Namespace,
		ConnectionMode: item.ConnectionMode,
		Connected:      item.Connected,
		PortForwards:   clonePortForwards(item.PortForwards),
		Exchanges:      cloneExchanges(item.Exchanges),
		Mirrors:        cloneMirrors(item.Mirrors),
		Previews:       clonePreviews(item.Previews),
		HostAliases:    cloneHostAliases(item.HostAliases),
	}
	if item.ManualNetwork != nil {
		copyItem := cloneManualNetwork(*item.ManualNetwork)
		out.ManualNetwork = &copyItem
	}
	return out
}

func cloneHostAliases(items []HostAliasSpec) []HostAliasSpec {
	if len(items) == 0 {
		return nil
	}
	out := make([]HostAliasSpec, len(items))
	copy(out, items)
	return out
}

func cloneManualNetwork(item ManualNetwork) ManualNetwork {
	return ManualNetwork{
		PodCIDRs:       cloneStrings(item.PodCIDRs),
		ServiceCIDRs:   cloneStrings(item.ServiceCIDRs),
		DNSServer:      item.DNSServer,
		ClusterDomains: cloneStrings(item.ClusterDomains),
		DNSNamespace:   item.DNSNamespace,
	}
}

func clonePortForwards(items []PortForwardSpec) []PortForwardSpec {
	if len(items) == 0 {
		return nil
	}
	out := make([]PortForwardSpec, len(items))
	copy(out, items)
	return out
}

func cloneExchanges(items []ExchangeSpec) []ExchangeSpec {
	if len(items) == 0 {
		return nil
	}
	out := make([]ExchangeSpec, len(items))
	for i, item := range items {
		out[i] = ExchangeSpec{
			Namespace: item.Namespace,
			Service:   item.Service,
			Ports:     clonePortMappings(item.Ports),
		}
	}
	return out
}

func cloneMirrors(items []MirrorSpec) []MirrorSpec {
	if len(items) == 0 {
		return nil
	}
	out := make([]MirrorSpec, len(items))
	for i, item := range items {
		out[i] = MirrorSpec{
			Namespace: item.Namespace,
			Service:   item.Service,
			Ports:     clonePortMappings(item.Ports),
		}
	}
	return out
}

func clonePreviews(items []PreviewSpec) []PreviewSpec {
	if len(items) == 0 {
		return nil
	}
	out := make([]PreviewSpec, len(items))
	for i, item := range items {
		out[i] = PreviewSpec{
			Namespace: item.Namespace,
			Name:      item.Name,
			Ports:     clonePortMappings(item.Ports),
		}
	}
	return out
}

func clonePortMappings(items []PortMapping) []PortMapping {
	if len(items) == 0 {
		return nil
	}
	out := make([]PortMapping, len(items))
	copy(out, items)
	return out
}
