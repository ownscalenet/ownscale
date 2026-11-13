# Development of ownscale. Run `make` for the list of targets. The frontend
# (frontend/) builds into webui/dist and the docs (docs/) into docsite/dist;
# both are embedded into the Go binary.

include deploy.mk

# Set by deploy.mk while `make osa ...` or `make restore ...` passes its goals
# through as arguments.
ifndef OSA_MODE

DEVBOX_IMAGE := ownscale-devbox:latest
GHCR_IMAGE := ghcr.io/ownscalenet/ownscale

# Containers run as the host user.
export HOST_UID := $(shell id -u)
export HOST_GID := $(shell id -g)

# Dev stack domain, set in compose.dev.yaml.
BASE_DOMAIN := ownscale.test
DATA_DIR := $(or $(OWNSCALE_DATA_DIR),data)
CERT := $(DATA_DIR)/local-ca/root.crt

# Shown in the console sidebar and the docs. Same <tag>-<sha> shape as the
# release workflow; -dirty marks uncommitted changes.
export BUILD_VERSION := $(shell git describe --tags --abbrev=0 2>/dev/null || echo dev)-$(shell git rev-parse --short HEAD 2>/dev/null)$(shell git diff --quiet HEAD 2>/dev/null || echo -dirty)

require_cert = @test -f $(CERT) || { echo "$(CERT) not found -- start the dev stack (local-ca-init) first"; exit 1; }

##@ Build

.PHONY: build frontend docs backend
build: frontend docs backend ## Build the frontend, docs and Go binaries

frontend: ## Build the console into webui/dist
	npm --prefix frontend ci
	npm --prefix frontend run build
	# Tailscale client compiled to WASM; needed for browser SSH page.
	cp frontend/node_modules/@tailscale/connect/main.wasm.zst webui/dist/assets/

# .gitkeep lets the binary build without docs.
docs: ## Build the docs site into docsite/dist
	find docsite/dist -mindepth 1 ! -name .gitkeep -delete
	HUGO_PARAMS_VERSION=$(BUILD_VERSION) hugo --source docs --destination ../docsite/dist --minify

backend: ## Build ownscale and osa into bin/
	go build -ldflags "-X github.com/ownscalenet/ownscale/utils.Version=$(BUILD_VERSION)" -o bin/ ./cmd/ownscale ./cmd/osa

##@ Dev stack

.PHONY: dev-up purge load-cert devbox stress
# Upserts into .env what compose.dev.yaml can't set, so plain `docker compose`
# sees the same stack: COMPOSE_FILE, HOST_UID/HOST_GID, and a session secret
# for compose.yaml's required check. The data dir is created first, or docker
# creates it root-owned.
BUILD ?=
DETACH ?=
dev-up: ## Start the dev stack at ownscale.test (BUILD=1 rebuilds, DETACH=1 backgrounds)
	@touch .env
	@mkdir -p $(DATA_DIR)
	@{ grep -vE '^(COMPOSE_FILE|HOST_UID|HOST_GID)=|^OWNSCALE_AUTH_SESSION_SECRET=$$' .env; \
		echo COMPOSE_FILE=compose.yaml:compose.dev.yaml; \
		echo HOST_UID=$(HOST_UID); \
		echo HOST_GID=$(HOST_GID); \
		grep -q '^OWNSCALE_AUTH_SESSION_SECRET=.' .env || echo OWNSCALE_AUTH_SESSION_SECRET=dev-session-secret; \
	} > .env.tmp && cat .env.tmp > .env && rm .env.tmp
	docker compose up $(if $(BUILD),--build) $(if $(DETACH),-d)

purge: ## Stop the dev stack and delete its data, keeping the local CA
	docker compose down
	sudo find $(DATA_DIR) -mindepth 1 -maxdepth 1 ! -name local-ca -exec rm -rf {} +
	# Recovery records of the connectivity suite describe orgs that are now gone.
	rm -rf e2e/.connectivity-private/recovery
	# Locks left by killed e2e runs.
	rm -f e2e/.*-run.lock

# To trust it in Firefox by hand: about:preferences#privacy, View
# Certificates, Authorities, Import $(CERT), "Trust this CA to identify websites".
load-cert: ## Trust the dev stack's local CA in Firefox and the OS
	$(require_cert)
	@command -v certutil >/dev/null || { echo "certutil not found -- install it (e.g. 'sudo apt install libnss3-tools')"; exit 1; }
	@found=0; \
	for profiles_dir in "$$HOME/.mozilla/firefox" "$$HOME/snap/firefox/common/.mozilla/firefox" "$$HOME/.var/app/org.mozilla.firefox/.mozilla/firefox"; do \
		[ -d "$$profiles_dir" ] || continue; \
		for profile in "$$profiles_dir"/*/; do \
			[ -f "$${profile}cert9.db" ] || continue; \
			found=1; \
			echo "importing into $$profile"; \
			certutil -A -n "ownscale local dev CA" -t "C,," -i "$(CERT)" -d "sql:$${profile%/}"; \
		done; \
	done; \
	if [ "$$found" = 0 ]; then \
		echo "no Firefox profiles (cert9.db) found under ~/.mozilla/firefox, ~/snap/firefox, or ~/.var/app/org.mozilla.firefox"; \
		exit 1; \
	fi
	sudo cp $(CERT) /usr/local/share/ca-certificates/ownscale-local-ca.crt
	sudo update-ca-certificates
	@echo "done -- firefox and os certs are updated to trust our local certificate"

ORG ?= aaaa
devbox: ## Open a shell in a container joined to organization ORG (default aaaa)
	@docker compose ps --status running --services | grep -qx ownscale || { echo "ownscale is not running -- run 'make dev-up' first"; exit 1; }
	$(require_cert)
	@docker build -q -t $(DEVBOX_IMAGE) e2e/devbox >/dev/null
	@echo "minting a pre-auth key for $(ORG)..."
	@n=1; while docker ps -a --format '{{.Names}}' | grep -qx "devbox$(ORG)$$n"; do n=$$((n+1)); done; \
	name=devbox$(ORG)$$n; \
	authkey=$$(docker compose exec -T ownscale osa devbox-key $(ORG)); \
	test -n "$$authkey" || { echo "failed to mint a pre-auth key"; exit 1; }; \
	echo "connecting to https://$(ORG).$(BASE_DOMAIN) as $$name..."; \
	docker run --rm -it --name=$$name --hostname=$$name \
		--cap-add=NET_ADMIN --cap-add=NET_RAW --device=/dev/net/tun \
		--add-host=$(ORG).$(BASE_DOMAIN):host-gateway \
		-v $(abspath $(CERT)):/usr/local/share/ca-certificates/ownscale-local-ca.crt:ro \
		$(DEVBOX_IMAGE) bash -c "\
			set -e; \
			devbox-up https://$(ORG).$(BASE_DOMAIN) $$authkey $$name; \
			mkdir -p /var/www; \
			echo '<html><body><h1>$$name</h1></body></html>' > /var/www/index.html; \
			python3 -m http.server 80 --directory /var/www >/var/log/httpserver.log 2>&1 & \
			exec bash"

# KEY can also come from the STRESS_AUTHKEY env var.
stress: ## Join many tsnet clients to ORG: KEY=<reusable key> STRESS_ARGS="-n 100"
	$(require_cert)
	@SSL_CERT_FILE=$(abspath $(CERT)) STRESS_AUTHKEY="$${STRESS_AUTHKEY:-$(KEY)}" \
		go run ./e2e/stress -subdomain $(ORG) -base-domain $(BASE_DOMAIN) $(STRESS_ARGS)

##@ Checks

.PHONY: ci coverage
# Hashes uncommitted changes, untracked files included, so ci can tell
# whether its formatters and fixers rewrote anything.
worktree_state = { git diff HEAD --binary; git ls-files -o --exclude-standard -z | xargs -0 -r sha256sum; } | sha256sum

ci: ## Run .github/workflows/ci.yml locally, fixing formatting in place
	@mkdir -p bin && $(worktree_state) > bin/ci-worktree-state
	npm --prefix frontend run lint -- --fix
	npm --prefix frontend run format
	cd frontend && npx tsc -b
	go fix ./...
	go mod tidy
	go run golang.org/x/tools/cmd/goimports@latest -w .
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run --fix ./...
	go build ./...
	go vet ./...
	go test -shuffle=on -covermode=atomic -coverprofile=coverage.out ./...
	go run golang.org/x/tools/cmd/deadcode@latest --test ./...
	@if [ "$$($(worktree_state))" != "$$(cat bin/ci-worktree-state)" ]; then \
		echo "ci: formatters/linters changed files, review and commit them:"; \
		git status --short; \
		exit 1; \
	fi

BROWSER := firefox
coverage: ## Open a Go test coverage report in BROWSER
	go test -coverprofile=coverage.out ./...
	BROWSER=$(BROWSER) go tool cover -html=coverage.out

##@ E2E tests (see e2e/README.md)

.PHONY: e2e e2e-deps e2e-setup e2e-frontend e2e-frontend-run e2e-connectivity e2e-ssh
.PHONY: e2e-remote e2e-remote-serve
CONNECTIVITY_ENV = TMPDIR="$(CURDIR)/bin/connectivity-work"

# The suites use disjoint organizations, so they run concurrently. ssh runs
# alone: its devbox network changes abort Chromium's in-flight requests.
e2e: e2e-deps e2e-setup ## Run all suites against the dev stack
	$(MAKE) -j2 --output-sync=target E2E_FRONTEND_ARGS="--project=chromium --no-deps" \
		e2e-frontend-run e2e-connectivity
	$(MAKE) e2e-ssh E2E_SSH_ARGS="--project=ssh --no-deps"

# tscl-bridge is built up front so a cold Go cache isn't compiled inside the
# tests' timeouts.
e2e-deps: ## Install the e2e dependencies
	npm --prefix e2e/frontend ci
	npx --prefix e2e/frontend playwright install chromium
	go build -o bin/tscl-bridge ./e2e/connectivity/cmd/tscl-bridge

e2e-setup: ## Sign in the frontend and ssh suites' accounts
	npm --prefix e2e/frontend test -- --project=setup
	npm --prefix e2e/frontend run test:ssh -- --project=setup

e2e-frontend: e2e-deps e2e-frontend-run ## Run the frontend suite (E2E_FRONTEND_ARGS)
e2e-frontend-run:
	npm --prefix e2e/frontend test -- $(E2E_FRONTEND_ARGS)

e2e-connectivity: ## Run the connectivity suite
	mkdir -p bin/connectivity-work
	$(CONNECTIVITY_ENV) npm --prefix e2e/frontend run test:connectivity

e2e-ssh: ## Run the Tailscale SSH and split DNS suite (needs /dev/net/tun)
	npm --prefix e2e/frontend run test:ssh -- $(E2E_SSH_ARGS)

REMOTE_BASE_DOMAIN ?= "beastbb.bond"
REMOTE_CONTENT_DOMAIN ?= "ownsc.net"
REMOTE_E2E_ENV = E2E_BASE_URL=https://console.$(REMOTE_BASE_DOMAIN) E2E_BASE_DOMAIN=$(REMOTE_BASE_DOMAIN) \
	E2E_CONTENT_DOMAIN=$(REMOTE_CONTENT_DOMAIN) E2E_CA_FILE=

e2e-remote: ## Run the suites against REMOTE_BASE_DOMAIN
	$(REMOTE_E2E_ENV) $(MAKE) e2e-frontend e2e-connectivity e2e-remote-serve

# Each run issues a certificate, so it's skipped unless EXTRA_TESTS has
# $(REMOTE_BASE_DOMAIN):serve.
e2e-remote-serve: ## Run the serve suite against REMOTE_BASE_DOMAIN
	mkdir -p bin/connectivity-work
	$(REMOTE_E2E_ENV) $(CONNECTIVITY_ENV) npm --prefix e2e/frontend run test:serve

##@ Remote host

.PHONY: deploy-local
DEPLOY_HOST ?= az

# Releases are deployed on the host with `make upgrade TAG=...`. Here nothing
# is pushed to ghcr; the images are streamed over ssh, gzipped since the upload
# is the bottleneck. The host keeps its own .env; etc/ is baked into the Caddy
# image.
deploy-local: frontend docs ## Build images from the working tree and run them on DEPLOY_HOST
	docker build --platform linux/amd64 --build-arg VERSION=$(BUILD_VERSION) \
		-t $(GHCR_IMAGE):$(BUILD_VERSION) .
	docker build --platform linux/amd64 --build-arg VERSION=$(BUILD_VERSION) \
		-t $(GHCR_IMAGE)-caddy:$(BUILD_VERSION) -f etc/caddy/Dockerfile .
	docker save $(GHCR_IMAGE):$(BUILD_VERSION) $(GHCR_IMAGE)-caddy:$(BUILD_VERSION) | gzip | ssh $(DEPLOY_HOST) docker load
	ssh $(DEPLOY_HOST) mkdir -p ownscale
	scp compose.yaml $(DEPLOY_HOST):ownscale/
	scp deploy.mk $(DEPLOY_HOST):ownscale/Makefile
	ssh $(DEPLOY_HOST) 'cd ownscale && touch .env && sed -i "/^OWNSCALE_VERSION=/d" .env && \
		echo OWNSCALE_VERSION=$(BUILD_VERSION) >> .env && \
		{ grep -q "^COMPOSE_PROFILES=" .env || echo COMPOSE_PROFILES=caddy >> .env; } && \
		docker compose up -d --pull never'

endif
