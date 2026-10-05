#!/usr/bin/env bash
# H30: a status change in a pane no run owns adds no event to the store, and the hook exits 0 and makes no herdr call.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
with_events
runner_up home
CALLS="$E2E/herdr/calls.log"

# One run is live, so the hook has a store to look in; the pane under test belongs to nobody.
start_task home "owns its own pane" --desk
wait_note 1 "run 1: workspace"
wait_long 10 "the stub worker to report its session" pane_has_session "$(run_field 1 session)"
created=$(herdr_do workspace create --cwd "$E2E" --label "not a run")
FOREIGN=$(jq -r .result.root_pane.pane_id <<<"$created")
[ "$FOREIGN" != "$(run_field 1 pane)" ] || fail "the foreign pane is the run's pane"

sleep 1
EVENTS=$(sqlite3 "$DB" "SELECT count(*) FROM events")
BEFORE=$(wc -l <"$CALLS" | tr -d ' ')
report "$FOREIGN" working
report "$FOREIGN" idle
report "$FOREIGN" blocked
sleep 3
[ "$(sqlite3 "$DB" "SELECT count(*) FROM events")" = "$EVENTS" ] || fail "a status change in pane $FOREIGN added an event"
ok "the event count is unchanged"
[ "$(($(wc -l <"$CALLS" | tr -d ' ') - BEFORE))" = 3 ] || fail "herdr was called $(($(wc -l <"$CALLS" | tr -d ' ') - BEFORE)) times for three report-agent calls: $(tail -n 6 "$CALLS")"

# The hook itself, run to the end: it exits 0 and calls nothing.
BEFORE=$(wc -l <"$CALLS" | tr -d ' ')
JSON=$(printf '{"event":"pane_agent_status_changed","data":{"type":"pane_agent_status_changed","pane_id":"%s","agent_status":"working"}}' "$FOREIGN")
run 0 on home env HERDR_PLUGIN_EVENT=pane.agent_status_changed HERDR_PLUGIN_EVENT_JSON="$JSON" herdr-desk hook herdr-event
ok "the hook exited 0"
[ "$(wc -l <"$CALLS" | tr -d ' ')" = "$BEFORE" ] || fail "the hook called herdr: $(tail -n 2 "$CALLS")"
[ "$(sqlite3 "$DB" "SELECT count(*) FROM events")" = "$EVENTS" ] || fail "the hook added an event"
ok "the hook made no herdr call"
pass
