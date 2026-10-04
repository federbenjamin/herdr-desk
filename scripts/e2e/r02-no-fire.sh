#!/usr/bin/env bash
# H2 (runner): nothing starts a task the user did not arm: thread me, an agent's proposal, a disabled runner, a
# paused runner. A task that must not start is checked after three polls (poll_seconds is 1).
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
route_to "$SCRATCH" in-place sonnet "stub"

no_runs() {
  run 0 on home desk runs --all --json
  [ "$OUT" = "[]" ] || fail "$1: a run exists: $OUT"
}

# The runner is off.
RC_ENABLED=false
runner_up home
run 0 on home desk runner
out_has "runner off"
run 0 on home desk add -t "armed, runner off" --desk --thread agent
run 0 on home desk set T1 ready
sleep 3
no_runs "runner off"
task_is 1 ready || fail "the task moved while the runner was off"
say "runner off never spawns ok"
run 0 on home desk set T1 open

# The runner is on.
RC_ENABLED=true
runner_config home
restart_daemon
run 0 on home desk runner
out_has "runner on"

# T2: thread me. T3: an agent's proposal. T4: ready, on no thread.
run 0 on home desk add -t "mine" --desk --thread me
run 0 on home desk set T2 ready
run 0 as_agent home s-r02 desk add -t "agent proposal" --desk --thread agent
run 0 on home desk add -t "ready without a thread" --desk
run 0 on home desk set T4 ready
run 1 as_agent home s-r02 desk set T3 ready
err_has "not-allowed"
say "agent ready refused ok"
run 1 as_agent home s-r02 desk set T4 --thread agent
err_has "not-allowed"
say "agent thread flip on a ready task refused ok"
run 1 as_agent home s-r02 desk runner pause
err_has "not-allowed"
run 0 on home desk runner
out_has "runner on"
say "agent pause refused ok"

sleep 3
no_runs "thread me, a proposal, a thread flip"
task_is 2 ready || fail "T2 moved"
task_is 3 open || fail "T3 moved"
task_is 4 ready || fail "T4 moved"
say "thread me never spawns ok"
say "agent proposal never spawns ok"

# Paused: a person's task that would start does not, and starts again on resume.
run 0 on home desk runner pause
out_has "runner paused"
run 0 on home desk add -t "armed while paused" --desk --thread agent
run 0 on home desk set T5 ready
sleep 3
no_runs "paused"
task_is 5 ready || fail "T5 moved while the runner was paused"
say "paused never spawns ok"
run 0 on home desk runner resume
wait_run 1 running 10
[ "$(run_field 1 task)" = 5 ] || fail "run 1 is for T$(run_field 1 task), not T5"
say "resume spawns ok"
pass
