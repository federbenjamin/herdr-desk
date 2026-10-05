#!/usr/bin/env bash
# H8: a config edit takes effect on the next command, with the ticker running and no restart.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
home_up home
ticker_up home
CONFIG="$E2E/home/config/herdr-desk/config.toml"

run 0 on home herdr-desk ticker status
jq -e '.runner_on == false' <<<"$OUT" >/dev/null || fail "the runner is on before the edit: $OUT"
ok "runner off"

sed -i.bak 's/^enabled = false/enabled = true/' "$CONFIG"
grep -q '^enabled = true' "$CONFIG" || fail "the edit did not change the config"
run 0 on home herdr-desk ticker status
jq -e '.runner_on == true' <<<"$OUT" >/dev/null || fail "the runner is off after the edit: $OUT"
ok "after the edit, runner on"

mkdir "$E2E/new-root"
write_config home <<TOML
[runner]
enabled = true

[[roots]]
path = '$E2E/new-root'
about = 'added by hand'
TOML
run 0 on home herdr-desk roots --json
jq -e --arg p "$E2E/new-root" 'map(.Path) | index($p) != null' <<<"$OUT" >/dev/null || fail "the new root is not listed: $OUT"
ok "a new root shows in roots at once"
pass
