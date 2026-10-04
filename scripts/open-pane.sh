#!/bin/sh
# Opens a desk pane: `board` focuses the pane titled "desk" in this workspace when one is open
# and opens it otherwise; `capture` opens the capture popup.
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
      *"popup already open"*) exit 0 ;;
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

if [ -n "${HERDR_WORKSPACE_ID:-}" ]; then
  panes=$("$herdr_bin" pane list --workspace "$HERDR_WORKSPACE_ID" 2>/dev/null) || panes=""
else
  panes=$("$herdr_bin" pane list 2>/dev/null) || panes=""
fi

pane_id=$(printf '%s' "$panes" | grep -o '"label":"desk","pane_id":"[^"]*"' | head -n 1 |
  sed 's/.*"pane_id":"\([^"]*\)"/\1/')

if [ -n "$pane_id" ]; then
  # herdr has no focus-by-id: zooming on focuses the pane, zooming off keeps the focus.
  "$herdr_bin" pane zoom "$pane_id" --on >/dev/null 2>&1 || true
  exec "$herdr_bin" pane zoom "$pane_id" --off
fi
exec "$herdr_bin" plugin pane open --plugin "$plugin_id" --entrypoint board --focus
