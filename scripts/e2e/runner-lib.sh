#!/usr/bin/env bash
# Shared setup for the runner's e2e scripts (r01 to r12). Source it after lib.sh. Every script uses one machine,
# `home`, with the stub router and the stub worker. Its stub files live in $STUB.
# shellcheck source=scripts/e2e/lib.sh

RUNNER_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
STUB="$E2E/stub"
mkdir -p "$STUB"
HERDR_REAL=0
# shellcheck disable=SC2034 # read by the scripts that source this file
SCRATCH="$E2E/home/data/desk/scratch"
# WORK holds the folders a script makes for its roots. It is the physical path: desk resolves a task's project to one.
mkdir -p "$E2E/work"
# shellcheck disable=SC2034 # read by the scripts that source this file
WORK=$(cd "$E2E/work" && pwd -P)

# use_fake_herdr: point DESK_HERDR at scripts/e2e/fake-herdr.py, and put it first on PATH as `herdr` for the stub
# worker's pane, with its state in $E2E/herdr. Call it before any daemon starts: the daemon and every pane inherit
# DESK_HERDR, PATH and FAKE_HERDR_DIR. A shim named sh in front of the real
# one records the command each fake pane is given, in $E2E/herdr/pane-commands.log.
use_fake_herdr() {
  mkdir -p "$E2E/fakebin" "$E2E/herdr"
  ln -s "$RUNNER_DIR/fake-herdr.py" "$E2E/fakebin/herdr"
  cat >"$E2E/fakebin/sh" <<'SH'
#!/bin/bash
if [ "${1-}" = "-c" ] && [ -n "${HERDR_PANE_ID-}" ] && [ -n "${FAKE_HERDR_DIR-}" ]; then
  printf '%s\n' "$2" >>"$FAKE_HERDR_DIR/pane-commands.log"
fi
exec /bin/sh "$@"
SH
  chmod +x "$E2E/fakebin/sh"
  export DESK_HERDR="$RUNNER_DIR/fake-herdr.py"
  export FAKE_HERDR_DIR="$E2E/herdr"
  export PATH="$E2E/fakebin:$PATH"
}

# need_real_herdr: refuse to run unless a real herdr is on PATH and its server answers.
need_real_herdr() {
  command -v herdr >/dev/null 2>&1 || fail "no herdr"
  herdr workspace list >/dev/null 2>&1 || fail "no herdr"
  HERDR_REAL=1
}

# focused_workspace: the id of the workspace herdr has focused.
focused_workspace() {
  herdr workspace list | jq -r '[.result.workspaces[] | select(.focused)][0].workspace_id // ""'
}

# track_workspaces: add the workspace of every run row to $E2E/workspaces.txt. Only a daemon that is up is asked.
track_workspaces() {
  [ -S "$(sock home)" ] || return 0
  on home desk runs --all --json 2>/dev/null | jq -r '.[].workspace | select(. != "")' >>"$E2E/workspaces.txt" || true
}

# close_tracked_workspaces: close each workspace in $E2E/workspaces.txt, which a run row named. On the real herdr
# nothing else is ever closed.
close_tracked_workspaces() {
  local id
  [ -f "$E2E/workspaces.txt" ] || return 0
  while read -r id; do
    herdr workspace close "$id" >/dev/null 2>&1 || true
  done < <(sort -u "$E2E/workspaces.txt")
}

# workspace_open <id>: herdr lists the workspace.
workspace_open() {
  herdr workspace list | jq -e --arg id "$1" '[.result.workspaces[].workspace_id] | index($id) != null' >/dev/null
}

workspace_closed() { ! workspace_open "$1"; }

# runner_cleanup: stop the daemons, close every pane the fake holds or every workspace a run row named, then the
# shared cleanup. Only workspaces named by a run row are closed on the real herdr.
runner_cleanup() {
  local d m id
  if [ "$HERDR_REAL" = 1 ]; then track_workspaces; fi
  for d in "$E2E"/*/state/desk; do
    [ -f "$d/daemon.json" ] || continue
    m=$(basename "$(dirname "$(dirname "$d")")")
    on "$m" desk daemon stop >/dev/null 2>&1 || true
  done
  if [ "$HERDR_REAL" = 0 ] && [ -f "$E2E/herdr/state.json" ]; then
    while read -r id; do
      herdr pane close "$id" >/dev/null 2>&1 || true
    done < <(herdr pane list 2>/dev/null | jq -r '.result.panes[].pane_id' 2>/dev/null)
  fi
  if [ "$HERDR_REAL" = 1 ]; then close_tracked_workspaces; fi
  cleanup
}
trap runner_cleanup EXIT

# add_root <path> <isolation> <about>: add a root to RC_ROOTS, the TOML runner_config writes.
add_root() {
  RC_ROOTS="${RC_ROOTS-}$(printf '[[roots]]\npath = "%s"\nabout = "%s"\nisolation = "%s"\n' "$1" "${3-}" "${2-}")"$'\n\n'
}

# runner_config <machine>: write the machine's config from these variables, each with a default:
#   RC_ENABLED true   RC_CAP 1   RC_DAY 20   RC_MINUTES 180   RC_POLL 1
#   RC_ROOTS ""       TOML for the roots, built with add_root
#   RC_MODELS '["sonnet", "opus"]'
#   RC_ROUTER stub | none   RC_NOTIFY herdr | none
# The router and the worker are the stubs in $STUB.
runner_config() {
  local m=$1 models router="" notify=""
  local default_models='["sonnet", "opus"]'
  models=${RC_MODELS-$default_models}
  if [ "${RC_ROUTER-stub}" != none ]; then
    router="router = [\"$RUNNER_DIR/stub-router.sh\", \"$STUB\", \"{system}\", \"{schema}\"]"
  fi
  if [ "${RC_NOTIFY-herdr}" != none ]; then
    notify=$'[notify]\ncommand = ["herdr", "notification", "show", "{title}", "--body", "{body}"]'
  fi
  write_config "$m" <<TOML
[runner]
enabled = ${RC_ENABLED-true}
cap = ${RC_CAP-1}
max_runs_per_day = ${RC_DAY-20}
max_run_minutes = ${RC_MINUTES-180}
poll_seconds = ${RC_POLL-1}

${RC_ROOTS-}
[agent]
$router
worker = ["$RUNNER_DIR/stub-worker.sh", "$STUB", "{model}", "{session}", "{message}"]
models = $models

$notify
TOML
}

# runner_up <machine>: set the machine up, write its runner config, and start its daemon.
runner_up() {
  run 0 on "$1" desk setup --no-herdr
  runner_config "$1"
  start_daemon "$1"
}

# restart_daemon: stop the home's daemon and start it again on the config as it is now.
restart_daemon() {
  stop_daemon home
  start_daemon home
}

# make_repo <dir>: a git repository with one commit.
make_repo() {
  mkdir -p "$1"
  git -C "$1" init -q
  git -C "$1" -c user.name=e2e -c user.email=e2e@example.invalid commit -q --allow-empty -m init
}

# route_to <root> <isolation> <model> <reason>: what the stub router answers, in the shape `claude -p` prints.
route_to() {
  printf '{"structured_output": {"root": "%s", "isolation": "%s", "model": "%s", "reason": "%s"}}\n' \
    "$1" "$2" "$3" "$4" >"$STUB/router-out.json"
}

# set_mode <task number> <mode>: what the stub worker does for that task.
set_mode() { printf '%s' "$2" >"$STUB/mode-T$1"; }

# arm_task <machine> <title> <add flags...>: add a task on thread agent and set it ready, as a person. The task
# number is left in OUT. Pass --desk or -p <dir>: without one `desk add` takes the project of the current folder.
arm_task() {
  local m=$1 title=$2 n
  shift 2
  run 0 on "$m" desk add -t "$title" --thread agent "$@"
  n=$OUT
  run 0 on "$m" desk set "$n" ready
  OUT=$n
}

# run_field <run id> <field>: a field of a run row.
run_field() {
  on home desk runs --all --json | jq -r --argjson id "$1" --arg f "$2" '.[] | select(.id == $id) | .[$f]'
}
run_is() { [ "$(run_field "$1" state)" = "$2" ]; }

# task_field <task number> <field>: a field of the task.
task_field() {
  on home desk show "T$1" --json | jq -r --arg f "$2" '.task[$f]'
}
task_is() { [ "$(task_field "$1" status)" = "$2" ]; }

# task_notes <task number>: the text of each note on the task, one per line.
task_notes() {
  on home desk show "T$1" --json | jq -r '.history[] | select(.kind == "note") | .data.text'
}
task_has_note() { task_notes "$1" | grep -F -- "$2" >/dev/null; }

# wait_long <seconds> <what> <command...>: poll until the command succeeds, every 0.2 s.
wait_long() {
  local secs=$1 what=$2 end
  shift 2
  end=$((SECONDS + secs + 1))
  while [ "$SECONDS" -lt "$end" ]; do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 0.2
  done
  fail "timed out after ${secs}s waiting for $what"
}

# wait_run <run id> <state> [seconds]: until the run is in that state (default 15 s).
wait_run() { wait_long "${3-15}" "run $1 to be $2" run_is "$1" "$2"; }
# wait_task <task number> <status> [seconds]: until the task has that status (default 15 s).
wait_task() { wait_long "${3-15}" "T$1 to be $2" task_is "$1" "$2"; }
# wait_note <task number> <text> [seconds]: until a note on the task holds the text (default 15 s).
wait_note() { wait_long "${3-15}" "a note on T$1 holding '$2'" task_has_note "$1" "$2"; }
wait_file() { wait_long "${2-15}" "$1" test -s "$1"; }

# env_value <file> <name>: the value of NAME in a stub worker's env file.
env_value() { sed -n "s/^$2=//p" "$1"; }

# pids_alive <file> / pids_dead <file>: every pid, or none, in a pids file is alive.
pids_alive() {
  local p
  while read -r p || [ -n "$p" ]; do kill -0 "$p" 2>/dev/null || return 1; done <"$1"
}
pids_dead() {
  local p
  while read -r p || [ -n "$p" ]; do if kill -0 "$p" 2>/dev/null; then return 1; fi; done <"$1"
}

# pane_ids: the pane ids herdr lists.
pane_ids() { herdr pane list | jq -r '.result.panes[].pane_id'; }

# pane_of_session <session>: the id of the pane herdr shows for that agent session; empty when it shows none.
pane_of_session() {
  herdr pane list | jq -r --arg s "$1" '[.result.panes[] | select(.agent_session.value? == $s)][0].pane_id // ""'
}
pane_has_session() { [ -n "$(pane_of_session "$1")" ]; }
