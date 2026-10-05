#!/usr/bin/env bash
# H19: `run start` refuses what it must (runner off, runner paused, no herdr, a done or archived task, a root outside the
# roots, the day's cap) and answers a task that has a live run with that run, exit 0. A config edit applies to the next
# command, so each case rewrites the config and runs.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
RC_CAP=4
runner_up home
for n in 1 2 3 4 5; do run 0 on home herdr-desk add -t "task $n" --desk; done

no_runs() { [ -z "$(sqlite3 "$DB" "SELECT id FROM runs" 2>/dev/null)" ]; }

RC_ENABLED=false
runner_config home
run 1 on home herdr-desk run start T1
err_has "runner-off"
no_runs || fail "a refused run start made a run"
RC_ENABLED=true
runner_config home
ok "runner-off"

run 0 on home herdr-desk runner pause
run 1 on home herdr-desk run start T1
err_has "runner-paused"
no_runs || fail "a refused run start made a run"
run 0 on home herdr-desk runner resume
ok "runner-paused"

run 1 on home env DESK_HERDR="$E2E/no-herdr" herdr-desk run start T1
err_has "no-herdr"
no_runs || fail "a refused run start made a run"
ok "no-herdr"

run 0 on home herdr-desk run start T1 --isolation self
out_has "run 1  T1  running"
run 0 on home herdr-desk run start T1
out_has "run 1  T1  running"
[ "$(sqlite3 "$DB" "SELECT count(*) FROM runs")" = 1 ] || fail "a second run start made a second run"
ok "a task with a running run: exit 0 and the run's line"

run 0 on home herdr-desk set T2 "done"
run 1 on home herdr-desk run start T2
err_has "not-allowed"
err_has "done"
ok "a done task is refused"

run 0 on home herdr-desk set T3 --archive
run 1 on home herdr-desk run start T3
err_has "not-allowed"
err_has "archived"
ok "an archived task is refused"

run 2 on home herdr-desk run start T4 --root "$E2E/not-a-root"
err_has "bad-input"
[ "$(sqlite3 "$DB" "SELECT count(*) FROM runs")" = 1 ] || fail "a bad root made a run"
ok "--root outside the roots is bad-input, exit 2"

# One run has started today: a day's cap of one stops the next.
RC_DAY=1
runner_config home
run 1 on home herdr-desk run start T4
err_has "cap-reached"
[ "$(sqlite3 "$DB" "SELECT count(*) FROM runs")" = 1 ] || fail "the cap did not hold"
ok "cap-reached at max_runs_per_day"
pass
