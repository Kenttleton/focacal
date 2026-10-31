#!/usr/bin/env bash
# focacal.sh — host-side wrapper for the focacal CLI.
#
# Place this in your PATH (e.g. /usr/local/bin/focacal) and make it executable:
#   chmod +x focacal.sh
#   sudo cp focacal.sh /usr/local/bin/focacal
#
# Then use it like:
#   focacal status
#   focacal auth google work-google
#   focacal sync

set -euo pipefail

CONTAINER="${FOCACAL_CONTAINER:-focacal}"

# Check the container is running.
if ! docker inspect --format '{{.State.Running}}' "$CONTAINER" 2>/dev/null | grep -q true; then
  echo "focacal container '$CONTAINER' is not running." >&2
  echo "Start it with: docker compose up -d" >&2
  exit 1
fi

# The auth command needs an interactive TTY for browser flow feedback.
if [[ "${1:-}" == "auth" ]]; then
  exec docker exec -it "$CONTAINER" focacal "$@"
else
  exec docker exec "$CONTAINER" focacal "$@"
fi
