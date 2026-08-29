#!/bin/sh
# Applies all pending migrations (osa's "migrate up" subcommand applies
# exactly one pending migration per invocation, printing the exact string
# "no pending migrations" once there's nothing left), then starts the
# ownscale server.
set -e

CONFIG_PATH="${OWNSCALE_CONFIG:-/etc/ownscale/config.yaml}"

# Fail fast (before the app ever starts) if ownscale's own database looks
# unhealthy despite an existing litestream backup.
/app/osa --config "$CONFIG_PATH" verify db

# TODO: is this automatic migration ok? Should it be manual?
while true; do
	output=$(/app/osa --config "$CONFIG_PATH" migrate up)
	echo "$output"
	if [ "$output" = "no pending migrations" ]; then
		break
	fi
done

# Now that ownscale.sqlite is present+migrated, verify every
# organization's headscale/headplane database the same way (manual
# `osa restore organizations` to recover).
/app/osa --config "$CONFIG_PATH" verify organizations

exec /app/ownscale -config "$CONFIG_PATH"
