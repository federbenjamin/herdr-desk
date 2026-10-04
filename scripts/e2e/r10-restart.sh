#!/usr/bin/env bash
# H10 (runner): a live run survives a daemon restart, a run still routing at a restart is failed, a pause survives
# it too, and the pane's desk talks to the home that started it.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
RC_CAP=2
runner_up home
route_to "$SCRATCH" in-place sonnet "stub"

run 0 on home desk add -t "outlives the daemon" --desk --thread agent
run 0 on home desk set T1 ready
wait_run 1 running
wait_file "$STUB/worker-run1.env"
ENV="$STUB/worker-run1.env"
for pair in "XDG_CONFIG_HOME=config" "XDG_STATE_HOME=state" "XDG_DATA_HOME=data" "XDG_CACHE_HOME=cache"; do
  name=${pair%%=*}
  [ "$(env_value "$ENV" "$name")" = "$E2E/home/${pair#*=}" ] || fail "$name in the pane is '$(env_value "$ENV" "$name")', not $E2E/home/${pair#*=}"
done
say "XDG variables in the pane ok"

# The daemon restarts; the run goes on and is still watched: an idle pane hands the task back.
restart_daemon
run_is 1 running || fail "run 1 is $(run_field 1 state) after the restart"
herdr pane report-agent "$(run_field 1 pane)" --source desk-e2e --agent stub --state idle \
  --agent-session-id "$(run_field 1 session)" >/dev/null
wait_task 1 review 10
run_is 1 ended || fail "run 1 is $(run_field 1 state)"
say "run still watched after restart ok"

# A run that was still routing when the daemon stopped is failed by the next daemon.
printf '30' >"$STUB/router-sleep"
run 0 on home desk add -t "caught routing" --desk --thread agent
run 0 on home desk set T2 ready
wait_run 2 routing
wait_file "$STUB/router-pid"
stop_daemon home
rm "$STUB/router-sleep"
start_daemon home
wait_run 2 failed 10
task_is 2 blocked || fail "T2 is $(task_field 2 status)"
task_has_note 2 "restarted" || fail "no note says the daemon restarted: $(task_notes 2)"
wait_long 35 "the stub router to end" pids_dead "$STUB/router-pid"
say "stale routing run failed ok"

# A pause is a file: it outlives the daemon.
run 0 on home desk runner pause
restart_daemon
run 0 on home desk runner
out_has "runner paused"
[ -f "$E2E/home/state/desk/runner-paused" ] || fail "no runner-paused file in the state folder"
say "pause kept across restart ok"
pass
