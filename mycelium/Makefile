.PHONY: generate manifests test build-engine build-controller

CONTROLLER_GEN ?= $(shell go env GOPATH)/bin/controller-gen

generate:
	$(CONTROLLER_GEN) object paths="./api/..."

manifests:
	$(CONTROLLER_GEN) crd paths="./api/..." output:crd:artifacts:config=config/crd/bases
	$(CONTROLLER_GEN) rbac:roleName=mycelium-controller paths="./internal/controller/..." output:rbac:artifacts:config=config/rbac

test:
	go test ./... -v -count=1

build-engine:
	go build -o bin/engine ./cmd/engine

build-controller:
	go build -o bin/controller ./cmd/controller
