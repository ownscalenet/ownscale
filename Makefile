# Build the ownscale binary. The frontend (frontend/) is a React + Vite
# app that builds directly into internal/webui/dist, which is embedded into
# the Go binary via go:embed. internal/webui/dist is committed to the repo
# (go:embed needs real files at Go build time), so run `make frontend`
# whenever frontend/ changes and commit the updated internal/webui/dist.

.PHONY: build frontend backend osa test coverage migration archive docker-image rm-orphans purge ci

DOCKER_IMAGE := ownscale:latest

build: frontend backend osa

frontend:
	npm --prefix frontend ci
	npm --prefix frontend run build

backend:
	go build -o bin/ownscale ./cmd/ownscale

osa:
	go build -o bin/osa ./cmd/osa

# Usage: make migration up | make migration status
migration:
	go run ./cmd/osa --config config.yaml migrate $(filter-out migration,$(MAKECMDGOALS))

up down status:
	@:

run:
	go run ./cmd/ownscale

test:
	go test ./...

BROWSER := firefox
coverage:
	go test -coverprofile=coverage.out ./...
	BROWSER=$(BROWSER) go tool cover -html=coverage.out

# Build the ownscale Docker image locally, tagged to match the "image:"
# name docker-compose.yml expects for the ownscale service.
docker-image:
	docker build -t $(DOCKER_IMAGE) .

# ghetto deploy script
# Send the ownscale image and repo files to remote.
archive: docker-image
	docker save $(DOCKER_IMAGE) | ssh az 'docker load'
	tar czf - \
		--exclude='.git' \
		--exclude='config.yaml' \
		--exclude='.env' \
		--exclude='data' \
		--exclude='node_modules' \
		--exclude='bin' \
		-C .. $(notdir $(CURDIR)) \
		| ssh az 'mkdir -p ownscale && tar -xzf - -C ownscale --strip-components=1'
mota:
	docker compose exec ownscale ./osa organization create mota

purge:
	sudo rm -rf data
	docker ps -aq --filter "label=ownscale.organization=true" | xargs -r docker stop | xargs -r docker rm

rm-orphans:
	docker ps -aq --filter "label=ownscale.organization=true" | xargs -r docker stop | xargs -r docker rm

# Run the same checks as .github/workflows/ci.yml, for local sanity-checking
# before pushing. Requires golangci-lint and govulncheck.
ci:
	go build ./...
	go vet ./...
	go fix ./...
	go test -race -shuffle=on -covermode=atomic -coverprofile=coverage.out ./...
	go mod tidy
	go run golang.org/x/tools/cmd/goimports@latest -w .
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...
	go run golang.org/x/tools/cmd/deadcode@latest ./cmd/...
