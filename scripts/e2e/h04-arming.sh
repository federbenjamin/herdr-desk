#!/usr/bin/env bash
# H22: an add is ready unless --status says otherwise; an agent may add or set ready and set done, whatever start_runs says.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
home_up home

# expect_status <task> <status> <what failed>
expect_status() {
  run 0 on home herdr-desk show "$1" --json
  jq -e --arg s "$2" '.task.status == $s' <<<"$OUT" >/dev/null || fail "$3"
}

run 0 on home herdr-desk add -t "person adds" --desk
expect_status T1 ready "a person's add did not default to ready"
run 0 as_agent home s-h4 herdr-desk add -t "agent adds" --desk
expect_status T2 ready "an agent's add did not default to ready"
run 0 on home herdr-desk add -t "kept open" --desk --status open
expect_status T3 open "--status open did not stick"
ok "add defaults to ready"

run 0 as_agent home s-h4 herdr-desk set T3 ready
expect_status T3 ready "an agent's ready did not stick"
run 0 as_agent home s-h4 herdr-desk set T2 "done"
expect_status T2 "done" "an agent's done did not stick"
ok "agent ready and done allowed"
pass
