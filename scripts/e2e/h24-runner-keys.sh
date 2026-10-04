#!/usr/bin/env bash
# H24: a live run shows in IN MOTION and the header, and k, P, f, and o make their calls.
# The home's runner has the fake herdr and the stub router, so it is on, and its runs.kill and runner.pause are the
# real ones: the kill leaves the task blocked, and the pause shows in the header. The board has its own stub herdr,
# which logs each call f and o make.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
runner_up home
run 0 on home desk add -t "run under test" --desk --status started
mkdir -p "$E2E/docs"
echo "the written file" >"$E2E/docs/result.md"
run 0 as_agent home sess-a desk note "wrote the result" --task 1 --ref "$E2E/docs/result.md"

# The seeded run's pane is one the fake herdr lists. The watch finds it by its id and workspace and, while no agent
# session shows on it, leaves the run running.
created=$(herdr_do workspace create --cwd "$E2E" --label "run under test")
WS=$(jq -r .result.workspace.workspace_id <<<"$created")
PANE=$(jq -r .result.root_pane.pane_id <<<"$created")

# The runs table is seeded while the daemon is stopped, so the daemon's own reads see the row.
stop_daemon home
DB="$E2E/home/data/desk/desk.db"
[ -f "$DB" ] || fail "the store file is not at $DB"
sqlite3 "$DB" "INSERT INTO runs(task, state, root, isolation, model, workspace, pane, started_ts)
  VALUES (1, 'running', '/work/example', 'worktree', 'opus', '$WS', '$PANE', '$(date -u +%Y-%m-%dT%H:%M:%SZ)')"
start_daemon home

HERDR_LOG="$E2E/board-herdr.log"
: >"$HERDR_LOG"
cat >"$E2E/board-herdr" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >>"$HERDR_LOG"
case "\$*" in
  "plugin list"*) printf '{"result":{"plugins":[{"id":"herdr-file-viewer"}]}}\n' ;;
esac
EOF
chmod +x "$E2E/board-herdr"

term_start board 100 30 home DESK_HERDR="$E2E/board-herdr" "$BIN/desk"
term_wait board "worktree · opus"
ok "the row shows the run's isolation and model"
term_wait board "runner ● on · 1/1 · home"
ok "the header counts 1 live run"

# f needs a live run, so it goes before the kill.
term_keys board f
wait_for "herdr workspace focus" grep -q "^workspace focus $WS$" "$HERDR_LOG"
wait_for "herdr pane zoom on" grep -q "^pane zoom $PANE --on$" "$HERDR_LOG"
wait_for "herdr pane zoom off" grep -q "^pane zoom $PANE --off$" "$HERDR_LOG"
ok "f ran herdr workspace focus and pane zoom"

term_keys board k
term_wait board "kill T1's run? y/n"
term_keys board z
term_wait_gone board "kill T1's run? y/n"
task_is 1 started || fail "another key after k still killed the run: T1 is $(task_field 1 status)"
run_is 1 running || fail "another key after k ended run 1: it is $(run_field 1 state)"
ok "k asks before it kills, and another key cancels"

term_keys board k
term_wait board "kill T1's run? y/n"
term_keys board y
wait_task 1 blocked 10
run_is 1 killed || fail "run 1 is $(run_field 1 state), not killed"
[ -z "$(pane_ids)" ] || fail "the run's pane is still open: $(pane_ids)"
ok "k then y calls runs.kill"
term_wait board "runner ● on · 0/1 · home"
ok "after the kill the header counts 0 live runs"

term_keys board P
term_wait board "runner ◐ paused"
run 0 on home desk runner status
out_has "runner paused"
ok "P calls runner.pause or says the runner is off"
term_keys board P
term_wait board "runner ● on"
run 0 on home desk runner status
out_has "runner on"
ok "a second P resumes the runner"

term_keys board Enter
term_wait board "FILES"
term_keys board o
wait_for "the file viewer call" grep -q "plugin pane open --plugin herdr-file-viewer .*HERDR_FILE_VIEWER_OPEN=$E2E/docs/result.md" "$HERDR_LOG"
ok "o ran the file viewer with HERDR_FILE_VIEWER_OPEN"

term_keys board q
pass
