SERVICE_NAME := binancetrader
IMAGE_NAME = github.com/foae/binancetrader
VERSION := latest
GIT_COMMIT_SHA = $(shell git describe --tags --always --dirty)
BUILD_ARGS := --build-arg GIT_COMMIT=$(shell git show -s --format=%H) \
	--build-arg APP_VERSION="$(shell git log -1 --date=format:%Y%m%d%H%M%S --format=%cd-%h)" \
	--build-arg GIT_COMMIT_DATE="$(shell git show -s --format=%ci)" \
	--build-arg BUILD_DATE=$(shell date +"%s") \
	--build-arg HOST_MACHINE="$(shell hostname)" \
	--build-arg IMAGE_NAME="$(IMAGE_NAME)"

.PHONY: clean-logs
clean-logs:
	cat /dev/null > ./binancetrader.log
	printf "\n=== Cleaned up binancetrader.log file\n"

.PHONY: run
run:
	go run -race ./cmd/binancetrader/main.go

.PHONY: run-logged
run-logged: clean-logs
	go run -race ./cmd/binancetrader/main.go >> binancetrader.log 2>&1

.PHONY: test
test:
	go fmt ./...
	go vet ./...
	go test -race ./...

.PHONY: docker-build
build-docker:
	docker buildx build \
		--cache-to type=inline \
		--load \
		$(BUILD_ARGS) \
		-f Dockerfile \
		-t $(IMAGE_NAME):$(VERSION) .

.PHONY: docker-run
run-docker: docker-build
	@echo "Starting $(SERVICE_NAME) in Docker..."
	docker run -it --rm \
		--name $(SERVICE_NAME) \
		--network=host \
		$(shell test -f .env && echo "--env-file .env") \
		$(IMAGE_NAME):$(VERSION)

# --- Claude Code Docker Environment ---

COMPOSE_CLAUDE = UID=$(shell id -u) GID=$(shell id -g) \
	DOCKER_GID=$(shell stat -c '%g' /var/run/docker.sock) \
	docker compose -f docker-compose.claude.yml

.PHONY: claude-build
claude-build:
	$(COMPOSE_CLAUDE) build claude

.PHONY: claude-up
claude-up: claude-build
	$(COMPOSE_CLAUDE) up -d --no-recreate claude

.PHONY: claude
claude: claude-up
	$(COMPOSE_CLAUDE) exec claude claude

.PHONY: claude-shell
claude-shell: claude-up
	$(COMPOSE_CLAUDE) exec claude bash

.PHONY: claude-root
claude-root: claude-up
	$(COMPOSE_CLAUDE) exec -u root claude bash

.PHONY: claude-stop
claude-stop:
	$(COMPOSE_CLAUDE) stop claude

.PHONY: claude-rebuild
claude-rebuild:
	$(COMPOSE_CLAUDE) build claude
	$(COMPOSE_CLAUDE) up -d --force-recreate claude

.PHONY: claude-down
claude-down:
	$(COMPOSE_CLAUDE) down
