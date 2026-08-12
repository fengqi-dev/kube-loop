# Root build entry for KubeLoop. The Operator is a component of this Go module,
# not a nested Kubebuilder project.
.DEFAULT_GOAL := help

SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

OPERATOR_IMG ?= ghcr.io/fengqi-dev/kube-loop/operator:latest
CONTAINER_TOOL ?= docker
YEAR ?= $(shell date +%Y)

LOCALBIN ?= $(CURDIR)/build/tools
KUBECTL ?= kubectl
MINIKUBE ?= minikube
MINIKUBE_DRIVER ?=
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint

KUSTOMIZE_VERSION ?= v5.8.1
CONTROLLER_TOOLS_VERSION ?= v0.21.0
GOLANGCI_LINT_VERSION ?= v2.12.2
ENVTEST_VERSION ?= $(shell v='$(call gomodver,sigs.k8s.io/controller-runtime)'; \
	[ -n "$$v" ] || { echo "Set ENVTEST_VERSION manually (controller-runtime replace has no tag)" >&2; exit 1; }; \
	printf '%s\n' "$$v")
ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
	[ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually (k8s.io/api replace has no tag)" >&2; exit 1; }; \
	printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

OPERATOR_E2E_PROFILE ?= kubeloop-operator-test-e2e
HELM_E2E_PROFILE ?= kubeloop-helm-e2e
HELM_E2E_CONTROL_PLANE_IMAGE ?= kubeloop/control-plane:e2e
HELM_E2E_DATA_PLANE_IMAGE ?= kubeloop/gateway:e2e
HELM_E2E_OPERATOR_IMAGE ?= kubeloop/operator:e2e
HELM_E2E_POSTGRES_IMAGE ?= postgres:17-alpine
HELM_E2E_BUSYBOX_IMAGE ?= busybox:1.36.1

SINGBOX_TARGET ?= $(shell go env GOOS)/$(shell go env GOARCH)
SINGBOX_GOOS = $(word 1,$(subst /, ,$(SINGBOX_TARGET)))
SINGBOX_GOARCH = $(word 2,$(subst /, ,$(SINGBOX_TARGET)))
SINGBOX_VERSION ?= v1.13.18
SINGBOX_BINARY = build/bin/sing-box$(if $(filter windows,$(SINGBOX_GOOS)),.exe,)

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-28s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Patched sing-box

.PHONY: singbox-patch-check
singbox-patch-check: ## Verify the patch applies to the exact pinned upstream revision.
	go run ./build/singbox-patched.go -check

.PHONY: singbox-build
singbox-build: singbox-patch-check ## Build the minimal sing-box for SINGBOX_TARGET.
	go run ./build/singbox-patched.go -target "$(SINGBOX_TARGET)" -output "$(SINGBOX_BINARY)"

.PHONY: singbox-package
singbox-package: singbox-build ## Create local binary and reconstructable patch archives under dist/.
	@set -eu; \
	version="$(SINGBOX_VERSION)"; version="$${version#v}"; \
	stage="$$(mktemp -d "$${TMPDIR:-/tmp}/kubeloop-singbox.XXXXXX")"; \
	trap 'rm -rf "$$stage"' EXIT; \
	mkdir -p dist "$$stage/bin" "$$stage/patches"; \
	cp "$(SINGBOX_BINARY)" "$$stage/bin/$$(basename "$(SINGBOX_BINARY)")"; \
	cp build/bin/LICENSE.sing-box.txt "$$stage/bin/LICENSE.sing-box.txt"; \
	cp third_party/patches/sing-box/README.md "$$stage/patches/README.md"; \
	cp third_party/patches/sing-box/*.patch "$$stage/patches/"; \
	tar -C "$$stage/bin" -czf "dist/sing-box-kubeloop-$$version-$(SINGBOX_GOOS)-$(SINGBOX_GOARCH).tar.gz" .; \
	tar -C "$$stage/patches" -czf "dist/sing-box-kubeloop-$$version-patches.tar.gz" .; \
	(cd dist && shasum -a 256 "sing-box-kubeloop-$$version-$(SINGBOX_GOOS)-$(SINGBOX_GOARCH).tar.gz" "sing-box-kubeloop-$$version-patches.tar.gz" > "sing-box-kubeloop-$$version-SHA256SUMS"); \
	ls -lh dist/sing-box-kubeloop-$$version-*

##@ Operator development

.PHONY: operator-manifests manifests
operator-manifests: controller-gen ## Generate Operator CRDs and RBAC from markers.
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd webhook paths="./api/...;./internal/controller/..." output:crd:artifacts:config=config/crd/bases
	cp config/crd/bases/traffic.kubeloop.io_trafficbindings.yaml charts/kubeloop/crds/traffic.kubeloop.io_trafficbindings.yaml
manifests: operator-manifests ## Kubebuilder-compatible alias for Operator manifest generation.

.PHONY: operator-generate generate
operator-generate: controller-gen ## Generate Operator DeepCopy implementations.
	"$(CONTROLLER_GEN)" object paths="./api/..."
generate: operator-generate ## Kubebuilder-compatible alias for Operator code generation.

.PHONY: operator-fmt
operator-fmt: ## Format Operator source and tests.
	go fmt ./api/... ./internal/controller/... ./cmd/kubeloop-operator ./e2e/operator/...

.PHONY: operator-vet
operator-vet: ## Run go vet for the Operator component.
	go vet ./api/... ./internal/controller/... ./cmd/kubeloop-operator

.PHONY: operator-test
operator-test: operator-manifests operator-generate operator-fmt operator-vet setup-envtest ## Run Operator unit and EnvTest suites.
	mkdir -p build
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" \
		go test ./api/... ./internal/controller/... ./cmd/kubeloop-operator -coverprofile build/operator-cover.out

.PHONY: operator-lint
operator-lint: golangci-lint ## Lint only the Operator component.
	"$(GOLANGCI_LINT)" run ./api/... ./internal/controller/... ./cmd/kubeloop-operator

.PHONY: operator-lint-fix
operator-lint-fix: golangci-lint ## Apply safe lint fixes to the Operator component.
	"$(GOLANGCI_LINT)" run --fix ./api/... ./internal/controller/... ./cmd/kubeloop-operator

.PHONY: operator-lint-config
operator-lint-config: golangci-lint ## Verify the repository lint configuration.
	"$(GOLANGCI_LINT)" config verify

##@ Operator build

.PHONY: operator-build
operator-build: operator-manifests operator-generate operator-fmt operator-vet ## Build the Operator binary.
	mkdir -p build/bin
	go build -o build/bin/kubeloop-operator ./cmd/kubeloop-operator

.PHONY: operator-run
operator-run: operator-manifests operator-generate operator-fmt operator-vet ## Run the Operator against the current kubeconfig.
	go run ./cmd/kubeloop-operator

.PHONY: operator-docker-build
operator-docker-build: ## Build the Operator container image from the repository root.
	$(CONTAINER_TOOL) build -f build/operator.Dockerfile -t $(OPERATOR_IMG) .

.PHONY: operator-docker-push
operator-docker-push: ## Push the Operator container image.
	$(CONTAINER_TOOL) push $(OPERATOR_IMG)

.PHONY: operator-build-installer
operator-build-installer: operator-manifests operator-generate kustomize ## Build a consolidated Operator installation manifest.
	mkdir -p dist
	@runtime_dir="$$(mktemp -d "$${TMPDIR:-/tmp}/kubeloop-operator-build.XXXXXX")"; \
	trap 'rm -rf "$$runtime_dir"' EXIT; \
	cp -R config "$$runtime_dir/config"; \
	cd "$$runtime_dir/config/manager" && "$(KUSTOMIZE)" edit set image controller=$(OPERATOR_IMG); \
	"$(KUSTOMIZE)" build "$$runtime_dir/config/default" > "$(CURDIR)/dist/operator-install.yaml"

##@ Operator deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: operator-install
operator-install: operator-manifests kustomize ## Install Operator CRDs into the current cluster.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: operator-uninstall
operator-uninstall: operator-manifests kustomize ## Remove Operator CRDs from the current cluster.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

.PHONY: operator-deploy
operator-deploy: operator-manifests kustomize ## Deploy the Operator into the current cluster.
	@runtime_dir="$$(mktemp -d "$${TMPDIR:-/tmp}/kubeloop-operator-deploy.XXXXXX")"; \
	trap 'rm -rf "$$runtime_dir"' EXIT; \
	cp -R config "$$runtime_dir/config"; \
	cd "$$runtime_dir/config/manager" && "$(KUSTOMIZE)" edit set image controller=$(OPERATOR_IMG); \
	"$(KUSTOMIZE)" build "$$runtime_dir/config/default" | "$(KUBECTL)" apply -f -

.PHONY: operator-undeploy
operator-undeploy: kustomize ## Remove the Operator deployment from the current cluster.
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -

##@ Operator end-to-end tests

.PHONY: operator-setup-test-e2e
operator-setup-test-e2e: ## Create the isolated Minikube profile when it does not exist.
	@command -v $(MINIKUBE) >/dev/null 2>&1 || { echo "Minikube is not installed. Please install Minikube manually."; exit 1; }
	@if [ "$$($(MINIKUBE) status --profile $(OPERATOR_E2E_PROFILE) --format='{{.Host}}' 2>/dev/null || true)" = "Running" ]; then \
		echo "Minikube profile '$(OPERATOR_E2E_PROFILE)' already exists. Skipping creation."; \
	else \
		echo "Creating Minikube profile '$(OPERATOR_E2E_PROFILE)'..."; \
		$(MINIKUBE) start --profile $(OPERATOR_E2E_PROFILE) $(if $(MINIKUBE_DRIVER),--driver=$(MINIKUBE_DRIVER),); \
	fi

.PHONY: operator-test-e2e
operator-test-e2e: operator-setup-test-e2e operator-manifests operator-generate operator-fmt operator-vet ## Run Operator E2E tests in Minikube.
	@runtime_dir="$$(mktemp -d "$${TMPDIR:-/tmp}/kubeloop-operator-e2e.XXXXXX")"; \
	trap 'rm -rf "$$runtime_dir"' EXIT; \
	"$(KUBECTL)" config view --raw --flatten --minify --context="$(OPERATOR_E2E_PROFILE)" > "$$runtime_dir/kubeconfig"; \
	KUBECONFIG="$$runtime_dir/kubeconfig" MINIKUBE=$(MINIKUBE) MINIKUBE_PROFILE=$(OPERATOR_E2E_PROFILE) \
		go test -tags=e2e ./e2e/operator/e2e -v -ginkgo.v

.PHONY: operator-cleanup-test-e2e
operator-cleanup-test-e2e: ## Delete the isolated Operator Minikube profile.
	@$(MINIKUBE) delete --profile $(OPERATOR_E2E_PROFILE)

##@ Helm end-to-end tests

.PHONY: helm-e2e-images
helm-e2e-images: ## Build the Control Plane, Data Plane and Operator E2E images.
	$(CONTAINER_TOOL) build -f build/control-plane.Dockerfile -t $(HELM_E2E_CONTROL_PLANE_IMAGE) .
	$(CONTAINER_TOOL) build -f build/gateway.Dockerfile -t $(HELM_E2E_DATA_PLANE_IMAGE) .
	$(CONTAINER_TOOL) build -f build/operator.Dockerfile -t $(HELM_E2E_OPERATOR_IMAGE) .

.PHONY: helm-setup-test-e2e
helm-setup-test-e2e: ## Start the dedicated Helm lifecycle Minikube profile with API Server auditing.
	@command -v $(MINIKUBE) >/dev/null 2>&1 || { echo "Minikube is not installed. Please install Minikube manually."; exit 1; }
	@minikube_home="$${MINIKUBE_HOME:-$${HOME}/.minikube}"; \
		mkdir -p "$${minikube_home}/files/etc/ssl/certs"; \
		cp e2e/impersonation/audit-policy.yaml "$${minikube_home}/files/etc/ssl/certs/kubeloop-audit-policy.yaml"; \
		if [ "$$($(MINIKUBE) status --profile $(HELM_E2E_PROFILE) --format='{{.Host}}' 2>/dev/null || true)" = "Running" ]; then \
			$(MINIKUBE) stop --profile $(HELM_E2E_PROFILE); \
		fi; \
		$(MINIKUBE) start --profile $(HELM_E2E_PROFILE) $(if $(MINIKUBE_DRIVER),--driver=$(MINIKUBE_DRIVER),) \
			--extra-config=apiserver.audit-policy-file=/etc/ssl/certs/kubeloop-audit-policy.yaml \
			--extra-config=apiserver.audit-log-path=-

.PHONY: helm-load-test-e2e-images
helm-load-test-e2e-images: helm-setup-test-e2e ## Build application images in Minikube and load dependency images.
	$(MINIKUBE) --profile $(HELM_E2E_PROFILE) image build -f build/control-plane.Dockerfile -t $(HELM_E2E_CONTROL_PLANE_IMAGE) .
	@image='$(HELM_E2E_CONTROL_PLANE_IMAGE)'; $(MINIKUBE) --profile $(HELM_E2E_PROFILE) image ls --format short | awk -v image="$$image" '$$0 == image || substr($$0, length($$0) - length(image) + 1) == image { found = 1 } END { exit !found }'
	$(MINIKUBE) --profile $(HELM_E2E_PROFILE) image build -f build/gateway.Dockerfile -t $(HELM_E2E_DATA_PLANE_IMAGE) .
	@image='$(HELM_E2E_DATA_PLANE_IMAGE)'; $(MINIKUBE) --profile $(HELM_E2E_PROFILE) image ls --format short | awk -v image="$$image" '$$0 == image || substr($$0, length($$0) - length(image) + 1) == image { found = 1 } END { exit !found }'
	$(MINIKUBE) --profile $(HELM_E2E_PROFILE) image build -f build/operator.Dockerfile -t $(HELM_E2E_OPERATOR_IMAGE) .
	@image='$(HELM_E2E_OPERATOR_IMAGE)'; $(MINIKUBE) --profile $(HELM_E2E_PROFILE) image ls --format short | awk -v image="$$image" '$$0 == image || substr($$0, length($$0) - length(image) + 1) == image { found = 1 } END { exit !found }'
	$(MINIKUBE) --profile $(HELM_E2E_PROFILE) image load --remote $(HELM_E2E_POSTGRES_IMAGE)
	@image='$(HELM_E2E_POSTGRES_IMAGE)'; $(MINIKUBE) --profile $(HELM_E2E_PROFILE) image ls --format short | awk -v image="$$image" '$$0 == image || substr($$0, length($$0) - length(image) + 1) == image { found = 1 } END { exit !found }'
	$(MINIKUBE) --profile $(HELM_E2E_PROFILE) image load --remote $(HELM_E2E_BUSYBOX_IMAGE)
	@image='$(HELM_E2E_BUSYBOX_IMAGE)'; $(MINIKUBE) --profile $(HELM_E2E_PROFILE) image ls --format short | awk -v image="$$image" '$$0 == image || substr($$0, length($$0) - length(image) + 1) == image { found = 1 } END { exit !found }'

.PHONY: helm-test-e2e
helm-test-e2e: helm-load-test-e2e-images ## Run install, upgrade, rollback, retention, recovery and uninstall checks.
	KUBELOOP_HELM_E2E=1 \
	KUBELOOP_HELM_E2E_CONTEXT=$(HELM_E2E_PROFILE) \
	KUBELOOP_HELM_E2E_AUDIT_SOURCE=kube-apiserver \
	KUBELOOP_HELM_E2E_CONTROL_PLANE_IMAGE=$(HELM_E2E_CONTROL_PLANE_IMAGE) \
	KUBELOOP_HELM_E2E_DATA_PLANE_IMAGE=$(HELM_E2E_DATA_PLANE_IMAGE) \
	KUBELOOP_HELM_E2E_OPERATOR_IMAGE=$(HELM_E2E_OPERATOR_IMAGE) \
	KUBELOOP_HELM_E2E_POSTGRES_IMAGE=$(HELM_E2E_POSTGRES_IMAGE) \
	KUBELOOP_HELM_E2E_BUSYBOX_IMAGE=$(HELM_E2E_BUSYBOX_IMAGE) \
		./e2e/helm/run.sh

.PHONY: helm-cleanup-test-e2e
helm-cleanup-test-e2e: ## Delete the dedicated Helm lifecycle Minikube profile.
	@$(MINIKUBE) delete --profile $(HELM_E2E_PROFILE)

##@ Quality gates

.PHONY: test-local
test-local: ## Run all non-E2E tests and vet locally.
	go test ./... -count=1
	go vet ./...

.PHONY: capacity-baseline
capacity-baseline: ## Verify capacity limits and record logical-stream throughput/allocation baselines.
	go test ./internal/gateway/websocketmux -run '^TestCapacity' -count=1 -v
	go test ./internal/gateway/websocketmux -run '^$$' -bench '^BenchmarkGatewayLogicalStreamRoundTrip$$' -benchmem -benchtime=1s -count=3

.PHONY: recovery-test
recovery-test: ## Run resource recovery, owner-safety and Task lifecycle tests with the race detector.
	go test -race -count=1 \
		./internal/remotetask \
		./internal/controlplane/servicebinding \
		./internal/controlplane/storage \
		./internal/controlplane/exchangeapi \
		./internal/controlplane/mirrorapi \
		./internal/controlplane/previewapi \
		./internal/controlplane/portforwardapi \
		./internal/controlplane/execapi \
		./internal/controlplane/fileapi \
		./internal/controlplane/trafficbindingclient \
		./internal/controlplane/maintenance \
		./internal/controller

##@ Tool dependencies

$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download Kubernetes EnvTest binaries.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@"$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3); \
echo "Downloading $${package}"; \
rm -f "$(1)"; \
GOBIN="$(LOCALBIN)" go install $${package}; \
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)"; \
}; \
ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"
endef

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef
