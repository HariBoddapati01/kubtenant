# kubtenant — self-service, guardrailed multi-tenancy operator for Kubernetes.

IMG ?= ghcr.io/hariboddapati01/kubtenant:latest
CONTROLLER_GEN_VERSION ?= v0.21.0
CONTROLLER_GEN = go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)

.DEFAULT_GOAL := help

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Development

.PHONY: generate
generate: ## Generate DeepCopy methods.
	$(CONTROLLER_GEN) object:headerFile=/dev/null paths=./api/...

.PHONY: manifests
manifests: ## Generate CRD and RBAC manifests.
	$(CONTROLLER_GEN) crd rbac:roleName=kubtenant-manager-role paths=./... \
		output:crd:artifacts:config=config/crd/bases output:rbac:artifacts:config=config/rbac

.PHONY: fmt
fmt: ## Run go fmt.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet.
	go vet ./...

.PHONY: test
test: generate fmt vet ## Run unit tests.
	go test ./... -race -coverprofile=cover.out

ENVTEST_K8S_VERSION ?= 1.36.x
SETUP_ENVTEST = go run sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.24

.PHONY: test-integration
test-integration: generate ## Run envtest integration tests against a real kube-apiserver.
	KUBEBUILDER_ASSETS="$$($(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(PWD)/bin -p path)" \
		go test ./internal/controller -tags=integration -run TestIntegration -v -timeout 10m

.PHONY: lint
lint: ## Run golangci-lint.
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...

.PHONY: helm-lint
helm-lint: ## Lint the Helm chart and check the chart CRD matches config/crd.
	helm lint charts/kubtenant
	diff -u config/crd/bases/platform.hariboddapati.dev_tenants.yaml \
		charts/kubtenant/crds/platform.hariboddapati.dev_tenants.yaml \
		|| (echo "chart CRD is out of date; run: cp config/crd/bases/*.yaml charts/kubtenant/crds/" && exit 1)

.PHONY: demo
demo: ## Full local demo on kind: build, helm install, self-heal + GC walkthrough.
	hack/demo.sh

##@ Build

.PHONY: build
build: generate fmt vet ## Build the manager binary.
	go build -trimpath -o bin/manager cmd/main.go

.PHONY: run
run: generate fmt vet ## Run the controller locally against the current kubeconfig.
	go run ./cmd/main.go

.PHONY: docker-build
docker-build: ## Build the container image.
	docker build -t $(IMG) .

.PHONY: docker-push
docker-push: ## Push the container image.
	docker push $(IMG)

##@ Deployment

.PHONY: install
install: manifests ## Install CRDs into the cluster.
	kubectl apply -f config/crd/bases

.PHONY: uninstall
uninstall: ## Remove CRDs from the cluster.
	kubectl delete -f config/crd/bases

.PHONY: deploy
deploy: install ## Deploy the controller to the cluster.
	kubectl apply -f config/rbac/service_account.yaml
	kubectl apply -f config/rbac/role.yaml
	kubectl apply -f config/rbac/role_binding.yaml
	kubectl apply -f config/manager/manager.yaml

.PHONY: undeploy
undeploy: ## Remove the controller from the cluster.
	kubectl delete -f config/manager/manager.yaml --ignore-not-found
	kubectl delete -f config/rbac/role_binding.yaml --ignore-not-found
	kubectl delete -f config/rbac/role.yaml --ignore-not-found
	kubectl delete -f config/rbac/service_account.yaml --ignore-not-found
