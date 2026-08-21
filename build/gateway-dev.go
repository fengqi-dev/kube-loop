//go:build ignore

// Command gateway-dev prepares the content-addressed Control Plane, Gateway, and
// Operator images used by wails dev, then deploys a complete development stack
// to the active local cluster.
package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	controlPlaneImageRepository = "kube-loop-control-plane"
	gatewayImageRepository      = "kubeloop-gateway"
	operatorImageRepository     = "kube-loop-operator"
	developmentNamespace        = "kubeloop-dev"
	developmentRelease          = "kubeloop-dev"
	developmentStorageBaseline  = "26"
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
	gatewayBinary := filepath.Join(root, "build", "bin", "kubeloop-gateway")
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
		publicURL, deployErr := deployDevelopmentStack(
			root,
			contextName,
			controlPlaneImage,
			gatewayImage,
			operatorImage,
		)
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
	return sourceHash(
		root,
		[]string{"go.mod", "go.sum", ".dockerignore", "build/control-plane.e2e.Dockerfile"},
		[]string{
			"cmd/kubeloop-control-plane",
			"internal",
		},
	)
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
	outputDirectory := filepath.Dir(output)
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return fmt.Errorf("create build directory: %w", err)
	}
	// Wails may recreate build/bin with owner-only permissions. Minikube copies
	// the Docker build context through a separate process, which must be able to
	// traverse this directory to read the prebuilt Linux binaries.
	if err := os.Chmod(outputDirectory, 0o755); err != nil {
		return fmt.Errorf("make build directory readable: %w", err)
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
	serviceID := strings.TrimSpace(os.Getenv("KUBELOOP_DEV_SERVICE_ID"))
	if serviceID == "" {
		serviceID = "kubeloop-dev"
	}
	host, err := developmentHost(contextName)
	if err != nil {
		return "", err
	}
	publicURL := "https://" + host
	materialDirectory := filepath.Join(root, "build", "development")
	ingressCertificate, ingressKey, ingressCA, err := generateDevelopmentIngressCertificate(
		root, materialDirectory, host,
	)
	if err != nil {
		return "", err
	}
	if err := writeEmbeddedDevelopmentCA(root, ingressCA); err != nil {
		return "", err
	}
	if err := applyNamespace(root, developmentNamespace); err != nil {
		return "", err
	}
	// Recover Helm before resetting storage. A rollback may briefly start an old
	// Control Plane and initialize its old breaking baseline on a fresh volume.
	if err := recoverDevelopmentHelmRelease(root); err != nil {
		return "", err
	}
	if err := resetDevelopmentStorageForBaseline(root, contextName, materialDirectory); err != nil {
		return "", err
	}
	ingressSecret := developmentRelease + "-ingress-tls"
	if err := applyTLSSecret(
		root, developmentNamespace, ingressSecret,
		ingressCertificate, ingressKey,
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
		"upgrade",
		"--install",
		developmentRelease,
		chart,
		"--namespace",
		developmentNamespace,
		"--reset-values",
		"--wait",
		"--rollback-on-failure",
		"--cleanup-on-fail",
		"--timeout",
		"5m",
		"--history-max",
		"5",
		"--set-string",
		"publicURL=" + publicURL,
		"--set-string",
		"serviceID=" + serviceID,
		"--set-string",
		"controlPlane.image.repository=" + controlPlaneRepository,
		"--set-string",
		"controlPlane.image.tag=" + controlPlaneTag,
		"--set-string",
		"controlPlane.image.pullPolicy=IfNotPresent",
		"--set-string",
		"dataPlane.image.repository=" + gatewayRepository,
		"--set-string",
		"dataPlane.image.tag=" + gatewayTag,
		"--set-string",
		"dataPlane.image.pullPolicy=IfNotPresent",
		"--set-string",
		"operator.image.repository=" + operatorRepository,
		"--set-string",
		"operator.image.tag=" + operatorTag,
		"--set-string",
		"operator.image.pullPolicy=IfNotPresent",
		"--set-string",
		"controlPlane.relayRegistry.endpointAllowedHosts=" + host,
		"--set-string",
		"dataPlane.relayRegistry.endpoint=wss://" + host + "/tunnel",
		"--set",
		"controlPlane.development.enabled=true",
		"--set",
		"ingress.enabled=true",
		"--set-string",
		"ingress.className=nginx",
		"--set-string",
		"ingress.annotations.nginx\\.ingress\\.kubernetes\\.io/ssl-redirect=true",
		"--set-string",
		"ingress.host=" + host,
		"--set",
		"ingress.tls.enabled=true",
		"--set-string",
		"ingress.tls.secretName=" + ingressSecret,
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
	return publicURL, nil
}

func recoverDevelopmentHelmRelease(root string) error {
	statusCommand := exec.Command(
		"helm", "status", developmentRelease, "--namespace", developmentNamespace, "--output", "json",
	)
	statusOutput, err := statusCommand.Output()
	if err != nil {
		return nil
	}
	var status struct {
		Info struct {
			Status string `json:"status"`
		} `json:"info"`
	}
	if json.Unmarshal(statusOutput, &status) != nil || !strings.HasPrefix(status.Info.Status, "pending-") {
		return nil
	}
	fmt.Printf("==> Removing pending development Helm release before a clean install\n")
	return removeDevelopmentHelmRelease(root)
}

func removeDevelopmentHelmRelease(root string) error {
	if err := run(root, exec.Command(
		"helm", "uninstall", developmentRelease, "--namespace", developmentNamespace, "--wait", "--timeout", "3m",
	)); err != nil {
		return fmt.Errorf("remove incomplete development Helm release: %w", err)
	}
	return nil
}

func resetDevelopmentStorageForBaseline(root, contextName, directory string) error {
	marker := filepath.Join(directory, "storage-baseline")
	current, err := os.ReadFile(marker)
	if err == nil && strings.TrimSpace(string(current)) == developmentStorageBaseline {
		return nil
	}
	pvc := developmentRelease + "-kubeloop-control-plane-data"
	volumeOutput, _ := exec.Command(
		"kubectl", "get", "persistentvolumeclaim", pvc,
		"--namespace", developmentNamespace,
		"--output", "jsonpath={.spec.volumeName}",
	).Output()
	volumeName := strings.TrimSpace(string(volumeOutput))
	fmt.Printf("==> Resetting development SQLite storage for schema baseline %s\n", developmentStorageBaseline)
	deployment := developmentRelease + "-kubeloop-control-plane"
	deploymentOutput, err := exec.Command(
		"kubectl", "get", "deployment", deployment,
		"--namespace", developmentNamespace, "--ignore-not-found", "--output=name",
	).Output()
	if err != nil {
		return fmt.Errorf("inspect development Control Plane before storage reset: %w", err)
	}
	if strings.TrimSpace(string(deploymentOutput)) != "" {
		if err := run(root, exec.Command(
			"kubectl", "scale", "deployment", deployment, "--replicas=0",
			"--namespace", developmentNamespace,
		)); err != nil {
			return fmt.Errorf("stop development Control Plane before storage reset: %w", err)
		}
		if err := run(root, exec.Command(
			"kubectl", "wait", "--for=delete", "pod",
			"--namespace", developmentNamespace,
			"--selector", "app.kubernetes.io/instance="+developmentRelease+",app.kubernetes.io/component=control-plane",
			"--timeout=90s",
		)); err != nil {
			return fmt.Errorf("wait for development Control Plane shutdown: %w", err)
		}
	}
	if err := run(root, exec.Command(
		"kubectl", "delete", "persistentvolumeclaim", pvc,
		"--namespace", developmentNamespace, "--ignore-not-found", "--wait=true",
	)); err != nil {
		return fmt.Errorf("reset development SQLite storage: %w", err)
	}
	// Minikube's hostPath provisioner removes the backing directory while the PV
	// is deleted. Waiting only for the PVC can race a Helm recreation with that
	// cleanup and silently preserve an incompatible SQLite database.
	if volumeName != "" {
		if err := run(root, exec.Command(
			"kubectl", "wait", "--for=delete", "persistentvolume/"+volumeName,
			"--timeout=90s",
		)); err != nil {
			return fmt.Errorf("wait for development SQLite volume cleanup: %w", err)
		}
	}
	if profile, ok := minikubeProfile(contextName); ok {
		backingDirectory := filepath.Join(
			"/tmp/hostpath-provisioner", developmentNamespace, pvc,
		)
		cleanup := "if [ -d " + backingDirectory + " ]; then sudo find " + backingDirectory + " -mindepth 1 -maxdepth 1 -delete; fi"
		if err := run(root, exec.Command(
			"minikube", "-p", profile, "ssh", "--", cleanup,
		)); err != nil {
			return fmt.Errorf("clear Minikube development SQLite directory: %w", err)
		}
	}
	if err := os.WriteFile(marker, []byte(developmentStorageBaseline+"\n"), 0o600); err != nil {
		return fmt.Errorf("record development storage baseline: %w", err)
	}
	return nil
}

func generateDevelopmentIngressCertificate(root, directory, host string) (string, string, string, error) {
	mkcert, err := exec.LookPath("mkcert")
	if err != nil {
		return "", "", "", errors.New(
			"mkcert is required for development TLS; install it and ensure it is available in PATH",
		)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", "", "", fmt.Errorf("create development material directory: %w", err)
	}
	fmt.Printf("==> Installing the mkcert development CA in the local trust store\n")
	if err := run(root, exec.Command(mkcert, "-install")); err != nil {
		return "", "", "", fmt.Errorf("install mkcert development CA: %w", err)
	}
	caRootOutput, err := exec.Command(mkcert, "-CAROOT").Output()
	if err != nil {
		return "", "", "", fmt.Errorf("find mkcert CA root: %w", err)
	}
	caRoot := strings.TrimSpace(string(caRootOutput))
	if caRoot == "" {
		return "", "", "", errors.New("mkcert returned an empty CA root")
	}
	caCertificate := filepath.Join(caRoot, "rootCA.pem")
	certificate := filepath.Join(directory, "ingress-tls.crt")
	privateKey := filepath.Join(directory, "ingress-tls.key")
	for _, target := range []string{certificate, privateKey} {
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", "", "", fmt.Errorf("remove previous development ingress certificate material: %w", err)
		}
	}
	fmt.Printf("==> Generating mkcert TLS certificate for %s\n", host)
	command := exec.Command(
		mkcert,
		"-cert-file", certificate,
		"-key-file", privateKey,
		host,
	)
	if err := run(root, command); err != nil {
		return "", "", "", fmt.Errorf("generate mkcert development ingress certificate: %w", err)
	}
	if err := os.Chmod(certificate, 0o644); err != nil {
		return "", "", "", fmt.Errorf("set development ingress certificate permissions: %w", err)
	}
	if err := os.Chmod(privateKey, 0o600); err != nil {
		return "", "", "", fmt.Errorf("set development ingress private key permissions: %w", err)
	}
	if err := validateDevelopmentIngressCertificate(certificate, privateKey, caCertificate, host); err != nil {
		return "", "", "", err
	}
	return certificate, privateKey, caCertificate, nil
}

func validateDevelopmentIngressCertificate(certificateFile, privateKeyFile, caFile, host string) error {
	certificatePEM, err := os.ReadFile(certificateFile)
	if err != nil {
		return fmt.Errorf("read development ingress certificate: %w", err)
	}
	privateKeyPEM, err := os.ReadFile(privateKeyFile)
	if err != nil {
		return fmt.Errorf("read development ingress private key: %w", err)
	}
	pair, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil || len(pair.Certificate) == 0 {
		return errors.New("parse development ingress certificate pair")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return errors.New("parse development ingress leaf certificate")
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return fmt.Errorf("read mkcert development CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return errors.New("parse mkcert development CA")
	}
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: roots}); err != nil {
		return fmt.Errorf("verify mkcert development ingress certificate: %w", err)
	}
	return nil
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
		return "kubeloop-dev." + address + ".sslip.io", nil
	}
	return "kubeloop-dev.local", nil
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
