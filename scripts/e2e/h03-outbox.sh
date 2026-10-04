#!/usr/bin/env bash
# H3: a note written on an offline client succeeds and reaches the home when it returns.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
PORT=$(free_port)
home_with_listen home "$PORT"
make_client cli home "$PORT"
stop_daemon home

run 0 as_agent cli s-h3 desk note "written while offline"
[ "$OUT" = "queued" ] || fail "an offline note printed '$OUT', want queued"
run 0 as_agent cli s-h3 desk decide "decided while offline"
[ "$OUT" = "queued" ] || fail "an offline decision printed '$OUT', want queued"
[ -s "$E2E/cli/state/desk/outbox.jsonl" ] || fail "the outbox is empty"

start_daemon home
run 0 on cli desk list
[ ! -s "$E2E/cli/state/desk/outbox.jsonl" ] || fail "the outbox was not forwarded"

run 0 on home desk session s-h3 --md
out_has "written while offline"
out_has "decided while offline"
say "the home's view holds both lines"
pass
