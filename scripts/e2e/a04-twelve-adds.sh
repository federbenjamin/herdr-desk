#!/usr/bin/env bash
# H7: twelve adds at once from separate processes, six on the home and six through a client, give twelve task
# numbers and no busy error, with a board refreshing and the ticker running.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
home_up home
make_client cli home
ticker_up home
term_start board 100 30 home "$BIN/herdr-desk"
term_wait board "+ add"

adders=()
for i in 1 2 3 4 5 6; do
  on home herdr-desk add -t "from the home $i" --desk >"$E2E/add.home$i.out" 2>"$E2E/add.home$i.err" &
  adders+=("$!")
  on cli herdr-desk add -t "from the client $i" --desk >"$E2E/add.cli$i.out" 2>"$E2E/add.cli$i.err" &
  adders+=("$!")
done
failed=0
for p in "${adders[@]}"; do
  wait "$p" || failed=$((failed + 1))
done
[ "$failed" = 0 ] || fail "$failed of 12 adds did not exit 0: $(cat "$E2E"/add.*.err)"
ok "12 adds exited 0"

got=$(cat "$E2E"/add.*.out | sort -V | tr '\n' ' ')
want=$(printf 'T%s ' 1 2 3 4 5 6 7 8 9 10 11 12)
[ "$got" = "$want" ] || fail "the tasks are '$got', want '$want'"
ok "the tasks are T1 to T12, each once"

if cat "$E2E"/add.*.err | grep -Eiq 'busy|locked'; then fail "a stderr holds busy or locked"; fi
ok "no stderr holds busy or locked"

term_alive board || fail "the board ended"
pass
