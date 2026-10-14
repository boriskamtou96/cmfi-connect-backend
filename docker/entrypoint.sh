#!/bin/sh
# Starts a command of the image (the server by default) with the settings the
# hosting platforms give, after the pending migrations.
#
# Read here, besides the variables of internal/utils/config.go:
#   DATABASE_URL               the database, as Render, Railway and Fly.io give it
#                              (copied to DB_DSN when DB_DSN is not set)
#   FIREBASE_CREDENTIALS_JSON  the content of the Firebase key, as a secret
#                              variable (written to a file only the app reads)
#   MIGRATE_ON_START           "false" to apply the migrations by hand
set -eu

if [ -z "${DB_DSN:-}" ] && [ -n "${DATABASE_URL:-}" ]; then
  export DB_DSN="$DATABASE_URL"
fi
if [ -z "${DB_DSN:-}" ]; then
  echo "DB_DSN (or DATABASE_URL) is not set: no database to use" >&2
  exit 1
fi

if [ -z "${FIREBASE_CREDENTIALS_FILE:-}" ] && [ -n "${FIREBASE_CREDENTIALS_JSON:-}" ]; then
  umask 077
  printf '%s' "$FIREBASE_CREDENTIALS_JSON" > /tmp/firebase-credentials.json
  export FIREBASE_CREDENTIALS_FILE=/tmp/firebase-credentials.json
  unset FIREBASE_CREDENTIALS_JSON
fi

if [ "${1:-server}" = "server" ] && [ "${MIGRATE_ON_START:-true}" = "true" ]; then
  # two instances starting together wait for each other (a lock in Postgres)
  migrate -path "$MIGRATIONS_PATH" -database "$DB_DSN" up
fi

exec "$@"
