#!/usr/bin/env bash
# Shared setup for the e2e scripts. Each script sources this file, builds desk into a temp dir,
# and gives every named machine its own XDG directories there. Nothing outside the temp dir is
# written unless a script says so.
set -euo pipefail

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
E2E=$(mktemp -d "${TMPDIR:-/tmp}/dk.XXXXXX")
E2E=$(cd "$E2E" && pwd)
BIN="$E2E/bin"
# A hermetic script never finds a herdr on PATH: the name is sealed to a path that does not exist.
export DESK_HERDR="$E2E/no-herdr"
PIDS=()
OUT=""
ERR=""

say() { printf '%s\n' "$*"; }
fail() {
  say "E2E FAIL: $*" >&2
  exit 1
}
pass() { say "E2E PASS"; }

# on <machine> <command...>: run a command as a person on that machine.
on() {
  local m=$1
  shift
  env -u DESK_SESSION -u DESK_RUN -u DESK_HOOKS -u CLAUDE_CODE_SESSION_ID \
    XDG_CONFIG_HOME="$E2E/$m/config" XDG_STATE_HOME="$E2E/$m/state" \
    XDG_DATA_HOME="$E2E/$m/data" XDG_CACHE_HOME="$E2E/$m/cache" "$@"
}

# as_agent <machine> <session> <command...>: the same, as an agent session.
as_agent() {
  local m=$1 s=$2
  shift 2
  on "$m" env DESK_SESSION="$s" "$@"
}

cleanup() {
  local d m p
  for d in "$E2E"/*/state/desk; do
    [ -f "$d/daemon.json" ] || continue
    m=$(basename "$(dirname "$(dirname "$d")")")
    on "$m" desk daemon stop >/dev/null 2>&1 || true
  done
  for p in "${PIDS[@]:-}"; do
    if [ -n "$p" ]; then kill "$p" 2>/dev/null || true; fi
  done
  wait 2>/dev/null || true
  rm -rf "$E2E"
}
trap cleanup EXIT

build() {
  mkdir -p "$BIN"
  (cd "$REPO" && CGO_ENABLED=0 go build -o "$BIN/desk" ./cmd/desk) || fail "go build ./cmd/desk"
  export PATH="$BIN:$PATH"
}

# run <want-exit> <command...>: run it, print it with its output and exit code, and fail on any
# other exit. Leaves stdout in OUT and stderr in ERR.
run() {
  local want=$1 rc=0
  shift
  "$@" >"$E2E/.out" 2>"$E2E/.err" || rc=$?
  OUT=$(cat "$E2E/.out")
  ERR=$(cat "$E2E/.err")
  say "\$ $*"
  [ -z "$OUT" ] || say "$OUT"
  [ -z "$ERR" ] || say "stderr: $ERR"
  say "exit=$rc"
  [ "$rc" = "$want" ] || fail "expected exit $want, got $rc: $*"
}

# run_in <want-exit> <stdin-text> <command...>: run with the text on stdin. The text is not printed.
run_in() {
  local want=$1 input=$2 rc=0
  shift 2
  "$@" >"$E2E/.out" 2>"$E2E/.err" <<<"$input" || rc=$?
  OUT=$(cat "$E2E/.out")
  ERR=$(cat "$E2E/.err")
  say "\$ $* <stdin>"
  [ -z "$OUT" ] || say "$OUT"
  [ -z "$ERR" ] || say "stderr: $ERR"
  say "exit=$rc"
  [ "$rc" = "$want" ] || fail "expected exit $want, got $rc: $*"
}

# Checks on the last run.
out_has() { case "$OUT" in *"$1"*) ;; *) fail "stdout lacks: $1" ;; esac }
err_has() { case "$ERR" in *"$1"*) ;; *) fail "stderr lacks: $1" ;; esac }
out_lacks() { case "$OUT" in *"$1"*) fail "stdout holds: $1" ;; *) ;; esac }
any_lacks() { case "$OUT$ERR" in *"$1"*) fail "output holds: $1" ;; *) ;; esac }

# wait_for <what> <command...>: poll until the command succeeds, up to 10 s.
wait_for() {
  local what=$1 i
  shift
  for i in $(seq 1 100); do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 0.1
  done
  fail "timed out waiting for $what (after $i tries)"
}

sock() { printf '%s' "$E2E/$1/state/desk/desk.sock"; }
no_sock() { [ ! -S "$(sock "$1")" ]; }

free_port() {
  python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])'
}

# mode <file>: its permission bits, as 600.
mode() { stat -f '%Lp' "$1" 2>/dev/null || stat -c '%a' "$1"; }

# write_config <machine>: replace that machine's desk config with stdin.
write_config() {
  local dir="$E2E/$1/config/desk"
  mkdir -p "$dir"
  cat >"$dir/config.toml"
  chmod 600 "$dir/config.toml"
}

start_daemon() {
  local m=$1
  on "$m" desk daemon run >>"$E2E/$m.daemon.log" 2>&1 &
  PIDS+=("$!")
  wait_for "the daemon socket on $m" test -S "$(sock "$m")"
}

stop_daemon() {
  local m=$1
  run 0 on "$m" desk daemon stop
  wait_for "the daemon on $m to stop" no_sock "$m"
}

# home_with_listen <machine> <port>: a set-up home serving TCP on 127.0.0.1:<port>, daemon running.
home_with_listen() {
  run 0 on "$1" desk setup --no-herdr --listen "127.0.0.1:$2"
  start_daemon "$1"
}

# make_client <client-machine> <home-machine> <port>: the token goes in on stdin.
make_client() {
  local token
  token=$(on "$2" desk token show)
  run_in 0 "$token" on "$1" desk client add "127.0.0.1:$3"
}
