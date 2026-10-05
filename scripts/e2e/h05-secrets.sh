#!/usr/bin/env bash
# H5: a note holding an AWS key is refused; the key is neither stored nor echoed.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
run 0 on home herdr-desk setup --no-herdr
start_daemon home

# Built at run time so no scanner reads a key in this file. The value is made up.
KEY="AKIA""ABCDEFGHIJKLMNOP"

# The key is an argument, so these two calls are made without `run`, which would print it.
rc=0
on home herdr-desk note "the deploy key is $KEY for now" >"$E2E/.out" 2>"$E2E/.err" || rc=$?
OUT=$(cat "$E2E/.out")
ERR=$(cat "$E2E/.err")
say "note with a key: exit=$rc stderr: $ERR"
[ "$rc" = 1 ] || fail "a note with a key exited $rc, want 1"
err_has "secret-detected"
err_has "aws-access-key"
any_lacks "$KEY"

rc=0
on home herdr-desk add -t "rotate $KEY" --desk >"$E2E/.out" 2>"$E2E/.err" || rc=$?
OUT=$(cat "$E2E/.out")
ERR=$(cat "$E2E/.err")
say "add with a key: exit=$rc stderr: $ERR"
[ "$rc" = 1 ] || fail "an add with a key exited $rc, want 1"
err_has "secret-detected"
any_lacks "$KEY"
say "key not echoed"

run 0 on home herdr-desk note "a clean note"
if grep -a -q "$KEY" "$E2E"/home/data/herdr-desk/desk.db*; then fail "the key is in the store"; fi
say "key not in store"

stop_daemon home
write_config home <<TOML
[secret_scan]
command = ["$E2E/no-such-scanner"]
TOML
start_daemon home
run 3 on home herdr-desk note "the scanner cannot start"
err_has "scan-failed"

stop_daemon home
printf '#!/bin/sh\ncat >/dev/null\nexit 1\n' >"$E2E/always-hit.sh"
chmod +x "$E2E/always-hit.sh"
write_config home <<TOML
[secret_scan]
command = ["$E2E/always-hit.sh"]
TOML
start_daemon home
run 1 on home herdr-desk note "the scanner says no"
err_has "secret-detected"
pass
