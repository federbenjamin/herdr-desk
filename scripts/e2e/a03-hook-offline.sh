#!/usr/bin/env bash
# H6: the session-start hook on a client whose home times out returns in under 10 s and says the journal is not
# loaded. The home holds each request 5 s before it fails, as ssh's connect timeout would.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
home_up home
make_client cli home
home_down home 5

start=$(date +%s)
run_in 0 '{"session_id":"s-a03","source":"startup","hook_event_name":"SessionStart","cwd":"/"}' \
  on cli herdr-desk hook start --format claude-code
took=$(($(date +%s) - start))

[ "$took" -lt 10 ] || fail "the hook took $took s"
ok "the hook took $took s (under 10)"
out_has "journal is not loaded"
ok "stdout holds journal is not loaded"
calls=$(wc -l <"$E2E/home.shim.log" | tr -d ' ')
[ "$calls" = 1 ] || fail "the shim was called $calls times, want 1"
ok "the shim was called once"
pass
