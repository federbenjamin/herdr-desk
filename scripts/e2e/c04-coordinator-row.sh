#!/usr/bin/env bash
# H47: the coordinator's row shows the count of blocked and review tasks with the runs live and waiting, and `idle` when
# nothing needs a person and nothing is live. Runs on the fake herdr, which keeps each pane's tokens.
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
RC_CAP=3
runner_up home

run 0 on home herdr-desk coordinator
COORD=$(sqlite3 "$DB" "SELECT pane FROM coordinator")
[ -n "$COORD" ] || fail "the store records no coordinator pane"

# row: the desk token herdr holds for the coordinator's pane.
row() { herdr_do pane get "$COORD" 2>/dev/null | jq -r '.result.pane.tokens.desk // ""'; }
row_is() { [ "$(row)" = "$1" ]; }
wait_row() { wait_long 15 "the coordinator's row to read '$1' (it reads '$(row)')" row_is "$1"; }

# T1 keeps working, T2 hands back for review, T3 blocks.
for n in 1 2 3 4; do run 0 on home herdr-desk add -t "task $n" --desk; done
set_mode 2 handback
set_mode 3 blocked
for n in 1 2 3; do run 0 on home herdr-desk run start "T$n" --root "$SELF" --model sonnet; done
wait_task 2 review
wait_task 3 blocked
wait_row "2 need you · 1 running"
ok "2 need you · 1 running"

# Nothing needs a person now, and a cap of 1 makes the next run wait behind T1.
run 0 on home herdr-desk set T2 'done'
run 0 on home herdr-desk set T3 'done'
RC_CAP=1
runner_config home
run 0 on home herdr-desk run start T4 --root "$SELF" --model sonnet
out_has "waiting"
wait_row "1 running · 1 waiting"
ok "1 running · 1 waiting"

# T1 is done; T4 takes its slot, then T4 is done too.
run 0 on home herdr-desk set T1 'done'
wait_run 4 running
run 0 on home herdr-desk set T4 'done'
wait_row "idle"
ok "idle"
pass
