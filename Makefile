IMAGE_NAME ?= binancetrader
VERSION := $(shell cat VERSION)
GIT_COMMIT := $(shell git rev-parse HEAD)

.PHONY: run test build build-docker run-docker release
run:
	go run -race ./cmd/binancetrader

test:
	@test -z "$$(gofmt -l cmd exchange service storage)" || { echo "Run gofmt on the listed source directories"; exit 1; }
	go vet ./...
	go test -race ./...

build:
	go build -trimpath -o binancetrader ./cmd/binancetrader

build-docker:
	docker build --build-arg APP_VERSION="$(VERSION)" --build-arg GIT_COMMIT="$(GIT_COMMIT)" -t $(IMAGE_NAME):$(VERSION) .

run-docker:
	docker compose up --build

# Example: make release RELEASE_VERSION=1.0.1 RELEASE_NAME="Reconciliation fixes" RELEASE_NOTES=release-notes.md
release:
	bash scripts/release.sh "$(RELEASE_VERSION)" "$(RELEASE_NAME)" "$(RELEASE_NOTES)"
