#!/usr/bin/env bash
# H1: a task added on the home shows on a client in one call.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
PORT=$(free_port)
home_with_listen home "$PORT"

run 0 on home desk add -t "first task" --desk
[ "$OUT" = "T1" ] || fail "add printed '$OUT', want T1"

make_client cli home "$PORT"
run 0 on cli desk list --json
jq -e '.offline == false and (.tasks | length) == 1 and .tasks[0].number == 1
  and .tasks[0].title == "first task" and .tasks[0].status == "open"' <<<"$OUT" >/dev/null ||
  fail "the client's list does not show T1 as added"
say "client sees: $(jq -c '.tasks[0] | {number, title, status}' <<<"$OUT")"

run 0 on cli desk add -t "from the client" --desk
[ "$OUT" = "T2" ] || fail "the client's add printed '$OUT', want T2"
run 0 on home desk list
out_has "from the client"
pass
