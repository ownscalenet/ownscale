# Build the ownscale binary. The frontend (frontend/) is a React + Vite
# app that builds directly into webui/dist, which is embedded into
# the Go binary via go:embed. webui/dist is committed to the repo
# (go:embed needs real files at Go build time), so run `make frontend`
# whenever frontend/ changes and commit the updated webui/dist.

.PHONY: build frontend backend osa restore test coverage migration archive docker-image caddy-image purge ci docs load-cert dev-up devbox e2e e2e-frontend e2e-connectivity e2e-ssh e2e-remote e2e-remote-frontend e2e-remote-connectivity

DOCKER_IMAGE := ownscale:latest
CADDY_IMAGE := ownscale-caddy:latest
DEVBOX_IMAGE := ownscale-devbox:latest

# Ensure containers run with same host user.
export HOST_UID := $(shell id -u)
export HOST_GID := $(shell id -g)

# Resolved the same way BASE_DOMAIN is below, so dev-up works without
# requiring OWNSCALE_DATA_DIR to already be exported into the shell.
DATA_DIR := $(shell grep -m1 '^OWNSCALE_DATA_DIR=' .env 2>/dev/null | cut -d= -f2)
DATA_DIR := $(if $(DATA_DIR),$(DATA_DIR),$(CURDIR)/data)

build: frontend backend

frontend:
	npm --prefix frontend ci
	npm --prefix frontend run build
	rm -rf etc/caddy/site
	hugo --source site --destination ../etc/caddy/site --minify

osa:
	docker compose exec ownscale ./osa $(filter-out osa,$(MAKECMDGOALS))

# Build the images, start the full stack, then stream logs from all
# containers. Organizations are created on each new user's first login; the
# first six get the fixed subdomains aaaa/bbbb/cccc/dddd/eeee/ffff (see
# orgs.Service.NextSubdomain).
#
# Pre-creates .litestream-backups as the host user before compose runs, so it's
# not created later as root.
#
# Also upserts HOST_UID/HOST_GID into .env: docker-compose.yml's
# user: "${HOST_UID:-0}:${HOST_GID:-0}" only sees these if they're in the shell
# env or in .env -- persisting them to .env means a bare `docker compose ...`
# (bypassing make) still runs containers as the host user instead of
# silently falling back to root.
#
# Pass `make dev-up BUILD=1`) to forward --build to `docker compose up`.
BUILD ?=
dev-up:
	mkdir -p "$(DATA_DIR)/.litestream-backups"
	@touch .env
	@for kv in HOST_UID=$(HOST_UID) HOST_GID=$(HOST_GID); do \
		key=$${kv%%=*}; \
		grep -q "^$$key=" .env && sed -i "s|^$$key=.*|$$kv|" .env || echo "$$kv" >> .env; \
	done
	docker compose up $(if $(BUILD),--build)

# Spin up a throwaway ubuntu container running tailscaled, auto-join it to
# an org's in-process headscale (ORG, default aaaa, the first org created --
# see orgs.Service.NextSubdomain) via a freshly-minted ephemeral pre-auth key,
# then drop into an interactive shell in that container. Requires
# `make dev-up` already running and that org to exist (log in once first).
ORG ?= aaaa
BASE_DOMAIN := $(shell grep -m1 '^BASE_DOMAIN=' .env 2>/dev/null | cut -d= -f2)
BASE_DOMAIN := $(if $(BASE_DOMAIN),$(BASE_DOMAIN),ownscale.test)

devbox:
	@docker compose ps --status running --services | grep -qx ownscale || { echo "ownscale is not running -- run 'make dev-up' first"; exit 1; }
	@test -f $(CERT) || { echo "$(CERT) not found -- start docker compose (local-ca-init) first"; exit 1; }
	@docker build -q -t $(DEVBOX_IMAGE) e2e/devbox >/dev/null
	@echo "minting a pre-auth key for $(ORG)..."
	@n=1; while docker ps -a --format '{{.Names}}' | grep -qx "devbox$(ORG)$$n"; do n=$$((n+1)); done; \
	name=devbox$(ORG)$$n; \
	authkey=$$(docker compose exec -T ownscale ./osa devbox-key $(ORG)); \
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

# Requires `make dev-up` already running.
e2e: e2e-frontend e2e-connectivity e2e-ssh

e2e-frontend:
	npm --prefix e2e/frontend ci
	npx --prefix e2e/frontend playwright install chromium
	npm --prefix e2e/frontend test
e2e-connectivity:
	mkdir -p bin/connectivity-work
	TMPDIR="$(CURDIR)/bin/connectivity-work" npm --prefix e2e/frontend run test:connectivity

# Tailscale SSH and split DNS between devbox containers; also needs docker with /dev/net/tun.
e2e-ssh:
	npm --prefix e2e/frontend run test:ssh

REMOTE_BASE_DOMAIN ?= "beastbb.bond"

e2e-remote: e2e-remote-frontend e2e-remote-connectivity

e2e-remote-frontend:
	E2E_BASE_URL=https://console.$(REMOTE_BASE_DOMAIN) E2E_BASE_DOMAIN=$(REMOTE_BASE_DOMAIN) $(MAKE) e2e-frontend

e2e-remote-connectivity:
	mkdir -p bin/connectivity-work
	E2E_BASE_URL=https://console.$(REMOTE_BASE_DOMAIN) E2E_BASE_DOMAIN=$(REMOTE_BASE_DOMAIN) E2E_CA_FILE= \
		TMPDIR="$(CURDIR)/bin/connectivity-work" npm --prefix e2e/frontend run test:connectivity

# Regenerate the headscale API client in hscl/hscodegen
# from the vendored OpenAPI spec (headscale.swagger.json). The generated
# code is committed to the repo (same convention as db/sqlcgen),
# so re-run this and commit the result whenever the spec is bumped to a
# new headscale version.
generate-headscale-client:
	rm -rf hscl/hscodegen/client hscl/hscodegen/models
	cd hscl/hscodegen && go run github.com/go-swagger/go-swagger/cmd/swagger@latest generate client -f headscale.swagger.json -A headscale --skip-validation

# Usage: make migration up | make migration status
migration:
	go run ./cmd/osa migrate $(filter-out migration,$(MAKECMDGOALS))


restore:
	docker compose run --rm --entrypoint /app/osa ownscale restore $(filter-out restore,$(MAKECMDGOALS))

up down status:
	@:

# Swallows `make restore db`/`make restore organizations`'s positional
# argument the same way `up down status:` does for `make migration ...`.
db organizations:
	@:

ifneq (,$(filter osa,$(MAKECMDGOALS)))
%:
	@:
endif

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

caddy-image:
	docker build -t $(CADDY_IMAGE) ./etc/caddy

# ghetto deploy script
# Send the ownscale and caddy images and repo files to remote.
archive: docker-image caddy-image
	docker save $(DOCKER_IMAGE) $(CADDY_IMAGE) | gzip | ssh az 'gzip -d | docker load'
	ssh az mkdir -p ownscale
	rsync -a --delete \
		--exclude='.git' \
		--exclude='.env' \
		--exclude='data' \
		--exclude='node_modules' \
		--exclude='bin' \
		--exclude='/etc/caddy/wildcard' \
		--exclude='/etc/caddy/admin' \
		./ az:ownscale/

purge:
	docker compose down
	# rm everything under data/ (org dirs, DBs, backups) except data/local-ca
	sudo find data -mindepth 1 -maxdepth 1 ! -name local-ca -exec rm -rf {} +

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

# Run the same checks as .github/workflows/ci.yml, for local sanity-checking
# before pushing. Requires golangci-lint and govulncheck.
ci:
	go build ./...
	go vet ./...
	go fix ./...
	go test -shuffle=on -covermode=atomic -coverprofile=coverage.out ./...
	go mod tidy
	go run golang.org/x/tools/cmd/goimports@latest -w .
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...
	go run golang.org/x/tools/cmd/deadcode@latest ./cmd/...
