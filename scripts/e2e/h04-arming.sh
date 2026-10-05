#!/usr/bin/env bash
# H22: an agent may set done; an agent's ready is refused unless start_runs = "auto"; a person's ready always sticks.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
home_up home

run 0 on home herdr-desk add -t "arm me" --desk
run 0 on home herdr-desk add -t "agent finishes this" --desk
run 1 as_agent home s-h4 herdr-desk set T1 ready
err_has "not-allowed"
run 1 as_agent home s-h4 herdr-desk add -t "self armed" --desk --status ready
err_has "not-allowed"
ok "agent ready refused not-allowed"
run 0 as_agent home s-h4 herdr-desk set T2 "done"
run 0 on home herdr-desk show T2 --json
jq -e '.task.status == "done"' <<<"$OUT" >/dev/null || fail "an agent's done did not stick"
ok "agent done allowed"
run 0 as_agent home s-h4 herdr-desk set T1 review
run 0 on home herdr-desk set T1 ready
run 0 on home herdr-desk list --json
jq -e '[.tasks[] | select(.number == 1)][0].status == "ready"' <<<"$OUT" >/dev/null || fail "a person's ready did not stick"

write_config home <<'TOML'
[coordinator]
start_runs = "auto"
TOML
say "config change: start_runs = auto"

run 0 on home herdr-desk set T1 open
run 0 as_agent home s-h4 herdr-desk set T1 ready
run 0 on home herdr-desk show T1 --json
jq -e '.task.status == "ready"' <<<"$OUT" >/dev/null || fail "an agent's ready did not stick with start_runs = auto"
ok "with start_runs = auto, agent ready allowed"
pass
