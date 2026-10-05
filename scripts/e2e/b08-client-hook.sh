#!/usr/bin/env bash
# H31: the event hook on a client machine exits 0 and starts no transport. The client's transport is the ssh shim, which
# logs every call.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
home_up home
make_client cli home

JSON='{"event":"pane_closed","data":{"type":"pane_closed","pane_id":"p1","workspace_id":"w1"}}'
run 0 on cli env HERDR_PLUGIN_EVENT=pane.closed HERDR_PLUGIN_EVENT_JSON="$JSON" herdr-desk hook herdr-event
ok "exit 0"
[ ! -s "$E2E/home.shim.log" ] || fail "the hook started the transport: $(cat "$E2E/home.shim.log")"
ok "the shim log is empty"

# The shim does log a call that goes through it, so an empty log above means no call.
run 0 on cli herdr-desk list
[ -s "$E2E/home.shim.log" ] || fail "the shim logs nothing for a list on the client"
pass
