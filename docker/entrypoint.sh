#!/bin/sh
# Runs pahe as PUID:PGID (default 1000:1000) so files match your media user, like linuxserver images.
set -e
umask "${UMASK:-002}"
if [ "$(id -u)" = "0" ]; then
  PUID="${PUID:-1000}"
  PGID="${PGID:-1000}"
  mkdir -p "$PAHE_CONFIG_DIR"
  chown -R "$PUID:$PGID" "$PAHE_CONFIG_DIR"
  exec su-exec "$PUID:$PGID" pahe "$@"
fi
exec pahe "$@"
