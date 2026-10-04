#!/bin/sh
# Opens a desk pane: `board` focuses this plugin's open board pane in this workspace when one is
# open and opens it otherwise; `capture` opens the capture popup.
set -u

entrypoint="${1:?usage: open-pane.sh board|capture}"
herdr_bin="${HERDR_BIN_PATH:-herdr}"
plugin_id="${HERDR_PLUGIN_ID:-desk}"

case "$entrypoint" in
  capture)
    # A popup is a session singleton: a second press while one is up is not an error.
    out=$("$herdr_bin" plugin pane open --plugin "$plugin_id" --entrypoint capture --focus 2>&1)
    status=$?
    case "$out" in
      *"popup pane is already open"*) exit 0 ;;
    esac
    [ -n "$out" ] && printf '%s\n' "$out"
    exit "$status"
    ;;
  board) ;;
  *)
    echo "open-pane.sh: unknown pane '$entrypoint'" >&2
    exit 2
    ;;
esac

# herdr starts a plugin's panes in the plugin's folder and lists a pane's cwd with symlinks
# resolved. A title plugin rewrites the label; the cwd stays.
root=$(cd "$(dirname "$0")/.." && pwd -P)

if [ -n "${HERDR_WORKSPACE_ID:-}" ]; then
  panes=$("$herdr_bin" pane list --workspace "$HERDR_WORKSPACE_ID" 2>/dev/null) || panes=""
else
  panes=$("$herdr_bin" pane list 2>/dev/null) || panes=""
fi

# herdr writes a pane's keys in order, so its "cwd" comes before its "pane_id".
pane_id=$(printf '%s' "$panes" | grep -oE '"(cwd|pane_id)":"[^"]*"' |
  awk -F'"' -v root="$root" '$2 == "cwd" { m = ($4 == root); next } m { print $4; exit }')

if [ -n "$pane_id" ]; then
  exec "$herdr_bin" plugin pane focus "$pane_id"
fi
exec "$herdr_bin" plugin pane open --plugin "$plugin_id" --entrypoint board --focus
