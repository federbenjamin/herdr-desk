#!/usr/bin/env bash
# Shared setup for the runner's e2e scripts (r01 to r12, the h scripts that start runs, b01 to b18). Source it after
# lib.sh. Every script uses one machine, `home`, with the stub worker and the stub coordinator. Their files live in
# $STUB.
# shellcheck source=scripts/e2e/lib.sh

RUNNER_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
STUB="$E2E/stub"
mkdir -p "$STUB"
HERDR_REAL=0
# shellcheck disable=SC2034 # read by the scripts that source this file
SCRATCH="$E2E/home/data/herdr-desk/scratch"
# The home's store, read with sqlite3 so a poll never reconciles runs against herdr.
DB="$E2E/home/data/herdr-desk/desk.db"
# WORK holds the folders a script makes for its roots. It is the physical path: herdr-desk resolves a task's project to one.
mkdir -p "$E2E/work"
# shellcheck disable=SC2034 # read by the scripts that source this file
WORK=$(cd "$E2E/work" && pwd -P)
# The plugin a real-herdr script links (link_event_plugin), whether it did, and the file it logs each event to.
EVENT_PLUGIN=desk-e2e
EVENT_PLUGIN_LINKED=0
EVENT_LOG="$E2E/herdr-events.log"

# use_fake_herdr: point DESK_HERDR at scripts/e2e/fake-herdr.py, with its state in $E2E/herdr. Every call names herdr
# through DESK_HERDR; a bare `herdr` finds the guard in $E2E/fakebin, which fails, never a real herdr. A shim named sh
# in front of the real one records the command each fake pane is given, in $E2E/herdr/pane-commands.log.
use_fake_herdr() {
  mkdir -p "$E2E/fakebin" "$E2E/herdr"
  cat >"$E2E/fakebin/herdr" <<'GUARD'
#!/bin/sh
printf 'E2E FAIL: a bare herdr call (herdr %s); name it through DESK_HERDR\n' "$*" >&2
exit 97
GUARD
  chmod +x "$E2E/fakebin/herdr"
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

# with_events: from now on the fake herdr runs `herdr-desk hook herdr-event` after a status change and after a pane
# closes, as herdr runs a plugin's [[events]] hook. The hook runs with the caller's environment, so every call of the
# fake goes through `on home` (herdr_do): a call made bare would fire the hook against the real XDG folders.
with_events() { export FAKE_HERDR_EVENTS=1; }

# herdr_do <args...>: run herdr as DESK_HERDR names it: the fake under use_fake_herdr, `herdr` on PATH in the
# real-herdr scripts. The fake runs as the home, so a hook it fires reaches the home's store.
herdr_do() {
  if [ "$HERDR_REAL" = 1 ]; then
    "${DESK_HERDR:-herdr}" "$@"
  else
    on home "${DESK_HERDR:-herdr}" "$@"
  fi
}

# need_real_herdr: refuse to run unless a real herdr is on PATH and its server answers.
need_real_herdr() {
  command -v herdr >/dev/null 2>&1 || fail "(env) no herdr"
  herdr workspace list >/dev/null 2>&1 || fail "(env) no herdr server answers"
  HERDR_REAL=1
}

# link_event_plugin <command...>: link a temp plugin, desk-e2e, into the real herdr with the event hooks of the repo's
# manifest (herdr-plugin.toml), each running the command. Nothing else of the manifest is linked. Every event it gets
# is logged to EVENT_LOG first, and runner_cleanup prints that log, so a script shows what herdr sent. It refuses to
# run when a plugin with that id is already linked, and runner_cleanup unlinks it.
link_event_plugin() {
  local plug="$E2E/event-plugin"
  if herdr plugin list --plugin "$EVENT_PLUGIN" --json 2>/dev/null | jq -e '.result.plugins | length > 0' >/dev/null 2>&1; then
    fail "(env) a plugin with the id $EVENT_PLUGIN is already linked"
  fi
  mkdir -p "$plug"
  {
    printf '#!/bin/sh\n'
    # shellcheck disable=SC2016 # the $ is the logger's, expanded when herdr runs it
    printf 'printf "%%s\\t%%s\\t%%s\\n" "$(date +%%T)" "$HERDR_PLUGIN_EVENT" "$HERDR_PLUGIN_EVENT_JSON" >>%q\n' "$EVENT_LOG"
    printf '[ "$#" = 0 ] || exec "$@"\n'
  } >"$plug/log-event.sh"
  chmod +x "$plug/log-event.sh"
  python3 - "$REPO/herdr-plugin.toml" "$plug/herdr-plugin.toml" "$EVENT_PLUGIN" "$plug/log-event.sh" "$@" <<'PY' || fail "could not write the temp plugin's manifest from $REPO/herdr-plugin.toml"
import json, sys, tomllib

manifest, dst, plugin_id, logger = sys.argv[1:5]
command = json.dumps([logger] + sys.argv[5:])
events = [e["on"] for e in tomllib.load(open(manifest, "rb")).get("events", [])]
if not events:
    sys.exit("the manifest has no [[events]]")
out = 'id = "%s"\nname = "%s"\nversion = "0.0.0"\nmin_herdr_version = "0.9.0"\nplatforms = ["linux", "macos"]\n' % (plugin_id, plugin_id)
for event in events:
    out += '\n[[events]]\non = "%s"\ncommand = %s\n' % (event, command)
open(dst, "w").write(out)
PY
  run 0 herdr plugin link "$plug" --enabled
  EVENT_PLUGIN_LINKED=1
}

# home_event_command: set EVENT_CMD to the event hook's command for the home: this build's binary on the home's four
# folders, with the PATH and the herdr socket of this script, so the hook reaches the herdr that fired it.
home_event_command() {
  machine_env home
  # shellcheck disable=SC2034 # read by the scripts that source this file
  EVENT_CMD=("${MACHINE_ENV[@]}" "PATH=$PATH"
    ${HERDR_SOCKET_PATH:+"HERDR_SOCKET_PATH=$HERDR_SOCKET_PATH"} "$BIN/herdr-desk" hook herdr-event)
}

# real_coordinator_up <start_runs>: for the scripts that run a real coordinator session (b02, b03) on the real herdr, with
# the stub as the worker. The home gets a root with self isolation, so any number of stub runs may share it, holding
# the two files JOBS names, and the profile's coordinator template with its first word wrapped (wrap_claude). No
# CLAUDE_CODE_* marker of the session that runs the script reaches the coordinator.
real_coordinator_up() {
  need_real_herdr
  claude_guard
  unset_claude_markers
  build
  mkdir -p "$WORK/shared"
  printf 'Teh first line.\n' >"$WORK/shared/a.txt"
  printf 'One line.\n' >"$WORK/shared/b.txt"
  # shellcheck disable=SC2034 # read by the scripts that source this file
  JOBS="fix the typo in $WORK/shared/a.txt and add a second line to $WORK/shared/b.txt"
  add_root "$WORK/shared" self "scratch space for stub workers; any number of runs may share it"
  RC_NOTIFY=none
  RC_CAP=2
  RC_START=$1
  runner_up home
  python3 - "$E2E/home/config/herdr-desk/config.toml" <<'PY' || fail "the config has no coordinator line"
import re, sys

path = sys.argv[1]
text = open(path).read()
claude = '["claude", "--permission-mode", "auto", "--session-id", "{session}", "--append-system-prompt", "{prompt}"]'
text, n = re.subn(r"(?m)^coordinator = \[.*\]$", lambda m: "coordinator = " + claude, text)
if n != 1:
    sys.exit(1)
open(path, "w").write(text)
PY
  wrap_claude coordinator
}

# coordinator_open: open the coordinator on the real herdr, answer claude's trust question for the scratch root as a
# person would, and wait until herdr shows its session. COORD_PANE and COORD_SESSION name it.
coordinator_open() {
  run 0 on home herdr-desk coordinator
  COORD_PANE=$(sqlite3 "$DB" "SELECT pane FROM coordinator")
  COORD_SESSION=$(sqlite3 "$DB" "SELECT session FROM coordinator")
  SHOW_PANE=$COORD_PANE
  track_workspaces
  answer_trust_question "$COORD_PANE"
  wait_long 120 "herdr to show the coordinator's session" pane_has_session "$COORD_SESSION"
}

# tell_coordinator <text>: type the text into the coordinator's pane and press Enter, as a person would.
tell_coordinator() {
  herdr_do pane send-text "$COORD_PANE" "$1" >/dev/null || fail "could not type into pane $COORD_PANE"
  sleep 1
  herdr_do pane send-keys "$COORD_PANE" Enter >/dev/null || fail "could not press Enter in pane $COORD_PANE"
}

run_count() { sqlite3 "$DB" "SELECT count(*) FROM runs"; }
task_count() { on home herdr-desk list --all --json | jq '.tasks | length'; }
at_least_two_tasks() { [ "$(task_count)" -ge 2 ]; }
at_least_two_runs() { [ "$(run_count)" -ge 2 ]; }

# coordinator_status: the agent status herdr shows for the coordinator's pane.
coordinator_status() { herdr_do pane get "$COORD_PANE" 2>/dev/null | jq -r '.result.pane.agent_status // ""'; }

# coordinator_turn_done: herdr shows the coordinator idle or done, its turn over, on two reads a second apart, so a
# status that flickers between two tool calls is not read as the end of the turn. Wait for the turn's first effect
# before this, so the status read is not the one from before the turn.
coordinator_turn_done() {
  case "$(coordinator_status)" in idle | done) ;; *) return 1 ;; esac
  sleep 1
  case "$(coordinator_status)" in idle | done) ;; *) return 1 ;; esac
}

# ab_tasks: exactly two tasks exist, one whose title or notes name a.txt and the other b.txt, for the two jobs of JOBS.
# A_TASK and B_TASK are their numbers.
ab_tasks() {
  local json
  json=$(on home herdr-desk list --all --json)
  [ "$(jq '.tasks | length' <<<"$json")" = 2 ] || return 1
  A_TASK=$(names_file a "$json")
  B_TASK=$(names_file b "$json")
  [ -n "$A_TASK" ] && [ -n "$B_TASK" ] && [ "$A_TASK" != "$B_TASK" ]
}

# names_file <a|b> <list json>: the number of the one task whose title or notes name <a|b>.txt; empty unless one does.
names_file() {
  jq -r --arg f "$1" '[.tasks[] | select((.title + " " + (.notes // "")) | test("\\b" + $f + "\\.txt\\b"))]
    | if length == 1 then .[0].number else "" end' <<<"$2"
}

# tasks_seen: the tasks' numbers and titles, for a failure message.
tasks_seen() { on home herdr-desk list --all --json | jq -c '[.tasks[] | {number, title}]'; }

# one_run_each: exactly two runs exist, one for A_TASK and one for B_TASK.
one_run_each() {
  [ "$(sqlite3 "$DB" "SELECT group_concat(task, ',') FROM (SELECT task FROM runs ORDER BY task)")" = \
    "$(printf '%s\n' "$A_TASK" "$B_TASK" | sort -n | paste -sd, -)" ]
}

# end_real_coordinator: close what the script opened, check it is closed, and check the session count.
end_real_coordinator() {
  local id
  SHOW_PANE=""
  track_workspaces
  close_tracked_workspaces
  while read -r id; do
    wait_long 20 "workspace $id to close" workspace_closed "$id"
  done < <(sort -u "$E2E/workspaces.txt")
  say "every workspace this test opened is closed ok"
  [ "$(claude_calls coordinator)" = 1 ] || fail "claude ran $(claude_calls coordinator) times as the coordinator, not once"
}

# focused_workspace: the id of the workspace herdr has focused.
focused_workspace() {
  herdr_do workspace list | jq -r '[.result.workspaces[] | select(.focused)][0].workspace_id // ""'
}

# track_workspaces: add the workspace of every run row, and the coordinator's, to $E2E/workspaces.txt.
track_workspaces() {
  [ -f "$DB" ] || return 0
  sqlite3 -cmd ".timeout 5000" -cmd "PRAGMA query_only = 1" "$DB" "SELECT workspace FROM runs WHERE workspace <> '' UNION SELECT workspace FROM coordinator" >>"$E2E/workspaces.txt" 2>/dev/null || true
}

# close_tracked_workspaces: close each workspace in $E2E/workspaces.txt, which a run row named. On the real herdr
# nothing else is ever closed.
close_tracked_workspaces() {
  local id
  [ -f "$E2E/workspaces.txt" ] || return 0
  while read -r id; do
    herdr_do workspace close "$id" >/dev/null 2>&1 || true
  done < <(sort -u "$E2E/workspaces.txt")
}

# workspace_open <id>: herdr lists the workspace.
workspace_open() {
  herdr_do workspace list | jq -e --arg id "$1" '[.result.workspaces[].workspace_id] | index($id) != null' >/dev/null
}

workspace_closed() { ! workspace_open "$1"; }

# SHOW_PANE: a pane whose visible screen runner_cleanup prints when the script fails, before anything is closed.
SHOW_PANE=""

# show_pane: print the last 30 lines of SHOW_PANE's visible screen on stderr; nothing when it is unset.
show_pane() {
  [ -n "$SHOW_PANE" ] || return 0
  say "--- pane $SHOW_PANE, visible screen, last 30 lines ---" >&2
  herdr_do pane read "$SHOW_PANE" --source visible --format text 2>&1 | tail -n 30 >&2 || true
  say "--- end of pane $SHOW_PANE ---" >&2
}

# pane_shows <pane> <text>: the pane's visible screen holds the text.
pane_shows() {
  herdr_do pane read "$1" --source visible --format text 2>/dev/null | grep -F -- "$2" >/dev/null
}

# answer_trust_question <pane>: answer "Yes, I trust this folder" to claude's trust question in the pane, as a person
# would. Keys sent at once after the question appears are ignored, and Enter alone picks "No, exit", so it waits,
# moves down, waits, and confirms. It fails, showing the screen, when the question is not on screen within 30 s.
answer_trust_question() {
  local pane=$1
  herdr_do pane wait-output "$pane" --match "trust this folder" --source visible --timeout 30000 >/dev/null 2>&1 || true
  if ! pane_shows "$pane" "trust this folder"; then
    SHOW_PANE=$pane
    fail "claude's trust question is not on pane $pane's screen after 30 s"
  fi
  sleep 2
  herdr_do pane send-keys "$pane" Down >/dev/null || fail "could not send Down to pane $pane"
  sleep 1
  herdr_do pane send-keys "$pane" Enter >/dev/null || fail "could not send Enter to pane $pane"
}

# show_files <file...>: print each file that is not empty on stderr, each line cut to 400 characters.
show_files() {
  local f
  for f in "$@"; do
    [ -s "$f" ] || continue
    say "--- $f ---" >&2
    cut -c1-400 "$f" >&2
    say "--- end of $f ---" >&2
  done
}

# pass: lib.sh's pass, after the events the temp plugin logged, so a script that passes shows what herdr sent too and
# still ends E2E PASS.
pass() {
  show_files "$EVENT_LOG"
  say "E2E PASS"
}

# runner_cleanup: show SHOW_PANE's screen, the events the temp plugin logged, and the home's herdr-desk.log when the
# script failed, close every pane the fake holds or every workspace a run row named, unlink the temp plugin, then the
# shared cleanup, which stops the tickers. Only workspaces named by a run row are closed on the real herdr.
runner_cleanup() {
  local rc=$?
  local id
  if [ "$rc" != 0 ]; then
    show_pane
    show_files "$EVENT_LOG" "$E2E/home/state/herdr-desk/herdr-desk.log"
  fi
  if [ "$HERDR_REAL" = 1 ]; then track_workspaces; fi
  if [ "$HERDR_REAL" = 0 ] && [ -f "$E2E/herdr/state.json" ]; then
    FAKE_HERDR_EVENTS=0
    export FAKE_HERDR_EVENTS
    while read -r id; do
      herdr_do pane close "$id" >/dev/null 2>&1 || true
    done < <(herdr_do pane list 2>/dev/null | jq -r '.result.panes[].pane_id' 2>/dev/null)
  fi
  if [ "$HERDR_REAL" = 1 ]; then close_tracked_workspaces; fi
  if [ "$EVENT_PLUGIN_LINKED" = 1 ]; then herdr plugin unlink "$EVENT_PLUGIN" >/dev/null 2>&1 || true; fi
  cleanup
}
trap runner_cleanup EXIT

# add_root <path> <isolation> <about>: add a root to RC_ROOTS, the TOML runner_config writes.
add_root() {
  RC_ROOTS="${RC_ROOTS-}$(printf '[[roots]]\npath = "%s"\nabout = "%s"\nisolation = "%s"\n' "$1" "${3-}" "${2-}")"$'\n\n'
}

# runner_config <machine>: write the machine's config from these variables, each with a default:
#   RC_ENABLED true   RC_CAP 1   RC_DAY 20   RC_MINUTES 180   RC_START propose
#   RC_ROOTS ""       TOML for the roots, built with add_root
#   RC_MODELS '["sonnet", "opus"]'
#   RC_NOTIFY herdr | none
# The worker and the coordinator are the stubs in $STUB; the coordinator's template has the shape of the claude-code
# profile's, so the stub shows which flags it was given.
runner_config() {
  local m=$1 models notify=""
  local default_models='["sonnet", "opus"]'
  models=${RC_MODELS-$default_models}
  if [ "${RC_NOTIFY-herdr}" != none ]; then
    notify="[notify]"$'\n'"command = [\"${DESK_HERDR:-herdr}\", \"notification\", \"show\", \"{title}\", \"--body\", \"{body}\"]"
  fi
  write_config "$m" <<TOML
[runner]
enabled = ${RC_ENABLED-true}
cap = ${RC_CAP-1}
max_runs_per_day = ${RC_DAY-20}
max_run_minutes = ${RC_MINUTES-180}

[coordinator]
start_runs = "${RC_START-propose}"

${RC_ROOTS-}
[agent]
worker = ["$RUNNER_DIR/stub-worker.sh", "$STUB", "{model}", "{session}", "{message}"]
coordinator = ["$RUNNER_DIR/stub-coordinator.sh", "$STUB", "--session-id", "{session}", "--append-system-prompt", "{prompt}"]
models = $models

$notify
TOML
}

# runner_up <machine>: set the machine up as a home and write its runner config. Nothing runs: every command opens
# the store, and the ticker is for the scripts that want it (ticker_up).
runner_up() {
  home_up "$1"
  runner_config "$1"
}

# make_repo <dir>: a git repository with one commit.
make_repo() {
  mkdir -p "$1"
  git -C "$1" init -q
  git -C "$1" -c user.name=e2e -c user.email=e2e@example.invalid commit -q --allow-empty -m init
}

# set_mode <task number> <mode>: what the stub worker does for that task.
set_mode() { printf '%s' "$2" >"$STUB/mode-T$1"; }

# start_task <machine> <title> <add flags...>: add a task and run `run start` on it, as a person. Pass --desk or -p
# <dir>: without one `herdr-desk add` takes the project of the current folder. The task's number is left in TASK_N
# and run start's line in OUT.
start_task() {
  local m=$1 title=$2
  shift 2
  run 0 on "$m" herdr-desk add -t "$title" "$@"
  TASK_N=$OUT
  run 0 on "$m" herdr-desk run start "$TASK_N"
}

# run_field <run id> <field>: a column of a run row, read from the store without reconciling.
run_field() {
  case "$2" in *[!a-z_]*) fail "run_field: no column '$2'" ;; esac
  sqlite3 -cmd ".timeout 5000" -cmd "PRAGMA query_only = 1" "$DB" "SELECT $2 FROM runs WHERE id = $1"
}
run_is() { [ "$(run_field "$1" state)" = "$2" ]; }

# task_field <task number> <field>: a field of the task.
task_field() {
  on home herdr-desk show "T$1" --json | jq -r --arg f "$2" '.task[$f]'
}
task_is() { [ "$(task_field "$1" status)" = "$2" ]; }

# task_notes <task number>: the text of each note on the task, one per line.
task_notes() {
  on home herdr-desk show "T$1" --json | jq -r '.history[] | select(.kind == "note") | .data.text'
}
task_has_note() { task_notes "$1" | grep -F -- "$2" >/dev/null; }

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
pane_ids() { herdr_do pane list | jq -r '.result.panes[].pane_id'; }

# pane_of_session <session>: the id of the pane herdr shows for that agent session; empty when it shows none.
pane_of_session() {
  herdr_do pane list | jq -r --arg s "$1" '[.result.panes[] | select(.agent_session.value? == $s)][0].pane_id // ""'
}
pane_has_session() { [ -n "$(pane_of_session "$1")" ]; }

# report <pane> <state> [report-agent flags...]: tell herdr a pane's agent is in that state, as the stub worker does.
report() {
  local pane=$1 state=$2
  shift 2
  herdr_do pane report-agent "$pane" --source desk-e2e --agent stub --state "$state" "$@" >/dev/null
}

# now: the time in seconds since the epoch, with fractions, to time a wait.
now() { python3 -c 'import time; print("%.3f" % time.time())'; }
# elapsed <start>: seconds since a now() reading.
elapsed() { awk -v a="$1" -v b="$(now)" 'BEGIN { printf "%.1f", b - a }'; }
