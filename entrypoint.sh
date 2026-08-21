#!/bin/sh
# Applies all pending migrations (osa's "migrate up" subcommand applies
# exactly one pending migration per invocation, printing the exact string
# "no pending migrations" once there's nothing left), then starts the
# ownscale server.
set -e

CONFIG_PATH="${OWNSCALE_CONFIG:-/etc/ownscale/config.yaml}"

while true; do
	output=$(/app/osa --config "$CONFIG_PATH" migrate up)
	echo "$output"
	if [ "$output" = "no pending migrations" ]; then
		break
	fi
done

exec /app/ownscale -config "$CONFIG_PATH"
