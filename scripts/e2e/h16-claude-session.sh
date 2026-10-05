#!/usr/bin/env bash
# H16: a real Claude Code session with the plugin loaded gets the journal path at start.
# Starts one short `claude -p` run (a few cents). The hook's `herdr-desk` is a wrapper that points at
# this script's home, so Claude Code itself runs with its normal environment.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
run 0 on home herdr-desk setup --no-herdr
start_daemon home

mkdir -p "$E2E/wrap"
cat >"$E2E/wrap/herdr-desk" <<WRAP
#!/bin/sh
XDG_CONFIG_HOME="$E2E/home/config" XDG_STATE_HOME="$E2E/home/state" \\
XDG_DATA_HOME="$E2E/home/data" XDG_CACHE_HOME="$E2E/home/cache" exec "$BIN/herdr-desk" "\$@"
WRAP
chmod +x "$E2E/wrap/herdr-desk"

SID=$(uuidgen | tr '[:upper:]' '[:lower:]')
VIEW="$E2E/home/state/herdr-desk/sessions/$SID.md"
PROMPT="A session-start hook added a line to your context that begins with: herdr-desk journal for this session. Reply with only the file path on that line, nothing else."

run 0 env -u DESK_HOOKS -u DESK_SESSION PATH="$E2E/wrap:$PATH" claude -p \
  --plugin-dir "$REPO/profiles/claude-code" --session-id "$SID" --model haiku \
  --max-budget-usd 0.50 --output-format json "$PROMPT"

[ -f "$VIEW" ] || fail "the hook did not write $VIEW"
say "hook ran: view file exists"
jq -r '.result' <<<"$OUT" | grep -q -F "$VIEW" || fail "the session's reply does not hold the view's path"
say "session knows the path"
pass
