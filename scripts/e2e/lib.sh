#!/usr/bin/env bash
# Shared setup for the e2e scripts. Each script sources this file, builds herdr-desk into a temp dir,
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
# The caller's own XDG folders as the script began, NAME=value for each one set: a real claude runs on these, never
# on a machine's temp folders (claude_wrapper).
CALLER_XDG=()
for v in XDG_CONFIG_HOME XDG_STATE_HOME XDG_DATA_HOME XDG_CACHE_HOME; do
  if [ -n "${!v+x}" ]; then CALLER_XDG+=("$v=${!v}"); fi
done
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

# machine_env <machine>: set MACHINE_ENV to the env argv a person's command on that machine runs under: no agent
# session or run, and the machine's four folders. on, term_start, and the event hook's command build from it.
machine_env() {
  local m=$1
  MACHINE_ENV=(env -u DESK_SESSION -u DESK_RUN -u DESK_HOOKS -u CLAUDE_CODE_SESSION_ID
    "XDG_CONFIG_HOME=$E2E/$m/config" "XDG_STATE_HOME=$E2E/$m/state"
    "XDG_DATA_HOME=$E2E/$m/data" "XDG_CACHE_HOME=$E2E/$m/cache")
}

# popup_manifest <dst>: write to <dst> the repo's herdr-plugin.toml as the temp plugin desk-e2e: no build, startup,
# or events block, and its board and capture commands run this build's binary as a person on the home.
popup_manifest() {
  local dst=$1
  machine_env home
  python3 - "$REPO/herdr-plugin.toml" "$dst" "$BIN/herdr-desk" "${MACHINE_ENV[@]}" <<'PY' || fail "cannot write the temp manifest"
import json
import re
import sys

src, dst, desk = sys.argv[1:4]
env = sys.argv[4:]
text = open(src).read()
parts = re.split(r"(?m)^(?=\[\[)", text)
text = "".join(p for p in parts if not p.startswith(("[[build]]", "[[startup]]", "[[events]]")))
text = text.replace('id = "herdr-desk"\n', 'id = "desk-e2e"\n', 1)
text = text.replace('command = ["herdr-desk", "capture"]', "command = " + json.dumps(env + [desk, "capture"]))
text = text.replace('command = ["herdr-desk"]', "command = " + json.dumps(env + [desk]))
open(dst, "w").write(text)
PY
  grep -q 'id = "desk-e2e"' "$dst" || fail "the temp manifest has no id desk-e2e"
  if grep -Eq '^\[\[(build|startup|events)\]\]' "$dst"; then fail "the temp manifest still has a build, startup, or events block"; fi
}

# on <machine> <command...>: run a command as a person on that machine.
on() {
  machine_env "$1"
  shift
  "${MACHINE_ENV[@]}" "$@"
}

# as_agent <machine> <session> <command...>: the same, as an agent session.
as_agent() {
  local m=$1 s=$2
  shift 2
  on "$m" env DESK_SESSION="$s" "$@"
}

cleanup() {
  local d m p
  for d in "$E2E"/*/state/herdr-desk; do
    [ -f "$d/ticker.json" ] || continue
    m=$(basename "$(dirname "$(dirname "$d")")")
    on "$m" herdr-desk ticker stop >/dev/null 2>&1 || true
  done
  for p in "${PIDS[@]:-}"; do
    if [ -n "$p" ]; then kill "$p" 2>/dev/null || true; fi
  done
  tmux -L "$TMUX_SOCK" kill-server >/dev/null 2>&1 || true
  wait 2>/dev/null || true
  local moved=""
  if [ -n "$CLAUDE_SEEN" ]; then
    moved=$(claude_resolves 2>&1) && [ "$moved" = "$CLAUDE_SEEN" ] && moved=""
  fi
  rm -rf "$E2E"
  if [ -n "$moved" ]; then
    say "E2E FAIL: (env) the user's claude moved during the test: it was $CLAUDE_SEEN; it is now $moved" >&2
    exit 1
  fi
}
trap cleanup EXIT

# The real claude. A script that starts one calls claude_wrapper (or wrap_claude) first, which runs claude_guard.

# CLAUDE_REAL is the user's claude, as PATH finds it when claude_guard runs; CLAUDE_SEEN is where it and
# ~/.local/bin/claude resolve then. cleanup checks them again and fails (env) when either moved.
CLAUDE_REAL=""
CLAUDE_SEEN=""
CLAUDE_CALLS="$E2E/claude-calls.txt"
AGENT_BIN="$E2E/agent-bin"

# claude_resolves: "<path> -> <target>" for CLAUDE_REAL and, when it exists, ~/.local/bin/claude; fails when a target
# is not a file.
claude_resolves() {
  python3 - "$CLAUDE_REAL" "$HOME/.local/bin/claude" <<'PY'
import os, sys

real, local = sys.argv[1:3]
paths = [real] + ([local] if os.path.lexists(local) and local != real else [])
ok = True
for p in paths:
    target = os.path.realpath(p)
    print("%s -> %s" % (p, target))
    ok = ok and os.path.isfile(target)
sys.exit(0 if ok else 1)
PY
}

# claude_guard: record where the user's claude resolves; fail (env) when there is none or it does not resolve.
claude_guard() {
  [ -z "$CLAUDE_REAL" ] || return 0
  CLAUDE_REAL=$(command -v claude) || fail "(env) no claude"
  CLAUDE_SEEN=$(claude_resolves) || {
    local seen=$CLAUDE_SEEN
    CLAUDE_SEEN=""
    fail "(env) the user's claude does not resolve: $seen"
  }
}

# claude_wrapper <key>: write CLAUDE_WRAP, a program that runs the user's claude for that key. It logs the key to
# $CLAUDE_CALLS (claude_calls counts them), gives claude the caller's own XDG folders (unset when the caller had none)
# in place of a machine's temp ones, drops every CLAUDE_CODE_* variable and CLAUDECODE it inherited, turns claude's
# updater off, and puts AGENT_BIN first on PATH, so the `herdr-desk` claude and its hooks run is this build on the
# home's temp folders. Its values are written into it: a pane under the real herdr does not inherit this script's
# environment.
claude_wrapper() {
  local key=$1 kv
  claude_guard
  CLAUDE_WRAP="$E2E/claude-$key"
  mkdir -p "$AGENT_BIN"
  {
    printf '#!/bin/bash\n'
    printf 'XDG_CONFIG_HOME=%q XDG_STATE_HOME=%q XDG_DATA_HOME=%q XDG_CACHE_HOME=%q exec %q "$@"\n' \
      "$E2E/home/config" "$E2E/home/state" "$E2E/home/data" "$E2E/home/cache" "$BIN/herdr-desk"
  } >"$AGENT_BIN/herdr-desk"
  {
    printf '#!/bin/bash\n'
    printf '%s %q >>%q\n' "printf '%s\n'" "$key" "$CLAUDE_CALLS"
    printf 'unset XDG_CONFIG_HOME XDG_STATE_HOME XDG_DATA_HOME XDG_CACHE_HOME\n'
    for kv in "${CALLER_XDG[@]}"; do printf 'export %s=%q\n' "${kv%%=*}" "${kv#*=}"; done
    # shellcheck disable=SC2016 # the $ is the wrapper's, expanded when it runs
    printf 'for v in $(compgen -e); do case $v in CLAUDE_CODE_* | CLAUDECODE) unset "$v" ;; esac; done\n'
    printf 'export DISABLE_AUTOUPDATER=1\n'
    # shellcheck disable=SC2016 # the $PATH is the wrapper's
    printf 'export PATH=%q:"$PATH"\n' "$AGENT_BIN"
    printf 'exec %q "$@"\n' "$CLAUDE_REAL"
  } >"$CLAUDE_WRAP"
  chmod +x "$AGENT_BIN/herdr-desk" "$CLAUDE_WRAP"
}

# wrap_claude <key>: claude_wrapper <key>, and in the home's config make CLAUDE_WRAP the first word of the [agent]
# <key> template (worker or coordinator), which the claude-code profile writes as claude.
wrap_claude() {
  local key=$1
  claude_wrapper "$key"
  python3 - "$E2E/home/config/herdr-desk/config.toml" "$CLAUDE_WRAP" "$key" <<'PY' || fail "the profile's $key template does not start with claude"
import re, sys

path, wrap, key = sys.argv[1:4]
text = open(path).read()
text, n = re.subn(r"(?m)^(%s\s*=\s*\[\s*)(['\"])claude\2" % key, lambda m: m.group(1) + '"%s"' % wrap, text)
if n != 1:
    sys.exit("no %s template starting with claude" % key)
open(path, "w").write(text)
PY
}

# unset_claude_markers: unset every CLAUDE_CODE_* variable and CLAUDECODE this script inherited from the session that
# runs it, so nothing it starts reads as part of that session.
unset_claude_markers() {
  local v
  for v in $(compgen -e); do
    case $v in CLAUDE_CODE_* | CLAUDECODE) unset "$v" ;; esac
  done
}

# claude_calls <key>: how many times the wrapper for that key ran.
claude_calls() { grep -cx "$1" "$CLAUDE_CALLS" 2>/dev/null || true; }

build() {
  mkdir -p "$BIN"
  (cd "$REPO" && CGO_ENABLED=0 go build -o "$BIN/herdr-desk" ./cmd/herdr-desk) || fail "go build ./cmd/herdr-desk"
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

# mode <file>: its permission bits, as 600.
mode() { stat -f '%Lp' "$1" 2>/dev/null || stat -c '%a' "$1"; }

# write_config <machine>: replace that machine's herdr-desk config with stdin.
write_config() {
  local dir="$E2E/$1/config/herdr-desk"
  mkdir -p "$dir"
  cat >"$dir/config.toml"
  chmod 600 "$dir/config.toml"
}

# home_up <machine>: set that machine up as a home. Nothing runs: every command opens the store.
home_up() { run 0 on "$1" herdr-desk setup --no-herdr; }

# make_client <client-machine> <home-machine>: write a client config whose [client] command is the ssh shim, so a
# request reaches the home machine's own folders with no sshd.
make_client() {
  write_config "$1" <<TOML
[client]
home = "$2"
command = ["$REPO/scripts/e2e/ssh-shim.sh", "$E2E", "{home}", "herdr-desk", "rpc"]
TOML
}

# home_down <machine> [seconds]: from now on the shim holds each request for that many seconds (0 when omitted),
# then fails it with ssh's exit 255.
home_down() { printf '%s' "${2:-}" >"$E2E/$1.down"; }

# home_back <machine>: the shim answers again.
home_back() { rm -f "$E2E/$1.down"; }

ticker_running() { on "$1" herdr-desk ticker status | jq -e '.ticker.running' >/dev/null; }
ticker_stopped() { ! ticker_running "$1"; }

# ticker_up <machine>: start that home's ticker in the background and wait until it holds its lock.
ticker_up() {
  local m=$1
  on "$m" herdr-desk ticker >>"$E2E/$m.ticker.log" 2>&1 &
  PIDS+=("$!")
  wait_for "the ticker on $m" ticker_running "$m"
}

# ticker_down <machine>: stop that home's ticker and wait until it lets go.
ticker_down() {
  run 0 on "$1" herdr-desk ticker stop
  wait_for "the ticker on $1 to stop" ticker_stopped "$1"
}

# The real pair (p01, p02). This machine is the home. E2E_CLIENT is the ssh target of the client machine from here;
# E2E_HOME is the target the client uses to reach this machine. Both machines get temp XDG folders: the home's under
# $E2E, the client's under RDIR, which pair_cleanup removes.

rssh() { ssh -o BatchMode=yes -o ConnectTimeout=5 "$E2E_CLIENT" "$@"; }

# pair_up: build for both machines, make this one a home, and put the client's binary, a timing helper, and a
# client config pointing at the home on the client machine.
pair_up() {
  { [ -n "${E2E_CLIENT:-}" ] && [ -n "${E2E_HOME:-}" ]; } || fail "(env) set E2E_CLIENT and E2E_HOME"
  build
  local info goos goarch
  info=$(rssh uname -sm) || fail "(env) cannot ssh to the client machine"
  case "${info% *}" in
    Darwin) goos=darwin ;;
    Linux) goos=linux ;;
    *) fail "(env) the client machine is '$info'" ;;
  esac
  case "${info#* }" in
    arm64 | aarch64) goarch=arm64 ;;
    x86_64) goarch=amd64 ;;
    *) fail "(env) the client machine is '$info'" ;;
  esac
  (cd "$REPO" && GOOS=$goos GOARCH=$goarch CGO_ENABLED=0 go build -o "$E2E/client-herdr-desk" ./cmd/herdr-desk) ||
    fail "go build for $goos/$goarch"
  home_up home
  trap 'pair_cleanup; cleanup' EXIT
  RDIR=$(rssh mktemp -d /tmp/dk.XXXXXX) || fail "(env) cannot make a temp folder on the client machine"
  rssh "mkdir -m 700 -p $RDIR/config/herdr-desk" || fail "cannot make the client's config folder"
  scp -q -o BatchMode=yes "$E2E/client-herdr-desk" "$E2E_CLIENT:$RDIR/herdr-desk" || fail "cannot copy the binary to the client machine"
  rssh "chmod 755 $RDIR/herdr-desk && cat >$RDIR/timed.py" <<'PY'
import subprocess, sys, time

# timed.py <file> <command...>: run the command with this process's stdin and stdout, write its seconds to <file>.
start = time.perf_counter()
rc = subprocess.run(sys.argv[2:]).returncode
open(sys.argv[1], "w").write("%.3f\n" % (time.perf_counter() - start))
sys.exit(rc)
PY
  CN_ENV="env XDG_CONFIG_HOME=$RDIR/config XDG_STATE_HOME=$RDIR/state XDG_DATA_HOME=$RDIR/data XDG_CACHE_HOME=$RDIR/cache"
  client_config "$E2E_HOME"
}

# PAIR_SSH is the ssh of the client's default [client] command, up to the home's target.
PAIR_SSH=(ssh -o BatchMode=yes -o ConnectTimeout=5 -o ControlMaster=auto -o "ControlPath={control}" -o ControlPersist=60)

# client_config <home-target>: write the client's config; its [client] command is the default ssh one, run to reach
# this machine's home folders with this build's binary.
client_config() {
  local words w
  words=$(for w in "${PAIR_SSH[@]}" "{home}" env "XDG_CONFIG_HOME=$E2E/home/config" "XDG_STATE_HOME=$E2E/home/state" \
    "XDG_DATA_HOME=$E2E/home/data" "XDG_CACHE_HOME=$E2E/home/cache" "$BIN/herdr-desk" rpc; do printf '"%s", ' "$w"; done)
  rssh "cat >$RDIR/config/herdr-desk/config.toml && chmod 600 $RDIR/config/herdr-desk/config.toml" <<TOML
[client]
home = "$1"
command = [${words%, }]
TOML
}

# floor_command: the client's [client] command with `true` in place of the remote herdr-desk, quoted for the client's
# shell: the cost of the pair's reused connection alone.
floor_command() {
  local w
  for w in "${PAIR_SSH[@]}" "$E2E_HOME" true; do
    printf '%q ' "${w//\{control\}/$RDIR/state/herdr-desk/ssh-%C}"
  done
}

# cn <args...>: run herdr-desk on the client machine as a person, or as the session in CN_SESSION. Its stdin and
# stdout come back, and so does its exit code. The words are plain, so they survive the remote shell.
cn() {
  rssh "$CN_ENV ${CN_SESSION:+DESK_SESSION=$CN_SESSION} $RDIR/herdr-desk $(printf '%q ' "$@")"
}

# ctimed <args...>: cn, timed on the client machine; the seconds are in CT afterwards.
ctimed() {
  local rc=0
  rssh "$CN_ENV python3 $RDIR/timed.py $RDIR/timed.txt $RDIR/herdr-desk $(printf '%q ' "$@")" || rc=$?
  # shellcheck disable=SC2034 # read by the scripts that source this file
  CT=$(rssh "cat $RDIR/timed.txt")
  return "$rc"
}

# under <seconds> <limit>: the seconds are under the limit.
under() { awk -v t="$1" -v l="$2" 'BEGIN { exit !(t < l) }'; }

pair_cleanup() {
  [ -n "${RDIR:-}" ] || return 0
  rssh "ssh -o ControlPath=$RDIR/state/herdr-desk/ssh-%C -O exit $E2E_HOME; rm -rf $RDIR" >/dev/null 2>&1 || true
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
  machine_env "$m"
  tm new-session -d -s "$name" -x "$cols" -y "$rows" -- "${MACHINE_ENV[@]}" "$@" || fail "tmux cannot start $name"
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

# task_field <machine> <task> <jq-filter>: a field of a task, from herdr-desk show --json.
task_field() { on "$1" herdr-desk show "$2" --json | jq -r "$3"; }

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
