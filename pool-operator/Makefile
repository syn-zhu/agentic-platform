# pool-operator/Makefile

CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen

.PHONY: generate test build lint

generate:
	$(CONTROLLER_GEN) object paths="./api/..."
	$(CONTROLLER_GEN) crd paths="./api/..." output:crd:dir=./config/crd

test:
	go test ./... -race -count=1

build:
	go build -o bin/pool-operator ./cmd/operator

lint:
	go vet ./...
