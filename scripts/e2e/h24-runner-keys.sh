#!/usr/bin/env bash
# H27: S starts a run, and a live run shows in IN MOTION and the header; k, P, and o make their calls.
# The board runs the home's runner itself (every command opens the store), with a herdr that logs each call and answers
# the ones a run needs through the fake herdr, so S, runs.kill, and runner.pause are the real ones: the kill leaves the
# task blocked, and the pause shows in the header. The wrapper also logs the call o makes.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
runner_up home
run 0 on home herdr-desk add -t "run under test" --desk
mkdir -p "$E2E/docs"
echo "the written file" >"$E2E/docs/result.md"
run 0 as_agent home sess-a herdr-desk note "wrote the result" --task 1 --ref "$E2E/docs/result.md"

HERDR_LOG="$E2E/board-herdr.log"
: >"$HERDR_LOG"
cat >"$E2E/board-herdr" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >>"$HERDR_LOG"
case "\$*" in
  "plugin list"*) printf '{"result":{"plugins":[{"id":"herdr-file-viewer"}]}}\n' ;;
  "plugin pane open"*) ;;
  *) exec "$RUNNER_DIR/fake-herdr.py" "\$@" ;;
esac
EOF
chmod +x "$E2E/board-herdr"

term_start board 100 30 home DESK_HERDR="$E2E/board-herdr" FAKE_HERDR_DIR="$FAKE_HERDR_DIR" "$BIN/herdr-desk"
term_wait board "run under test"
term_keys board S
wait_run 1 running
ok "S started run 1"
term_wait board "in-place · sonnet"
ok "the row shows the run's isolation and model"
term_wait board "runner ● on · 1/1 · home"
ok "the header counts 1 live run"

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
run 0 on home herdr-desk runner status
out_has "runner paused"
ok "P calls runner.pause"
term_keys board P
term_wait board "runner ● on"
run 0 on home herdr-desk runner status
out_has "runner on"
ok "a second P resumes the runner"

term_keys board Enter
term_wait board "FILES"
term_keys board o
wait_for "the file viewer call" grep -q "plugin pane open --plugin herdr-file-viewer .*HERDR_FILE_VIEWER_OPEN=$E2E/docs/result.md" "$HERDR_LOG"
ok "o ran the file viewer with HERDR_FILE_VIEWER_OPEN"

term_keys board q
pass
