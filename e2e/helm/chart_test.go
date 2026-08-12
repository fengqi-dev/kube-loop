package helm

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestSQLiteChartRendersIndependentSecureWorkloads(t *testing.T) {
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "ingress.enabled=true",
		"--set", "ingress.host=kubeloop.example.test",
	)
	controlPlanes := objectsByComponent(t, objects, "Deployment", "control-plane")
	dataPlanes := objectsByComponent(t, objects, "Deployment", "data-plane")
	operators := objectsByComponent(t, objects, "Deployment", "operator")
	if len(controlPlanes) != 1 || len(dataPlanes) != 1 || len(operators) != 1 {
		t.Fatalf("controlPlane deployments = %d, data-plane deployments = %d, operator deployments = %d", len(controlPlanes), len(dataPlanes), len(operators))
	}
	controlPlane := controlPlanes[0]
	dataPlane := dataPlanes[0]
	operator := operators[0]
	if valueAt(t, controlPlane, "spec", "strategy", "type") != "Recreate" {
		t.Fatal("SQLite ControlPlane must use Recreate strategy")
	}
	if valueAt(t, controlPlane, "spec", "replicas") != 1 {
		t.Fatalf("SQLite ControlPlane replicas = %#v", valueAt(t, controlPlane, "spec", "replicas"))
	}
	if valueAt(t, dataPlane, "spec", "strategy", "type") != "RollingUpdate" {
		t.Fatal("Data Plane must use RollingUpdate independently of ControlPlane storage")
	}
	if valueAt(t, controlPlane, "spec", "template", "spec", "automountServiceAccountToken") != true {
		t.Fatal("ControlPlane requires its own Kubernetes service account token")
	}
	if valueAt(t, dataPlane, "spec", "template", "spec", "automountServiceAccountToken") != false {
		t.Fatal("Data Plane must not mount a Kubernetes service account token")
	}
	if valueAt(t, operator, "spec", "template", "spec", "automountServiceAccountToken") != true {
		t.Fatal("Operator requires its isolated Kubernetes service account token")
	}
	if countKind(objects, "CustomResourceDefinition") != 1 {
		t.Fatalf("TrafficBinding CRD count = %d", countKind(objects, "CustomResourceDefinition"))
	}
	if countKind(objects, "PersistentVolumeClaim") != 1 {
		t.Fatalf("SQLite PVC count = %d", countKind(objects, "PersistentVolumeClaim"))
	}
	if countKind(objects, "ConfigMap") != 3 {
		t.Fatalf("component ConfigMap count = %d", countKind(objects, "ConfigMap"))
	}
	controlPlaneConfig := objectByName(t, objects, "ConfigMap", "test-kubeloop-control-plane-config")
	dataPlaneConfig := objectByName(t, objects, "ConfigMap", "test-kubeloop-gateway-config")
	if valueAt(t, controlPlaneConfig, "data", "KUBELOOP_TUNNEL_PATH") != "/tunnel" ||
		valueAt(t, dataPlaneConfig, "data", "KUBELOOP_GATEWAY_HTTP_PATH") != "/tunnel" {
		t.Fatal("ControlPlane discovery and Data Plane ingress use different tunnel paths")
	}
	if valueAt(t, dataPlaneConfig, "data", "KUBELOOP_GATEWAY_STREAM_IDLE_TIMEOUT") != "30m" {
		t.Fatalf("Data Plane stream idle timeout = %#v", valueAt(t, dataPlaneConfig, "data", "KUBELOOP_GATEWAY_STREAM_IDLE_TIMEOUT"))
	}
	if valueAt(t, dataPlaneConfig, "data", "KUBELOOP_MIN_CLIENT_VERSION") != "" {
		t.Fatalf("Data Plane minimum client version = %#v", valueAt(t, dataPlaneConfig, "data", "KUBELOOP_MIN_CLIENT_VERSION"))
	}
	if valueAt(t, controlPlaneConfig, "data", "KUBELOOP_FILE_MAX_BYTES") != "1073741824" ||
		valueAt(t, controlPlaneConfig, "data", "KUBELOOP_FILE_ALLOWED_ROOTS_JSON") != `["/"]` {
		t.Fatalf("ControlPlane file limits = %#v", valueAt(t, controlPlaneConfig, "data"))
	}
	if !containerHasEnvironment(t, controlPlane, "KUBELOOP_SQLITE_PATH") {
		t.Fatal("ControlPlane is missing SQLite configuration")
	}
	controlPlaneYAML, _ := yaml.Marshal(controlPlane)
	if !strings.Contains(string(controlPlaneYAML), "--session-ttl=2m") ||
		!strings.Contains(string(controlPlaneYAML), "--session-max-lifetime=8h") ||
		!strings.Contains(string(controlPlaneYAML), "--maintenance-interval=1m") ||
		!strings.Contains(string(controlPlaneYAML), "--maintenance-batch-size=100") ||
		!strings.Contains(string(controlPlaneYAML), "--log-level=info") {
		t.Fatal("ControlPlane is missing bounded Cluster Session lifetime configuration")
	}
	dataPlaneYAML, _ := yaml.Marshal(dataPlane)
	if !strings.Contains(string(dataPlaneYAML), "--log-level=info") {
		t.Fatal("Data Plane is missing its configured log level")
	}
	if containerHasEnvironment(t, dataPlane, "KUBELOOP_SQLITE_PATH") || containerHasEnvironment(t, dataPlane, "KUBELOOP_POSTGRESQL_DSN") {
		t.Fatal("Data Plane received ControlPlane database configuration")
	}
	if strings.Contains(string(dataPlaneYAML), "KUBELOOP_GATEWAY_TOKEN") || strings.Contains(string(dataPlaneYAML), "legacy-tcp") {
		t.Fatal("Data Plane still exposes legacy token or raw TCP compatibility paths")
	}
	if !strings.Contains(string(dataPlaneYAML), "relay-identity") ||
		!strings.Contains(string(dataPlaneYAML), "audience: kubeloop-relay") ||
		!strings.Contains(string(dataPlaneYAML), "--relay-replay-entries=65536") ||
		!strings.Contains(string(dataPlaneYAML), "--max-websocket-sessions-per-user=8") ||
		!strings.Contains(string(dataPlaneYAML), "--max-websocket-frame-bytes=1048576") ||
		!strings.Contains(string(dataPlaneYAML), "--websocket-handshake-timeout=10s") ||
		strings.Contains(string(dataPlaneYAML), "relay-verification-keys") {
		t.Fatal("Data Plane is missing dynamic Relay registration, projected identity, or replay protection")
	}
	if !strings.Contains(string(controlPlaneYAML), "relay/signing-key.pem") ||
		strings.Contains(string(dataPlaneYAML), "relay/signing-key.pem") {
		t.Fatal("RelayTicket private signing key is not isolated to ControlPlane")
	}
	if countKind(objects, "Ingress") != 1 {
		t.Fatal("expected one same-origin Ingress")
	}
	ingress := objectByName(t, objects, "Ingress", "test-kubeloop")
	ingressYAML, _ := yaml.Marshal(ingress)
	for _, want := range []string{
		"host: kubeloop.example.test", "path: /.well-known", "path: /auth",
		"path: /kubeloop/api", "path: /tunnel", "path: /traffic/v1", "pathType: Prefix", "tls:",
	} {
		if !strings.Contains(string(ingressYAML), want) {
			t.Fatalf("same-origin Ingress is missing %q: %s", want, ingressYAML)
		}
	}
	controlPlaneService := objectByName(t, objects, "Service", "test-kubeloop-control-plane")
	dataPlaneService := objectByName(t, objects, "Service", "test-kubeloop-gateway")
	if serviceAppProtocol(t, controlPlaneService) != "http" || serviceAppProtocol(t, dataPlaneService) != "kubernetes.io/ws" {
		t.Fatalf("backend appProtocols = controlPlane %q, data plane %q", serviceAppProtocol(t, controlPlaneService), serviceAppProtocol(t, dataPlaneService))
	}
	if len(objectsByComponent(t, objects, "Service", "control-plane-relay-registry")) != 1 {
		t.Fatal("expected one internal Relay Registry Service")
	}
	registryService := objectByName(t, objects, "Service", "test-kubeloop-control-plane-relay")
	if valueAt(t, registryService, "spec", "type") != "ClusterIP" {
		t.Fatal("Relay Registry must remain ClusterIP-only")
	}
}

func TestGatewayAPIHTTPRouteUsesOneTLSOriginAndUnboundedWebSocketTimeout(t *testing.T) {
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "gatewayAPI.enabled=true",
		"--set", "gatewayAPI.host=kubeloop.example.test",
		"--set", "gatewayAPI.parentRef.name=shared-gateway",
		"--set", "gatewayAPI.parentRef.namespace=networking",
		"--set", "gatewayAPI.parentRef.sectionName=https",
	)
	if countKind(objects, "Ingress") != 0 || countKind(objects, "Gateway") != 0 || countKind(objects, "HTTPRoute") != 1 {
		t.Fatalf("external Gateway route kinds: Ingress=%d Gateway=%d HTTPRoute=%d", countKind(objects, "Ingress"), countKind(objects, "Gateway"), countKind(objects, "HTTPRoute"))
	}
	route := objectByName(t, objects, "HTTPRoute", "test-kubeloop")
	routeYAML, err := yaml.Marshal(route)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"apiVersion: gateway.networking.k8s.io/v1", "name: shared-gateway",
		"namespace: networking", "sectionName: https", "kubeloop.example.test",
		"value: /.well-known", "value: /auth", "value: /kubeloop/api", "value: /tunnel", "value: /traffic/v1",
		"name: test-kubeloop-control-plane", "name: test-kubeloop-gateway",
		"request: 30s", "backendRequest: 30s", "request: 0s", "backendRequest: 0s",
	} {
		if !strings.Contains(string(routeYAML), want) {
			t.Fatalf("HTTPRoute is missing %q: %s", want, routeYAML)
		}
	}
}

func TestGatewayAPIChartCanOwnTheHTTPSGateway(t *testing.T) {
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "gatewayAPI.enabled=true",
		"--set", "gatewayAPI.host=kubeloop.example.test",
		"--set", "gatewayAPI.gateway.create=true",
		"--set", "gatewayAPI.gateway.className=example-gateway",
		"--set", "gatewayAPI.gateway.tls.secretName=kubeloop-public-tls",
	)
	if countKind(objects, "Gateway") != 1 || countKind(objects, "HTTPRoute") != 1 || countKind(objects, "Ingress") != 0 {
		t.Fatalf("owned Gateway route kinds: Gateway=%d HTTPRoute=%d Ingress=%d", countKind(objects, "Gateway"), countKind(objects, "HTTPRoute"), countKind(objects, "Ingress"))
	}
	gateway := objectByName(t, objects, "Gateway", "test-kubeloop")
	gatewayYAML, err := yaml.Marshal(gateway)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"gatewayClassName: example-gateway", "hostname: kubeloop.example.test",
		"port: 443", "protocol: HTTPS", "mode: Terminate", "name: kubeloop-public-tls",
	} {
		if !strings.Contains(string(gatewayYAML), want) {
			t.Fatalf("Gateway is missing %q: %s", want, gatewayYAML)
		}
	}
}

func TestRuntimeSecurityBaselineIsAppliedToEveryWorkload(t *testing.T) {
	objects := renderChart(t, "--set", "publicURL=https://kubeloop.example.test")
	for _, component := range []string{"control-plane", "data-plane", "operator"} {
		deployments := objectsByComponent(t, objects, "Deployment", component)
		if len(deployments) != 1 {
			t.Fatalf("%s deployments = %d", component, len(deployments))
		}
		assertRestrictedPodAndContainerSecurity(t, deployments[0])
	}
	if countKind(objects, "NetworkPolicy") != 2 {
		t.Fatalf("default ingress NetworkPolicy count = %d", countKind(objects, "NetworkPolicy"))
	}
	if countKind(objects, "PodDisruptionBudget") != 0 {
		t.Fatal("SQLite mode must not create a PodDisruptionBudget")
	}
	if countKind(objects, "ServiceMonitor") != 0 {
		t.Fatal("ServiceMonitor must remain opt-in")
	}
}

func TestEveryWorkloadUsesItsDocumentedHealthContract(t *testing.T) {
	objects := renderChart(t, "--set", "publicURL=https://kubeloop.example.test")
	tests := []struct {
		component      string
		livenessPath   string
		readinessPath  string
		operationsPort string
	}{
		{component: "control-plane", livenessPath: "/health/live", readinessPath: "/health/ready", operationsPort: "http"},
		{component: "data-plane", livenessPath: "/health/live", readinessPath: "/health/ready", operationsPort: "http"},
		{component: "operator", livenessPath: "/healthz", readinessPath: "/readyz", operationsPort: "health"},
	}
	for _, test := range tests {
		t.Run(test.component, func(t *testing.T) {
			deployments := objectsByComponent(t, objects, "Deployment", test.component)
			if len(deployments) != 1 {
				t.Fatalf("deployments = %d", len(deployments))
			}
			assertHTTPProbe(t, deployments[0], "livenessProbe", test.livenessPath, test.operationsPort)
			assertHTTPProbe(t, deployments[0], "readinessProbe", test.readinessPath, test.operationsPort)
		})
	}
}

func TestRestrictedEgressRequiresExplicitPerComponentAllowRules(t *testing.T) {
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "networkPolicy.egress.enabled=true",
		"--set-json", `networkPolicy.egress.controlPlane=[{"to":[{"ipBlock":{"cidr":"10.96.0.1/32"}}],"ports":[{"protocol":"TCP","port":443}]}]`,
		"--set-json", `networkPolicy.egress.dataPlane=[{"to":[{"ipBlock":{"cidr":"10.244.0.0/16","except":["10.244.0.10/32"]}}]}]`,
		"--set-json", `networkPolicy.egress.operator=[{"to":[{"ipBlock":{"cidr":"10.96.0.1/32"}}],"ports":[{"protocol":"TCP","port":443}]}]`,
	)
	if countKind(objects, "NetworkPolicy") != 3 {
		t.Fatalf("restricted NetworkPolicy count = %d", countKind(objects, "NetworkPolicy"))
	}
	for _, name := range []string{"test-kubeloop-control-plane", "test-kubeloop-gateway", "test-kubeloop-operator"} {
		policy := objectByName(t, objects, "NetworkPolicy", name)
		policyTypes, ok := valueAt(t, policy, "spec", "policyTypes").([]any)
		if !ok || !containsYAMLString(policyTypes, "Egress") {
			t.Fatalf("%s does not enforce Egress: %#v", name, policyTypes)
		}
	}
	dataPlanePolicy, err := yaml.Marshal(objectByName(t, objects, "NetworkPolicy", "test-kubeloop-gateway"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"10.244.0.0/16", "10.244.0.10/32", "port: 9443", "port: 53"} {
		if !strings.Contains(string(dataPlanePolicy), want) {
			t.Fatalf("Data Plane egress policy is missing %q: %s", want, dataPlanePolicy)
		}
	}
	if strings.Contains(string(dataPlanePolicy), "10.96.0.1/32") || strings.Contains(string(dataPlanePolicy), "database") || strings.Contains(string(dataPlanePolicy), "secret") {
		t.Fatalf("Data Plane egress policy received Kubernetes API/database/Secret access: %s", dataPlanePolicy)
	}
}

func TestMonitoringTopologySpreadAndPostgreSQLPDBAreOptInAndScoped(t *testing.T) {
	spread := `[{"maxSkew":1,"topologyKey":"topology.kubernetes.io/zone","whenUnsatisfiable":"DoNotSchedule","labelSelector":{"matchLabels":{"app.kubernetes.io/name":"kubeloop"}}}]`
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controlPlane.storage.type=postgresql",
		"--set", "controlPlane.storage.postgresql.existingSecret=database",
		"--set", "controlPlane.replicas=3",
		"--set", "controlPlane.relayRegistry.enabled=false",
		"--set", "monitoring.serviceMonitor.enabled=true",
		"--set", "monitoring.serviceMonitor.labels.release=prometheus",
		"--set-json", "controlPlane.topologySpreadConstraints="+spread,
		"--set-json", "dataPlane.topologySpreadConstraints="+spread,
		"--set-json", "operator.topologySpreadConstraints="+spread,
	)
	if countKind(objects, "PodDisruptionBudget") != 1 || countKind(objects, "ServiceMonitor") != 1 {
		t.Fatalf("HA safety objects: PDB=%d ServiceMonitor=%d", countKind(objects, "PodDisruptionBudget"), countKind(objects, "ServiceMonitor"))
	}
	pdb := objectByName(t, objects, "PodDisruptionBudget", "test-kubeloop-control-plane")
	if valueAt(t, pdb, "spec", "minAvailable") != 1 || valueAt(t, pdb, "metadata", "labels", "app.kubernetes.io/component") != "control-plane" {
		t.Fatalf("ControlPlane PDB = %#v", pdb)
	}
	monitor := objectByName(t, objects, "ServiceMonitor", "test-kubeloop-gateway")
	monitorYAML, err := yaml.Marshal(monitor)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"app.kubernetes.io/component: data-plane", "path: /metrics", "port: http", "interval: 30s", "scrapeTimeout: 10s", "release: prometheus"} {
		if !strings.Contains(string(monitorYAML), want) {
			t.Fatalf("ServiceMonitor is missing %q: %s", want, monitorYAML)
		}
	}
	for _, component := range []string{"control-plane", "data-plane", "operator"} {
		deployment := objectsByComponent(t, objects, "Deployment", component)[0]
		if !strings.Contains(mustYAML(t, deployment), "topologySpreadConstraints:") || !strings.Contains(mustYAML(t, deployment), "topology.kubernetes.io/zone") {
			t.Fatalf("%s Deployment is missing topology spread constraints", component)
		}
	}
}

func TestTrafficBindingCRDMatchesGeneratedManifest(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve chart test path")
	}
	root := filepath.Join(filepath.Dir(filename), "..", "..")
	generated, err := os.ReadFile(filepath.Join(root, "config", "crd", "bases", "traffic.kubeloop.io_trafficbindings.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	packaged, err := os.ReadFile(filepath.Join(root, "charts", "kubeloop", "crds", "traffic.kubeloop.io_trafficbindings.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated, packaged) {
		t.Fatal("Helm TrafficBinding CRD is stale; run make operator-manifests and copy the generated CRD into charts/kubeloop/crds")
	}
}

func TestOIDCConfigurationSeparatesPublicConfigAndSecrets(t *testing.T) {
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controlPlane.auth.providers[0].id=corporate",
		"--set", "controlPlane.auth.providers[0].type=oidc",
		"--set", "controlPlane.auth.providers[0].displayName=Corporate SSO",
		"--set", "controlPlane.auth.providers[0].oidc.issuer=https://login.example.test",
		"--set", "controlPlane.auth.providers[0].oidc.clientID=kubeloop",
		"--set", "controlPlane.auth.providers[0].oidc.existingSecret=kubeloop-oidc",
		"--set", "controlPlane.auth.providers[0].oidc.clientSecretKey=client-secret",
		"--set", "controlPlane.auth.providers[0].oidc.caKey=ca.crt",
		"--set", "controlPlane.auth.token.existingSecret=kubeloop-token",
	)
	controlPlane := objectsByComponent(t, objects, "Deployment", "control-plane")[0]
	dataPlane := objectsByComponent(t, objects, "Deployment", "data-plane")[0]
	authConfig := objectByName(t, objects, "ConfigMap", "test-kubeloop-control-plane-auth-config")
	authJSON, ok := valueAt(t, authConfig, "data", "auth.json").(string)
	if !ok {
		t.Fatalf("auth.json is not a string: %#v", valueAt(t, authConfig, "data", "auth.json"))
	}
	for _, want := range []string{
		`"issuer":"https://login.example.test"`,
		`"redirectUrl":"https://kubeloop.example.test/auth/callback/corporate"`,
		`"clientSecretFile":"/var/run/secrets/kubeloop/auth/corporate/client-secret"`,
		`"caFile":"/var/run/secrets/kubeloop/auth/corporate/ca.crt"`,
	} {
		if !strings.Contains(authJSON, want) {
			t.Fatalf("auth config missing %s: %s", want, authJSON)
		}
	}
	if strings.Contains(authJSON, "kubeloop-oidc") || strings.Contains(authJSON, "kubeloop-token") {
		t.Fatalf("Kubernetes Secret reference leaked into public config: %s", authJSON)
	}
	controlPlaneYAML, err := yaml.Marshal(controlPlane)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(controlPlaneYAML), "kubeloop-oidc") ||
		!strings.Contains(string(controlPlaneYAML), "kubeloop-token") ||
		!strings.Contains(string(controlPlaneYAML), "auth-secrets") {
		t.Fatal("ControlPlane does not project the OIDC Secret")
	}
	dataPlaneYAML, err := yaml.Marshal(dataPlane)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(dataPlaneYAML), "kubeloop-oidc") || strings.Contains(string(dataPlaneYAML), "auth-secrets") {
		t.Fatal("Data Plane received OIDC Secret configuration")
	}
}

func TestAnonymousAuthenticationRequiresExplicitMode(t *testing.T) {
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controlPlane.auth.developmentMode=true",
		"--set", "controlPlane.auth.token.existingSecret=kubeloop-token",
		"--set", "controlPlane.auth.providers[0].id=guest",
		"--set", "controlPlane.auth.providers[0].type=anonymous",
		"--set", "controlPlane.auth.providers[0].anonymous.subject=guest",
	)
	authConfig := objectByName(t, objects, "ConfigMap", "test-kubeloop-control-plane-auth-config")
	authJSON := valueAt(t, authConfig, "data", "auth.json").(string)
	for _, want := range []string{`"developmentMode":true`, `"type":"anonymous"`, `"subject":"guest"`} {
		if !strings.Contains(authJSON, want) {
			t.Fatalf("development auth config missing %s: %s", want, authJSON)
		}
	}
}

func TestGatewayPolicyRendersAsControlPlaneOnlyDenyByDefaultConfig(t *testing.T) {
	defaultObjects := renderChart(t, "--set", "publicURL=https://kubeloop.example.test")
	defaultConfig := objectByName(t, defaultObjects, "ConfigMap", "test-kubeloop-control-plane-auth-config")
	if got := valueAt(t, defaultConfig, "data", "policy.json"); got != `{"rules":[],"version":1}` {
		t.Fatalf("default policy.json = %#v", got)
	}

	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controlPlane.policy.rules[0].id=developers-read",
		"--set", "controlPlane.policy.rules[0].groups[0]=developers",
		"--set", "controlPlane.policy.rules[0].namespaces[0]=development",
		"--set", "controlPlane.policy.rules[0].operations[0]=list",
		"--set", "controlPlane.policy.rules[0].resourceKinds[0]=pods",
	)
	config := objectByName(t, objects, "ConfigMap", "test-kubeloop-control-plane-auth-config")
	policyJSON := valueAt(t, config, "data", "policy.json").(string)
	for _, want := range []string{
		`"id":"developers-read"`, `"groups":["developers"]`,
		`"namespaces":["development"]`, `"operations":["list"]`, `"resourceKinds":["pods"]`,
	} {
		if !strings.Contains(policyJSON, want) {
			t.Fatalf("policy config missing %s: %s", want, policyJSON)
		}
	}
	dataPlaneYAML, _ := yaml.Marshal(objectsByComponent(t, objects, "Deployment", "data-plane")[0])
	if strings.Contains(string(dataPlaneYAML), "policy.json") || strings.Contains(string(dataPlaneYAML), "developers-read") {
		t.Fatal("Data Plane received Gateway policy configuration")
	}
}

func TestManagementBootstrapAndBreakGlassRemainControlPlaneOnly(t *testing.T) {
	defaultObjects := renderChart(t, "--set", "publicURL=https://kubeloop.example.test")
	defaultConfig := objectByName(t, defaultObjects, "ConfigMap", "test-kubeloop-control-plane-auth-config")
	defaultJSON := valueAt(t, defaultConfig, "data", "management.json").(string)
	for _, want := range []string{
		`"subjects":[]`, `"groups":[]`, `"recoveryEnabled":false`,
		`"enabled":false`, `"secretAlias":""`, `"sessionTtl":""`,
		`"providerSecretAliases":{}`,
	} {
		if !strings.Contains(defaultJSON, want) {
			t.Fatalf("default management config missing %s: %s", want, defaultJSON)
		}
	}

	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controlPlane.management.bootstrap.subjects[0]=00000000-0000-4000-8000-000000000001",
		"--set", "controlPlane.management.bootstrap.groups[0]=platform-bootstrap",
		"--set", "controlPlane.management.breakGlass.enabled=true",
		"--set", "controlPlane.management.breakGlass.secretAlias=emergency",
		"--set", "controlPlane.management.breakGlass.sessionTTL=10m",
		"--set", "controlPlane.management.breakGlass.allowedSourceCIDRs[0]=10.0.0.0/8",
		"--set", "controlPlane.management.breakGlass.secretAliases.emergency.existingSecret=kubeloop-break-glass",
		"--set", "controlPlane.management.breakGlass.secretAliases.emergency.credentialKey=credential",
	)
	config := objectByName(t, objects, "ConfigMap", "test-kubeloop-control-plane-auth-config")
	managementJSON := valueAt(t, config, "data", "management.json").(string)
	for _, want := range []string{
		`"subjects":["00000000-0000-4000-8000-000000000001"]`, `"groups":["platform-bootstrap"]`,
		`"enabled":true`, `"secretAlias":"emergency"`, `"sessionTtl":"10m"`,
		`"secretFile":"/var/run/secrets/kubeloop/management/break-glass/emergency/credential"`,
		`"allowedSourceCidrs":["10.0.0.0/8"]`,
	} {
		if !strings.Contains(managementJSON, want) {
			t.Fatalf("management config missing %s: %s", want, managementJSON)
		}
	}
	if strings.Contains(managementJSON, "kubeloop-break-glass") || strings.Contains(managementJSON, `"credentialKey"`) {
		t.Fatalf("Kubernetes Secret reference leaked into management config: %s", managementJSON)
	}
	controlPlane := objectsByComponent(t, objects, "Deployment", "control-plane")[0]
	controlPlaneYAML, _ := yaml.Marshal(controlPlane)
	for _, want := range []string{"management-secrets", "kubeloop-break-glass", "break-glass/emergency/credential"} {
		if !strings.Contains(string(controlPlaneYAML), want) {
			t.Fatalf("ControlPlane management Secret projection missing %q: %s", want, controlPlaneYAML)
		}
	}
	for _, component := range []string{"data-plane", "operator"} {
		componentYAML, _ := yaml.Marshal(objectsByComponent(t, objects, "Deployment", component)[0])
		for _, forbidden := range []string{"management-secrets", "kubeloop-break-glass", "break-glass/emergency"} {
			if strings.Contains(string(componentYAML), forbidden) {
				t.Fatalf("%s received Management Plane Secret %q", component, forbidden)
			}
		}
	}
}

func TestManagedProviderSecretAliasesAreFixedControlPlaneOnlyProjections(t *testing.T) {
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controlPlane.auth.token.existingSecret=kubeloop-token",
		"--set", "controlPlane.management.providerSecretAliases.corporate.existingSecret=kubeloop-corporate",
		"--set", "controlPlane.management.providerSecretAliases.corporate.clientSecretKey=oidc-secret",
		"--set", "controlPlane.management.providerSecretAliases.corporate.caKey=ca.pem",
	)
	config := objectByName(t, objects, "ConfigMap", "test-kubeloop-control-plane-auth-config")
	managementJSON := valueAt(t, config, "data", "management.json").(string)
	for _, want := range []string{
		`"providerSecretAliases":{"corporate":`,
		`"clientSecretFile":"/var/run/secrets/kubeloop/management/providers/corporate/client-secret"`,
		`"caFile":"/var/run/secrets/kubeloop/management/providers/corporate/ca.crt"`,
	} {
		if !strings.Contains(managementJSON, want) {
			t.Fatalf("managed Provider config missing %s: %s", want, managementJSON)
		}
	}
	for _, forbidden := range []string{"kubeloop-corporate", "oidc-secret", "ca.pem"} {
		if strings.Contains(managementJSON, forbidden) {
			t.Fatalf("Kubernetes Secret detail %q leaked into management config", forbidden)
		}
	}
	controlPlaneYAML, _ := yaml.Marshal(objectsByComponent(t, objects, "Deployment", "control-plane")[0])
	for _, want := range []string{"kubeloop-corporate", "providers/corporate/client-secret", "providers/corporate/ca.crt", "kubeloop-token"} {
		if !strings.Contains(string(controlPlaneYAML), want) {
			t.Fatalf("ControlPlane managed Provider projection missing %q", want)
		}
	}
	for _, component := range []string{"data-plane", "operator"} {
		componentYAML, _ := yaml.Marshal(objectsByComponent(t, objects, "Deployment", component)[0])
		if strings.Contains(string(componentYAML), "kubeloop-corporate") || strings.Contains(string(componentYAML), "providers/corporate") {
			t.Fatalf("%s received managed Provider Secret", component)
		}
	}
}

func TestKubernetesProviderUsesControlPlaneServiceAccountWithoutDefaultImpersonation(t *testing.T) {
	objects := renderChart(t, "--set", "publicURL=https://kubeloop.example.test")
	config := objectByName(t, objects, "ConfigMap", "test-kubeloop-control-plane-auth-config")
	kubernetesJSON, ok := valueAt(t, config, "data", "kubernetes.json").(string)
	if !ok {
		t.Fatalf("kubernetes.json is not a string: %#v", valueAt(t, config, "data", "kubernetes.json"))
	}
	for _, want := range []string{`"enabled":false`, `"timeout":"15s"`, `"qps":20`, `"burst":40`} {
		if !strings.Contains(kubernetesJSON, want) {
			t.Fatalf("Kubernetes Provider config missing %s: %s", want, kubernetesJSON)
		}
	}
	role := objectByName(t, objects, "ClusterRole", "test-kubeloop-control-plane")
	roleYAML, err := yaml.Marshal(role)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(roleYAML), "impersonate") {
		t.Fatal("default ControlPlane RBAC grants impersonate")
	}
	for _, want := range []string{"namespaces", "nodes", "servicecidrs", "selfsubjectaccessreviews", "tokenreviews"} {
		if !strings.Contains(string(roleYAML), want) {
			t.Fatalf("ControlPlane platform RBAC missing %q: %s", want, roleYAML)
		}
	}
	trafficRole := objectByName(t, objects, "ClusterRole", "test-kubeloop-control-plane-traffic")
	assertRuleVerbs(t, trafficRole, "", "services", "get", "list")
	assertRuleVerbs(t, trafficRole, "", "endpoints", "get", "list")
	assertRuleVerbs(t, trafficRole, "discovery.k8s.io", "endpointslices", "get", "list")
	assertRuleVerbs(t, trafficRole, "traffic.kubeloop.io", "trafficbindings", "get", "list", "watch", "create", "delete")
	operatorRole := objectByName(t, objects, "ClusterRole", "test-kubeloop-operator")
	assertRuleVerbs(t, operatorRole, "", "services", "get", "list", "watch", "create", "update", "patch", "delete")
	assertRuleVerbs(t, operatorRole, "traffic.kubeloop.io", "trafficbindings/status", "get", "update", "patch")
	controlPlaneYAML, _ := yaml.Marshal(objectsByComponent(t, objects, "Deployment", "control-plane")[0])
	for _, want := range []string{"KUBELOOP_POD_NAME", "metadata.name"} {
		if !strings.Contains(string(controlPlaneYAML), want) {
			t.Fatalf("ControlPlane owner identity environment missing %q: %s", want, controlPlaneYAML)
		}
	}
	binding := objectByName(t, objects, "ClusterRoleBinding", "test-kubeloop-control-plane")
	if got := valueAt(t, binding, "subjects"); got == nil {
		t.Fatal("ControlPlane ClusterRoleBinding has no subject")
	}
	dataPlaneYAML, _ := yaml.Marshal(objectsByComponent(t, objects, "Deployment", "data-plane")[0])
	for _, want := range []string{"KUBELOOP_POD_IP", "status.podIP"} {
		if !strings.Contains(string(dataPlaneYAML), want) {
			t.Fatalf("Data Plane traffic listener identity environment missing %q: %s", want, dataPlaneYAML)
		}
	}
	if strings.Contains(string(dataPlaneYAML), "kubernetes.json") || strings.Contains(string(dataPlaneYAML), "KUBELOOP_KUBERNETES_CONFIG_FILE") {
		t.Fatal("Data Plane received ControlPlane Kubernetes Provider configuration")
	}
}

func TestDefaultRBACIsSplitAndExcludesDangerousPermissions(t *testing.T) {
	objects := renderChart(t, "--set", "publicURL=https://kubeloop.example.test")
	if countKind(objects, "ClusterRole") != 5 || countKind(objects, "ClusterRoleBinding") != 5 {
		t.Fatalf("cluster RBAC counts = roles %d, bindings %d", countKind(objects, "ClusterRole"), countKind(objects, "ClusterRoleBinding"))
	}
	if countKind(objects, "Role") != 2 || countKind(objects, "RoleBinding") != 2 {
		t.Fatalf("release namespace RBAC counts = roles %d, bindings %d", countKind(objects, "Role"), countKind(objects, "RoleBinding"))
	}

	groups := map[string]string{
		"test-kubeloop-control-plane":           "platform",
		"test-kubeloop-control-plane-inventory": "inventory",
		"test-kubeloop-control-plane-exec-file": "exec-file",
		"test-kubeloop-control-plane-traffic":   "traffic",
	}
	for name, group := range groups {
		role := objectByName(t, objects, "ClusterRole", name)
		if got := valueAt(t, role, "metadata", "labels", "app.kubernetes.io/part-of-permission-group"); got != group {
			t.Fatalf("ClusterRole %s permission group = %#v", name, got)
		}
		binding := objectByName(t, objects, "ClusterRoleBinding", name)
		assertControlPlaneOnlyBinding(t, binding)
	}
	assertControlPlaneOnlyBinding(t, objectByName(t, objects, "RoleBinding", "test-kubeloop-control-plane-relay-registry"))
	dnsRole := objectByNameNamespace(t, objects, "Role", "kube-system", "test-kubeloop-control-plane-dns-discovery")
	assertRuleVerbs(t, dnsRole, "", "services", "get")
	assertRuleVerbs(t, dnsRole, "", "configmaps", "get")
	dnsRoleYAML, _ := yaml.Marshal(dnsRole)
	for _, want := range []string{"resourceNames", "kube-dns", "coredns"} {
		if !strings.Contains(string(dnsRoleYAML), want) {
			t.Fatalf("DNS discovery Role missing %q: %s", want, dnsRoleYAML)
		}
	}
	assertControlPlaneOnlyBinding(t, objectByNameNamespace(t, objects, "RoleBinding", "kube-system", "test-kubeloop-control-plane-dns-discovery"))
	assertBindingSubject(t, objectByName(t, objects, "ClusterRoleBinding", "test-kubeloop-operator"), "test-kubeloop-operator")

	for _, object := range objects {
		kind, _ := object["kind"].(string)
		if kind != "Role" && kind != "ClusterRole" {
			continue
		}
		encoded, err := yaml.Marshal(object)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"secrets", "nodes/proxy", "impersonate", `"*"`, "'*'"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("%s contains forbidden RBAC token %q: %s", valueAt(t, object, "metadata", "name"), forbidden, encoded)
			}
		}
	}
}

func TestNamespaceScopedRBACConfinesWorkflowPermissions(t *testing.T) {
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controlPlane.rbac.scope=namespace",
		"--set", "controlPlane.rbac.namespaces[0]=team-a",
		"--set", "controlPlane.rbac.namespaces[1]=team-b",
	)
	if countKind(objects, "ClusterRole") != 2 || countKind(objects, "ClusterRoleBinding") != 2 {
		t.Fatalf("namespace mode cluster RBAC counts = roles %d, bindings %d", countKind(objects, "ClusterRole"), countKind(objects, "ClusterRoleBinding"))
	}
	if countKind(objects, "Role") != 8 || countKind(objects, "RoleBinding") != 8 {
		t.Fatalf("namespace mode Role counts = roles %d, bindings %d", countKind(objects, "Role"), countKind(objects, "RoleBinding"))
	}
	platformYAML, _ := yaml.Marshal(objectByName(t, objects, "ClusterRole", "test-kubeloop-control-plane"))
	for _, forbidden := range []string{"pods", "services", "endpoints", "pods/exec"} {
		if strings.Contains(string(platformYAML), forbidden) {
			t.Fatalf("namespace platform ClusterRole contains workload permission %q: %s", forbidden, platformYAML)
		}
	}
	for _, namespace := range []string{"team-a", "team-b"} {
		for _, name := range []string{
			"test-kubeloop-control-plane-inventory",
			"test-kubeloop-control-plane-exec-file",
			"test-kubeloop-control-plane-traffic",
		} {
			objectByNameNamespace(t, objects, "Role", namespace, name)
			binding := objectByNameNamespace(t, objects, "RoleBinding", namespace, name)
			assertControlPlaneOnlyBinding(t, binding)
			if got := valueAt(t, binding, "roleRef", "kind"); got != "Role" {
				t.Fatalf("RoleBinding %s/%s roleRef kind = %#v", namespace, name, got)
			}
		}
	}
}

func TestRBACPermissionGroupsCanBeDisabled(t *testing.T) {
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controlPlane.rbac.permissions.inventory.enabled=false",
		"--set", "controlPlane.rbac.permissions.execFile.enabled=false",
		"--set", "controlPlane.rbac.permissions.traffic.enabled=false",
	)
	if countKind(objects, "ClusterRole") != 2 || countKind(objects, "ClusterRoleBinding") != 2 {
		t.Fatalf("disabled workflow RBAC counts = roles %d, bindings %d", countKind(objects, "ClusterRole"), countKind(objects, "ClusterRoleBinding"))
	}
	if countKind(objects, "Role") != 2 || countKind(objects, "RoleBinding") != 2 {
		t.Fatalf("disabled workflow release RBAC counts = roles %d, bindings %d", countKind(objects, "Role"), countKind(objects, "RoleBinding"))
	}
}

func TestKubernetesImpersonationRendersOnlyExplicitMappings(t *testing.T) {
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controlPlane.kubernetes.impersonation.enabled=true",
		"--set", "controlPlane.kubernetes.impersonation.usernamePrefix=gateway:",
		"--set", "controlPlane.kubernetes.impersonation.groupMappings.engineering[0]=k8s:developers",
	)
	config := objectByName(t, objects, "ConfigMap", "test-kubeloop-control-plane-auth-config")
	kubernetesJSON := valueAt(t, config, "data", "kubernetes.json").(string)
	for _, want := range []string{`"enabled":true`, `"usernamePrefix":"gateway:"`, `"engineering":["k8s:developers"]`} {
		if !strings.Contains(kubernetesJSON, want) {
			t.Fatalf("Kubernetes impersonation config missing %s: %s", want, kubernetesJSON)
		}
	}
	roleYAML, _ := yaml.Marshal(objectByName(t, objects, "ClusterRole", "test-kubeloop-control-plane"))
	if strings.Contains(string(roleYAML), "impersonate") {
		t.Fatal("Helm chart inferred broad impersonate RBAC from application mappings")
	}
}

func TestPostgreSQLChartAllowsControlPlaneHAWithoutPVC(t *testing.T) {
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controlPlane.relayRegistry.enabled=false",
		"--set", "controlPlane.storage.type=postgresql",
		"--set", "controlPlane.storage.postgresql.existingSecret=database",
		"--set", "controlPlane.replicas=3",
	)
	controlPlane := objectsByComponent(t, objects, "Deployment", "control-plane")[0]
	if valueAt(t, controlPlane, "spec", "strategy", "type") != "RollingUpdate" {
		t.Fatal("PostgreSQL ControlPlane must use RollingUpdate")
	}
	if valueAt(t, controlPlane, "spec", "replicas") != 3 {
		t.Fatalf("PostgreSQL ControlPlane replicas = %#v", valueAt(t, controlPlane, "spec", "replicas"))
	}
	if countKind(objects, "PersistentVolumeClaim") != 0 {
		t.Fatal("PostgreSQL mode must not create a SQLite PVC")
	}
	if !containerHasEnvironment(t, controlPlane, "KUBELOOP_POSTGRESQL_DSN") {
		t.Fatal("ControlPlane is missing PostgreSQL DSN Secret reference")
	}
	controlPlaneYAML, _ := yaml.Marshal(controlPlane)
	for _, want := range []string{
		"KUBELOOP_POSTGRESQL_CONNECT_TIMEOUT", "KUBELOOP_POSTGRESQL_QUERY_TIMEOUT",
		"KUBELOOP_POSTGRESQL_MAX_OPEN_CONNECTIONS", "KUBELOOP_POSTGRESQL_MAX_IDLE_CONNECTIONS",
		"KUBELOOP_POSTGRESQL_CONNECTION_MAX_LIFETIME",
		"KUBELOOP_POSTGRESQL_TRANSACTION_MAX_RETRIES", "KUBELOOP_POSTGRESQL_TRANSACTION_RETRY_BACKOFF",
		"database", "dsn",
	} {
		if !strings.Contains(string(controlPlaneYAML), want) {
			t.Fatalf("PostgreSQL ControlPlane configuration missing %q: %s", want, controlPlaneYAML)
		}
	}
	dataPlaneYAML, _ := yaml.Marshal(objectsByComponent(t, objects, "Deployment", "data-plane")[0])
	if strings.Contains(string(dataPlaneYAML), "KUBELOOP_POSTGRESQL") || strings.Contains(string(dataPlaneYAML), "database") {
		t.Fatal("Data Plane received PostgreSQL configuration or Secret reference")
	}
}

func TestChartRejectsUnsafeStorageConfigurations(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing public URL", want: "publicURL is required"},
		{name: "public URL path", args: []string{"--set", "publicURL=https://kubeloop.example.test/base"}, want: "must be one HTTPS origin"},
		{name: "two external routes", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "ingress.enabled=true", "--set", "ingress.host=kubeloop.example.test", "--set", "gatewayAPI.enabled=true", "--set", "gatewayAPI.host=kubeloop.example.test"}, want: "mutually exclusive"},
		{name: "Ingress origin mismatch", args: []string{"--set", "publicURL=https://other.example.test", "--set", "ingress.enabled=true", "--set", "ingress.host=kubeloop.example.test"}, want: "exactly equal"},
		{name: "Ingress without TLS", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "ingress.enabled=true", "--set", "ingress.host=kubeloop.example.test", "--set", "ingress.tls.enabled=false"}, want: "requires TLS"},
		{name: "Gateway origin mismatch", args: []string{"--set", "publicURL=https://other.example.test", "--set", "gatewayAPI.enabled=true", "--set", "gatewayAPI.host=kubeloop.example.test"}, want: "exactly equal"},
		{name: "Gateway parent", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "gatewayAPI.enabled=true", "--set", "gatewayAPI.host=kubeloop.example.test"}, want: "parentRef.name is required"},
		{name: "Gateway class", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "gatewayAPI.enabled=true", "--set", "gatewayAPI.host=kubeloop.example.test", "--set", "gatewayAPI.gateway.create=true"}, want: "className is required"},
		{name: "Gateway TLS certificate", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "gatewayAPI.enabled=true", "--set", "gatewayAPI.host=kubeloop.example.test", "--set", "gatewayAPI.gateway.create=true", "--set", "gatewayAPI.gateway.className=example"}, want: "tls.secretName is required"},
		{name: "Gateway tunnel timeout", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "gatewayAPI.enabled=true", "--set", "gatewayAPI.host=kubeloop.example.test", "--set", "gatewayAPI.parentRef.name=shared", "--set", "gatewayAPI.parentRef.sectionName=https", "--set", "gatewayAPI.timeouts.tunnel=30m"}, want: "must be 0s"},
		{name: "restricted egress without rules", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "networkPolicy.egress.enabled=true"}, want: "egress.controlPlane must contain"},
		{name: "blocking ControlPlane PDB", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.storage.type=postgresql", "--set", "controlPlane.storage.postgresql.existingSecret=database", "--set", "controlPlane.replicas=2", "--set", "controlPlane.relayRegistry.enabled=false", "--set", "podDisruptionBudget.controlPlane.minAvailable=2"}, want: "must be at least 1 and lower"},
		{name: "SQLite replicas", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.replicas=2"}, want: "controlPlane.replicas must be 1"},
		{name: "PostgreSQL secret", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.storage.type=postgresql"}, want: "existingSecret is required"},
		{name: "PostgreSQL max open", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.storage.type=postgresql", "--set", "controlPlane.storage.postgresql.existingSecret=database", "--set", "controlPlane.storage.postgresql.maxOpenConnections=0"}, want: "maxOpenConnections must be positive"},
		{name: "PostgreSQL max idle", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.storage.type=postgresql", "--set", "controlPlane.storage.postgresql.existingSecret=database", "--set", "controlPlane.storage.postgresql.maxOpenConnections=2", "--set", "controlPlane.storage.postgresql.maxIdleConnections=3"}, want: "maxIdleConnections must not exceed"},
		{name: "PostgreSQL retries", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.storage.type=postgresql", "--set", "controlPlane.storage.postgresql.existingSecret=database", "--set", "controlPlane.storage.postgresql.transactionMaxRetries=11"}, want: "transactionMaxRetries must be between"},
		{name: "unknown storage", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.storage.type=mysql"}, want: "must be sqlite or postgresql"},
		{name: "ControlPlane relay secret", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set-string", "controlPlane.relay.existingSecret="}, want: "controlPlane.relay.existingSecret is required"},
		{name: "Data Plane relay secret", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.relayRegistry.enabled=false", "--set-string", "dataPlane.relay.existingSecret="}, want: "dataPlane.relay.existingSecret is required"},
		{name: "relay ID mismatch", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.relayRegistry.enabled=false", "--set", "dataPlane.relay.id=other"}, want: "must match"},
		{name: "invalid Registry auth", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.relayRegistry.authentication=password"}, want: "must be tokenreview or mtls"},
		{name: "multi-replica endpoint", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "dataPlane.replicas=2"}, want: "must contain {podName} or {podUID}"},
		{name: "in-memory Registry ControlPlane replicas", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.storage.type=postgresql", "--set", "controlPlane.storage.postgresql.existingSecret=database", "--set", "controlPlane.replicas=2"}, want: "in-memory Relay Registry"},
		{name: "empty file roots", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set-json", "controlPlane.files.allowedRoots=[]"}, want: "allowedRoots must contain"},
		{name: "invalid file size", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.files.maxBytes=0"}, want: "maxBytes must be positive"},
		{name: "per-user WSS capacity", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "dataPlane.relayRegistry.maxWebSocketSessions=2", "--set", "dataPlane.relayRegistry.maxWebSocketSessionsPerUser=3"}, want: "maxWebSocketSessionsPerUser"},
		{name: "WSS frame size", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "dataPlane.relayRegistry.maxWebSocketFrameBytes=1024"}, want: "maxWebSocketFrameBytes"},
		{name: "ControlPlane log level", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.logLevel=trace"}, want: "controlPlane.logLevel must be"},
		{name: "wildcard management bootstrap", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set-string", "controlPlane.management.bootstrap.subjects[0]=*"}, want: "must be an exact stable Principal UUID"},
		{name: "management recovery without identity", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.management.bootstrap.recoveryEnabled=true"}, want: "requires a bootstrap subject or group"},
		{name: "break-glass without alias", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.management.breakGlass.enabled=true"}, want: "secretAlias must be a valid stable alias"},
		{name: "unknown break-glass alias", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.management.breakGlass.enabled=true", "--set", "controlPlane.management.breakGlass.secretAlias=emergency"}, want: "must select a configured secretAliases entry"},
		{name: "disabled break-glass alias", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.management.breakGlass.secretAlias=emergency"}, want: "must be empty while break-glass is disabled"},
		{name: "Data Plane log level", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "dataPlane.logLevel=verbose"}, want: "dataPlane.logLevel must be"},
		{name: "invalid RBAC scope", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.rbac.scope=tenant"}, want: "controlPlane.rbac.scope must be cluster or namespace"},
		{name: "missing RBAC namespaces", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.rbac.scope=namespace"}, want: "controlPlane.rbac.namespaces must contain"},
		{name: "invalid RBAC namespace", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.rbac.scope=namespace", "--set", "controlPlane.rbac.namespaces[0]=Team_A"}, want: "contains invalid namespace"},
		{name: "duplicate RBAC namespace", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.rbac.scope=namespace", "--set", "controlPlane.rbac.namespaces[0]=team-a", "--set", "controlPlane.rbac.namespaces[1]=team-a"}, want: "contains duplicate namespace"},
		{name: "token signing secret", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.auth.providers[0].id=corp", "--set", "controlPlane.auth.providers[0].type=oidc", "--set", "controlPlane.auth.providers[0].oidc.issuer=https://login.example.test", "--set", "controlPlane.auth.providers[0].oidc.clientID=kubeloop", "--set", "controlPlane.auth.providers[0].oidc.existingSecret=kubeloop-oidc", "--set", "controlPlane.auth.providers[0].oidc.clientSecretKey=client-secret"}, want: "controlPlane.auth.token.existingSecret is required"},
		{name: "OIDC secret", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.auth.token.existingSecret=kubeloop-token", "--set", "controlPlane.auth.providers[0].id=corp", "--set", "controlPlane.auth.providers[0].type=oidc", "--set", "controlPlane.auth.providers[0].oidc.issuer=https://login.example.test", "--set", "controlPlane.auth.providers[0].oidc.clientID=kubeloop"}, want: "existingSecret is required"},
		{name: "unsupported auth type", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.auth.token.existingSecret=kubeloop-token", "--set", "controlPlane.auth.providers[0].id=corp", "--set", "controlPlane.auth.providers[0].type=saml"}, want: "must be oidc or anonymous"},
		{name: "anonymous without development mode", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.auth.token.existingSecret=kubeloop-token", "--set", "controlPlane.auth.providers[0].id=guest", "--set", "controlPlane.auth.providers[0].type=anonymous", "--set", "controlPlane.auth.providers[0].anonymous.subject=guest"}, want: "requires controlPlane.auth.developmentMode=true"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := helmCommand(t, test.args...)
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), test.want) {
				t.Fatalf("helm error = %v, output = %s", err, output)
			}
		})
	}
}

func renderChart(t *testing.T, args ...string) []map[string]any {
	t.Helper()
	command := helmCommand(t, args...)
	output, err := command.Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			t.Fatalf("helm template: %v: %s", err, exitError.Stderr)
		}
		t.Fatal(err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(output))
	var objects []map[string]any
	for {
		var object map[string]any
		if err := decoder.Decode(&object); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatal(err)
		}
		if len(object) > 0 {
			objects = append(objects, object)
		}
	}
	return objects
}

func helmCommand(t *testing.T, extra ...string) *exec.Cmd {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not installed")
	}
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve chart test path")
	}
	chart := filepath.Join(filepath.Dir(filename), "..", "..", "charts", "kubeloop")
	chartFingerprint(t, chart)
	args := []string{
		"template", "test", chart, "--namespace", "kubeloop-system", "--include-crds",
		"--set", "controlPlane.relay.existingSecret=test-relay-controlPlane",
		"--set", "dataPlane.relay.existingSecret=test-relay-data-plane",
	}
	args = append(args, extra...)
	return exec.Command(helm, args...)
}

// chartFingerprint makes Go's test cache observe every chart input even though
// Helm itself reads them in a child process.
func chartFingerprint(t *testing.T, chart string) {
	t.Helper()
	hash := sha256.New()
	err := filepath.WalkDir(chart, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, _ = hash.Write([]byte(path))
		_, _ = hash.Write(content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("chart fingerprint: %x", hash.Sum(nil))
}

func objectsByComponent(t *testing.T, objects []map[string]any, kind, component string) []map[string]any {
	t.Helper()
	var matched []map[string]any
	for _, object := range objects {
		if object["kind"] != kind {
			continue
		}
		if valueAt(t, object, "metadata", "labels", "app.kubernetes.io/component") == component {
			matched = append(matched, object)
		}
	}
	return matched
}

func countKind(objects []map[string]any, kind string) int {
	count := 0
	for _, object := range objects {
		if object["kind"] == kind {
			count++
		}
	}
	return count
}

func objectByName(t *testing.T, objects []map[string]any, kind, name string) map[string]any {
	t.Helper()
	for _, object := range objects {
		if object["kind"] == kind && valueAt(t, object, "metadata", "name") == name {
			return object
		}
	}
	t.Fatalf("%s %q not found", kind, name)
	return nil
}

func objectByNameNamespace(t *testing.T, objects []map[string]any, kind, namespace, name string) map[string]any {
	t.Helper()
	for _, object := range objects {
		if object["kind"] == kind && valueAt(t, object, "metadata", "name") == name && valueAt(t, object, "metadata", "namespace") == namespace {
			return object
		}
	}
	t.Fatalf("%s %q/%q not found", kind, namespace, name)
	return nil
}

func assertControlPlaneOnlyBinding(t *testing.T, binding map[string]any) {
	assertBindingSubject(t, binding, "test-kubeloop-control-plane")
}

func assertBindingSubject(t *testing.T, binding map[string]any, serviceAccount string) {
	t.Helper()
	subjects, ok := valueAt(t, binding, "subjects").([]any)
	if !ok || len(subjects) != 1 {
		t.Fatalf("binding subjects = %#v", valueAt(t, binding, "subjects"))
	}
	subject, ok := subjects[0].(map[string]any)
	if !ok || subject["kind"] != "ServiceAccount" || subject["name"] != serviceAccount || subject["namespace"] != "kubeloop-system" {
		t.Fatalf("binding subject = %#v", subjects[0])
	}
}

func assertRuleVerbs(t *testing.T, role map[string]any, apiGroup, resource string, expected ...string) {
	t.Helper()
	rules, ok := valueAt(t, role, "rules").([]any)
	if !ok {
		t.Fatalf("role rules = %#v", valueAt(t, role, "rules"))
	}
	for _, rawRule := range rules {
		rule, ok := rawRule.(map[string]any)
		if !ok {
			continue
		}
		groups, _ := rule["apiGroups"].([]any)
		resources, _ := rule["resources"].([]any)
		if !containsYAMLString(groups, apiGroup) || !containsYAMLString(resources, resource) {
			continue
		}
		verbs, _ := rule["verbs"].([]any)
		if len(verbs) != len(expected) {
			t.Fatalf("%s/%s verbs = %#v, want %v", apiGroup, resource, verbs, expected)
		}
		for _, verb := range expected {
			if !containsYAMLString(verbs, verb) {
				t.Fatalf("%s/%s verbs = %#v, missing %q", apiGroup, resource, verbs, verb)
			}
		}
		return
	}
	t.Fatalf("role %s has no rule for %s/%s", valueAt(t, role, "metadata", "name"), apiGroup, resource)
}

func containsYAMLString(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func valueAt(t *testing.T, value any, path ...string) any {
	t.Helper()
	current := value
	for _, key := range path {
		mapping, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("%v is not an object while resolving %v", current, path)
		}
		current, ok = mapping[key]
		if !ok {
			t.Fatalf("key %q missing while resolving %v", key, path)
		}
	}
	return current
}

func serviceAppProtocol(t *testing.T, service map[string]any) string {
	t.Helper()
	ports, ok := valueAt(t, service, "spec", "ports").([]any)
	if !ok || len(ports) != 1 {
		t.Fatalf("Service ports = %#v", valueAt(t, service, "spec", "ports"))
	}
	port, ok := ports[0].(map[string]any)
	if !ok {
		t.Fatalf("Service port = %#v", ports[0])
	}
	appProtocol, _ := port["appProtocol"].(string)
	return appProtocol
}

func assertRestrictedPodAndContainerSecurity(t *testing.T, deployment map[string]any) {
	t.Helper()
	podSpec := valueAt(t, deployment, "spec", "template", "spec")
	if valueAt(t, podSpec, "securityContext", "runAsNonRoot") != true ||
		valueAt(t, podSpec, "securityContext", "seccompProfile", "type") != "RuntimeDefault" {
		t.Fatalf("Pod security context = %#v", valueAt(t, podSpec, "securityContext"))
	}
	containers, ok := valueAt(t, podSpec, "containers").([]any)
	if !ok || len(containers) != 1 {
		t.Fatalf("containers = %#v", valueAt(t, podSpec, "containers"))
	}
	container, ok := containers[0].(map[string]any)
	if !ok {
		t.Fatalf("container = %#v", containers[0])
	}
	security := valueAt(t, container, "securityContext")
	if valueAt(t, security, "allowPrivilegeEscalation") != false || valueAt(t, security, "readOnlyRootFilesystem") != true {
		t.Fatalf("container security context = %#v", security)
	}
	dropped, ok := valueAt(t, security, "capabilities", "drop").([]any)
	if !ok || !containsYAMLString(dropped, "ALL") {
		t.Fatalf("dropped capabilities = %#v", dropped)
	}
}

func assertHTTPProbe(t *testing.T, deployment map[string]any, probeName, path, port string) {
	t.Helper()
	containers, ok := valueAt(t, deployment, "spec", "template", "spec", "containers").([]any)
	if !ok || len(containers) != 1 {
		t.Fatalf("containers = %#v", valueAt(t, deployment, "spec", "template", "spec", "containers"))
	}
	container, ok := containers[0].(map[string]any)
	if !ok {
		t.Fatalf("container = %#v", containers[0])
	}
	probe, ok := container[probeName].(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v", probeName, container[probeName])
	}
	httpGet, ok := probe["httpGet"].(map[string]any)
	if !ok || httpGet["path"] != path || httpGet["port"] != port {
		t.Fatalf("%s httpGet = %#v, want path=%q port=%q", probeName, httpGet, path, port)
	}
}

func mustYAML(t *testing.T, value any) string {
	t.Helper()
	raw, err := yaml.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func containerHasEnvironment(t *testing.T, deployment map[string]any, name string) bool {
	t.Helper()
	containers, ok := valueAt(t, deployment, "spec", "template", "spec", "containers").([]any)
	if !ok || len(containers) != 1 {
		t.Fatalf("unexpected containers: %#v", containers)
	}
	container, ok := containers[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected container: %#v", containers[0])
	}
	environment, _ := container["env"].([]any)
	for _, raw := range environment {
		variable, ok := raw.(map[string]any)
		if ok && variable["name"] == name {
			return true
		}
	}
	return false
}
