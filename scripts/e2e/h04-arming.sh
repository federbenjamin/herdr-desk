#!/usr/bin/env bash
# H4: an agent cannot set ready or done; a person can; agents_may_arm lifts ready only.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
run 0 on home herdr-desk setup --no-herdr
start_daemon home

run 0 on home herdr-desk add -t "arm me" --desk --thread agent
run 1 as_agent home s-h4 herdr-desk set T1 ready
err_has "not-allowed"
run 1 as_agent home s-h4 herdr-desk set T1 "done"
err_has "not-allowed"
run 1 as_agent home s-h4 herdr-desk add -t "self armed" --desk --status ready
err_has "not-allowed"
run 0 as_agent home s-h4 herdr-desk set T1 review
run 0 on home herdr-desk set T1 ready
run 0 on home herdr-desk list --json
jq -e '.tasks[0].status == "ready"' <<<"$OUT" >/dev/null || fail "a person's ready did not stick"

stop_daemon home
write_config home <<'TOML'
[runner]
agents_may_arm = true
TOML
say "config change: agents_may_arm = true"
start_daemon home

run 0 on home herdr-desk set T1 open
run 0 as_agent home s-h4 herdr-desk set T1 ready
say "agent ready allowed with agents_may_arm"
run 1 as_agent home s-h4 herdr-desk set T1 "done"
err_has "not-allowed"
pass
