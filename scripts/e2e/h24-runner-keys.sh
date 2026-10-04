#!/usr/bin/env bash
# H24: a live run shows in IN MOTION and the header, and k, P, f, and o make their calls.
# This branch's home serves neither runs.kill nor runner.pause, so those two keys are shown to have made the call
# by the home's refusal on the status line. A home that serves them shows the kill as the task going blocked and
# the pause in the header.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
run 0 on home desk setup --no-herdr --runner on
run 0 on home desk add -t "run under test" --desk --status started
mkdir -p "$E2E/docs"
echo "the written file" >"$E2E/docs/result.md"
run 0 as_agent home sess-a desk note "wrote the result" --task 1 --ref "$E2E/docs/result.md"

# The runs table is seeded while the daemon is stopped, so the daemon's own reads see the row.
stop_daemon home
DB="$E2E/home/data/desk/desk.db"
[ -f "$DB" ] || fail "the store file is not at $DB"
sqlite3 "$DB" "INSERT INTO runs(task, state, root, isolation, model, workspace, pane, started_ts)
  VALUES (1, 'running', '/work/example', 'worktree', 'opus', 'w_1', 'p_1', '$(date -u +%Y-%m-%dT%H:%M:%SZ)')"
start_daemon home

HERDR_LOG="$E2E/herdr.log"
: >"$HERDR_LOG"
mkdir -p "$E2E/stub"
cat >"$E2E/stub/herdr" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >>"$HERDR_LOG"
case "\$*" in
  "plugin list"*) printf '{"result":{"plugins":[{"id":"herdr-file-viewer"}]}}\n' ;;
esac
EOF
chmod +x "$E2E/stub/herdr"

term_start board 100 30 home DESK_HERDR="$E2E/stub/herdr" "$BIN/desk"
term_wait board "worktree · opus"
ok "the row shows the run's isolation and model"
term_wait board "runner ● on · 1/1 · home"
ok "the header counts 1 live run"

# answered <method> <blocked> <status-line text...>: the screen holds one of the texts (the home's answer naming
# the method, or the board's own reason), or, with <blocked> yes, the task is blocked because a home that serves
# runs.kill killed the run. Only the kill may count the blocked task: the kill leaves it so.
answered() {
  local method=$1 blocked=$2 t
  shift 2
  for _ in $(seq 1 100); do
    for t in "$@"; do
      if term_has board "$t"; then return 0; fi
    done
    if [ "$blocked" = yes ] && [ "$(task_field home 1 .task.status)" = blocked ]; then return 0; fi
    sleep 0.1
  done
  say "--- screen ---" >&2
  term_screen board >&2
  fail "no answer to $method on the status line"
}

term_keys board k
term_wait board "kill T1's run? y/n"
term_keys board z
term_wait_gone board "kill T1's run? y/n"
[ "$(task_field home 1 .task.status)" = started ] || fail "another key after k still killed the run"
ok "k asks before it kills, and another key cancels"

term_keys board k
term_wait board "kill T1's run? y/n"
term_keys board y
answered runs.kill yes "unknown method /v1/runs.kill"
ok "k then y calls runs.kill"

# A home that serves runner.pause answers with its status, which the header shows as paused.
term_keys board P
answered runner.pause no "unknown method /v1/runner.pause" "runner ◐ paused" "the runner is off"
ok "P calls runner.pause or says the runner is off"

term_keys board f
wait_for "herdr workspace focus" grep -q "^workspace focus w_1$" "$HERDR_LOG"
wait_for "herdr pane zoom on" grep -q "^pane zoom p_1 --on$" "$HERDR_LOG"
wait_for "herdr pane zoom off" grep -q "^pane zoom p_1 --off$" "$HERDR_LOG"
ok "f ran herdr workspace focus and pane zoom"

term_keys board Enter
term_wait board "FILES"
term_keys board o
wait_for "the file viewer call" grep -q "plugin pane open --plugin herdr-file-viewer .*HERDR_FILE_VIEWER_OPEN=$E2E/docs/result.md" "$HERDR_LOG"
ok "o ran the file viewer with HERDR_FILE_VIEWER_OPEN"

term_keys board q
pass
