#!/usr/bin/env bash
# The worker's stand-in for the runner's e2e scripts: stub-worker.sh <stubdir> <model> <session> <message>.
# `herdr-desk worker` execs it. It records what it was started with, tells herdr it is an agent working, then acts by the
# word in <stubdir>/mode-<DESK_TASK>: busy (default) stays working; idle and blocked report that state; exit ends at
# once; handback sets the task to review and reports idle; children also starts a background sleep. Its own pid, and
# the sleep's, are recorded in <stubdir>/pids-run<DESK_RUN>. The stub directory is an argument because a pane does not inherit the
# daemon's environment under the real herdr.
set -eu
dir=$1
model=$2
session=$3
message=$4
task=${DESK_TASK:?}
run=${DESK_RUN:?}
{
  printf 'DESK_TASK=%s\n' "$task"
  printf 'DESK_SESSION=%s\n' "${DESK_SESSION-}"
  printf 'DESK_RUN=%s\n' "$run"
  printf 'XDG_CONFIG_HOME=%s\n' "${XDG_CONFIG_HOME-}"
  printf 'XDG_STATE_HOME=%s\n' "${XDG_STATE_HOME-}"
  printf 'XDG_DATA_HOME=%s\n' "${XDG_DATA_HOME-}"
  printf 'XDG_CACHE_HOME=%s\n' "${XDG_CACHE_HOME-}"
  printf 'PWD=%s\n' "$(pwd -P)"
  printf 'MODEL=%s\n' "$model"
  printf 'SESSION=%s\n' "$session"
} >"$dir/worker-run$run.env"
printf '%s' "$message" >"$dir/worker-run$run.message"
printf '%s\n' "$$" >"$dir/pids-run$run"

report() {
  local state=$1
  shift
  "${DESK_HERDR:-herdr}" pane report-agent "$HERDR_PANE_ID" --source desk-e2e --agent stub --state "$state" "$@" >/dev/null
}

report working --agent-session-id "$session"
# The real herdr keeps an agent session only from the source herdr:claude, and report-agent does not set one.
"${DESK_HERDR:-herdr}" pane report-agent-session "$HERDR_PANE_ID" --source herdr:claude --agent claude --agent-session-id "$session" >/dev/null
mode=busy
if [ -f "$dir/mode-$task" ]; then mode=$(cat "$dir/mode-$task"); fi
case "$mode" in
busy) ;;
idle) report idle ;;
blocked) report blocked ;;
exit) exit 0 ;;
handback)
  herdr-desk set "$task" review --ref e2e >/dev/null
  report idle
  ;;
children)
  sleep 300 &
  printf '%s\n' "$!" >>"$dir/pids-run$run"
  ;;
*)
  printf 'stub-worker: unknown mode %s\n' "$mode" >&2
  exit 2
  ;;
esac
sleep 300
