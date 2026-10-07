#!/bin/sh
# Applies all pending migrations (osa's "migrate up" subcommand applies
# exactly one pending migration per invocation, printing the exact string
# "no pending migrations" once there's nothing left), then starts the
# ownscale server.
set -e

# Fail fast (before the app ever starts) if ownscale's own database is
# missing although it has a backup.
osa verify db

# TODO: is this automatic migration ok? Should it be manual?
while true; do
	output=$(osa migrate up)
	echo "$output"
	if [ "$output" = "no pending migrations" ]; then
		break
	fi
done

# Now that ownscale.sqlite is present+migrated, verify every
# organization's headscale database the same way (manual
# `osa restore organizations` to recover).
osa verify organizations

exec ownscale
