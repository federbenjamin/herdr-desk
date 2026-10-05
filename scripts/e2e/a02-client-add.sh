#!/usr/bin/env bash
# H2: `client add <ssh target>` checks the home before it saves, and the token and the listener are gone.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
home_up home
run 0 on home herdr-desk add -t "the home's task" --desk

# A client machine that has the shim as its command and no home yet.
write_config cli <<TOML
[client]
command = ["$REPO/scripts/e2e/ssh-shim.sh", "$E2E", "{home}", "herdr-desk", "rpc"]
TOML
CONFIG="$E2E/cli/config/herdr-desk/config.toml"
before=$(cat "$CONFIG")

home_down home
run 3 on cli herdr-desk client add home
err_has "home-unreachable"
[ "$(cat "$CONFIG")" = "$before" ] || fail "client add with the home down changed the config"
grep -q '^home' "$CONFIG" && fail "client add with the home down saved a home"
ok "client add with the home down exits 3 and writes no config"

home_back home
run 0 on cli herdr-desk client add home
grep -Eq "^home = ['\"]home['\"]$" "$CONFIG" || fail "the config has no [client] home: $(cat "$CONFIG")"
grep -q '^command' "$CONFIG" || fail "client add dropped [client] command"
ok "client add with the home up writes [client] home"

run 0 on cli herdr-desk list
out_has "the home's task"
ok "the client lists the home's task"

write_config old <<TOML
[home]
listen = "127.0.0.1:7777"
TOML
run 2 on old herdr-desk list
err_has "unknown key home"
ok "a config holding [home] listen is refused with exit 2 naming the key"

run 2 on home herdr-desk token show
err_has "unknown command"
ok "herdr-desk token is an unknown command"
pass
