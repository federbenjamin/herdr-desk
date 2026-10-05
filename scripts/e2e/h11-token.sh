#!/usr/bin/env bash
# H11: the TCP listener refuses a missing, wrong, or rotated-out token and never serves the
# backup method; a wildcard listen address is refused. A client whose token the home refuses gets
# the code bad-token, and its notes are queued, not lost.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
PORT=$(free_port)
home_with_listen home "$PORT"
TOKEN=$(on home herdr-desk token show)
[ "${#TOKEN}" = 64 ] || fail "the token is ${#TOKEN} characters, want 64"
[ "$(mode "$E2E/home/config/herdr-desk/token")" = 600 ] || fail "the token file's mode is $(mode "$E2E/home/config/herdr-desk/token")"

# code <method> [token]: the HTTP status of one call.
code() {
  if [ -n "${2:-}" ]; then
    curl -s -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:$PORT/v1/$1" \
      -H 'Content-Type: application/json' -H "Authorization: Bearer $2" -d '{}'
  else
    curl -s -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:$PORT/v1/$1" \
      -H 'Content-Type: application/json' -d '{}'
  fi
}
# expect <label> <want> <got>
expect() {
  [ "$2" = "$3" ] || fail "$1: got HTTP $3, want $2"
  say "$1"
}

expect "no token 401" 401 "$(code status)"
expect "wrong token 401" 401 "$(code status "$(printf '%064d' 0)")"
expect "good token 200" 200 "$(code status "$TOKEN")"
expect "no token 401 on a write" 401 "$(code tasks.add)"

got=$(code backup.run "$TOKEN")
if [ "$got" = 200 ] || [ "$got" = 409 ]; then
  fail "backup.run answered $got over TCP"
fi
say "backup.run not served on tcp (HTTP $got)"

make_client cli home "$PORT"
NEW=$(on home herdr-desk token rotate)
[ "$NEW" != "$TOKEN" ] || fail "rotate returned the same token"
expect "old token 401 after rotate" 401 "$(code status "$TOKEN")"
expect "new token 200 after rotate" 200 "$(code status "$NEW")"

run 3 on cli herdr-desk list
err_has "bad-token"
run 0 as_agent cli s-h11 herdr-desk note "written while the token is refused"
[ "$OUT" = queued ] || fail "a note with a refused token printed '$OUT', want queued"
err_has "refused the token"
run_in 0 "$NEW" on cli herdr-desk client add "127.0.0.1:$PORT"
run 0 on cli herdr-desk session s-h11
out_has "written while the token is refused"
say "refused token queued ok"

head -c 1200000 /dev/zero | tr '\0' 'a' >"$E2E/big.txt"
big=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:$PORT/v1/tasks.add" \
  -H 'Content-Type: application/json' -H "Authorization: Bearer $NEW" --data-binary "@$E2E/big.txt")
expect "oversized body 413" 413 "$big"

run 2 on wild herdr-desk setup --no-herdr --listen "0.0.0.0:$PORT"
[ ! -e "$E2E/wild/config/herdr-desk/config.toml" ] || fail "a refused setup wrote a config"
say "wildcard listen refused"
pass
