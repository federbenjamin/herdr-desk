#!/usr/bin/env bash
# H1 (steps): `steps add --id` twice makes one step, an id shaped like a generated one is refused, `done` prints changed
# then unchanged, a generated id skips no number after a caller's id, and a stale run's step write is stale-run.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
runner_up home

step_count() { on home herdr-desk show T1 --json | jq '.task.steps | length'; }
step_ids() { on home herdr-desk show T1 --json | jq -r '[.task.steps[].short_id] | join(" ")'; }

run 0 on home herdr-desk add -t "Ship the unit" --desk
run 0 on home herdr-desk steps T1 add --id u1 "unit one"
out_has "u1 [ ] unit one"
run 0 on home herdr-desk steps T1 add --id u1 "unit one again"
[ "$(step_count)" = 1 ] || fail "T1 has $(step_count) steps after two adds with one id"
out_lacks "unit one again"
ok "add --id twice: one step"

run 2 on home herdr-desk steps T1 add --id s3 "looks generated"
err_has "bad-input"
[ "$(step_count)" = 1 ] || fail "a refused add made a step"
ok "--id s3: bad-input, exit 2"

run 0 on home herdr-desk steps T1 "done" u1
[ "$OUT" = changed ] || fail "the first done printed '$OUT', want changed"
run 0 on home herdr-desk steps T1 "done" u1
[ "$OUT" = unchanged ] || fail "the second done printed '$OUT', want unchanged"
ok "done: changed then unchanged"

run 0 on home herdr-desk steps T1 add "a generated one"
[ "$(step_ids)" = "u1 s1" ] || fail "step ids are '$(step_ids)', want 'u1 s1'"
ok "generated ids skip no number"

# Run 1's worker ends its run; a person starts run 2. Run 1 may no longer write T1's steps.
run 0 on home herdr-desk run start T1
wait_run 1 running
SESSION1=$(run_field 1 session)
run 0 as_agent home "$SESSION1" env DESK_RUN=1 herdr-desk set T1 blocked
run 0 on home herdr-desk run start T1
wait_run 2 running
run 1 as_agent home "$SESSION1" env DESK_RUN=1 herdr-desk steps T1 add --id u2 "from a stale run"
err_has "stale-run"
[ "$(step_count)" = 2 ] || fail "a stale run's add made a step"
ok "stale run: stale-run"
pass
