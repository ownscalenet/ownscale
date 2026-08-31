#!/usr/bin/env bash
# Real, end-to-end connectivity test: provisions a fresh organization,
# logs in an owner and an invitee (skipping OIDC/Dex entirely -- see "osa
# user login" below), has the invitee accept a real invite, then runs two
# real Tailscale clients -- each in its own network-isolated Docker
# container, with no route to each other or to ownscale's own networks --
# and verifies they can reach each other. Since the two client containers
# share no network route, this is only possible via headscale's
# coordination plus a DERP relay, so a successful ping here is a real
# end-to-end proof that both pieces work together.
#
# Unlike e2e/ (Playwright), this does not drive a browser or a real OIDC
# login: it uses a new `osa user login` subcommand that mints a valid
# session cookie directly (reusing the same production auth code a real
# login uses), and drives the real invite HTTP API via curl. See
# cmd/osa/user.go's loginUser for details and rationale.
#
# Usage: e2e/tailscale-connectivity.sh
#
# Assumes `docker compose up` (ownscale, dex, caddy, litestream) is
# already running -- this script does not start or stop the stack. Must
# be run from the repo root (or with PWD set there).
set -euo pipefail

log() { printf '>> %s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

for bin in docker curl jq gnome-terminal; do
	command -v "$bin" >/dev/null || die "$bin not found on PATH"
done

# Load the same .env docker compose reads, for BASE_DOMAIN/OWNSCALE_DATA_DIR.
if [ -f .env ]; then
	set -a
	# shellcheck disable=SC1091
	source .env
	set +a
fi
BASE_DOMAIN="${BASE_DOMAIN:-ownscale.test}"
: "${OWNSCALE_DATA_DIR:?set OWNSCALE_DATA_DIR (e.g. in .env) to the same value docker compose uses}"

CA_CERT="$OWNSCALE_DATA_DIR/local-ca/root.crt"
[ -f "$CA_CERT" ] || die "local dev CA not found at $CA_CERT -- is the stack running (local-ca-init)?"

API="https://$BASE_DOMAIN"
OSA=(docker compose exec -T ownscale ./osa --config /etc/ownscale/config.yaml)

RUN_ID=$(tr -dc 'a-z0-9' </dev/urandom 2>/dev/null | head -c6 || true)
[ -n "$RUN_ID" ] || RUN_ID=$RANDOM$RANDOM
SUBDOMAIN="tsit${RUN_ID}"
OWNER_EMAIL="owner-${RUN_ID}@tailscale-connectivity.test"
INVITEE_EMAIL="invitee-${RUN_ID}@tailscale-connectivity.test"
NET1="tsit-${RUN_ID}-c1"
NET2="tsit-${RUN_ID}-c2"
CLIENT1="tsit-${RUN_ID}-client1"
CLIENT2="tsit-${RUN_ID}-client2"

cleanup() {
	status=$?
	log "cleaning up (exit status $status)..."
	docker rm -f "$CLIENT1" "$CLIENT2" >/dev/null 2>&1 || true
	docker network rm "$NET1" "$NET2" >/dev/null 2>&1 || true
	if [ "${KEEP_ORG:-0}" != "1" ]; then
		for id in "${OWNER_ID:-}" "${INVITEE_ID:-}"; do
			[ -n "$id" ] && "${OSA[@]}" user delete "$id" >/dev/null 2>&1 || true
		done
		"${OSA[@]}" organization delete "$SUBDOMAIN" >/dev/null 2>&1 || true
	else
		log "KEEP_ORG=1 set: leaving organization $SUBDOMAIN and its users in place"
	fi
	exit "$status"
}
trap cleanup EXIT

curl_api() {
	# curl_api <cookie> <method> <path> [json-body]
	local cookie=$1 method=$2 path=$3 body=${4:-}
	if [ -n "$body" ]; then
		curl -sS --fail-with-body --cacert "$CA_CERT" -b "$cookie" -X "$method" \
			-H 'Content-Type: application/json' -d "$body" "$API$path"
	else
		curl -sS --fail-with-body --cacert "$CA_CERT" -b "$cookie" -X "$method" "$API$path"
	fi
}

log "provisioning organization $SUBDOMAIN (real headscale/headplane containers)..."
"${OSA[@]}" organization create "$SUBDOMAIN" >&2

log "minting a session for the owner ($OWNER_EMAIL), attached to $SUBDOMAIN..."
COOKIE_OWNER=$("${OSA[@]}" user login --org "$SUBDOMAIN" --role owner "$OWNER_EMAIL" 2>/dev/null | tr -d '\r')
[ -n "$COOKIE_OWNER" ] || die "osa user login (owner) produced no cookie"

log "creating an invite for the invitee ($INVITEE_EMAIL)..."
INVITE_JSON=$(curl_api "$COOKIE_OWNER" POST /api/invites "{\"email\":\"$INVITEE_EMAIL\",\"role\":\"member\"}")
TOKEN=$(echo "$INVITE_JSON" | jq -r .invite.token)
[ -n "$TOKEN" ] && [ "$TOKEN" != null ] || die "no invite token in response: $INVITE_JSON"

log "minting a session for the invitee (no organization yet)..."
COOKIE_INVITEE=$("${OSA[@]}" user login "$INVITEE_EMAIL" 2>/dev/null | tr -d '\r')
[ -n "$COOKIE_INVITEE" ] || die "osa user login (invitee) produced no cookie"

log "accepting the invite as the invitee (real POST /api/invites/{token}/accept)..."
curl_api "$COOKIE_INVITEE" POST "/api/invites/$TOKEN/accept" >/dev/null

# GET /api/session both links the caller's headscale user (on its first
# call for a given user, once their org is active) and reports it (from
# its *second* call onward -- see handlers.Handlers.Session/
# ensureCallerHeadscaleUser, which reads/writes the DB but doesn't mutate
# the in-memory user used to build that same response).
session_after_link() {
	local cookie=$1
	curl_api "$cookie" GET /api/session >/dev/null
	curl_api "$cookie" GET /api/session
}

log "linking both users to headscale users..."
OWNER_SESSION=$(session_after_link "$COOKIE_OWNER")
OWNER_ID=$(echo "$OWNER_SESSION" | jq -r .user.ID)
OWNER_HSID=$(echo "$OWNER_SESSION" | jq -r .user.HeadscaleUserID.String)
INVITEE_SESSION=$(session_after_link "$COOKIE_INVITEE")
INVITEE_ID=$(echo "$INVITEE_SESSION" | jq -r .user.ID)
INVITEE_HSID=$(echo "$INVITEE_SESSION" | jq -r .user.HeadscaleUserID.String)
[ -n "$OWNER_HSID" ] && [ "$OWNER_HSID" != null ] || die "owner has no linked headscale user"
[ -n "$INVITEE_HSID" ] && [ "$INVITEE_HSID" != null ] || die "invitee has no linked headscale user"

log "creating pre-auth keys for both users..."
authkey_for() {
	local hsid=$1
	curl_api "$COOKIE_OWNER" POST /api/auth-keys \
		"{\"userId\":\"$hsid\",\"expiryDays\":1,\"reusable\":false,\"ephemeral\":false}" \
		| jq -r .authKey.key
}
AUTHKEY1=$(authkey_for "$OWNER_HSID")
AUTHKEY2=$(authkey_for "$INVITEE_HSID")

log "creating two network-isolated Docker networks for the clients..."
docker network create "$NET1" >/dev/null
docker network create "$NET2" >/dev/null

# TS_USERSPACE=true (the official image's containerboot entrypoint) uses a
# userspace netstack, so no --cap-add=NET_ADMIN/--device=/dev/net/tun is
# needed. The CA cert is mounted and trusted via update-ca-certificates
# before containerboot starts, so the client's TLS connection to
# headscale is genuinely verified against our local dev CA (no
# --insecure-skip-verify shortcut). --add-host maps the org's domain to
# the host's gateway IP, exactly like a real remote peer reaching the
# host's Caddy-published port -- there is no other route between these
# containers and the rest of the stack.
run_client() {
	local name=$1 network=$2 authkey=$3 hostname=$4
	docker run -d --rm --name "$name" --network "$network" \
		--add-host "$SUBDOMAIN.$BASE_DOMAIN:host-gateway" \
		-v "$CA_CERT:/usr/local/share/ca-certificates/ownscale-local-ca.crt:ro" \
		-e TS_AUTHKEY="$authkey" \
		-e TS_USERSPACE=true \
		-e TS_EXTRA_ARGS="--login-server=https://$SUBDOMAIN.$BASE_DOMAIN --hostname=$hostname --accept-routes=false" \
		--entrypoint /bin/sh \
		tailscale/tailscale:latest \
		-c 'update-ca-certificates >/dev/null 2>&1; exec /usr/local/bin/containerboot' >/dev/null
}

log "starting client1 (owner) on $NET1..."
run_client "$CLIENT1" "$NET1" "$AUTHKEY1" client1
log "starting client2 (invitee) on $NET2..."
run_client "$CLIENT2" "$NET2" "$AUTHKEY2" client2

log "opening a gnome-terminal window per client (tailscale status)..."
gnome-terminal --title "tailscale client1 ($SUBDOMAIN)" -- bash -c \
	"docker logs -f '$CLIENT1' & docker exec -it '$CLIENT1' sh -c 'sleep 2; tailscale status'; exec bash" || true
gnome-terminal --title "tailscale client2 ($SUBDOMAIN)" -- bash -c \
	"docker logs -f '$CLIENT2' & docker exec -it '$CLIENT2' sh -c 'sleep 2; tailscale status'; exec bash" || true

log "waiting for both clients to register with headscale..."
tailscale_ip() {
	local container=$1 peer_hostname=$2
	docker exec "$container" tailscale status --json 2>/dev/null \
		| jq -r --arg h "$peer_hostname" '.Peer[]? | select(.HostName==$h) | .TailscaleIPs[0]'
}
CLIENT2_IP=""
for _ in $(seq 1 30); do
	CLIENT2_IP=$(tailscale_ip "$CLIENT1" client2)
	[ -n "$CLIENT2_IP" ] && [ "$CLIENT2_IP" != null ] && break
	sleep 2
done
[ -n "$CLIENT2_IP" ] && [ "$CLIENT2_IP" != null ] || die "client1 never saw client2 as a peer (headscale coordination failed)"
log "client2's tailscale IP (as seen from client1): $CLIENT2_IP"

log "pinging client2 from client1 (only possible via headscale + DERP -- the two client containers share no network route)..."
PING_OUT=$(docker exec "$CLIENT1" tailscale ping --c 5 "$CLIENT2_IP" 2>&1) || true
echo "$PING_OUT" >&2
echo "$PING_OUT" | grep -q "pong from" || die "no successful ping between the isolated clients -- DERP relay connectivity failed"

log "success: client1 and client2 reached each other across isolated networks via headscale + DERP."
