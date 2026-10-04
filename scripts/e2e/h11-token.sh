#!/usr/bin/env bash
# H11: the TCP listener refuses a missing, wrong, or rotated-out token and never serves the
# backup method; a wildcard listen address is refused.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
PORT=$(free_port)
home_with_listen home "$PORT"
TOKEN=$(on home desk token show)
[ "${#TOKEN}" = 64 ] || fail "the token is ${#TOKEN} characters, want 64"
[ "$(mode "$E2E/home/config/desk/token")" = 600 ] || fail "the token file's mode is $(mode "$E2E/home/config/desk/token")"

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
[ "$got" != 200 ] && [ "$got" != 409 ] || fail "backup.run answered $got over TCP"
say "backup.run not served on tcp (HTTP $got)"

NEW=$(on home desk token rotate)
[ "$NEW" != "$TOKEN" ] || fail "rotate returned the same token"
expect "old token 401 after rotate" 401 "$(code status "$TOKEN")"
expect "new token 200 after rotate" 200 "$(code status "$NEW")"

head -c 1200000 /dev/zero | tr '\0' 'a' >"$E2E/big.txt"
big=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:$PORT/v1/tasks.add" \
  -H 'Content-Type: application/json' -H "Authorization: Bearer $NEW" --data-binary "@$E2E/big.txt")
expect "oversized body 413" 413 "$big"

run 2 on wild desk setup --no-herdr --listen "0.0.0.0:$PORT"
[ ! -e "$E2E/wild/config/desk/config.toml" ] || fail "a refused setup wrote a config"
say "wildcard listen refused"
pass
