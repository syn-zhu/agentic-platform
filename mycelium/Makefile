.PHONY: codegen agw-applycfg generate manifests vet test build-engine build-controller

CONTROLLER_GEN ?= $(shell go env GOPATH)/bin/controller-gen
AGW_APIS_PKG   := github.com/agentgateway/agentgateway

# codegen runs all code generation steps.
# Run after any change to api/ or to agentgateway API types.
# Requires go.work pointing at a local agentgateway checkout for the agw-applycfg step.
codegen: agw-applycfg generate manifests

# agw-applycfg generates typed apply-configuration builders for the agentgateway
# API types and writes them into internal/applyconfiguration/. Generated files are
# committed — both go build and make codegen read from vendor/ with no local
# agentgateway checkout required. Re-run after updating agentgateway in vendor/.
agw-applycfg:
	go tool applyconfiguration-gen \
	  --output-dir ./internal/applyconfiguration \
	  --output-pkg mycelium.io/mycelium/internal/applyconfiguration \
	  "$(AGW_APIS_PKG)/controller/api/v1alpha1/agentgateway"

generate:
	$(CONTROLLER_GEN) object paths="./api/..."

manifests:
	$(CONTROLLER_GEN) crd paths="./api/..." output:crd:artifacts:config=config/crd/bases
	$(CONTROLLER_GEN) rbac:roleName=mycelium-controller paths="./internal/controller/..." output:rbac:artifacts:config=config/rbac

vet:
	go vet ./...

test:
	go test ./... -v -count=1

build-engine:
	go build -o bin/engine ./cmd/engine

build-controller:
	go build -o bin/controller ./cmd/controller
