package helmchart

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
	controllers := objectsByComponent(t, objects, "Deployment", "controller")
	dataPlanes := objectsByComponent(t, objects, "Deployment", "data-plane")
	operators := objectsByComponent(t, objects, "Deployment", "operator")
	if len(controllers) != 1 || len(dataPlanes) != 1 || len(operators) != 1 {
		t.Fatalf("controller deployments = %d, data-plane deployments = %d, operator deployments = %d", len(controllers), len(dataPlanes), len(operators))
	}
	controller := controllers[0]
	dataPlane := dataPlanes[0]
	operator := operators[0]
	if valueAt(t, controller, "spec", "strategy", "type") != "Recreate" {
		t.Fatal("SQLite Controller must use Recreate strategy")
	}
	if valueAt(t, controller, "spec", "replicas") != 1 {
		t.Fatalf("SQLite Controller replicas = %#v", valueAt(t, controller, "spec", "replicas"))
	}
	if valueAt(t, dataPlane, "spec", "strategy", "type") != "RollingUpdate" {
		t.Fatal("Data Plane must use RollingUpdate independently of Controller storage")
	}
	if valueAt(t, controller, "spec", "template", "spec", "automountServiceAccountToken") != true {
		t.Fatal("Controller requires its own Kubernetes service account token")
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
	controllerConfig := objectByName(t, objects, "ConfigMap", "test-kubeloop-controller-config")
	dataPlaneConfig := objectByName(t, objects, "ConfigMap", "test-kubeloop-gateway-config")
	if valueAt(t, controllerConfig, "data", "KUBELOOP_TUNNEL_PATH") != "/tunnel" ||
		valueAt(t, dataPlaneConfig, "data", "KUBELOOP_GATEWAY_HTTP_PATH") != "/tunnel" {
		t.Fatal("Controller discovery and Data Plane ingress use different tunnel paths")
	}
	if valueAt(t, dataPlaneConfig, "data", "KUBELOOP_GATEWAY_STREAM_IDLE_TIMEOUT") != "30m" {
		t.Fatalf("Data Plane stream idle timeout = %#v", valueAt(t, dataPlaneConfig, "data", "KUBELOOP_GATEWAY_STREAM_IDLE_TIMEOUT"))
	}
	if valueAt(t, dataPlaneConfig, "data", "KUBELOOP_MIN_CLIENT_VERSION") != "" {
		t.Fatalf("Data Plane minimum client version = %#v", valueAt(t, dataPlaneConfig, "data", "KUBELOOP_MIN_CLIENT_VERSION"))
	}
	if valueAt(t, controllerConfig, "data", "KUBELOOP_FILE_MAX_BYTES") != "1073741824" ||
		valueAt(t, controllerConfig, "data", "KUBELOOP_FILE_ALLOWED_ROOTS_JSON") != `["/"]` {
		t.Fatalf("Controller file limits = %#v", valueAt(t, controllerConfig, "data"))
	}
	if !containerHasEnvironment(t, controller, "KUBELOOP_SQLITE_PATH") {
		t.Fatal("Controller is missing SQLite configuration")
	}
	controllerYAML, _ := yaml.Marshal(controller)
	if !strings.Contains(string(controllerYAML), "--session-ttl=2m") ||
		!strings.Contains(string(controllerYAML), "--session-max-lifetime=8h") ||
		!strings.Contains(string(controllerYAML), "--maintenance-interval=1m") ||
		!strings.Contains(string(controllerYAML), "--maintenance-batch-size=100") ||
		!strings.Contains(string(controllerYAML), "--log-level=info") {
		t.Fatal("Controller is missing bounded Cluster Session lifetime configuration")
	}
	dataPlaneYAML, _ := yaml.Marshal(dataPlane)
	if !strings.Contains(string(dataPlaneYAML), "--log-level=info") {
		t.Fatal("Data Plane is missing its configured log level")
	}
	if containerHasEnvironment(t, dataPlane, "KUBELOOP_SQLITE_PATH") || containerHasEnvironment(t, dataPlane, "KUBELOOP_POSTGRESQL_DSN") {
		t.Fatal("Data Plane received Controller database configuration")
	}
	if strings.Contains(string(dataPlaneYAML), "KUBELOOP_GATEWAY_TOKEN") || strings.Contains(string(dataPlaneYAML), "legacy-tcp") {
		t.Fatal("Data Plane still exposes static-token or raw TCP compatibility paths")
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
	if !strings.Contains(string(controllerYAML), "relay/signing-key.pem") ||
		strings.Contains(string(dataPlaneYAML), "relay/signing-key.pem") {
		t.Fatal("RelayTicket private signing key is not isolated to Controller")
	}
	if countKind(objects, "Ingress") != 1 {
		t.Fatal("expected one same-origin Ingress")
	}
	ingress := objectByName(t, objects, "Ingress", "test-kubeloop")
	ingressYAML, _ := yaml.Marshal(ingress)
	for _, want := range []string{
		"host: kubeloop.example.test", "path: /.well-known", "path: /auth",
		"path: /api", "path: /tunnel", "pathType: Prefix", "tls:",
	} {
		if !strings.Contains(string(ingressYAML), want) {
			t.Fatalf("same-origin Ingress is missing %q: %s", want, ingressYAML)
		}
	}
	controllerService := objectByName(t, objects, "Service", "test-kubeloop-controller")
	dataPlaneService := objectByName(t, objects, "Service", "test-kubeloop-gateway")
	if serviceAppProtocol(t, controllerService) != "http" || serviceAppProtocol(t, dataPlaneService) != "kubernetes.io/ws" {
		t.Fatalf("backend appProtocols = controller %q, data plane %q", serviceAppProtocol(t, controllerService), serviceAppProtocol(t, dataPlaneService))
	}
	if len(objectsByComponent(t, objects, "Service", "controller-relay-registry")) != 1 {
		t.Fatal("expected one internal Relay Registry Service")
	}
	registryService := objectByName(t, objects, "Service", "test-kubeloop-controller-relay")
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
		"value: /.well-known", "value: /auth", "value: /api", "value: /tunnel",
		"name: test-kubeloop-controller", "name: test-kubeloop-gateway",
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
	for _, component := range []string{"controller", "data-plane", "operator"} {
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
		{component: "controller", livenessPath: "/health/live", readinessPath: "/health/ready", operationsPort: "http"},
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
		"--set-json", `networkPolicy.egress.controller=[{"to":[{"ipBlock":{"cidr":"10.96.0.1/32"}}],"ports":[{"protocol":"TCP","port":443}]}]`,
		"--set-json", `networkPolicy.egress.dataPlane=[{"to":[{"ipBlock":{"cidr":"10.244.0.0/16","except":["10.244.0.10/32"]}}]}]`,
		"--set-json", `networkPolicy.egress.operator=[{"to":[{"ipBlock":{"cidr":"10.96.0.1/32"}}],"ports":[{"protocol":"TCP","port":443}]}]`,
	)
	if countKind(objects, "NetworkPolicy") != 3 {
		t.Fatalf("restricted NetworkPolicy count = %d", countKind(objects, "NetworkPolicy"))
	}
	for _, name := range []string{"test-kubeloop-controller", "test-kubeloop-gateway", "test-kubeloop-operator"} {
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
		"--set", "controller.storage.type=postgresql",
		"--set", "controller.storage.postgresql.existingSecret=database",
		"--set", "controller.replicas=3",
		"--set", "controller.relayRegistry.enabled=false",
		"--set", "monitoring.serviceMonitor.enabled=true",
		"--set", "monitoring.serviceMonitor.labels.release=prometheus",
		"--set-json", "controller.topologySpreadConstraints="+spread,
		"--set-json", "dataPlane.topologySpreadConstraints="+spread,
		"--set-json", "operator.topologySpreadConstraints="+spread,
	)
	if countKind(objects, "PodDisruptionBudget") != 1 || countKind(objects, "ServiceMonitor") != 1 {
		t.Fatalf("HA safety objects: PDB=%d ServiceMonitor=%d", countKind(objects, "PodDisruptionBudget"), countKind(objects, "ServiceMonitor"))
	}
	pdb := objectByName(t, objects, "PodDisruptionBudget", "test-kubeloop-controller")
	if valueAt(t, pdb, "spec", "minAvailable") != 1 || valueAt(t, pdb, "metadata", "labels", "app.kubernetes.io/component") != "controller" {
		t.Fatalf("Controller PDB = %#v", pdb)
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
	for _, component := range []string{"controller", "data-plane", "operator"} {
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
		"--set", "controller.auth.providers[0].id=corporate",
		"--set", "controller.auth.providers[0].type=oidc",
		"--set", "controller.auth.providers[0].displayName=Corporate SSO",
		"--set", "controller.auth.providers[0].oidc.issuer=https://login.example.test",
		"--set", "controller.auth.providers[0].oidc.clientID=kubeloop",
		"--set", "controller.auth.providers[0].oidc.existingSecret=kubeloop-oidc",
		"--set", "controller.auth.providers[0].oidc.clientSecretKey=client-secret",
		"--set", "controller.auth.providers[0].oidc.caKey=ca.crt",
		"--set", "controller.auth.token.existingSecret=kubeloop-token",
	)
	controller := objectsByComponent(t, objects, "Deployment", "controller")[0]
	dataPlane := objectsByComponent(t, objects, "Deployment", "data-plane")[0]
	authConfig := objectByName(t, objects, "ConfigMap", "test-kubeloop-controller-auth-config")
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
	controllerYAML, err := yaml.Marshal(controller)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(controllerYAML), "kubeloop-oidc") ||
		!strings.Contains(string(controllerYAML), "kubeloop-token") ||
		!strings.Contains(string(controllerYAML), "auth-secrets") {
		t.Fatal("Controller does not project the OIDC Secret")
	}
	dataPlaneYAML, err := yaml.Marshal(dataPlane)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(dataPlaneYAML), "kubeloop-oidc") || strings.Contains(string(dataPlaneYAML), "auth-secrets") {
		t.Fatal("Data Plane received OIDC Secret configuration")
	}
}

func TestADConfigurationSeparatesDirectorySecretsFromDataPlane(t *testing.T) {
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controller.auth.token.existingSecret=kubeloop-token",
		"--set", "controller.auth.providers[0].id=legacy-ad",
		"--set", "controller.auth.providers[0].type=ad",
		"--set", "controller.auth.providers[0].displayName=Corporate AD",
		"--set", "controller.auth.providers[0].ad.directoryID=corp.example",
		"--set", "controller.auth.providers[0].ad.url=ldaps://dc.corp.example:636",
		"--set", `controller.auth.providers[0].ad.baseDN=DC=corp\,DC=example`,
		"--set", `controller.auth.providers[0].ad.bindDN=CN=reader\,DC=corp\,DC=example`,
		"--set", "controller.auth.providers[0].ad.existingSecret=kubeloop-ad",
		"--set", "controller.auth.providers[0].ad.bindPasswordKey=bind-password",
		"--set", "controller.auth.providers[0].ad.caKey=ca.crt",
	)
	controller := objectsByComponent(t, objects, "Deployment", "controller")[0]
	dataPlane := objectsByComponent(t, objects, "Deployment", "data-plane")[0]
	authConfig := objectByName(t, objects, "ConfigMap", "test-kubeloop-controller-auth-config")
	authJSON := valueAt(t, authConfig, "data", "auth.json").(string)
	for _, want := range []string{
		`"type":"ad"`, `"directoryId":"corp.example"`,
		`"url":"ldaps://dc.corp.example:636"`,
		`"bindPasswordFile":"/var/run/secrets/kubeloop/auth/legacy-ad/bind-password"`,
		`"caFile":"/var/run/secrets/kubeloop/auth/legacy-ad/ca.crt"`,
	} {
		if !strings.Contains(authJSON, want) {
			t.Fatalf("AD auth config missing %s: %s", want, authJSON)
		}
	}
	if strings.Contains(authJSON, "kubeloop-ad") || strings.Contains(authJSON, "kubeloop-token") {
		t.Fatalf("AD Secret names leaked into ConfigMap: %s", authJSON)
	}
	controllerYAML, _ := yaml.Marshal(controller)
	if !strings.Contains(string(controllerYAML), "kubeloop-ad") || !strings.Contains(string(controllerYAML), "bind-password") {
		t.Fatal("Controller does not project AD bind/CA Secret")
	}
	dataPlaneYAML, _ := yaml.Marshal(dataPlane)
	if strings.Contains(string(dataPlaneYAML), "kubeloop-ad") || strings.Contains(string(dataPlaneYAML), "bind-password") {
		t.Fatal("Data Plane received AD Secret configuration")
	}
}

func TestDevelopmentAuthenticationRequiresExplicitModeAndIsControllerOnly(t *testing.T) {
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controller.auth.developmentMode=true",
		"--set", "controller.auth.token.existingSecret=kubeloop-token",
		"--set", "controller.auth.providers[0].id=local",
		"--set", "controller.auth.providers[0].type=static-token",
		"--set", "controller.auth.providers[0].staticToken.existingSecret=kubeloop-development",
		"--set", "controller.auth.providers[0].staticToken.tokenKey=token",
		"--set", "controller.auth.providers[0].staticToken.subject=developer",
		"--set", "controller.auth.providers[0].staticToken.groups[0]=developers",
		"--set", "controller.auth.providers[1].id=guest",
		"--set", "controller.auth.providers[1].type=anonymous",
		"--set", "controller.auth.providers[1].anonymous.subject=guest",
	)
	authConfig := objectByName(t, objects, "ConfigMap", "test-kubeloop-controller-auth-config")
	authJSON := valueAt(t, authConfig, "data", "auth.json").(string)
	for _, want := range []string{
		`"developmentMode":true`, `"type":"static-token"`,
		`"tokenFile":"/var/run/secrets/kubeloop/auth/local/static-token"`,
		`"type":"anonymous"`, `"subject":"guest"`,
	} {
		if !strings.Contains(authJSON, want) {
			t.Fatalf("development auth config missing %s: %s", want, authJSON)
		}
	}
	if strings.Contains(authJSON, "kubeloop-development") || strings.Contains(authJSON, `"token":"`) {
		t.Fatalf("development Secret leaked into ConfigMap: %s", authJSON)
	}
	controller := objectsByComponent(t, objects, "Deployment", "controller")[0]
	dataPlane := objectsByComponent(t, objects, "Deployment", "data-plane")[0]
	controllerYAML, _ := yaml.Marshal(controller)
	dataPlaneYAML, _ := yaml.Marshal(dataPlane)
	if !strings.Contains(string(controllerYAML), "kubeloop-development") ||
		!strings.Contains(string(controllerYAML), "local/static-token") {
		t.Fatal("Controller does not project the development static-token Secret")
	}
	if strings.Contains(string(dataPlaneYAML), "kubeloop-development") ||
		strings.Contains(string(dataPlaneYAML), "local/static-token") {
		t.Fatal("Data Plane received the development static-token Secret")
	}
}

func TestGatewayPolicyRendersAsControllerOnlyDenyByDefaultConfig(t *testing.T) {
	defaultObjects := renderChart(t, "--set", "publicURL=https://kubeloop.example.test")
	defaultConfig := objectByName(t, defaultObjects, "ConfigMap", "test-kubeloop-controller-auth-config")
	if got := valueAt(t, defaultConfig, "data", "policy.json"); got != `{"rules":[],"version":1}` {
		t.Fatalf("default policy.json = %#v", got)
	}

	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controller.policy.rules[0].id=developers-read",
		"--set", "controller.policy.rules[0].groups[0]=developers",
		"--set", "controller.policy.rules[0].namespaces[0]=development",
		"--set", "controller.policy.rules[0].operations[0]=list",
		"--set", "controller.policy.rules[0].resourceKinds[0]=pods",
	)
	config := objectByName(t, objects, "ConfigMap", "test-kubeloop-controller-auth-config")
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

func TestManagementBootstrapAndBreakGlassRemainControllerOnly(t *testing.T) {
	defaultObjects := renderChart(t, "--set", "publicURL=https://kubeloop.example.test")
	defaultConfig := objectByName(t, defaultObjects, "ConfigMap", "test-kubeloop-controller-auth-config")
	defaultJSON := valueAt(t, defaultConfig, "data", "management.json").(string)
	for _, want := range []string{
		`"subjects":[]`, `"groups":[]`, `"recoveryEnabled":false`,
		`"enabled":false`, `"secretAlias":""`, `"sessionTtl":"15m"`,
		`"providerSecretAliases":{}`,
	} {
		if !strings.Contains(defaultJSON, want) {
			t.Fatalf("default management config missing %s: %s", want, defaultJSON)
		}
	}

	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controller.management.bootstrap.subjects[0]=00000000-0000-4000-8000-000000000001",
		"--set", "controller.management.bootstrap.groups[0]=platform-bootstrap",
		"--set", "controller.management.breakGlass.enabled=true",
		"--set", "controller.management.breakGlass.secretAlias=emergency",
		"--set", "controller.management.breakGlass.sessionTTL=10m",
		"--set", "controller.management.breakGlass.allowedSourceCIDRs[0]=10.0.0.0/8",
		"--set", "controller.management.breakGlass.secretAliases.emergency.existingSecret=kubeloop-break-glass",
		"--set", "controller.management.breakGlass.secretAliases.emergency.credentialKey=credential",
	)
	config := objectByName(t, objects, "ConfigMap", "test-kubeloop-controller-auth-config")
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
	controller := objectsByComponent(t, objects, "Deployment", "controller")[0]
	controllerYAML, _ := yaml.Marshal(controller)
	for _, want := range []string{"management-secrets", "kubeloop-break-glass", "break-glass/emergency/credential"} {
		if !strings.Contains(string(controllerYAML), want) {
			t.Fatalf("Controller management Secret projection missing %q: %s", want, controllerYAML)
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

func TestManagedProviderSecretAliasesAreFixedControllerOnlyProjections(t *testing.T) {
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controller.auth.token.existingSecret=kubeloop-token",
		"--set", "controller.management.providerSecretAliases.corporate.existingSecret=kubeloop-corporate",
		"--set", "controller.management.providerSecretAliases.corporate.clientSecretKey=oidc-secret",
		"--set", "controller.management.providerSecretAliases.corporate.caKey=ca.pem",
	)
	config := objectByName(t, objects, "ConfigMap", "test-kubeloop-controller-auth-config")
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
	controllerYAML, _ := yaml.Marshal(objectsByComponent(t, objects, "Deployment", "controller")[0])
	for _, want := range []string{"kubeloop-corporate", "providers/corporate/client-secret", "providers/corporate/ca.crt", "kubeloop-token"} {
		if !strings.Contains(string(controllerYAML), want) {
			t.Fatalf("Controller managed Provider projection missing %q", want)
		}
	}
	for _, component := range []string{"data-plane", "operator"} {
		componentYAML, _ := yaml.Marshal(objectsByComponent(t, objects, "Deployment", component)[0])
		if strings.Contains(string(componentYAML), "kubeloop-corporate") || strings.Contains(string(componentYAML), "providers/corporate") {
			t.Fatalf("%s received managed Provider Secret", component)
		}
	}
}

func TestKubernetesProviderUsesControllerServiceAccountWithoutDefaultImpersonation(t *testing.T) {
	objects := renderChart(t, "--set", "publicURL=https://kubeloop.example.test")
	config := objectByName(t, objects, "ConfigMap", "test-kubeloop-controller-auth-config")
	kubernetesJSON, ok := valueAt(t, config, "data", "kubernetes.json").(string)
	if !ok {
		t.Fatalf("kubernetes.json is not a string: %#v", valueAt(t, config, "data", "kubernetes.json"))
	}
	for _, want := range []string{`"enabled":false`, `"timeout":"15s"`, `"qps":20`, `"burst":40`} {
		if !strings.Contains(kubernetesJSON, want) {
			t.Fatalf("Kubernetes Provider config missing %s: %s", want, kubernetesJSON)
		}
	}
	role := objectByName(t, objects, "ClusterRole", "test-kubeloop-controller")
	roleYAML, err := yaml.Marshal(role)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(roleYAML), "impersonate") {
		t.Fatal("default Controller RBAC grants impersonate")
	}
	for _, want := range []string{"namespaces", "nodes", "servicecidrs", "selfsubjectaccessreviews", "tokenreviews"} {
		if !strings.Contains(string(roleYAML), want) {
			t.Fatalf("Controller platform RBAC missing %q: %s", want, roleYAML)
		}
	}
	trafficRole := objectByName(t, objects, "ClusterRole", "test-kubeloop-controller-traffic")
	assertRuleVerbs(t, trafficRole, "", "services", "get", "list")
	assertRuleVerbs(t, trafficRole, "", "endpoints", "get", "list")
	assertRuleVerbs(t, trafficRole, "discovery.k8s.io", "endpointslices", "get", "list")
	assertRuleVerbs(t, trafficRole, "traffic.kubeloop.io", "trafficbindings", "get", "list", "watch", "create", "delete")
	operatorRole := objectByName(t, objects, "ClusterRole", "test-kubeloop-operator")
	assertRuleVerbs(t, operatorRole, "", "services", "get", "list", "watch", "create", "update", "patch", "delete")
	assertRuleVerbs(t, operatorRole, "traffic.kubeloop.io", "trafficbindings/status", "get", "update", "patch")
	controllerYAML, _ := yaml.Marshal(objectsByComponent(t, objects, "Deployment", "controller")[0])
	for _, want := range []string{"KUBELOOP_POD_IP", "status.podIP", "KUBELOOP_POD_NAME", "metadata.name"} {
		if !strings.Contains(string(controllerYAML), want) {
			t.Fatalf("Controller owner identity environment missing %q: %s", want, controllerYAML)
		}
	}
	binding := objectByName(t, objects, "ClusterRoleBinding", "test-kubeloop-controller")
	if got := valueAt(t, binding, "subjects"); got == nil {
		t.Fatal("Controller ClusterRoleBinding has no subject")
	}
	dataPlaneYAML, _ := yaml.Marshal(objectsByComponent(t, objects, "Deployment", "data-plane")[0])
	if strings.Contains(string(dataPlaneYAML), "kubernetes.json") || strings.Contains(string(dataPlaneYAML), "KUBELOOP_KUBERNETES_CONFIG_FILE") {
		t.Fatal("Data Plane received Controller Kubernetes Provider configuration")
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
		"test-kubeloop-controller":           "platform",
		"test-kubeloop-controller-inventory": "inventory",
		"test-kubeloop-controller-exec-file": "exec-file",
		"test-kubeloop-controller-traffic":   "traffic",
	}
	for name, group := range groups {
		role := objectByName(t, objects, "ClusterRole", name)
		if got := valueAt(t, role, "metadata", "labels", "app.kubernetes.io/part-of-permission-group"); got != group {
			t.Fatalf("ClusterRole %s permission group = %#v", name, got)
		}
		binding := objectByName(t, objects, "ClusterRoleBinding", name)
		assertControllerOnlyBinding(t, binding)
	}
	assertControllerOnlyBinding(t, objectByName(t, objects, "RoleBinding", "test-kubeloop-controller-relay-registry"))
	dnsRole := objectByNameNamespace(t, objects, "Role", "kube-system", "test-kubeloop-controller-dns-discovery")
	assertRuleVerbs(t, dnsRole, "", "services", "get")
	assertRuleVerbs(t, dnsRole, "", "configmaps", "get")
	dnsRoleYAML, _ := yaml.Marshal(dnsRole)
	for _, want := range []string{"resourceNames", "kube-dns", "coredns"} {
		if !strings.Contains(string(dnsRoleYAML), want) {
			t.Fatalf("DNS discovery Role missing %q: %s", want, dnsRoleYAML)
		}
	}
	assertControllerOnlyBinding(t, objectByNameNamespace(t, objects, "RoleBinding", "kube-system", "test-kubeloop-controller-dns-discovery"))
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
		"--set", "controller.rbac.scope=namespace",
		"--set", "controller.rbac.namespaces[0]=team-a",
		"--set", "controller.rbac.namespaces[1]=team-b",
	)
	if countKind(objects, "ClusterRole") != 2 || countKind(objects, "ClusterRoleBinding") != 2 {
		t.Fatalf("namespace mode cluster RBAC counts = roles %d, bindings %d", countKind(objects, "ClusterRole"), countKind(objects, "ClusterRoleBinding"))
	}
	if countKind(objects, "Role") != 8 || countKind(objects, "RoleBinding") != 8 {
		t.Fatalf("namespace mode Role counts = roles %d, bindings %d", countKind(objects, "Role"), countKind(objects, "RoleBinding"))
	}
	platformYAML, _ := yaml.Marshal(objectByName(t, objects, "ClusterRole", "test-kubeloop-controller"))
	for _, forbidden := range []string{"pods", "services", "endpoints", "pods/exec"} {
		if strings.Contains(string(platformYAML), forbidden) {
			t.Fatalf("namespace platform ClusterRole contains workload permission %q: %s", forbidden, platformYAML)
		}
	}
	for _, namespace := range []string{"team-a", "team-b"} {
		for _, name := range []string{
			"test-kubeloop-controller-inventory",
			"test-kubeloop-controller-exec-file",
			"test-kubeloop-controller-traffic",
		} {
			objectByNameNamespace(t, objects, "Role", namespace, name)
			binding := objectByNameNamespace(t, objects, "RoleBinding", namespace, name)
			assertControllerOnlyBinding(t, binding)
			if got := valueAt(t, binding, "roleRef", "kind"); got != "Role" {
				t.Fatalf("RoleBinding %s/%s roleRef kind = %#v", namespace, name, got)
			}
		}
	}
}

func TestRBACPermissionGroupsCanBeDisabled(t *testing.T) {
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controller.rbac.permissions.inventory.enabled=false",
		"--set", "controller.rbac.permissions.execFile.enabled=false",
		"--set", "controller.rbac.permissions.traffic.enabled=false",
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
		"--set", "controller.kubernetes.impersonation.enabled=true",
		"--set", "controller.kubernetes.impersonation.usernamePrefix=gateway:",
		"--set", "controller.kubernetes.impersonation.groupMappings.engineering[0]=k8s:developers",
	)
	config := objectByName(t, objects, "ConfigMap", "test-kubeloop-controller-auth-config")
	kubernetesJSON := valueAt(t, config, "data", "kubernetes.json").(string)
	for _, want := range []string{`"enabled":true`, `"usernamePrefix":"gateway:"`, `"engineering":["k8s:developers"]`} {
		if !strings.Contains(kubernetesJSON, want) {
			t.Fatalf("Kubernetes impersonation config missing %s: %s", want, kubernetesJSON)
		}
	}
	roleYAML, _ := yaml.Marshal(objectByName(t, objects, "ClusterRole", "test-kubeloop-controller"))
	if strings.Contains(string(roleYAML), "impersonate") {
		t.Fatal("Helm chart inferred broad impersonate RBAC from application mappings")
	}
}

func TestPostgreSQLChartAllowsControllerHAWithoutPVC(t *testing.T) {
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controller.relayRegistry.enabled=false",
		"--set", "controller.storage.type=postgresql",
		"--set", "controller.storage.postgresql.existingSecret=database",
		"--set", "controller.replicas=3",
	)
	controller := objectsByComponent(t, objects, "Deployment", "controller")[0]
	if valueAt(t, controller, "spec", "strategy", "type") != "RollingUpdate" {
		t.Fatal("PostgreSQL Controller must use RollingUpdate")
	}
	if valueAt(t, controller, "spec", "replicas") != 3 {
		t.Fatalf("PostgreSQL Controller replicas = %#v", valueAt(t, controller, "spec", "replicas"))
	}
	if countKind(objects, "PersistentVolumeClaim") != 0 {
		t.Fatal("PostgreSQL mode must not create a SQLite PVC")
	}
	if !containerHasEnvironment(t, controller, "KUBELOOP_POSTGRESQL_DSN") {
		t.Fatal("Controller is missing PostgreSQL DSN Secret reference")
	}
	controllerYAML, _ := yaml.Marshal(controller)
	for _, want := range []string{
		"KUBELOOP_POSTGRESQL_CONNECT_TIMEOUT", "KUBELOOP_POSTGRESQL_QUERY_TIMEOUT",
		"KUBELOOP_POSTGRESQL_MAX_OPEN_CONNECTIONS", "KUBELOOP_POSTGRESQL_MAX_IDLE_CONNECTIONS",
		"KUBELOOP_POSTGRESQL_CONNECTION_MAX_LIFETIME",
		"KUBELOOP_POSTGRESQL_TRANSACTION_MAX_RETRIES", "KUBELOOP_POSTGRESQL_TRANSACTION_RETRY_BACKOFF",
		"database", "dsn",
	} {
		if !strings.Contains(string(controllerYAML), want) {
			t.Fatalf("PostgreSQL Controller configuration missing %q: %s", want, controllerYAML)
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
		{name: "restricted egress without rules", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "networkPolicy.egress.enabled=true"}, want: "egress.controller must contain"},
		{name: "blocking Controller PDB", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.storage.type=postgresql", "--set", "controller.storage.postgresql.existingSecret=database", "--set", "controller.replicas=2", "--set", "controller.relayRegistry.enabled=false", "--set", "podDisruptionBudget.controller.minAvailable=2"}, want: "must be at least 1 and lower"},
		{name: "SQLite replicas", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.replicas=2"}, want: "controller.replicas must be 1"},
		{name: "PostgreSQL secret", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.storage.type=postgresql"}, want: "existingSecret is required"},
		{name: "PostgreSQL max open", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.storage.type=postgresql", "--set", "controller.storage.postgresql.existingSecret=database", "--set", "controller.storage.postgresql.maxOpenConnections=0"}, want: "maxOpenConnections must be positive"},
		{name: "PostgreSQL max idle", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.storage.type=postgresql", "--set", "controller.storage.postgresql.existingSecret=database", "--set", "controller.storage.postgresql.maxOpenConnections=2", "--set", "controller.storage.postgresql.maxIdleConnections=3"}, want: "maxIdleConnections must not exceed"},
		{name: "PostgreSQL retries", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.storage.type=postgresql", "--set", "controller.storage.postgresql.existingSecret=database", "--set", "controller.storage.postgresql.transactionMaxRetries=11"}, want: "transactionMaxRetries must be between"},
		{name: "unknown storage", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.storage.type=mysql"}, want: "must be sqlite or postgresql"},
		{name: "Controller relay secret", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set-string", "controller.relay.existingSecret="}, want: "controller.relay.existingSecret is required"},
		{name: "Data Plane relay secret", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.relayRegistry.enabled=false", "--set-string", "dataPlane.relay.existingSecret="}, want: "dataPlane.relay.existingSecret is required"},
		{name: "relay ID mismatch", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.relayRegistry.enabled=false", "--set", "dataPlane.relay.id=other"}, want: "must match"},
		{name: "invalid Registry auth", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.relayRegistry.authentication=password"}, want: "must be tokenreview or mtls"},
		{name: "multi-replica endpoint", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "dataPlane.replicas=2"}, want: "must contain {podName} or {podUID}"},
		{name: "in-memory Registry Controller replicas", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.storage.type=postgresql", "--set", "controller.storage.postgresql.existingSecret=database", "--set", "controller.replicas=2"}, want: "in-memory Relay Registry"},
		{name: "empty file roots", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set-json", "controller.files.allowedRoots=[]"}, want: "allowedRoots must contain"},
		{name: "invalid file size", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.files.maxBytes=0"}, want: "maxBytes must be positive"},
		{name: "per-user WSS capacity", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "dataPlane.relayRegistry.maxWebSocketSessions=2", "--set", "dataPlane.relayRegistry.maxWebSocketSessionsPerUser=3"}, want: "maxWebSocketSessionsPerUser"},
		{name: "WSS frame size", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "dataPlane.relayRegistry.maxWebSocketFrameBytes=1024"}, want: "maxWebSocketFrameBytes"},
		{name: "Controller log level", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.logLevel=trace"}, want: "controller.logLevel must be"},
		{name: "wildcard management bootstrap", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set-string", "controller.management.bootstrap.subjects[0]=*"}, want: "must be an exact stable Principal UUID"},
		{name: "management recovery without identity", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.management.bootstrap.recoveryEnabled=true"}, want: "requires a bootstrap subject or group"},
		{name: "break-glass without alias", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.management.breakGlass.enabled=true"}, want: "secretAlias must be a valid stable alias"},
		{name: "unknown break-glass alias", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.management.breakGlass.enabled=true", "--set", "controller.management.breakGlass.secretAlias=emergency"}, want: "must select a configured secretAliases entry"},
		{name: "disabled break-glass alias", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.management.breakGlass.secretAlias=emergency"}, want: "must be empty while break-glass is disabled"},
		{name: "Data Plane log level", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "dataPlane.logLevel=verbose"}, want: "dataPlane.logLevel must be"},
		{name: "invalid RBAC scope", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.rbac.scope=tenant"}, want: "controller.rbac.scope must be cluster or namespace"},
		{name: "missing RBAC namespaces", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.rbac.scope=namespace"}, want: "controller.rbac.namespaces must contain"},
		{name: "invalid RBAC namespace", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.rbac.scope=namespace", "--set", "controller.rbac.namespaces[0]=Team_A"}, want: "contains invalid namespace"},
		{name: "duplicate RBAC namespace", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.rbac.scope=namespace", "--set", "controller.rbac.namespaces[0]=team-a", "--set", "controller.rbac.namespaces[1]=team-a"}, want: "contains duplicate namespace"},
		{name: "token signing secret", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.auth.providers[0].id=corp", "--set", "controller.auth.providers[0].type=oidc", "--set", "controller.auth.providers[0].oidc.issuer=https://login.example.test", "--set", "controller.auth.providers[0].oidc.clientID=kubeloop", "--set", "controller.auth.providers[0].oidc.existingSecret=kubeloop-oidc", "--set", "controller.auth.providers[0].oidc.clientSecretKey=client-secret"}, want: "controller.auth.token.existingSecret is required"},
		{name: "OIDC secret", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.auth.token.existingSecret=kubeloop-token", "--set", "controller.auth.providers[0].id=corp", "--set", "controller.auth.providers[0].type=oidc", "--set", "controller.auth.providers[0].oidc.issuer=https://login.example.test", "--set", "controller.auth.providers[0].oidc.clientID=kubeloop"}, want: "existingSecret is required"},
		{name: "unsupported auth type", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.auth.token.existingSecret=kubeloop-token", "--set", "controller.auth.providers[0].id=corp", "--set", "controller.auth.providers[0].type=saml"}, want: "must be oidc, ad, static-token, or anonymous"},
		{name: "static token without development mode", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.auth.token.existingSecret=kubeloop-token", "--set", "controller.auth.providers[0].id=local", "--set", "controller.auth.providers[0].type=static-token", "--set", "controller.auth.providers[0].staticToken.existingSecret=development", "--set", "controller.auth.providers[0].staticToken.tokenKey=token"}, want: "requires controller.auth.developmentMode=true"},
		{name: "anonymous without development mode", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.auth.token.existingSecret=kubeloop-token", "--set", "controller.auth.providers[0].id=guest", "--set", "controller.auth.providers[0].type=anonymous", "--set", "controller.auth.providers[0].anonymous.subject=guest"}, want: "requires controller.auth.developmentMode=true"},
		{name: "plain AD LDAP", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controller.auth.token.existingSecret=kubeloop-token", "--set", "controller.auth.providers[0].id=ad", "--set", "controller.auth.providers[0].type=ad", "--set", "controller.auth.providers[0].ad.directoryID=corp", "--set", "controller.auth.providers[0].ad.url=ldap://dc.example.test:389", "--set", `controller.auth.providers[0].ad.baseDN=DC=example\,DC=test`}, want: "must use ldaps:// or explicitly enabled StartTLS"},
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
		"--set", "controller.relay.existingSecret=test-relay-controller",
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

func assertControllerOnlyBinding(t *testing.T, binding map[string]any) {
	assertBindingSubject(t, binding, "test-kubeloop-controller")
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
