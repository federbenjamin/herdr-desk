#!/usr/bin/env bash
# H3: a task added on a client shows on the home, and one added on the home shows on the client.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
home_up home

run 0 on home herdr-desk add -t "first task" --desk
[ "$OUT" = "T1" ] || fail "add printed '$OUT', want T1"

make_client cli home
run 0 on cli herdr-desk list --json
jq -e '.offline == false and (.tasks | length) == 1 and .tasks[0].number == 1
  and .tasks[0].title == "first task" and .tasks[0].status == "ready"' <<<"$OUT" >/dev/null ||
  fail "the client's list does not show T1 as added"
say "client sees: $(jq -c '.tasks[0] | {number, title, status}' <<<"$OUT")"

run 0 on cli herdr-desk add -t "from the client" --desk
[ "$OUT" = "T2" ] || fail "the client's add printed '$OUT', want T2"
run 0 on home herdr-desk list
out_has "from the client"
pass
