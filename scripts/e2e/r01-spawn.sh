#!/usr/bin/env bash
# H1 (runner): an armed task is running in a pane within two polls, with the task, session, and run in the pane's
# environment. poll_seconds is 2, so "within two polls" is 2 x 2 s plus 1 s of slack.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
RC_POLL=2
runner_up home
route_to "$SCRATCH" in-place sonnet "stub router: nothing to isolate"

run 0 on home herdr-desk add -t "spawn me" --desk --thread agent
run 0 on home herdr-desk set T1 ready
wait_long 5 "run 1 to be running within 2 polls" run_is 1 running
say "spawned within 2 polls ok"

wait_file "$STUB/worker-run1.env"
ENV="$STUB/worker-run1.env"
[ "$(env_value "$ENV" DESK_TASK)" = T1 ] || fail "DESK_TASK is '$(env_value "$ENV" DESK_TASK)'"
say "DESK_TASK=T1 ok"
SESSION=$(env_value "$ENV" DESK_SESSION)
case "$SESSION" in
[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]-[0-9a-f][0-9a-f][0-9a-f][0-9a-f]-[0-9a-f][0-9a-f][0-9a-f][0-9a-f]-[0-9a-f][0-9a-f][0-9a-f][0-9a-f]-[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]) ;;
*) fail "DESK_SESSION is '$SESSION', not a uuid" ;;
esac
[ "$(run_field 1 session)" = "$SESSION" ] || fail "the run row's session is not the pane's DESK_SESSION"
say "DESK_SESSION is a uuid ok"
[ "$(env_value "$ENV" DESK_RUN)" = 1 ] || fail "DESK_RUN is '$(env_value "$ENV" DESK_RUN)'"
say "DESK_RUN=1 ok"

PHYSICAL=$(cd "$BIN" && pwd -P)
grep -Fx -e "exec $BIN/herdr-desk worker" -e "exec $PHYSICAL/herdr-desk worker" "$E2E/herdr/pane-commands.log" >/dev/null ||
  fail "the pane was not given 'exec <herdr-desk> worker': $(cat "$E2E/herdr/pane-commands.log")"
say "pane command is exec <herdr-desk> worker ok"

run 0 on home herdr-desk runs
out_has "run 1  T1  running  $SCRATCH  in-place  sonnet"
say "runs shows T1 running ok"

PANE=$(run_field 1 pane)
WS=$(run_field 1 workspace)
if [ -z "$PANE" ] || [ -z "$WS" ]; then fail "the run row has no workspace or pane"; fi
task_has_note 1 "run 1: workspace $WS, pane $PANE" || fail "no note 'run 1: workspace $WS, pane $PANE'"
say "note records workspace and pane ok"

grep -F "herdr-desk: T1 started" "$E2E/herdr/notifications.log" | grep -F "spawn me" >/dev/null ||
  fail "the notify command did not run: $(cat "$E2E/herdr/notifications.log" 2>&1)"
say "notify ran ok"
pass
