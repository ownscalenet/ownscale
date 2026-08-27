# Build the ownscale binary. The frontend (frontend/) is a React + Vite
# app that builds directly into webui/dist, which is embedded into
# the Go binary via go:embed. webui/dist is committed to the repo
# (go:embed needs real files at Go build time), so run `make frontend`
# whenever frontend/ changes and commit the updated webui/dist.

.PHONY: build frontend backend osa test test-integration coverage migration archive docker-image rm-orphans purge ci docs load-cert e2e

DOCKER_IMAGE := ownscale:latest

build: frontend backend

frontend:
	npm --prefix frontend ci
	npm --prefix frontend run build

osa:
	docker compose exec ownscale ./osa $(filter-out osa,$(MAKECMDGOALS))

# Build the site/ Hugo module (hugo-book theme) into etc/caddy/site, which
# Caddy serves at {$BASE_DOMAIN}. The generated output is committed to the
# repo, so re-run this and commit the result whenever site/content changes.
# Requires `go` on PATH for Hugo Modules to resolve the hugo-book theme.
docs:
	rm -rf etc/caddy/site
	hugo --source site --destination ../etc/caddy/site --minify

# Regenerate the headscale API client in headscaleclient/hscodegen
# from the vendored OpenAPI spec (headscale.swagger.json). The generated
# code is committed to the repo (same convention as db/sqlcgen),
# so re-run this and commit the result whenever the spec is bumped to a
# new headscale version.
generate-headscale-client:
	rm -rf headscaleclient/hscodegen/client headscaleclient/hscodegen/models
	cd headscaleclient/hscodegen && go run github.com/go-swagger/go-swagger/cmd/swagger@latest generate client -f headscale.swagger.json -A headscale --skip-validation

# Usage: make migration up | make migration status
migration:
	go run ./cmd/osa --config config.yaml migrate $(filter-out migration,$(MAKECMDGOALS))

up down status:
	@:

ifneq (,$(filter osa,$(MAKECMDGOALS)))
%:
	@:
endif

run:
	go run ./cmd/ownscale

test:
	go test ./...

test-integration:
	go test -tags=integration ./...

# Run the Playwright e2e suite (e2e/) against a real local docker-compose
# stack (ownscale + dex + caddy) and a real, freshly-migrated SQLite
# database -- see e2e/README.md for prerequisites. Resets local dev data
# in data/ (see e2e/lib/stack.ts); don't run this against data you care about.
e2e:
	npm --prefix e2e ci
	npm --prefix e2e exec playwright install --with-deps chromium
	npm --prefix e2e test

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
	docker save $(DOCKER_IMAGE) | gzip | ssh az 'gzip -d | docker load'
	# delete existing go files
	ssh az rm -rf ownscale/internal ownscale/cmd
	tar czf - \
		--exclude='.git' \
		--exclude='config.yaml' \
		--exclude='.env' \
		--exclude='data' \
		--exclude='node_modules' \
		--exclude='bin' \
		-C .. $(notdir $(CURDIR)) \
		| ssh az 'mkdir -p ownscale && tar -xzf - -C ownscale --strip-components=1'

purge:
	# rm all except data/local-ca
	sudo rm -rf data/headscale data/headplane data/.litestream-backups data/.ownscale.sqlite-litestream
	sudo rm -f data/*.*
	docker ps -aq --filter "label=ownscale.organization=true" | xargs -r docker stop | xargs -r docker rm

rm-orphans:
	docker ps -aq --filter "label=ownscale.organization=true" | xargs -r docker stop | xargs -r docker rm

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
	go test -race -shuffle=on -covermode=atomic -coverprofile=coverage.out ./...
	go mod tidy
	go run golang.org/x/tools/cmd/goimports@latest -w .
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...
	go run golang.org/x/tools/cmd/deadcode@latest ./cmd/...
