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

# A list that fails is not an empty one: its error and exit status end the script, and no pane opens.
if [ -n "${HERDR_WORKSPACE_ID:-}" ]; then
  panes=$("$herdr_bin" pane list --workspace "$HERDR_WORKSPACE_ID") || exit
else
  panes=$("$herdr_bin" pane list) || exit
fi

# herdr writes a pane's keys in order: "agent" (an agent's pane only), "cwd", then "pane_id". An agent's
# pane in the plugin's folder is the user's, never the board.
candidates=$(printf '%s' "$panes" | grep -oE '"(agent|cwd|pane_id)":"[^"]*"' |
  awk -F'"' -v root="$root" '
    $2 == "agent" { agent = 1; next }
    $2 == "cwd" { m = ($4 == root && !agent); next }
    { if (m) print $4; agent = 0; m = 0 }')

# herdr focuses by id only a pane a plugin owns, so a shell in the plugin's folder refuses the focus
# and the next pane is tried. When none takes it, the board opens.
for pane_id in $candidates; do
  "$herdr_bin" plugin pane focus "$pane_id" 2>/dev/null && exit 0
done
exec "$herdr_bin" plugin pane open --plugin "$plugin_id" --entrypoint board --focus
