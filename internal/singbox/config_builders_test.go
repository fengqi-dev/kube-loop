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
	clusterRouteRuleCount := 0
	localLogicalIndex := -1
	localSubRules := 0
	kubernetesLogicalIndex := -1
	for index, rule := range rules {
		if rule[configActionKey] == rejectRouteAction {
			rejectRules++
		}
		ipCIDRs, ok := rule[configIPCIDRKey].([]string)
		if ok && slices.Equal(ipCIDRs, routes) && rule[configActionKey] == rejectRouteAction {
			clusterRejectIndex = index
			users, _ := rule[configAuthUserKey].([]string)
			if !slices.Equal(users, []string{TrafficLocalUser}) {
				t.Fatalf("cluster reject auth users = %v", users)
			}
		}
		if rule[configTypeKey] != logicalRuleType {
			continue
		}
		subRules, _ := rule[configRulesKey].([]map[string]any)
		switch rule[configOutboundKey] {
		case LocalOutbound:
			localLogicalIndex = index
			for _, subRule := range subRules {
				users, _ := subRule[configAuthUserKey].([]string)
				if !slices.Equal(users, []string{TrafficLocalUser}) {
					t.Fatalf("logical sub-rule auth users = %v", users)
				}
				if subRule["ip_is_private"] == true {
					localSubRules++
				}
			}
		case KubernetesOutbound:
			kubernetesLogicalIndex = index
			for _, subRule := range subRules {
				ipCIDRs, _ := subRule[configIPCIDRKey].([]string)
				if slices.Equal(ipCIDRs, routes) {
					clusterRouteRuleCount++
				}
			}
		}
	}

	ipCIDRs, ok := rules[clusterRejectIndex][configIPCIDRKey].([]string)
	if !ok || !slices.Equal(ipCIDRs, routes) {
		t.Fatalf("cluster reject rule ip_cidr = %v", rules[clusterRejectIndex][configIPCIDRKey])
	}
	users, _ := rules[clusterRejectIndex][configAuthUserKey].([]string)
	if !slices.Equal(users, []string{TrafficLocalUser}) {
		t.Fatalf("cluster reject auth users = %v", users)
	}

	if rejectRules != 2 {
		t.Fatalf("reject rule count = %d, want 2", rejectRules)
	}
	if clusterRejectIndex < 0 || clusterRejectIndex >= localLogicalIndex {
		t.Fatalf("cluster reject index = %d, local-logical index = %d", clusterRejectIndex, localLogicalIndex)
	}
	if kubernetesLogicalIndex <= localLogicalIndex {
		t.Fatalf(
			"kubernetes-logical index = %d, must be after local-logical index %d",
			kubernetesLogicalIndex, localLogicalIndex,
		)
	}
	if localSubRules != 1 {
		t.Fatalf("logical sub-rules with ip_is_private = %d, want 1", localSubRules)
	}
	if clusterRouteRuleCount != 1 {
		t.Fatalf("kubernetes logical route rule count = %d, want 1", clusterRouteRuleCount)
	}
}
