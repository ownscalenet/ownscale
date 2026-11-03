# Build the ownscale binary. The frontend (frontend/) is a React + Vite
# app that builds directly into webui/dist, which is embedded into
# the Go binary via go:embed.

.PHONY: build frontend backend osa restore test coverage deploy
.PHONY: docker-image caddy-image purge ci docs load-cert dev-up devbox stress
.PHONY: e2e e2e-deps e2e-setup e2e-ssh
.PHONY: e2e-frontend e2e-frontend-run e2e-connectivity
.PHONY: e2e-remote e2e-remote-frontend e2e-remote-connectivity

DOCKER_IMAGE := ownscale:latest
CADDY_IMAGE := ownscale-caddy:latest
DEVBOX_IMAGE := ownscale-devbox:latest

# Ensure containers run with same host user.
export HOST_UID := $(shell id -u)
export HOST_GID := $(shell id -g)

# Dev stack domain, set in compose.dev.yaml.
BASE_DOMAIN := ownscale.test

build: frontend backend

frontend: docs
	npm --prefix frontend ci
	npm --prefix frontend run build
	# Tailscale client compiled to WASM; needed for browser SSH page.
	cp frontend/node_modules/@tailscale/connect/main.wasm.zst webui/dist/assets/

# Docs site for the dev stack, which bind-mounts it over the copy that
# etc/caddy/Dockerfile renders into the Caddy image.
docs:
	rm -rf etc/caddy/site
	hugo --source docs --destination ../etc/caddy/site --minify

ifneq ($(filter osa restore,$(MAKECMDGOALS)),)
%:
	@:
endif

osa:
	docker compose exec ownscale osa $(filter-out osa,$(MAKECMDGOALS))

# Build the images, start the full stack, then stream logs from all
# containers. Organizations are created on each new user's first login.
#
# Upserts into .env what compose.dev.yaml can't set itself, so plain
# `docker compose` commands see the same stack:
#  - COMPOSE_FILE, which layers compose.dev.yaml on top of compose.yaml;
#  - HOST_UID/HOST_GID, for compose.yaml's user: "${HOST_UID:-0}:${HOST_GID:-0}";
#  - a session secret, if none is set, for compose.yaml's required check
#    (compose.dev.yaml overrides the value the container gets).
#
# Pass `make dev-up BUILD=1` to forward --build to `docker compose up`.
BUILD ?=
dev-up:
	@touch .env
	@{ grep -vE '^(COMPOSE_FILE|HOST_UID|HOST_GID)=|^OWNSCALE_AUTH_SESSION_SECRET=$$' .env; \
		echo COMPOSE_FILE=compose.yaml:compose.dev.yaml; \
		echo HOST_UID=$(HOST_UID); \
		echo HOST_GID=$(HOST_GID); \
		grep -q '^OWNSCALE_AUTH_SESSION_SECRET=.' .env || echo OWNSCALE_AUTH_SESSION_SECRET=dev-session-secret; \
	} > .env.tmp && cat .env.tmp > .env && rm .env.tmp
	docker compose up $(if $(BUILD),--build)

# devbox opens up a shell in a container that has joined a tailnet.
# It's useful for debugging, but requires make dev-up to already run.
ORG ?= aaaa

devbox:
	@docker compose ps --status running --services | grep -qx ownscale || { echo "ownscale is not running -- run 'make dev-up' first"; exit 1; }
	@test -f $(CERT) || { echo "$(CERT) not found -- start docker compose (local-ca-init) first"; exit 1; }
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

# stress joins many ephemeral tsnet clients to $(ORG) with a reusable
# pre-auth key: make stress ORG=aaaa KEY=... STRESS_ARGS="-n 100 -duration 10m".
# KEY can also be passed as the STRESS_AUTHKEY env var.
stress:
	@test -f $(CERT) || { echo "$(CERT) not found -- start docker compose (local-ca-init) first"; exit 1; }
	@SSL_CERT_FILE=$(abspath $(CERT)) STRESS_AUTHKEY="$${STRESS_AUTHKEY:-$(KEY)}" \
		go run ./e2e/stress -subdomain $(ORG) -base-domain $(BASE_DOMAIN) $(STRESS_ARGS)

# Requires `make dev-up` already running. The suites use disjoint
# organizations to perfoorm concurrent tests. ssh runs alone afterwards:
# its devbox containers add and remove host network links, which makes Chromium
# abort other browsers' in-flight requests (net::ERR_NETWORK_CHANGED).
e2e: e2e-deps e2e-setup
	$(MAKE) -j2 --output-sync=target E2E_FRONTEND_ARGS="--project=chromium --no-deps" \
		e2e-frontend-run e2e-connectivity
	$(MAKE) e2e-ssh E2E_SSH_ARGS="--project=ssh --no-deps"

e2e-deps:
	npm --prefix e2e/frontend ci
	npx --prefix e2e/frontend playwright install chromium

# Signs in the e2e identities and records their organizations.
e2e-setup:
	npm --prefix e2e/frontend test -- --project=setup

e2e-frontend: e2e-deps e2e-frontend-run
e2e-frontend-run:
	npm --prefix e2e/frontend test -- $(E2E_FRONTEND_ARGS)
e2e-connectivity:
	mkdir -p bin/connectivity-work
	TMPDIR="$(CURDIR)/bin/connectivity-work" npm --prefix e2e/frontend run test:connectivity

# Tailscale SSH and split DNS between devbox containers; also needs docker with /dev/net/tun.
e2e-ssh:
	npm --prefix e2e/frontend run test:ssh -- $(E2E_SSH_ARGS)

REMOTE_BASE_DOMAIN ?= "beastbb.bond"
REMOTE_CONTENT_DOMAIN ?= "ownsc.net"

e2e-remote: e2e-remote-frontend e2e-remote-connectivity

e2e-remote-frontend:
	E2E_BASE_URL=https://console.$(REMOTE_BASE_DOMAIN) E2E_BASE_DOMAIN=$(REMOTE_BASE_DOMAIN) E2E_CONTENT_DOMAIN=$(REMOTE_CONTENT_DOMAIN) E2E_CA_FILE= $(MAKE) e2e-frontend

e2e-remote-connectivity:
	mkdir -p bin/connectivity-work
	E2E_BASE_URL=https://console.$(REMOTE_BASE_DOMAIN) E2E_BASE_DOMAIN=$(REMOTE_BASE_DOMAIN) E2E_CONTENT_DOMAIN=$(REMOTE_CONTENT_DOMAIN) E2E_CA_FILE= \
		TMPDIR="$(CURDIR)/bin/connectivity-work" npm --prefix e2e/frontend run test:connectivity

restore:
	docker compose run --rm --entrypoint osa ownscale restore $(filter-out restore,$(MAKECMDGOALS))

BROWSER := firefox
coverage:
	go test -coverprofile=coverage.out ./...
	BROWSER=$(BROWSER) go tool cover -html=coverage.out

# Build the ownscale Docker image locally, tagged to match the "image:"
# name compose.dev.yaml expects for the ownscale service.
docker-image:
	docker build -t $(DOCKER_IMAGE) .

caddy-image:
	docker build -t $(CADDY_IMAGE) -f etc/caddy/Dockerfile .

# Deploy a published release to the az host: make deploy VERSION=v0.1.0.
# The remote keeps its own .env; etc/ is baked into the Caddy image.
VERSION ?=
deploy:
	@test -n "$(VERSION)" || { echo "usage: make deploy VERSION=vX.Y.Z"; exit 1; }
	ssh az mkdir -p ownscale
	scp compose.yaml Makefile az:ownscale/
	ssh az 'cd ownscale && touch .env && sed -i "/^OWNSCALE_VERSION=/d" .env && \
		echo OWNSCALE_VERSION=$(VERSION) >> .env && \
		docker compose pull && docker compose up -d'

purge:
	docker compose down
	# rm everything under data/ (org dirs, DBs, backups) except data/local-ca
	sudo find data -mindepth 1 -maxdepth 1 ! -name local-ca -exec rm -rf {} +
	# The connectivity suite's recovery records describe orgs that are now gone.
	rm -rf e2e/.connectivity-private/recovery
	# Guards left by e2e runs that were killed before releasing them.
	rm -f e2e/.*-run.lock

# ghetto script for trusting caddy's self-signed certificates.
# To manually trust the certificates in firefox:
#  1. Copy the root cert out of `${OWNSCALE_DATA_DIR}/local-ca/root.crt` (no
#     `docker cp` needed -- it's a plain file on the host).
#  2. Go to `about:preferences#privacy` → scroll to Certificates → View Certificates
#  3. Authorities tab → Import → select `root.crt`
#  4. Check "Trust this CA to identify websites"
CERT := $(if $(OWNSCALE_DATA_DIR),$(OWNSCALE_DATA_DIR),data)/local-ca/root.crt
load-cert:
	@test -f $(CERT) || { echo "$(CERT) not found -- start docker compose (local-ca-init) first"; exit 1; }
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

# worktree_state hashes uncommitted changes, untracked files included, so ci
# can tell whether its formatters and fixers rewrote anything.
worktree_state = { git diff HEAD --binary; git ls-files -o --exclude-standard -z | xargs -0 -r sha256sum; } | sha256sum

# Run the same checks as .github/workflows/ci.yml, for local sanity-checking
# before pushing. Formatting and auto-fixable lint issues are fixed in place
# instead of failing; ci then fails only if that changed files, so they can be
# reviewed and committed.
ci:
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
