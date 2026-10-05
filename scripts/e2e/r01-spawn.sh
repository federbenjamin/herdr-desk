#!/usr/bin/env bash
# H23 (runner): `herdr-desk run start` opens a pane with the task, session, and run in its environment, records the
# pane on the run row, and notifies once.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
runner_up home

start_task home "spawn me" --desk
out_has "run 1  T1  running  $SCRATCH  in-place  sonnet"
say "run start printed the running run ok"

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
for pair in "XDG_CONFIG_HOME=config" "XDG_STATE_HOME=state" "XDG_DATA_HOME=data" "XDG_CACHE_HOME=cache"; do
  name=${pair%%=*}
  [ "$(env_value "$ENV" "$name")" = "$E2E/home/${pair#*=}" ] || fail "$name in the pane is '$(env_value "$ENV" "$name")', not $E2E/home/${pair#*=}"
done
say "XDG variables in the pane ok"

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
task_has_note 1 "run 1 starting: $SCRATCH (in-place, sonnet)" || fail "no runner note for the start: $(task_notes 1)"
say "notes record the start, workspace and pane ok"

[ "$(grep -c "herdr-desk: T1 started" "$E2E/herdr/notifications.log")" = 1 ] || fail "the notify command did not run exactly once: $(cat "$E2E/herdr/notifications.log" 2>&1)"
grep -F "herdr-desk: T1 started" "$E2E/herdr/notifications.log" | grep -F "spawn me" >/dev/null || fail "the notification lacks the title"
say "notify ran once ok"
pass
