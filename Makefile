.PHONY: build test clean docker-build docker-push vendor

IMAGE_REPO ?= foundation-platform-docker.registry.vptech.eu/machine-controller-manager-provider-vpcloud
IMAGE_TAG ?= v0.2.2

build:
	go build -mod=vendor -o bin/machine-controller ./cmd/machine-controller/

test:
	go test -mod=vendor ./... -v

clean:
	rm -rf bin/

vendor:
	GOPRIVATE='git.vptech.eu/*' GONOSUMCHECK='git.vptech.eu/*' go mod vendor

docker-build:
	docker build --platform linux/amd64 -t $(IMAGE_REPO):$(IMAGE_TAG) .

docker-push: docker-build
	docker push $(IMAGE_REPO):$(IMAGE_TAG)
