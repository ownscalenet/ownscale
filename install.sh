#!/bin/sh
# Installs ownscale with Docker Compose:
#   curl -fsSL https://raw.githubusercontent.com/ownscalenet/ownscale/main/install.sh | sh
#
# OWNSCALE_VERSION picks a release instead of the latest one, and
# OWNSCALE_INSTALL_URL downloads the files from elsewhere (tests use a checkout).
set -eu

REPO=ownscalenet/ownscale

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
heading() { printf '\n== %s\n\n' "$*"; }

# ask PROMPT DEFAULT: reads from the terminal, since stdin is the script when
# piped from curl.
ask() {
	if [ -n "$2" ]; then
		printf '%s [%s]: ' "$1" "$2" >/dev/tty
	else
		printf '%s: ' "$1" >/dev/tty
	fi
	IFS= read -r answer </dev/tty || die "no answer, run the installer from a terminal"
	[ -n "$answer" ] || answer=$2
}

# set_env KEY VALUE: sets KEY in .env, uncommenting its "# KEY=" line if
# that's all there is, so example.env's comments stay next to it.
set_env() {
	if grep -q "^$1=" .env; then
		sed "s|^$1=.*|$1=$2|" .env >.env.tmp
	elif grep -q "^# $1=" .env; then
		sed "s|^# $1=.*|$1=$2|" .env >.env.tmp
	else
		cp .env .env.tmp && printf '%s=%s\n' "$1" "$2" >>.env.tmp
	fi
	mv .env.tmp .env
}

latest_version() {
	curl -fsSI "https://github.com/$REPO/releases/latest" |
		tr -d '\r' | sed -n 's|^[Ll]ocation: .*/tag/||p'
}

random_secret() {
	openssl rand -hex 32 2>/dev/null ||
		od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
}

valid_domain() {
	printf '%s' "$1" | grep -Eq '^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]([a-z0-9-]*[a-z0-9])?$'
}

# Upstream addresses printed in the proxy configs, set by write_override.
upstream=
ask_upstream=

# write_override PROXY NETWORK: removes the bundled Caddy and lets the proxy
# reach ownscale, over a Docker network or on loopback ports.
write_override() {
	set_env COMPOSE_PROFILES ""
	{
		say "# Written by install.sh for your own reverse proxy ($1)."
		say "services:"
		say "  ownscale:"
		if [ -n "$2" ]; then
			say "    networks: [default, proxy]"
		else
			say "    ports:"
			say '      - "127.0.0.1:8080:8080"'
			if [ "$1" = caddy ]; then
				say '      - "127.0.0.1:8081:8081"'
			fi
		fi
		if [ "$1" = traefik ]; then
			traefik_labels "$2"
		fi
		if [ -n "$2" ]; then
			say "networks:"
			say "  proxy:"
			say "    name: $2"
			say "    external: true"
		fi
	} >compose.override.yaml
	if [ -n "$2" ]; then
		upstream=ownscale:8080
		ask_upstream=ownscale:8081
		if command -v docker >/dev/null 2>&1 && ! docker network inspect "$2" >/dev/null 2>&1; then
			say "warning: Docker network \"$2\" doesn't exist yet; compose needs it to start."
		fi
	else
		upstream=127.0.0.1:8080
		ask_upstream=127.0.0.1:8081
	fi
}

traefik_labels() {
	escaped=$(printf '%s' "$domain" | sed 's/\./\\\\./g')
	cat <<EOF
    labels:
      traefik.enable: "true"
      traefik.docker.network: "$1"
      traefik.http.routers.ownscale.rule: "Host(\`$domain\`) || HostRegexp(\`^[a-z0-9-]+\\\\.$escaped\$\$\`)"
      traefik.http.routers.ownscale.entrypoints: websecure
      traefik.http.routers.ownscale.tls.certresolver: letsencrypt
      traefik.http.routers.ownscale.tls.domains[0].main: "$domain"
      traefik.http.routers.ownscale.tls.domains[0].sans: "*.$domain"
      traefik.http.services.ownscale.loadbalancer.server.port: "8080"
EOF
}

print_caddy() {
	heading "Add this to your Caddyfile"
	cat <<EOF
{
	on_demand_tls {
		ask http://$ask_upstream/tls-allowed
	}
}

$domain, *.$domain {
	tls {
		on_demand
	}
	reverse_proxy $upstream
}
EOF
	say ""
	say "Caddy gets a certificate for each host the first time it's visited,"
	say "after ownscale confirms the host exists. No DNS provider needed."
}

print_traefik() {
	heading "Traefik"
	say "compose.override.yaml labels ownscale for Traefik. In Traefik's own config:"
	say ""
	say "- Use a certificate resolver named \"letsencrypt\" with a DNS challenge"
	say "  (wildcard certificates need one), or rename it in compose.override.yaml."
	say "- Let requests stay open, for clients' long-polls:"
	say "    entryPoints.websecure.transport.respondingTimeouts.readTimeout=0"
	say "- Redirect the \"web\" entry point to \"websecure\"."
}

print_nginx() {
	heading "Add this to your nginx config"
	cat <<EOF
map \$http_upgrade \$connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 80;
    server_name $domain .$domain;
    return 301 https://\$host\$request_uri;
}

server {
    listen 443 ssl;
    http2 on;
    server_name $domain .$domain;

    ssl_certificate     /etc/letsencrypt/live/$domain/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/$domain/privkey.pem;

    location / {
        proxy_pass http://$upstream;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Forwarded-For \$remote_addr;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$connection_upgrade;
        proxy_buffering off;
        proxy_read_timeout 1d;
        proxy_send_timeout 1d;
    }
}
EOF
	say ""
	say "The certificate must cover $domain and *.$domain. Let's Encrypt only"
	say "issues wildcards through a DNS challenge, e.g. with a certbot DNS plugin:"
	say "  certbot certonly --dns-cloudflare -d $domain -d '*.$domain'"
}

print_npm() {
	heading "In Nginx Proxy Manager, add a proxy host"
	say "Details:"
	say "  Domain names:          $domain  *.$domain"
	say "  Scheme / host / port:  http / ${upstream%:*} / 8080"
	say "  Websockets support:    on"
	say "SSL:"
	say "  Request a new certificate with \"Use a DNS Challenge\" (needed for"
	say "  the wildcard), and turn on Force SSL and HTTP/2."
	say "Advanced, custom nginx configuration:"
	say "  proxy_buffering off;"
	say "  proxy_read_timeout 1d;"
	say "  proxy_send_timeout 1d;"
}

main() {
	command -v curl >/dev/null 2>&1 || die "curl is required"
	if ! docker compose version >/dev/null 2>&1; then
		say "warning: Docker Compose not found; install it before starting ownscale."
	fi

	say "This installs ownscale with Docker Compose. See"
	say "https://github.com/$REPO for the documentation."
	say ""

	ask "Install directory" ./ownscale
	dir=$answer
	for f in .env compose.override.yaml; do
		[ ! -e "$dir/$f" ] || die "$dir/$f exists; to upgrade an install, run 'make upgrade' there"
	done

	version=${OWNSCALE_VERSION:-}
	[ -n "$version" ] || version=$(latest_version) || true
	[ -n "$version" ] || die "can't find the latest release; set OWNSCALE_VERSION=vX.Y.Z"
	url=${OWNSCALE_INSTALL_URL:-https://raw.githubusercontent.com/$REPO/$version}

	while :; do
		ask "Domain for ownscale, with a wildcard DNS record (e.g. example.com)" ""
		domain=$(printf '%s' "$answer" | tr '[:upper:]' '[:lower:]')
		valid_domain "$domain" && break
		say "\"$answer\" isn't a domain name like example.com."
	done

	say ""
	say "How does HTTPS reach ownscale?"
	say "  1) Bundled Caddy: gets certificates itself (recommended)"
	say "  2) My own Caddy"
	say "  3) My own Traefik"
	say "  4) My own nginx"
	say "  5) My own Nginx Proxy Manager"
	while :; do
		ask "Choice" 1
		case $answer in
		1) proxy=bundled ;;
		2) proxy=caddy ;;
		3) proxy=traefik ;;
		4) proxy=nginx ;;
		5) proxy=npm ;;
		*) say "Pick 1 to 5."; continue ;;
		esac
		break
	done

	network=
	case $proxy in
	caddy | nginx)
		ask "Docker network your proxy is on (empty if it runs on this host)" ""
		network=$answer
		;;
	traefik)
		ask "Docker network Traefik is on" traefik
		network=$answer
		;;
	npm)
		while [ -z "$network" ]; do
			ask "Docker network Nginx Proxy Manager is on (see 'docker network ls')" ""
			network=$answer
		done
		;;
	esac

	heading "Installing ownscale $version in $dir"
	mkdir -p "$dir"
	cd "$dir"
	curl -fsSL -o compose.yaml "$url/compose.yaml" || die "download $url/compose.yaml failed"
	curl -fsSL -o example.env "$url/example.env" || die "download $url/example.env failed"
	curl -fsSL -o Makefile "$url/deploy.mk" ||
		{ rm -f Makefile; say "warning: no Makefile for $version; use docker compose directly."; }

	cp example.env .env
	chmod 600 .env
	set_env BASE_DOMAIN "$domain"
	set_env OWNSCALE_AUTH_SESSION_SECRET "$(random_secret)"
	set_env OWNSCALE_VERSION "$version"
	say "Wrote .env with a new session secret."

	case $proxy in
	bundled) ;;
	*)
		write_override "$proxy" "$network"
		say "Wrote compose.override.yaml: the bundled Caddy is off."
		;;
	esac
	case $proxy in
	caddy) print_caddy ;;
	traefik) print_traefik ;;
	nginx) print_nginx ;;
	npm) print_npm ;;
	esac

	heading "Next steps"
	say "1. Point DNS records for $domain and *.$domain at this server."
	if [ "$proxy" = bundled ]; then
		say "   Ports 80 and 443 must be free and reachable."
	fi
	say "2. Optionally, edit $dir/.env to add login providers (passkeys work"
	say "   without any), backups, or a DNS provider for machine certificates."
	say "3. Start ownscale:"
	say "     cd $dir && docker compose up -d"
	say "4. Sign in at https://console.$domain/login. The first sign-in creates"
	say "   your organization."
}

main "$@"
