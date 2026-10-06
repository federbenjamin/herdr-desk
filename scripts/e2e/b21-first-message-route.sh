#!/usr/bin/env bash
# H3 (route): a run's first_message is run start's --first-message, else the task's field, else the root's; a task with
# none starts on plain text with the hand-back; a template run's task file has no hand-back; a template without
# {task_file} is bad-input.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
PROJ="$WORK/proj"
mkdir -p "$PROJ"
add_root "$PROJ" self "a root with a template" 'first_message = "/root {task_file}"'
RC_CAP=3
runner_up home
RUNS="$E2E/home/state/herdr-desk/runs"
HANDBACK="hand the task back"

# message_starts <run id> <prefix>: the stub worker of that run was started with a message that begins with prefix.
message_starts() {
  local file="$STUB/worker-run$1.message"
  wait_file "$file"
  case "$(cat "$file")" in "$2"*) ;; *) fail "run $1's message is '$(cat "$file")', want it to start '$2'" ;; esac
}

# end_run <run id>: the run's worker blocks the task, which ends the run.
end_run() {
  run 0 as_agent home "$(run_field "$1" session)" env DESK_RUN="$1" herdr-desk set T1 blocked
  run_is "$1" ended || fail "run $1 is $(run_field "$1" state) after its worker's blocked"
}

run 0 on home herdr-desk add -t "Ship the unit" -p "$PROJ"
run 0 on home herdr-desk set T1 --first-message "/task {task_file}"
run 0 on home herdr-desk run start T1 --first-message "/flag {task_file}"
wait_run 1 running
message_starts 1 "/flag $RUNS/run-1.md"
ok "flag wins"
end_run 1

run 0 on home herdr-desk run start T1
wait_run 2 running
message_starts 2 "/task $RUNS/run-2.md"
ok "task field wins over the root"
grep -F -- "You are working on desk task T1" "$RUNS/run-2.md" >/dev/null || fail "run 2's task file lacks the task"
if grep -F -- "$HANDBACK" "$RUNS/run-2.md" >/dev/null; then fail "run 2's task file holds the hand-back"; fi
ok "task file has no hand-back"
end_run 2

run 0 on home herdr-desk set T1 --first-message ''
[ "$(task_field 1 first_message)" = "" ] || fail "T1's first_message is '$(task_field 1 first_message)' after --first-message ''"
run 0 on home herdr-desk run start T1
wait_run 3 running
message_starts 3 "/root $RUNS/run-3.md"
[ "$(task_field 1 first_message)" = "" ] || fail "a run on the root's template wrote it onto T1"
ok "cleared field falls back to the root"

run 0 on home herdr-desk add -t "Plain text" --desk
run 0 on home herdr-desk run start T2
wait_run 4 running
message_starts 4 "You are working on desk task T2"
grep -F -- "$HANDBACK" "$STUB/worker-run4.message" >/dev/null || fail "run 4's plain message lacks the hand-back"
[ ! -e "$RUNS/run-4.md" ] || fail "a run with no template wrote a task file"
ok "no template: plain text with the hand-back"

run 0 on home herdr-desk add -t "Bad template" --desk
run 2 on home herdr-desk run start T3 --first-message "no placeholder"
err_has "bad-input"
[ -z "$(sqlite3 "$DB" "SELECT id FROM runs WHERE task = 3")" ] || fail "a refused template made a run"
ok "a template without {task_file}: bad-input"
pass
