//go:build ignore

// Command gateway-dev prepares the content-addressed Control Plane, Gateway, and
// Operator images used by wails dev, then deploys a complete development stack
// to the active local cluster.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	controlPlaneImageRepository = "kube-loop-control-plane"
	gatewayImageRepository      = "kube-loop-gateway"
	operatorImageRepository     = "kube-loop-operator"
	developmentNamespace        = "kubeloop-dev"
	developmentRelease          = "kubeloop-dev"
)

func main() {
	root, err := findRoot()
	if err != nil {
		fatalf("%v", err)
	}
	if err := buildDevelopmentSingBox(root); err != nil {
		fatalf("build local sing-box: %v", err)
	}
	controlPlaneHash, err := controlPlaneSourceHash(root)
	if err != nil {
		fatalf("hash Control Plane sources: %v", err)
	}
	controlPlaneImage := controlPlaneImageRepository + ":dev-" + controlPlaneHash[:12]
	controlPlaneBinary := filepath.Join(root, "build", "bin", "kubeloop-control-plane")
	gatewayHash, err := gatewaySourceHash(root)
	if err != nil {
		fatalf("hash Gateway sources: %v", err)
	}
	gatewayImage := gatewayImageRepository + ":dev-" + gatewayHash[:12]
	gatewayBinary := filepath.Join(root, "build", "bin", "kube-loop-gateway")
	operatorHash, err := operatorSourceHash(root)
	if err != nil {
		fatalf("hash Operator sources: %v", err)
	}
	operatorImage := operatorImageRepository + ":dev-" + operatorHash[:12]
	operatorBinary := filepath.Join(root, "build", "bin", "kubeloop-operator")
	contextName := currentKubeContext()

	fmt.Printf("==> Building local Control Plane image %s\n", controlPlaneImage)
	if err := buildLinuxBinary(root, controlPlaneBinary, "./cmd/kubeloop-control-plane"); err != nil {
		fatalf("build Control Plane binary: %v", err)
	}
	if err := buildImage(root, contextName, controlPlaneImage, "build/control-plane.e2e.Dockerfile"); err != nil {
		fatalf("build Control Plane image: %v", err)
	}
	if err := loadIntoActiveLocalCluster(root, contextName, controlPlaneImage); err != nil {
		fatalf("load Control Plane image: %v", err)
	}
	if err := writeImageMetadata(root, "control-plane-image", controlPlaneImage); err != nil {
		fatalf("write Control Plane image metadata: %v", err)
	}

	fmt.Printf("==> Building local Gateway image %s\n", gatewayImage)
	if err := buildLinuxBinary(root, gatewayBinary, "./cmd/kubeloop-gateway"); err != nil {
		fatalf("build Gateway binary: %v", err)
	}
	if err := buildImage(root, contextName, gatewayImage, "build/gateway.e2e.Dockerfile"); err != nil {
		fatalf("build Gateway image: %v", err)
	}
	if err := loadIntoActiveLocalCluster(root, contextName, gatewayImage); err != nil {
		fatalf("load Gateway image: %v", err)
	}
	if err := writeImageMetadata(root, "gateway-image", gatewayImage); err != nil {
		fatalf("write Gateway image metadata: %v", err)
	}

	fmt.Printf("==> Building local Operator image %s\n", operatorImage)
	if err := buildLinuxBinary(root, operatorBinary, "./cmd/kubeloop-operator"); err != nil {
		fatalf("build Operator binary: %v", err)
	}
	if err := buildImage(root, contextName, operatorImage, "build/operator.e2e.Dockerfile"); err != nil {
		fatalf("build Operator image: %v", err)
	}
	if err := loadIntoActiveLocalCluster(root, contextName, operatorImage); err != nil {
		fatalf("load Operator image: %v", err)
	}
	if err := writeImageMetadata(root, "operator-image", operatorImage); err != nil {
		fatalf("write Operator image metadata: %v", err)
	}
	if contextName == "" {
		fmt.Println("==> kubectl context unavailable; skipping development stack deployment")
	} else if !localClusterContext(contextName) {
		fatalf("refusing to deploy the local development stack to non-local Kubernetes context %q", contextName)
	} else {
		publicURL, deployErr := deployDevelopmentStack(root, contextName, controlPlaneImage, gatewayImage, operatorImage)
		if deployErr != nil {
			fatalf("deploy development stack: %v", deployErr)
		}
		fmt.Printf("==> KubeLoop development server: %s\n", publicURL)
	}
	fmt.Printf(
		"==> Wails development images ready: Control Plane %s, Gateway %s, Operator %s\n",
		controlPlaneImage, gatewayImage, operatorImage,
	)
}

// buildDevelopmentSingBox uses the same staging command as a packaged Wails
// build. Keeping the binary under build/bin lets the development application
// and Helper exercise the exact patched sing-box that will ship to users.
// KUBELOOP_SINGBOX_SOURCE remains available for the documented debug and
// upstream variants.
func buildDevelopmentSingBox(root string) error {
	target := runtime.GOOS + "/" + runtime.GOARCH
	fmt.Printf("==> Building local sing-box for %s\n", target)
	return run(root, exec.Command(
		"go", "run", "./build/stage-package-assets.go", target,
	))
}

func controlPlaneSourceHash(root string) (string, error) {
	return sourceHash(root, []string{"go.mod", "go.sum", ".dockerignore", "build/control-plane.e2e.Dockerfile"}, []string{
		"cmd/kubeloop-control-plane",
		"internal",
	})
}

func gatewaySourceHash(root string) (string, error) {
	return sourceHash(root, []string{"go.mod", "go.sum", ".dockerignore", "build/gateway.e2e.Dockerfile"}, []string{
		"cmd/kubeloop-gateway",
		"internal",
	})
}

func operatorSourceHash(root string) (string, error) {
	return sourceHash(root, []string{"go.mod", "go.sum", ".dockerignore", "build/operator.e2e.Dockerfile"}, []string{
		"cmd/kubeloop-operator",
		"internal",
	})
}

func sourceHash(root string, paths, directories []string) (string, error) {
	for _, directory := range directories {
		err := filepath.WalkDir(
			filepath.Join(root, directory),
			func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
					relative, err := filepath.Rel(root, path)
					if err != nil {
						return err
					}
					paths = append(paths, filepath.ToSlash(relative))
				}
				return nil
			},
		)
		if err != nil {
			return "", err
		}
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, relative := range paths {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00", relative)
		_, _ = hash.Write(content)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func buildLinuxBinary(root, output, packagePath string) error {
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return fmt.Errorf("create build directory: %w", err)
	}
	command := exec.Command(
		"go", "build", "-trimpath", "-ldflags=-s -w",
		"-o", output, packagePath,
	)
	command.Env = append(
		os.Environ(),
		"CGO_ENABLED=0",
		"GOOS=linux",
		"GOARCH="+runtime.GOARCH,
	)
	if err := run(root, command); err != nil {
		return err
	}
	if err := os.Chmod(output, 0o755); err != nil {
		return fmt.Errorf("make binary executable: %w", err)
	}
	return nil
}

func writeImageMetadata(root, name, image string) error {
	metadata := filepath.Join(root, "build", "embedded", name)
	if err := os.MkdirAll(filepath.Dir(metadata), 0o755); err != nil {
		return fmt.Errorf("create embedded metadata directory: %w", err)
	}
	return os.WriteFile(metadata, []byte(image+"\n"), 0o644)
}

func currentKubeContext() string {
	contextOutput, err := exec.Command("kubectl", "config", "current-context").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(contextOutput))
}

func buildImage(root, contextName, image, dockerfile string) error {
	if profile, ok := minikubeProfile(contextName); ok {
		fmt.Printf("==> Building image inside Minikube profile %s\n", profile)
		if err := run(root, exec.Command(
			"minikube", "-p", profile,
			"image", "build",
			"-t", image,
			"-f", dockerfile,
			".",
		)); err != nil {
			return err
		}
		output, err := exec.Command("minikube", "-p", profile, "image", "ls").Output()
		if err != nil {
			return fmt.Errorf("list Minikube images: %w", err)
		}
		if !strings.Contains(string(output), image) {
			return fmt.Errorf("Minikube image build did not create %s", image)
		}
		return nil
	}
	return run(root, exec.Command(
		"docker", "build",
		"-t", image,
		"-f", dockerfile,
		".",
	))
}

func loadIntoActiveLocalCluster(root, contextName, image string) error {
	if _, ok := minikubeProfile(contextName); ok {
		return nil
	}
	switch {
	case contextName == "":
		fmt.Println("==> kubectl context unavailable; image remains in the Docker daemon")
		return nil
	case strings.HasPrefix(contextName, "kind-"):
		return run(root, exec.Command(
			"kind", "load", "docker-image", image,
			"--name", strings.TrimPrefix(contextName, "kind-"),
		))
	case strings.HasPrefix(contextName, "k3d-"):
		return run(root, exec.Command(
			"k3d", "image", "import", image,
			"--cluster", strings.TrimPrefix(contextName, "k3d-"),
		))
	default:
		fmt.Printf("==> Using Docker image directly for Kubernetes context %s\n", contextName)
		return nil
	}
}

func localClusterContext(contextName string) bool {
	if _, ok := minikubeProfile(contextName); ok {
		return true
	}
	return strings.HasPrefix(contextName, "kind-") ||
		strings.HasPrefix(contextName, "k3d-") ||
		contextName == "docker-desktop" ||
		contextName == "rancher-desktop"
}

func deployDevelopmentStack(
	root, contextName, controlPlaneImage, gatewayImage, operatorImage string,
) (string, error) {
	host, err := developmentHost(contextName)
	if err != nil {
		return "", err
	}
	publicURL := "https://" + host
	registryHost := developmentRelease + "-kubeloop-control-plane-relay." + developmentNamespace + ".svc"
	materialDirectory := filepath.Join(root, "build", "development")
	if err := ensureDevelopmentMaterial(materialDirectory, []string{host, registryHost}); err != nil {
		return "", err
	}
	if err := writeEmbeddedDevelopmentCA(root, filepath.Join(materialDirectory, "ca.crt")); err != nil {
		return "", err
	}
	if err := applyNamespace(root, developmentNamespace); err != nil {
		return "", err
	}
	relaySecret := developmentRelease + "-relay"
	if err := applyGenericSecret(root, developmentNamespace, relaySecret, map[string]string{
		"signing-key.pem": filepath.Join(materialDirectory, "signing-key.pem"),
		"tls.crt":         filepath.Join(materialDirectory, "tls.crt"),
		"tls.key":         filepath.Join(materialDirectory, "tls.key"),
		"ca.crt":          filepath.Join(materialDirectory, "ca.crt"),
	}); err != nil {
		return "", err
	}
	ingressSecret := developmentRelease + "-ingress-tls"
	if err := applyTLSSecret(
		root, developmentNamespace, ingressSecret,
		filepath.Join(materialDirectory, "tls.crt"), filepath.Join(materialDirectory, "tls.key"),
	); err != nil {
		return "", err
	}
	controlPlaneRepository, controlPlaneTag, err := splitImage(controlPlaneImage)
	if err != nil {
		return "", err
	}
	gatewayRepository, gatewayTag, err := splitImage(gatewayImage)
	if err != nil {
		return "", err
	}
	operatorRepository, operatorTag, err := splitImage(operatorImage)
	if err != nil {
		return "", err
	}
	chart := filepath.Join(root, "charts", "kubeloop")
	arguments := []string{
		"upgrade", "--install", developmentRelease, chart,
		"--namespace", developmentNamespace,
		"--wait", "--timeout", "5m", "--history-max", "5",
		"--set-string", "publicURL=" + publicURL,
		"--set-string", "serviceID=kubeloop-dev",
		"--set-string", "controlPlane.image.repository=" + controlPlaneRepository,
		"--set-string", "controlPlane.image.tag=" + controlPlaneTag,
		"--set-string", "controlPlane.image.pullPolicy=IfNotPresent",
		"--set-string", "dataPlane.image.repository=" + gatewayRepository,
		"--set-string", "dataPlane.image.tag=" + gatewayTag,
		"--set-string", "dataPlane.image.pullPolicy=IfNotPresent",
		"--set-string", "operator.image.repository=" + operatorRepository,
		"--set-string", "operator.image.tag=" + operatorTag,
		"--set-string", "operator.image.pullPolicy=IfNotPresent",
		"--set-string", "controlPlane.relay.existingSecret=" + relaySecret,
		"--set-string", "controlPlane.relayRegistry.existingSecret=" + relaySecret,
		"--set-string", "controlPlane.relayRegistry.endpointAllowedHosts=" + host,
		"--set-string", "dataPlane.relayRegistry.endpoint=wss://" + host + "/tunnel",
		"--set", "controlPlane.auth.developmentMode=true",
		"--set-string", "controlPlane.auth.token.existingSecret=" + relaySecret,
		"--set-string", "controlPlane.auth.providers[0].id=local-development",
		"--set-string", "controlPlane.auth.providers[0].type=anonymous",
		"--set-string", "controlPlane.auth.providers[0].displayName=Local Development",
		"--set-string", "controlPlane.auth.providers[0].anonymous.subject=local-developer",
		"--set-string", "controlPlane.auth.providers[0].anonymous.groups[0]=kubeloop-dev",
		"--set-string", "controlPlane.policy.rules[0].id=local-development",
		"--set-string", "controlPlane.policy.rules[0].groups[0]=kubeloop-dev",
		"--set-string", "controlPlane.policy.rules[0].namespaces[0]=*",
		"--set-string", "controlPlane.policy.rules[0].operations[0]=*",
		"--set-string", "controlPlane.policy.rules[0].resourceKinds[0]=*",
		"--set-string", "controlPlane.management.bootstrap.groups[0]=kubeloop-dev",
		"--set", "ingress.enabled=true",
		"--set-string", "ingress.className=nginx",
		"--set-string", "ingress.annotations.nginx\\.ingress\\.kubernetes\\.io/ssl-redirect=false",
		"--set-string", "ingress.host=" + host,
		"--set", "ingress.tls.enabled=true",
		"--set-string", "ingress.tls.secretName=" + ingressSecret,
	}
	fmt.Printf("==> Deploying KubeLoop development stack to namespace %s\n", developmentNamespace)
	if err := run(root, exec.Command("helm", arguments...)); err != nil {
		return "", err
	}
	// Development certificates and signing keys are refreshed on every start.
	// Restart all three workloads so no process keeps the previous Secret data.
	if err := restartDevelopmentStack(root); err != nil {
		return "", err
	}
	if err := removeLegacyOperator(root); err != nil {
		return "", err
	}
	return publicURL, nil
}

func ensureDevelopmentMaterial(directory string, dnsNames []string) error {
	if developmentMaterialValid(directory, dnsNames) {
		fmt.Printf("==> Reusing development TLS and Relay keys from %s\n", directory)
		return nil
	}
	if importDevelopmentMaterial(directory) && developmentMaterialValid(directory, dnsNames) {
		fmt.Printf("==> Imported existing development TLS and Relay keys into %s\n", directory)
		return nil
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create development material directory: %w", err)
	}
	fmt.Printf("==> Generating development TLS and Relay keys in %s\n", directory)
	return writeDevelopmentMaterial(directory, dnsNames)
}

func importDevelopmentMaterial(directory string) bool {
	type secretFile struct {
		secret string
		key    string
		mode   os.FileMode
	}
	files := []secretFile{
		{secret: developmentRelease + "-relay", key: "signing-key.pem", mode: 0o600},
		{secret: developmentRelease + "-relay", key: "tls.crt", mode: 0o644},
		{secret: developmentRelease + "-relay", key: "tls.key", mode: 0o600},
		{secret: developmentRelease + "-relay", key: "ca.crt", mode: 0o644},
	}
	contents := make(map[string][]byte, len(files))
	for _, file := range files {
		template := `{{ index .data "` + file.key + `" | base64decode }}`
		output, err := exec.Command(
			"kubectl", "get", "secret", file.secret,
			"--namespace", developmentNamespace, "--output", "go-template="+template,
		).Output()
		if err != nil || len(output) == 0 {
			return false
		}
		contents[file.key] = output
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return false
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(directory, file.key), contents[file.key], file.mode); err != nil {
			return false
		}
	}
	return true
}

func developmentMaterialValid(directory string, dnsNames []string) bool {
	certificatePEM, err := os.ReadFile(filepath.Join(directory, "tls.crt"))
	if err != nil {
		return false
	}
	privateKeyPEM, err := os.ReadFile(filepath.Join(directory, "tls.key"))
	if err != nil {
		return false
	}
	pair, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil || len(pair.Certificate) == 0 {
		return false
	}
	certificate, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || time.Now().Add(24*time.Hour).After(certificate.NotAfter) {
		return false
	}
	for _, dnsName := range dnsNames {
		if certificate.VerifyHostname(dnsName) != nil {
			return false
		}
	}
	for _, name := range []string{"ca.crt", "signing-key.pem"} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil || info.IsDir() || info.Size() == 0 {
			return false
		}
	}
	return true
}

func developmentHost(contextName string) (string, error) {
	if profile, ok := minikubeProfile(contextName); ok {
		output, err := exec.Command("minikube", "-p", profile, "ip").Output()
		if err != nil {
			return "", fmt.Errorf("read Minikube IP: %w", err)
		}
		address := strings.TrimSpace(string(output))
		if address == "" {
			return "", fmt.Errorf("Minikube profile %q returned an empty IP", profile)
		}
		return "kubeloop." + address + ".sslip.io", nil
	}
	return "kubeloop.local", nil
}

func writeDevelopmentMaterial(directory string, dnsNames []string) error {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate development signing key: %w", err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("encode development signing key: %w", err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	tlsKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate development TLS key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generate development TLS serial: %w", err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: dnsNames[0], Organization: []string{"KubeLoop Development"}},
		DNSNames:              dnsNames,
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(30 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &tlsKey.PublicKey, tlsKey)
	if err != nil {
		return fmt.Errorf("generate development TLS certificate: %w", err)
	}
	tlsCertificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	tlsPrivateKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(tlsKey)})
	files := map[string][]byte{
		"signing-key.pem": privatePEM,
		"tls.crt":         tlsCertificate,
		"tls.key":         tlsPrivateKey,
		"ca.crt":          tlsCertificate,
	}
	for name, content := range files {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".key") || name == "signing-key.pem" {
			mode = 0o600
		}
		if err := os.WriteFile(filepath.Join(directory, name), content, mode); err != nil {
			return fmt.Errorf("write development material %s: %w", name, err)
		}
	}
	return nil
}

func writeEmbeddedDevelopmentCA(root, source string) error {
	certificate, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read development CA: %w", err)
	}
	directory := filepath.Join(root, "build", "embedded")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create embedded development CA directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "development-ca.pem"), certificate, 0o644); err != nil {
		return fmt.Errorf("write embedded development CA: %w", err)
	}
	return nil
}

func applyNamespace(root, namespace string) error {
	command := exec.Command("kubectl", "create", "namespace", namespace, "--dry-run=client", "-o", "yaml")
	rendered, err := command.Output()
	if err != nil {
		return fmt.Errorf("render development namespace: %w", err)
	}
	return applyManifest(root, rendered)
}

func applyGenericSecret(root, namespace, name string, files map[string]string) error {
	arguments := []string{"create", "secret", "generic", name, "--namespace", namespace}
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		arguments = append(arguments, "--from-file="+key+"="+files[key])
	}
	arguments = append(arguments, "--dry-run=client", "-o", "yaml")
	rendered, err := exec.Command("kubectl", arguments...).Output()
	if err != nil {
		return fmt.Errorf("render development Secret %s: %w", name, err)
	}
	return applyManifest(root, rendered)
}

func applyTLSSecret(root, namespace, name, certificate, key string) error {
	command := exec.Command(
		"kubectl", "create", "secret", "tls", name,
		"--namespace", namespace, "--cert", certificate, "--key", key,
		"--dry-run=client", "-o", "yaml",
	)
	rendered, err := command.Output()
	if err != nil {
		return fmt.Errorf("render development TLS Secret %s: %w", name, err)
	}
	return applyManifest(root, rendered)
}

func applyManifest(root string, rendered []byte) error {
	apply := exec.Command("kubectl", "apply", "-f", "-")
	apply.Stdin = bytes.NewReader(rendered)
	return run(root, apply)
}

func splitImage(image string) (string, string, error) {
	separator := strings.LastIndex(image, ":")
	if separator <= strings.LastIndex(image, "/") || separator == len(image)-1 {
		return "", "", fmt.Errorf("development image %q must include a tag", image)
	}
	return image[:separator], image[separator+1:], nil
}

func restartDevelopmentStack(root string) error {
	// SQLite uses a Recreate Control Plane deployment. Bring it back before
	// restarting its dependants so the Data Plane does not crash-loop while the
	// relay registration endpoint is temporarily unavailable.
	for _, component := range []string{"control-plane", "data-plane", "operator"} {
		selector := "app.kubernetes.io/instance=" + developmentRelease +
			",app.kubernetes.io/component=" + component
		if err := run(root, exec.Command(
			"kubectl", "rollout", "restart", "deployment",
			"--namespace", developmentNamespace, "--selector", selector,
		)); err != nil {
			return fmt.Errorf("restart development %s: %w", component, err)
		}
		if err := run(root, exec.Command(
			"kubectl", "rollout", "status", "deployment",
			"--namespace", developmentNamespace, "--selector", selector, "--timeout=180s",
		)); err != nil {
			return fmt.Errorf("wait for development %s: %w", component, err)
		}
	}
	return nil
}

func removeLegacyOperator(root string) error {
	command := exec.Command(
		"kubectl", "delete", "deployment", "kubeloop-operator-controller-manager",
		"--namespace", "kubeloop-operator-system", "--ignore-not-found", "--wait=true",
	)
	if err := run(root, command); err != nil {
		return fmt.Errorf("remove legacy development Operator: %w", err)
	}
	return nil
}

func minikubeProfile(contextName string) (string, bool) {
	if contextName == "" {
		return "", false
	}
	candidates := []string{contextName}
	if trimmed := strings.TrimPrefix(contextName, "minikube-"); trimmed != contextName {
		candidates = append(candidates, trimmed)
	}
	for _, profile := range candidates {
		output, err := exec.Command(
			"minikube", "-p", profile, "status", "--format={{.Host}}",
		).Output()
		if err == nil && strings.EqualFold(strings.TrimSpace(string(output)), "running") {
			return profile, true
		}
	}
	return "", false
}

func run(directory string, command *exec.Cmd) error {
	command.Dir = directory
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s: %w", command.String(), err)
	}
	return nil
}

func findRoot() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", fmt.Errorf("go.mod not found above working directory")
		}
		directory = parent
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
