package singbox

import (
	"slices"
	"testing"
)

func TestBuildRouteRulesGroupsClusterRoutes(t *testing.T) {
	t.Parallel()
	routes := []string{"10.96.0.0/12", "10.244.0.0/16", "10.245.7.9/32"}
	rules := buildRouteRules(routes, []string{"cluster.local"})

	rejectRules := 0
	clusterRejectIndex := -1
	clusterRouteRules := 0
	privateLocalIndex := -1
	for index, rule := range rules {
		if rule[configActionKey] == rejectRouteAction {
			rejectRules++
		}
		if rule["ip_is_private"] == true {
			privateLocalIndex = index
		}
		ipCIDRs, ok := rule[configIPCIDRKey].([]string)
		if !ok || !slices.Equal(ipCIDRs, routes) {
			continue
		}
		switch {
		case rule[configActionKey] == rejectRouteAction:
			clusterRejectIndex = index
			users, _ := rule[configAuthUserKey].([]string)
			if !slices.Equal(users, localTrafficUsers()) {
				t.Fatalf("cluster reject auth users = %v", users)
			}
		case rule[configOutboundKey] == KubernetesOutbound:
			clusterRouteRules++
		}
	}

	if rejectRules != 2 {
		t.Fatalf("reject rule count = %d, want 2", rejectRules)
	}
	if clusterRejectIndex < 0 || clusterRejectIndex >= privateLocalIndex {
		t.Fatalf("cluster reject index = %d, private-local index = %d", clusterRejectIndex, privateLocalIndex)
	}
	if clusterRouteRules != 1 {
		t.Fatalf("cluster route rule count = %d, want 1", clusterRouteRules)
	}
}
