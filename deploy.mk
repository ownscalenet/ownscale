# Commands for running a released ownscale with Docker Compose. Download it
# next to compose.yaml as "Makefile"; see the installation docs. The
# repository's Makefile includes it, so they work from a checkout too.

.DEFAULT_GOAL := help
# This file's name, listed first in help.
DEPLOY_MK := $(lastword $(MAKEFILE_LIST))

RELEASE_URL := https://raw.githubusercontent.com/ownscalenet/ownscale

# osa and restore pass their remaining goals to osa as arguments, so no other
# rule may exist then: `make osa migrate down` must not run `down`.
ifneq ($(filter osa restore,$(firstword $(MAKECMDGOALS))),)
OSA_MODE := 1
OSA_ARGS := $(wordlist 2,$(words $(MAKECMDGOALS)),$(MAKECMDGOALS))

# Phony, so arguments named like files (db) don't print "is up to date".
.PHONY: $(MAKECMDGOALS)
ifeq ($(firstword $(MAKECMDGOALS)),osa)
osa:
	docker compose exec ownscale osa $(OSA_ARGS)
else
restore:
	docker compose run --rm --entrypoint osa ownscale restore $(OSA_ARGS)
endif

$(filter-out $(firstword $(MAKECMDGOALS)),$(OSA_ARGS)):
	@:
else

##@ Running ownscale

.PHONY: help up down logs upgrade osa restore
help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} \
		/^##@ / {printf "\n\033[1m%s\033[0m\n", substr($$0, 5)} \
		/^[a-zA-Z0-9_-]+:.*## / {printf "  \033[36m%-24s\033[0m %s\n", $$1, $$2}' $(DEPLOY_MK) $(filter-out $(DEPLOY_MK),$(MAKEFILE_LIST))

up: ## Start the stack in the background
	docker compose up -d

down: ## Stop the stack
	docker compose down

logs: ## Follow the logs of all containers
	docker compose logs -f

upgrade: ## Switch to release TAG=vX.Y.Z: download its files, pull and restart
	@test -n "$(TAG)" || { echo "usage: make upgrade TAG=vX.Y.Z"; exit 1; }
	@test ! -d .git || { echo "upgrade is for deployments, not source checkouts"; exit 1; }
	@# Download everything before replacing anything.
	curl -fsSL -o compose.yaml.new "$(RELEASE_URL)/$(TAG)/compose.yaml" && \
		curl -fsSL -o example.env.new "$(RELEASE_URL)/$(TAG)/example.env" && \
		curl -fsSL -o Makefile.new "$(RELEASE_URL)/$(TAG)/deploy.mk" || \
		{ rm -f compose.yaml.new example.env.new Makefile.new; exit 1; }
	mv compose.yaml.new compose.yaml && mv example.env.new example.env && mv Makefile.new Makefile
	touch .env && sed -i '/^OWNSCALE_VERSION=/d' .env && echo OWNSCALE_VERSION=$(TAG) >> .env
	@# Installs from before the caddy profile expect Caddy to keep running.
	grep -q '^COMPOSE_PROFILES=' .env || echo COMPOSE_PROFILES=caddy >> .env
	docker compose pull
	docker compose up -d
	@echo "upgraded to $(TAG); compare example.env with .env for new settings"

# Only listed in help; the rules are above.
osa: ## Run osa in the running container: make osa organization list
restore: ## Restore databases from backup, stack stopped: make restore db
endif
