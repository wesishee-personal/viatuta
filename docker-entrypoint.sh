#!/bin/sh
# Applies pending migrations, then execs the container's command.
#
# Running this here rather than as a separate compose step means the API
# container and the CLI container (same image, different `command:`) both
# always start against an up-to-date schema, with no extra orchestration.
set -eu

: "${VIATUTA_DATABASE_URL:?VIATUTA_DATABASE_URL must be set}"

echo "waiting for database to accept connections..."
attempt=0
until /app/goose -dir /app/db/migrations postgres "$VIATUTA_DATABASE_URL" status >/dev/null 2>&1; do
    attempt=$((attempt + 1))
    if [ "$attempt" -ge 30 ]; then
        echo "database did not become ready in time" >&2
        exit 1
    fi
    sleep 2
done

echo "applying migrations..."
/app/goose -dir /app/db/migrations postgres "$VIATUTA_DATABASE_URL" up

exec "$@"
