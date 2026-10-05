#!/usr/bin/env bash
# A real Claude Code session with the plugin loaded gets the journal path at start.
# Starts one short `claude -p` run (a few cents) through claude_wrapper: Claude Code runs on the caller's own folders,
# and the hook's `herdr-desk` is the wrapper in AGENT_BIN, which points at this script's home.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
home_up home

claude_wrapper session

SID=$(uuidgen | tr '[:upper:]' '[:lower:]')
VIEW="$E2E/home/state/herdr-desk/sessions/$SID.md"
PROMPT="A session-start hook added a line to your context that begins with: herdr-desk journal for this session. Reply with only the file path on that line, nothing else."

run 0 env -u DESK_HOOKS -u DESK_SESSION "$CLAUDE_WRAP" -p \
  --plugin-dir "$REPO/profiles/claude-code" --session-id "$SID" --model haiku \
  --max-budget-usd 0.50 --output-format json "$PROMPT"

[ -f "$VIEW" ] || fail "the hook did not write $VIEW"
say "hook ran: view file exists"
jq -r '.result' <<<"$OUT" | grep -q -F "$VIEW" || fail "the session's reply does not hold the view's path"
say "session knows the path"
pass
