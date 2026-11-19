#!/bin/sh
# Runs install.sh against this checkout for each reverse proxy choice, typing
# the answers into a terminal with util-linux script(1).
set -eu

repo=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail() { printf 'FAIL %s: %s\n' "$name" "$*" >&2; exit 1; }

# run NAME ANSWERS: one answer per line, as typed.
run() {
	name=$1
	mkdir "$tmp/$name"
	printf '%b' "$2" | (cd "$tmp/$name" && OWNSCALE_VERSION=v1.2.3 OWNSCALE_INSTALL_URL="file://$repo" \
		script -qec "sh '$repo/install.sh'" /dev/null) >"$tmp/$name.log" 2>&1 ||
		{ cat "$tmp/$name.log"; fail "install.sh failed"; }
	cd "$tmp/$name/ownscale"
	grep -qx 'BASE_DOMAIN=example.invalid' .env || fail "BASE_DOMAIN not set"
	grep -qx 'OWNSCALE_VERSION=v1.2.3' .env || fail "OWNSCALE_VERSION not set"
	grep -Eqx 'OWNSCALE_AUTH_SESSION_SECRET=[0-9a-f]{64}' .env || fail "no session secret"
	cmp -s Makefile "$repo/deploy.mk" || fail "Makefile isn't deploy.mk"
}

# services: the services compose would start. External networks are
# only checked on start, so config works without them.
services() { docker compose config --services | sort | tr '\n' ' '; }

run bundled 'ownscale\nexample.invalid\ny\n1\n'
grep -qx 'COMPOSE_PROFILES=caddy' .env || fail "caddy profile not set"
[ ! -e compose.override.yaml ] || fail "unexpected override"
[ "$(services)" = "caddy ownscale " ] || fail "services: $(services)"
grep -q '443/udp' "../../bundled.log" || fail "ports not listed"

run caddy 'ownscale\nExample.invalid\ny\n2\n\n'
grep -qx 'COMPOSE_PROFILES=' .env || fail "caddy profile still set"
[ "$(services)" = "ownscale " ] || fail "services: $(services)"
docker compose config | grep -q 'host_ip: 127.0.0.1' || fail "port not on loopback"
docker compose config | grep -q 'target: 8081' || fail "ask port not published"
grep -q 'ask http://127.0.0.1:8081/tls-allowed' "../../caddy.log" || fail "no Caddyfile printed"

run traefik 'ownscale\nexample.invalid\ny\n3\n\n'
# config prints $ escaped as $$.
docker compose config --format json >config.json
jq -e '.services.ownscale.networks | has("proxy")' config.json >/dev/null || fail "not on traefik network"
jq -e '.networks.proxy.name == "traefik"' config.json >/dev/null || fail "wrong network"
jq -e '.services.ownscale.labels["traefik.http.routers.ownscale.rule"] ==
	"Host(`example.invalid`) || HostRegexp(`^[a-z0-9-]+\\.example\\.invalid$$`)"' config.json >/dev/null ||
	fail "rule: $(jq '.services.ownscale.labels' config.json)"

run nginx 'ownscale\nnot a domain\nexample.invalid\nn\nexample.invalid\ny\n9\n4\nproxy-net\n'
grep -q 'proxy_pass http://ownscale:8080;' "../../nginx.log" || fail "no nginx config printed"
grep -q "isn't a domain name" "../../nginx.log" || fail "bad domain accepted"
[ "$(grep -c "names don't resolve" "../../nginx.log")" = 2 ] || fail "no DNS warning, or domain not asked again"
docker compose config --format json | jq -e '.networks.proxy.name == "proxy-net"' >/dev/null ||
	fail "wrong network"

run npm 'ownscale\nexample.invalid\ny\n5\n\nnpm_default\n'
grep -q 'http / ownscale / 8080' "../../npm.log" || fail "no NPM steps printed"

name=rerun
printf 'ownscale\n' | (cd "$tmp/npm" && OWNSCALE_VERSION=v1.2.3 \
	script -qec "sh '$repo/install.sh'" /dev/null) >"$tmp/rerun.log" 2>&1 && fail "overwrote an install"
grep -q 'ownscale/.env exists' "$tmp/rerun.log" || fail "$(cat "$tmp/rerun.log")"

echo "install.sh: ok"
