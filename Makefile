# NUMA Resource Plugin Makefile

BINARY_NAME=numa-resource-plugin
IMAGE_NAME=numa-resource-plugin
REGISTRY=registry.local:5000
VERSION?=latest

GO=go
GOFLAGS=-ldflags="-s -w"

.PHONY: all build clean test docker-build docker-push deploy undeploy fmt vet

all: build

## Build
build:
	$(GO) build $(GOFLAGS) -o bin/$(BINARY_NAME) ./cmd/plugin

build-linux:
	GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -o bin/$(BINARY_NAME) ./cmd/plugin

## Test
test:
	$(GO) test -v ./...

test-coverage:
	$(GO) test -v -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

test-race:
	$(GO) test -race -v ./...

## Lint/Format
fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

## Docker
docker-build: build-linux
	docker build -t $(IMAGE_NAME):$(VERSION) .

docker-push: docker-build
	docker tag $(IMAGE_NAME):$(VERSION) $(REGISTRY)/$(IMAGE_NAME):$(VERSION)
	docker push $(REGISTRY)/$(IMAGE_NAME):$(VERSION)

## Kubernetes Deployment
deploy:
	kubectl apply -f deployments/serviceaccount.yaml
	kubectl apply -f deployments/configmap.yaml
	kubectl apply -f deployments/daemonset.yaml

undeploy:
	kubectl delete -f deployments/daemonset.yaml --ignore-not-found
	kubectl delete -f deployments/configmap.yaml --ignore-not-found
	kubectl delete -f deployments/serviceaccount.yaml --ignore-not-found

## Integration Tests
test-e2e: deploy
	./test/scripts/test-numa-allocation.sh

## Clean
clean:
	rm -rf bin/
	rm -f coverage.out coverage.html

## Help
help:
	@echo "Available targets:"
	@echo "  build         - Build the plugin binary"
	@echo "  build-linux   - Build for Linux amd64"
	@echo "  test          - Run unit tests"
	@echo "  test-coverage - Run tests with coverage report"
	@echo "  test-race     - Run tests with race detector"
	@echo "  fmt           - Format Go code"
	@echo "  vet           - Run go vet"
	@echo "  docker-build  - Build Docker image"
	@echo "  docker-push   - Push to local registry"
	@echo "  deploy        - Deploy to Kubernetes"
	@echo "  undeploy      - Remove from Kubernetes"
	@echo "  test-e2e      - Run end-to-end tests"
	@echo "  clean         - Remove build artifacts"
