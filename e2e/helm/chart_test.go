package helm

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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
		"--set", "controlPlane.management.publicURL=https://kubeloop.example.test",
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
	if countKind(objects, "ConfigMap") != 1 {
		t.Fatalf("shared ConfigMap count = %d", countKind(objects, "ConfigMap"))
	}
	controlPlaneConfig := objectByName(t, objects, "ConfigMap", "test-kubeloop-config")
	controlPlaneDocument := parseControlPlaneConfig(t, controlPlaneConfig)
	gatewayConfig := parseUnifiedConfig(t, controlPlaneConfig)["gateway"].(map[string]any)
	if valueAt(t, controlPlaneDocument, "api", "tunnelPath") != "/tunnel" ||
		valueAt(t, gatewayConfig, "http", "path") != "/tunnel" {
		t.Fatal("ControlPlane discovery and Data Plane ingress use different tunnel paths")
	}
	if valueAt(t, gatewayConfig, "websocket", "streamIdleTimeout") != "30m" {
		t.Fatalf("Data Plane stream idle timeout = %#v", valueAt(t, gatewayConfig, "websocket", "streamIdleTimeout"))
	}
	if valueAt(t, gatewayConfig, "minClientVersion") != "" {
		t.Fatalf("Data Plane minimum client version = %#v", valueAt(t, gatewayConfig, "minClientVersion"))
	}
	if valueAt(t, controlPlaneDocument, "files", "maxBytes") != float64(1073741824) ||
		len(valueAt(t, controlPlaneDocument, "files", "allowedRoots").([]any)) != 1 {
		t.Fatalf("ControlPlane file limits = %#v", valueAt(t, controlPlaneDocument, "files"))
	}
	if valueAt(t, controlPlaneDocument, "management", "listen") != ":8081" ||
		valueAt(t, controlPlaneDocument, "management", "publicURL") != "https://kubeloop.example.test" {
		t.Fatalf("Management Plane listener = %#v", valueAt(t, controlPlaneDocument, "management"))
	}
	storageDocument := valueAt(t, controlPlaneDocument, "storage").(map[string]any)
	if _, configured := storageDocument["datasourceURLFile"]; configured ||
		valueAt(t, controlPlaneDocument, "storage", "sqlite", "path") == "" {
		t.Fatal("ControlPlane is missing SQLite configuration in its YAML document")
	}
	controlPlaneYAML, _ := yaml.Marshal(controlPlane)
	if !strings.Contains(string(controlPlaneYAML), "--config=/etc/kubeloop/kubeloop.yaml") ||
		strings.Contains(string(controlPlaneYAML), "--session-ttl") || strings.Contains(string(controlPlaneYAML), "envFrom:") {
		t.Fatal("ControlPlane must start from only its mounted YAML configuration")
	}
	dataPlaneYAML, _ := yaml.Marshal(dataPlane)
	for component, document := range map[string]string{"control-plane": string(controlPlaneYAML), "data-plane": string(dataPlaneYAML)} {
		if !strings.Contains(document, "name: test-kubeloop-config") || !strings.Contains(document, "checksum/config:") {
			t.Fatalf("%s does not use the rollout-aware shared ConfigMap", component)
		}
	}
	if !strings.Contains(string(controlPlaneYAML), "key: kubeloop.yaml") {
		t.Fatal("Control Plane does not project the unified kubeloop.yaml")
	}
	if !strings.Contains(string(dataPlaneYAML), "key: kubeloop.yaml") {
		t.Fatal("Data Plane does not project the unified kubeloop.yaml")
	}
	if strings.Contains(string(dataPlaneYAML), "args:") ||
		!strings.Contains(string(dataPlaneYAML), "KUBELOOP_GATEWAY_CONFIG_FILE") {
		t.Fatal("Data Plane must use only its mounted Gateway configuration file")
	}
	if containerHasEnvironment(t, dataPlane, "KUBELOOP_SQLITE_PATH") || containerHasEnvironment(t, dataPlane, "KUBELOOP_DATASOURCE_URL") {
		t.Fatal("Data Plane received ControlPlane database configuration")
	}
	if strings.Contains(string(dataPlaneYAML), "KUBELOOP_GATEWAY_TOKEN") || strings.Contains(string(dataPlaneYAML), "legacy-tcp") {
		t.Fatal("Data Plane still exposes legacy token or raw TCP compatibility paths")
	}
	if !strings.Contains(string(dataPlaneYAML), "relay-identity") ||
		!strings.Contains(string(dataPlaneYAML), "audience: kubeloop-relay") ||
		strings.Contains(string(dataPlaneYAML), "relay-verification-keys") {
		t.Fatal("Data Plane is missing dynamic Relay registration, projected identity, or replay protection")
	}
	if valueAt(t, gatewayConfig, "relay", "replayEntries") != float64(65536) ||
		valueAt(t, gatewayConfig, "websocket", "maxSessionsPerUser") != float64(8) ||
		valueAt(t, gatewayConfig, "websocket", "maxFrameBytes") != float64(1048576) ||
		valueAt(t, gatewayConfig, "websocket", "handshakeTimeout") != "10s" {
		t.Fatalf("Gateway runtime limits = %#v", gatewayConfig)
	}
	if !strings.Contains(string(controlPlaneYAML), "relay/signing-key.pem") ||
		strings.Contains(string(dataPlaneYAML), "relay/signing-key.pem") {
		t.Fatal("RelayTicket private signing key is not isolated to ControlPlane")
	}
	initialAdminSecret := objectByName(t, objects, "Secret", "test-kubeloop-control-plane-initial-admin")
	mfaEncryptionKey, err := base64.StdEncoding.DecodeString(valueAt(t, initialAdminSecret, "data", "mfa-encryption-key").(string))
	if err != nil || len(mfaEncryptionKey) != 32 {
		t.Fatalf("initial administrator MFA encryption key length = %d, error = %v", len(mfaEncryptionKey), err)
	}
	authSecret := objectByName(t, objects, "Secret", "test-kubeloop-control-plane-auth")
	for _, key := range []string{"oidc-signing-key.pem", "hmac-secret"} {
		if valueAt(t, authSecret, "data", key) == "" {
			t.Fatalf("independent authentication Secret is missing %q", key)
		}
	}
	if _, coupled := initialAdminSecret["signing-key.pem"]; coupled ||
		strings.Contains(string(controlPlaneYAML), "oauth/signing-key.pem") ||
		!strings.Contains(string(controlPlaneYAML), "oauth/oidc-signing-key.pem") ||
		!strings.Contains(string(controlPlaneYAML), "oauth/hmac-secret") {
		t.Fatal("OAuth/OIDC key material is not isolated from the initial administrator Secret")
	}
	if countKind(objects, "Ingress") != 1 {
		t.Fatal("expected one same-origin Ingress")
	}
	ingress := objectByName(t, objects, "Ingress", "test-kubeloop")
	ingressYAML, _ := yaml.Marshal(ingress)
	for _, want := range []string{
		"host: kubeloop.example.test", "path: /.well-known", "path: /oauth2",
		"path: /kubeloop/api", "path: /api/sessions", "path: /tunnel", "path: /traffic/v1", "pathType: Prefix", "tls:",
	} {
		if !strings.Contains(string(ingressYAML), want) {
			t.Fatalf("same-origin Ingress is missing %q: %s", want, ingressYAML)
		}
	}
	controlPlaneService := objectByName(t, objects, "Service", "test-kubeloop-control-plane")
	managementService := objectByName(t, objects, "Service", "test-kubeloop-control-plane-management")
	dataPlaneService := objectByName(t, objects, "Service", "test-kubeloop-gateway")
	if serviceAppProtocol(t, controlPlaneService) != "http" || serviceAppProtocol(t, dataPlaneService) != "kubernetes.io/ws" {
		t.Fatalf("backend appProtocols = controlPlane %q, data plane %q", serviceAppProtocol(t, controlPlaneService), serviceAppProtocol(t, dataPlaneService))
	}
	if valueAt(t, managementService, "spec", "type") != "ClusterIP" {
		t.Fatal("Management Plane Service must remain ClusterIP-only")
	}
	assertServicePort(t, managementService, "management", 8081, "management")
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
		"--set", "controlPlane.management.publicURL=https://kubeloop.example.test",
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
		"value: /.well-known", "value: /oauth2", "value: /kubeloop/api", "value: /api/sessions", "value: /tunnel", "value: /traffic/v1",
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
		"--set", "controlPlane.management.publicURL=https://kubeloop.example.test",
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

func TestMonitoringAndTopologySpreadAreOptInAndScoped(t *testing.T) {
	spread := `[{"maxSkew":1,"topologyKey":"topology.kubernetes.io/zone","whenUnsatisfiable":"DoNotSchedule","labelSelector":{"matchLabels":{"app.kubernetes.io/name":"kubeloop"}}}]`
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controlPlane.storage.datasource.existingSecret=database",
		"--set", "monitoring.serviceMonitor.enabled=true",
		"--set", "monitoring.serviceMonitor.labels.release=prometheus",
		"--set-json", "controlPlane.topologySpreadConstraints="+spread,
		"--set-json", "dataPlane.topologySpreadConstraints="+spread,
		"--set-json", "operator.topologySpreadConstraints="+spread,
	)
	if countKind(objects, "PodDisruptionBudget") != 0 || countKind(objects, "ServiceMonitor") != 1 {
		t.Fatalf("runtime objects: PDB=%d ServiceMonitor=%d", countKind(objects, "PodDisruptionBudget"), countKind(objects, "ServiceMonitor"))
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

func TestOIDCProvidersAreNotConfiguredOrMountedByHelm(t *testing.T) {
	objects := renderChart(t, "--set", "publicURL=https://kubeloop.example.test")
	config := controlPlaneConfigRaw(t, objects)
	for _, forbidden := range []string{"providers:", "clientSecret", "providerSecretAliases", "management/providers"} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("database-managed Provider field %q leaked into Helm config: %s", forbidden, config)
		}
	}
	deployment, err := yaml.Marshal(objectsByComponent(t, objects, "Deployment", "control-plane")[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(deployment), "management/providers") {
		t.Fatal("Control Plane still mounts database-managed Provider credentials")
	}
}

func TestAuthorizationIsDatabaseManaged(t *testing.T) {
	objects := renderChart(t, "--set", "publicURL=https://kubeloop.example.test")
	config := controlPlaneConfigRaw(t, objects)
	for _, forbidden := range []string{`"authorization"`, `"policy"`, `"rules"`} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("database-managed authorization field %s leaked into Helm config: %s", forbidden, config)
		}
	}
}

func TestDevelopmentPresetIsLocalAndDoesNotEnableManagementBootstrap(t *testing.T) {
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.192.168.64.70.sslip.io",
		"--set", "controlPlane.management.publicURL=https://kubeloop.192.168.64.70.sslip.io",
		"--set", "controlPlane.development.enabled=true",
	)
	config := controlPlaneConfigRaw(t, objects)
	for _, want := range []string{`"subjects":[]`, `"groups":[]`, `"recoveryEnabled":false`} {
		if !strings.Contains(config, want) {
			t.Fatalf("development config missing %s: %s", want, config)
		}
	}
	for _, forbidden := range []string{"development-authenticated-full-access", "development-policy"} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("development mode leaked authorization preset %q: %s", forbidden, config)
		}
	}
}

func TestManagementBootstrapRemainsControlPlaneOnly(t *testing.T) {
	defaultObjects := renderChart(t, "--set", "publicURL=https://kubeloop.example.test")
	defaultJSON := controlPlaneConfigRaw(t, defaultObjects)
	for _, want := range []string{
		`"subjects":[]`, `"groups":[]`, `"recoveryEnabled":false`,
	} {
		if !strings.Contains(defaultJSON, want) {
			t.Fatalf("default management config missing %s: %s", want, defaultJSON)
		}
	}

	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controlPlane.management.bootstrap.subjects[0]=00000000-0000-4000-8000-000000000001",
		"--set", "controlPlane.management.bootstrap.groups[0]=platform-bootstrap",
	)
	managementJSON := controlPlaneConfigRaw(t, objects)
	for _, want := range []string{
		`"subjects":["00000000-0000-4000-8000-000000000001"]`, `"groups":["platform-bootstrap"]`,
	} {
		if !strings.Contains(managementJSON, want) {
			t.Fatalf("management config missing %s: %s", want, managementJSON)
		}
	}
	if strings.Contains(strings.ToLower(managementJSON), "breakglass") {
		t.Fatalf("removed break-glass configuration remains present: %s", managementJSON)
	}
	controlPlane := objectsByComponent(t, objects, "Deployment", "control-plane")[0]
	controlPlaneYAML, _ := yaml.Marshal(controlPlane)
	if strings.Contains(string(controlPlaneYAML), "break-glass") {
		t.Fatalf("ControlPlane still projects a break-glass Secret: %s", controlPlaneYAML)
	}
	for _, component := range []string{"data-plane", "operator"} {
		componentYAML, _ := yaml.Marshal(objectsByComponent(t, objects, "Deployment", component)[0])
		for _, forbidden := range []string{"management-secrets", "break-glass"} {
			if strings.Contains(string(componentYAML), forbidden) {
				t.Fatalf("%s received Management Plane Secret %q", component, forbidden)
			}
		}
	}
}

func TestKubernetesProviderUsesControlPlaneServiceAccountWithoutDefaultImpersonation(t *testing.T) {
	objects := renderChart(t, "--set", "publicURL=https://kubeloop.example.test")
	kubernetesJSON := controlPlaneConfigRaw(t, objects)
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
	kubernetesJSON := controlPlaneConfigRaw(t, objects)
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

func TestExternalDatasourceChartUsesNoSQLitePVC(t *testing.T) {
	objects := renderChart(t,
		"--set", "publicURL=https://kubeloop.example.test",
		"--set", "controlPlane.storage.datasource.existingSecret=database",
	)
	controlPlane := objectsByComponent(t, objects, "Deployment", "control-plane")[0]
	if valueAt(t, controlPlane, "spec", "strategy", "type") != "RollingUpdate" {
		t.Fatal("external datasource ControlPlane must use RollingUpdate")
	}
	if valueAt(t, controlPlane, "spec", "replicas") != 1 {
		t.Fatalf("external datasource ControlPlane replicas = %#v", valueAt(t, controlPlane, "spec", "replicas"))
	}
	if countKind(objects, "PersistentVolumeClaim") != 0 {
		t.Fatal("external datasource mode must not create a SQLite PVC")
	}
	controlPlaneYAML, _ := yaml.Marshal(controlPlane)
	for _, want := range []string{"storage-secret", "database", "datasource-url"} {
		if !strings.Contains(string(controlPlaneYAML), want) {
			t.Fatalf("external datasource ControlPlane configuration missing %q: %s", want, controlPlaneYAML)
		}
	}
	configYAML := controlPlaneConfigRaw(t, objects)
	for _, want := range []string{
		`"datasourceURLFile":"/var/run/secrets/kubeloop/storage/datasource-url"`, `"connectTimeout":"10s"`,
		`"queryTimeout":"5s"`, `"maxOpenConnections":20`, `"maxIdleConnections":5`,
		`"connectionMaxLifetime":"30m"`, `"transactionMaxRetries":3`, `"transactionRetryBackoff":"25ms"`,
	} {
		if !strings.Contains(configYAML, want) {
			t.Fatalf("external datasource YAML configuration missing %q: %s", want, configYAML)
		}
	}
	dataPlaneYAML, _ := yaml.Marshal(objectsByComponent(t, objects, "Deployment", "data-plane")[0])
	if strings.Contains(string(dataPlaneYAML), "KUBELOOP_DATASOURCE") || strings.Contains(string(dataPlaneYAML), "database") {
		t.Fatal("Data Plane received datasource configuration or Secret reference")
	}
}

func TestChartRejectsUnsafeStorageConfigurations(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing public URL", want: "publicURL is required"},
		{name: "development on external origin", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.development.enabled=true"}, want: "requires a local"},
		{name: "public URL path", args: []string{"--set", "publicURL=https://kubeloop.example.test/base"}, want: "must be one HTTPS origin"},
		{name: "two external routes", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.management.publicURL=https://kubeloop.example.test", "--set", "ingress.enabled=true", "--set", "ingress.host=kubeloop.example.test", "--set", "gatewayAPI.enabled=true", "--set", "gatewayAPI.host=kubeloop.example.test"}, want: "mutually exclusive"},
		{name: "Ingress origin mismatch", args: []string{"--set", "publicURL=https://other.example.test", "--set", "controlPlane.management.publicURL=https://other.example.test", "--set", "ingress.enabled=true", "--set", "ingress.host=kubeloop.example.test"}, want: "exactly equal"},
		{name: "Ingress without TLS", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.management.publicURL=https://kubeloop.example.test", "--set", "ingress.enabled=true", "--set", "ingress.host=kubeloop.example.test", "--set", "ingress.tls.enabled=false"}, want: "TLS"},
		{name: "Gateway origin mismatch", args: []string{"--set", "publicURL=https://other.example.test", "--set", "controlPlane.management.publicURL=https://other.example.test", "--set", "gatewayAPI.enabled=true", "--set", "gatewayAPI.host=kubeloop.example.test"}, want: "exactly equal"},
		{name: "Gateway parent", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.management.publicURL=https://kubeloop.example.test", "--set", "gatewayAPI.enabled=true", "--set", "gatewayAPI.host=kubeloop.example.test"}, want: "parentRef.name is required"},
		{name: "Gateway class", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.management.publicURL=https://kubeloop.example.test", "--set", "gatewayAPI.enabled=true", "--set", "gatewayAPI.host=kubeloop.example.test", "--set", "gatewayAPI.gateway.create=true"}, want: "className is required"},
		{name: "Gateway TLS certificate", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.management.publicURL=https://kubeloop.example.test", "--set", "gatewayAPI.enabled=true", "--set", "gatewayAPI.host=kubeloop.example.test", "--set", "gatewayAPI.gateway.create=true", "--set", "gatewayAPI.gateway.className=example"}, want: "tls.secretName is required"},
		{name: "Gateway tunnel timeout", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.management.publicURL=https://kubeloop.example.test", "--set", "gatewayAPI.enabled=true", "--set", "gatewayAPI.host=kubeloop.example.test", "--set", "gatewayAPI.parentRef.name=shared", "--set", "gatewayAPI.parentRef.sectionName=https", "--set", "gatewayAPI.timeouts.tunnel=30m"}, want: "must be 0s"},
		{name: "restricted egress without rules", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "networkPolicy.egress.enabled=true"}, want: "egress.controlPlane must contain"},
		{name: "SQLite replicas", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.replicas=2"}, want: "controlPlane.replicas must be 1"},
		{name: "datasource max open", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.storage.datasource.existingSecret=database", "--set", "controlPlane.storage.maxOpenConnections=0"}, want: "maxOpenConnections must be positive"},
		{name: "datasource max idle", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.storage.datasource.existingSecret=database", "--set", "controlPlane.storage.maxOpenConnections=2", "--set", "controlPlane.storage.maxIdleConnections=3"}, want: "maxIdleConnections must not exceed"},
		{name: "datasource retries", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.storage.datasource.existingSecret=database", "--set", "controlPlane.storage.transactionMaxRetries=11"}, want: "transactionMaxRetries must be between"},
		{name: "ControlPlane relay secret", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set-string", "controlPlane.relay.existingSecret="}, want: "controlPlane.relay.existingSecret is required"},
		{name: "invalid Registry auth", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.relayRegistry.authentication=password"}, want: "must be tokenreview or mtls"},
		{name: "multi-replica endpoint", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "dataPlane.replicas=2"}, want: "must contain {podName} or {podUID}"},
		{name: "in-memory Registry ControlPlane replicas", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.storage.datasource.existingSecret=database", "--set", "controlPlane.replicas=2"}, want: "in-memory Relay Registry"},
		{name: "empty file roots", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set-json", "controlPlane.files.allowedRoots=[]"}, want: "allowedRoots must contain"},
		{name: "invalid file size", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.files.maxBytes=0"}, want: "maxBytes must be positive"},
		{name: "per-user WSS capacity", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "dataPlane.relayRegistry.maxWebSocketSessions=2", "--set", "dataPlane.relayRegistry.maxWebSocketSessionsPerUser=3"}, want: "maxWebSocketSessionsPerUser"},
		{name: "WSS frame size", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "dataPlane.relayRegistry.maxWebSocketFrameBytes=1024"}, want: "maxWebSocketFrameBytes"},
		{name: "ControlPlane log level", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.logLevel=trace"}, want: "controlPlane.logLevel must be"},
		{name: "wildcard management bootstrap", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set-string", "controlPlane.management.bootstrap.subjects[0]=*"}, want: "must be an exact stable Principal UUID"},
		{name: "management recovery without identity", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.management.bootstrap.recoveryEnabled=true"}, want: "requires a bootstrap subject or group"},
		{name: "Data Plane log level", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "dataPlane.logLevel=verbose"}, want: "dataPlane.logLevel must be"},
		{name: "invalid RBAC scope", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.rbac.scope=tenant"}, want: "controlPlane.rbac.scope must be cluster or namespace"},
		{name: "missing RBAC namespaces", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.rbac.scope=namespace"}, want: "controlPlane.rbac.namespaces must contain"},
		{name: "invalid RBAC namespace", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.rbac.scope=namespace", "--set", "controlPlane.rbac.namespaces[0]=Team_A"}, want: "contains invalid namespace"},
		{name: "duplicate RBAC namespace", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set", "controlPlane.rbac.scope=namespace", "--set", "controlPlane.rbac.namespaces[0]=team-a", "--set", "controlPlane.rbac.namespaces[1]=team-a"}, want: "contains duplicate namespace"},
		{name: "empty OIDC signing key", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set-string", "controlPlane.auth.oauth.oidcSigningKeyKey="}, want: "oidcSigningKeyKey is required"},
		{name: "empty HMAC key", args: []string{"--set", "publicURL=https://kubeloop.example.test", "--set-string", "controlPlane.auth.oauth.hmacSecretKey="}, want: "hmacSecretKey is required"},
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
	if !ok {
		t.Fatalf("Service ports = %#v", valueAt(t, service, "spec", "ports"))
	}
	for _, value := range ports {
		port, ok := value.(map[string]any)
		if ok && port["name"] == "http" {
			appProtocol, _ := port["appProtocol"].(string)
			return appProtocol
		}
	}
	t.Fatalf("Service has no http port: %#v", ports)
	return ""
}

func assertServicePort(t *testing.T, service map[string]any, name string, portNumber int, targetPort string) {
	t.Helper()
	ports, ok := valueAt(t, service, "spec", "ports").([]any)
	if !ok {
		t.Fatalf("Service ports = %#v", valueAt(t, service, "spec", "ports"))
	}
	for _, value := range ports {
		port, ok := value.(map[string]any)
		if ok && port["name"] == name {
			if port["port"] != portNumber || port["targetPort"] != targetPort {
				t.Fatalf("Service port %s = %#v", name, port)
			}
			return
		}
	}
	t.Fatalf("Service has no %s port: %#v", name, ports)
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

func controlPlaneConfigRaw(t *testing.T, objects []map[string]any) string {
	t.Helper()
	config := objectByName(t, objects, "ConfigMap", "test-kubeloop-config")
	controlPlane, err := json.Marshal(parseUnifiedConfig(t, config)["controlPlane"])
	if err != nil {
		t.Fatal(err)
	}
	return string(controlPlane)
}

func parseControlPlaneConfig(t *testing.T, configMap map[string]any) map[string]any {
	t.Helper()
	document := parseUnifiedConfig(t, configMap)
	controlPlane, ok := document["controlPlane"].(map[string]any)
	if !ok {
		t.Fatalf("controlPlane is not an object: %#v", document["controlPlane"])
	}
	return controlPlane
}

func parseUnifiedConfig(t *testing.T, configMap map[string]any) map[string]any {
	t.Helper()
	raw, ok := valueAt(t, configMap, "data", "kubeloop.yaml").(string)
	if !ok {
		t.Fatalf("kubeloop.yaml is not a string: %#v", valueAt(t, configMap, "data"))
	}
	var yamlConfig map[string]any
	if err := yaml.Unmarshal([]byte(raw), &yamlConfig); err != nil {
		t.Fatalf("parse kubeloop.yaml: %v", err)
	}
	normalized, err := json.Marshal(yamlConfig)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(normalized, &config); err != nil {
		t.Fatal(err)
	}
	return config
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
