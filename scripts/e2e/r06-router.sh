#!/usr/bin/env bash
# H6 (runner): the router is given the task, the roots, and the models, and only a valid answer routes.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
REPO_ROOT="$WORK/shop"
make_repo "$REPO_ROOT"
add_root "$REPO_ROOT" "" "the shop app"
RC_CAP=3
runner_up home

# A good answer: the root, in-place, a model.
route_to "$REPO_ROOT" in-place opus "the task is about the shop app"
run 0 on home desk add -t "Fix the cart total" -n "It adds the tax twice." -p "$REPO_ROOT" --thread agent
run 0 on home desk steps T1 add "reproduce it"
run 0 on home desk set T1 ready
wait_run 1 running
wait_file "$STUB/router-stdin.json"
jq -e --arg root "$REPO_ROOT" --arg scratch "$SCRATCH" '
  .task.number == 1 and .task.title == "Fix the cart total" and .task.notes == "It adds the tax twice."
  and .task.project == $root and .task.steps[0].text == "reproduce it"
  and ([.roots[].path] == [$root, $scratch]) and .roots[0].about == "the shop app" and .roots[1].isolation == "in-place"
  and .models == ["sonnet", "opus"]' "$STUB/router-stdin.json" >/dev/null ||
  fail "the router's stdin is not task, roots, models: $(cat "$STUB/router-stdin.json")"
say "stdin holds task, roots, models ok"

SCHEMA=$(sed -n 2p "$STUB/router-argv.txt")
jq -e --arg root "$REPO_ROOT" --arg scratch "$SCRATCH" '
  .properties.root.enum == [$root, $scratch] and .properties.model.enum == ["sonnet", "opus"]' <<<"$SCHEMA" >/dev/null ||
  fail "the schema argument lacks the root and model enums: $SCHEMA"
say "schema holds the root and model enums ok"
[ -s "$(sed -n 1p "$STUB/router-argv.txt")" ] || fail "the system prompt file {system} is missing or empty"
[ "$(cat "$STUB/router-hooks.txt")" = off ] || fail "DESK_HOOKS is '$(cat "$STUB/router-hooks.txt")' in the router"
say "DESK_HOOKS=off ok"

[ "$(task_field 1 root)" = "$REPO_ROOT" ] && [ "$(task_field 1 isolation)" = in-place ] && [ "$(task_field 1 model)" = opus ] ||
  fail "the route is not on the task: $(on home desk show T1 --json | jq -c .task)"
task_has_note 1 "routed to $REPO_ROOT (in-place, opus): the task is about the shop app" || fail "no 'routed to' note"
say "route saved on the task ok"
run 0 on home desk set T1 review

# Answers the runner must refuse. Each one blocks its task and fails its run.
refused() {
  local n=$1 want=$2
  wait_task "$n" blocked
  task_has_note "$n" "router: " || fail "T$n has no 'router: ' note"
  wait_run "$((n))" failed
  say "$want → blocked ok"
}
printf 'this is not json' >"$STUB/router-out.json"
arm_task home "bad answer one" --desk
refused 2 "not JSON"

route_to "/nowhere/else" in-place opus "a root that is not listed"
arm_task home "bad answer two" --desk
refused 3 "unknown root"

route_to "$REPO_ROOT" in-place opus "valid, but the router failed"
printf '1' >"$STUB/router-exit"
arm_task home "bad answer three" --desk
refused 4 "exit 1"
rm "$STUB/router-exit"
say "run failed ok"

# Fields set on the task: the router is not called.
CALLS=$(wc -l <"$STUB/router-calls.txt")
run 0 on home desk add -t "decided by hand" --desk --thread agent
run 0 on home desk set T5 --root "$REPO_ROOT" --isolation in-place --model sonnet
run 0 on home desk set T5 ready
wait_run 5 running
[ "$(wc -l <"$STUB/router-calls.txt")" -eq "$CALLS" ] || fail "the router was called for a task whose fields were set"
say "fields set: router not called ok"

# No router: the state shows in daemon.json and in status, and the owner is told once. The count is taken once T5's
# spawn has sent its start notification, its last step, so no spawn still in flight can add to it.
wait_long 10 "T5's start notification" grep -F "desk: T5 started" "$E2E/herdr/notifications.log"
run 0 on home desk set T5 review
NOTIFIED=$(wc -l <"$E2E/herdr/notifications.log")
RC_ROUTER=none
runner_config home
restart_daemon
wait_long 5 "daemon.json to say no-router" test "$(jq -r .runner "$E2E/home/state/desk/daemon.json")" = no-router
run 0 on home desk daemon status
jq -e '.runner_state == "no-router"' <<<"$OUT" >/dev/null || fail "status says $(jq -r .runner_state <<<"$OUT")"
run 0 on home desk runner
out_has "runner no-router"
say "no-router in daemon.json and status ok"
sleep 3
[ "$(wc -l <"$E2E/herdr/notifications.log")" -eq "$((NOTIFIED + 1))" ] ||
  fail "no-router was notified $(($(wc -l <"$E2E/herdr/notifications.log") - NOTIFIED)) times: $(tail -n 3 "$E2E/herdr/notifications.log")"
tail -n 1 "$E2E/herdr/notifications.log" | grep -F "started" >/dev/null && fail "the new notification is a spawn, not no-router"
say "no-router notified once ok"
pass
