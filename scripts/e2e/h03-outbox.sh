#!/usr/bin/env bash
# H5: a note written on an offline client succeeds, and reaches the home when it returns with the time it was
# written, not the time it arrived.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
home_up home
make_client cli home
home_down home

written=$(date +%s)
run 0 as_agent cli s-h3 herdr-desk note "written while offline"
[ "$OUT" = "queued" ] || fail "an offline note printed '$OUT', want queued"
run 0 as_agent cli s-h3 herdr-desk decide "decided while offline"
[ "$OUT" = "queued" ] || fail "an offline decision printed '$OUT', want queued"
[ -s "$E2E/cli/state/herdr-desk/outbox.jsonl" ] || fail "the outbox is empty"

sleep 3
home_back home
arrived=$(date +%s)
run 0 on cli herdr-desk list
[ ! -s "$E2E/cli/state/herdr-desk/outbox.jsonl" ] || fail "the outbox was not forwarded"

run 0 on home herdr-desk session s-h3 --md
out_has "written while offline"
out_has "decided while offline"
say "the home's view holds both lines"

ts=$(sqlite3 "$E2E/home/data/herdr-desk/desk.db" "select ts from events where kind = 'note' and session = 's-h3'")
epoch=$(python3 -c 'import sys; from datetime import datetime; print(int(datetime.fromisoformat(sys.argv[1].replace("Z", "+00:00")).timestamp()))' "$ts")
[ "$epoch" -ge $((written - 1)) ] && [ "$epoch" -le $((written + 1)) ] || fail "the note's ts is $ts (epoch $epoch), written at $written"
[ "$epoch" -lt $((arrived - 1)) ] || fail "the note's ts $ts is the time it arrived ($arrived)"
ok "the note's ts on the home is the time it was written, not the time it arrived"
pass
