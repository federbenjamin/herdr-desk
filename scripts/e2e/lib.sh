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
TMUX_SOCK="desk-e2e-$$"
OUT=""
ERR=""

say() { printf '%s\n' "$*"; }
fail() {
  say "E2E FAIL: $*" >&2
  exit 1
}
pass() { say "E2E PASS"; }
ok() { say "ok: $*"; }

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
  tmux -L "$TMUX_SOCK" kill-server >/dev/null 2>&1 || true
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

# wait_long <seconds> <what> <command...>: poll until the command succeeds, every 0.1 s, for up to that long. The
# one poll loop of the e2e scripts: every other wait is built on it.
wait_long() {
  local secs=$1 what=$2 end
  shift 2
  end=$((SECONDS + secs + 1))
  while [ "$SECONDS" -lt "$end" ]; do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 0.1
  done
  fail "timed out after ${secs}s waiting for $what"
}

# wait_for <what> <command...>: poll until the command succeeds, up to 10 s.
wait_for() { wait_long 10 "$@"; }

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

# The terminal helpers. Every screen runs on a private tmux server (TMUX_SOCK) that cleanup kills, so nothing
# opens on anyone's display. A finished program stays on the server as a dead pane, so its exit code can be read.

tm() { tmux -L "$TMUX_SOCK" "$@"; }

# term_start <name> <cols> <rows> <machine> <command...>: run the command as a person on that machine, in a
# detached terminal of that size.
term_start() {
  local name=$1 cols=$2 rows=$3 m=$4
  shift 4
  if ! tm has-session -t keeper 2>/dev/null; then
    tm new-session -d -s keeper -x 80 -y 24 -- sleep 3600 || fail "tmux cannot start a server"
    tm set-option -g remain-on-exit on
  fi
  tm new-session -d -s "$name" -x "$cols" -y "$rows" -- env -u DESK_SESSION -u DESK_RUN -u DESK_HOOKS \
    -u CLAUDE_CODE_SESSION_ID XDG_CONFIG_HOME="$E2E/$m/config" XDG_STATE_HOME="$E2E/$m/state" \
    XDG_DATA_HOME="$E2E/$m/data" XDG_CACHE_HOME="$E2E/$m/cache" "$@" || fail "tmux cannot start $name"
}

# term_screen <name>: the text on the screen now.
term_screen() { tm capture-pane -p -t "$1"; }

# term_has <name> <text>: the screen holds the text.
term_has() { term_screen "$1" | grep -qF -- "$2"; }

# term_wait <name> <text>: poll until the screen holds the text, up to 10 s; on a timeout print the screen.
term_wait() {
  local name=$1 text=$2
  for _ in $(seq 1 100); do
    if term_has "$name" "$text"; then return 0; fi
    sleep 0.1
  done
  say "--- screen of $name ---" >&2
  term_screen "$name" >&2
  fail "timed out waiting for '$text' on the screen of $name"
}

# term_wait_gone <name> <text>: poll until the screen no longer holds the text.
term_wait_gone() {
  local name=$1 text=$2
  for _ in $(seq 1 100); do
    if ! term_has "$name" "$text"; then return 0; fi
    sleep 0.1
  done
  say "--- screen of $name ---" >&2
  term_screen "$name" >&2
  fail "timed out waiting for '$text' to leave the screen of $name"
}

# term_keys <name> <key...>: send tmux key names (Enter, Escape, C-d, Down, a, ...).
term_keys() {
  local name=$1
  shift
  tm send-keys -t "$name" "$@"
}

# term_esc <name>: send Escape and let it settle; a key that follows at once would arrive as Alt+key.
term_esc() {
  tm send-keys -t "$1" Escape
  sleep 0.3
}

# term_type <name> <text>: send the text as typed characters.
term_type() { tm send-keys -t "$1" -l -- "$2"; }

# term_alive <name>: the program is still running.
term_alive() { [ "$(tm display-message -p -t "$1" '#{pane_dead}')" = 0 ]; }

# term_wait_exit <name>: poll until the program ends, up to 10 s, and print its exit code.
term_wait_exit() {
  local name=$1
  for _ in $(seq 1 100); do
    if ! term_alive "$name"; then
      tm display-message -p -t "$name" '#{pane_dead_status}'
      return 0
    fi
    sleep 0.1
  done
  say "--- screen of $name ---" >&2
  term_screen "$name" >&2
  fail "timed out waiting for $name to end"
}

# term_widest <name>: the width of the widest line on the screen.
term_widest() { term_screen "$1" | python3 -c 'import sys; print(max([len(l.rstrip("\n")) for l in sys.stdin] or [0]))'; }

# board_pick <name> <task-number>: filter the board to one task with / so that it is the selected row.
board_pick() {
  term_keys "$1" /
  term_wait "$1" "search:"
  term_type "$1" "T$2"
  term_keys "$1" Enter
  term_wait_gone "$1" "search:"
}

# board_unpick <name>: clear the filter.
board_unpick() { term_esc "$1"; }

# task_field <machine> <task> <jq-filter>: a field of a task, from desk show --json.
task_field() { on "$1" desk show "$2" --json | jq -r "$3"; }

# wait_task <machine> <task> <jq-filter> <want>: poll until the field equals the value, up to 10 s.
wait_task() {
  local got
  for _ in $(seq 1 100); do
    got=$(task_field "$1" "$2" "$3" 2>/dev/null || true)
    if [ "$got" = "$4" ]; then return 0; fi
    sleep 0.1
  done
  fail "T$2 $3 is '$got', want '$4'"
}
