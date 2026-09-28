#!/bin/sh
# devbox-up joins this container to an ownscale organization's tailnet:
#
#   devbox-up LOGIN_SERVER AUTHKEY HOSTNAME [tailscale up flags...]
#
# The local dev CA must be mounted under /usr/local/share/ca-certificates.
set -e

if [ $# -lt 3 ]; then
	echo "usage: devbox-up LOGIN_SERVER AUTHKEY HOSTNAME [tailscale up flags...]" >&2
	exit 2
fi
login_server=$1
authkey=$2
hostname=$3
shift 3

update-ca-certificates >/dev/null
mkdir -p /var/run/tailscale
tailscaled --state=/var/lib/tailscale/tailscaled.state --socket=/var/run/tailscale/tailscaled.sock >/var/log/tailscaled.log 2>&1 &
for _ in $(seq 1 20); do
	[ -S /var/run/tailscale/tailscaled.sock ] && break
	sleep 0.5
done
tailscale up --login-server="$login_server" --authkey="$authkey" --hostname="$hostname" --accept-dns=false "$@"
