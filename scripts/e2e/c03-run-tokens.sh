#!/usr/bin/env bash
# H46: each live run's pane gets its desk token and loses it when the run ends: the row text for a running run, a
# hand-back with a pull request ref, a blocked run, an idle one, a task set done while its pane is open, and a killed
# run, whose pane is closed and gets no report. Runs on the fake herdr, which keeps each pane's tokens.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
with_events
SELF="$WORK/self"
mkdir -p "$SELF"
add_root "$SELF" self "runs many"
RC_CAP=4
runner_up home
CALLS="$E2E/herdr/calls.log"

# token <pane>: the desk token herdr holds for the pane; empty when it has none or the pane is gone.
token() { herdr_do pane get "$1" 2>/dev/null | jq -r '.result.pane.tokens.desk // ""'; }
# token_is <pane> <text>: the pane's desk token is the text.
token_is() { [ "$(token "$1")" = "$2" ]; }
# token_matches <pane> <regex>: the pane's desk token matches the extended regex.
token_matches() { token "$1" | grep -Eqx -- "$2"; }
# wait_token <pane> <text>: until the pane's desk token is the text; on a timeout say what it is.
wait_token() {
  wait_long 15 "pane $1 to show '$2' (it shows '$(token "$1")')" token_is "$1" "$2"
}

# Four tasks on a root that takes any number of runs. T2 and T3's workers act as soon as they start.
for n in 1 2 3 4; do run 0 on home herdr-desk add -t "task $n" --desk; done
set_mode 2 blocked
set_mode 3 idle
for n in 1 2 3; do run 0 on home herdr-desk run start "T$n" --root "$SELF" --model sonnet; done
# T2 and T3 leave `running` at once, so the wait is for their workers to start.
for n in 1 2 3; do wait_file "$STUB/worker-run$n.env"; done
PANE1=$(run_field 1 pane)
PANE2=$(run_field 2 pane)
PANE3=$(run_field 3 pane)
wait_long 10 "the stub workers to report their sessions" pane_has_session "$(run_field 1 session)"

wait_long 15 "T1's row to read running" token_matches "$PANE1" 'T1 running · since [0-9]{2}:[0-9]{2}'
ok "running: $(token "$PANE1")"

run 0 as_agent home "$(run_field 1 session)" env DESK_RUN=1 herdr-desk set T1 review --ref https://github.com/example/repo/pull/12
wait_token "$PANE1" "T1 review · PR #12"
ok "hand-back with a PR ref: $(token "$PANE1")"

wait_token "$PANE2" "T2 needs you · blocked"
ok "blocked: $(token "$PANE2")"

wait_token "$PANE3" "T3 review · went idle"
ok "idle: $(token "$PANE3")"

run 0 on home herdr-desk set T3 'done'
wait_token "$PANE3" "T3 done"
herdr_do pane get "$PANE3" >/dev/null || fail "pane $PANE3 is not open after T3 was set done"
ok "done with the pane open: $(token "$PANE3")"

# Every desk token the home ever reported fits a 30-column sidebar's row.
LONG=$(grep -o 'desk=[^"]*' "$CALLS" | awk '{ v = substr($0, 6); if (length(v) > 28) print v }' || true)
[ -z "$LONG" ] || fail "a row is over 28 characters: $LONG"
[ "$(grep -c -- '--token desk=' "$CALLS")" -ge 5 ] || fail "fewer than five desk tokens were reported: $(grep -- 'report-metadata' "$CALLS")"
ok "no line is over 28 characters"

run 0 on home herdr-desk run start T4 --root "$SELF" --model sonnet
wait_run 4 running
PANE4=$(run_field 4 pane)
wait_long 15 "T4's row to read running" token_matches "$PANE4" 'T4 running · since [0-9]{2}:[0-9]{2}'
run 0 on home herdr-desk runs kill T4
run_is 4 killed || fail "run 4 is $(run_field 4 state)"
herdr_do pane get "$PANE4" >/dev/null 2>&1 && fail "pane $PANE4 is still open after the kill"
sleep 2
CLOSE_LINE=$(grep -n "pane close $PANE4\$" "$CALLS" | tail -n 1 | cut -d: -f1)
[ -n "$CLOSE_LINE" ] || fail "no pane close of $PANE4 in the herdr calls"
AFTER=$(tail -n +"$((CLOSE_LINE + 1))" "$CALLS" | grep -c "report-metadata $PANE4 " || true)
[ "$AFTER" = 0 ] || fail "herdr was told about pane $PANE4 $AFTER times after its close"
ok "a killed run's pane is closed and nothing is reported to it"
pass
