#!/usr/bin/env bash
# H6: a session's view hides and shows lines as the journal rules say.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
run 0 on home herdr-desk setup --no-herdr
start_daemon home
S="s-h6"

compact() {
  run_in 0 "{\"session_id\":\"$S\",\"source\":\"compact\",\"hook_event_name\":\"SessionStart\",\"cwd\":\"/\"}" \
    on home herdr-desk hook start --format claude-code
}
view() { run 0 on home herdr-desk session "$S" --md "$@"; }

run 0 as_agent home "$S" herdr-desk note "alpha fact"
run 0 as_agent home "$S" herdr-desk note "built the x branch" --branch quick/x
run 0 as_agent home "$S" herdr-desk note "is the cache safe" --tag question
run 0 as_agent home "$S" herdr-desk add -t "session todo" --desk
run 0 as_agent home "$S" herdr-desk add -t "branch todo" --desk --branch quick/x
run 0 as_agent home "$S" herdr-desk add -t "still open todo" --desk
run 0 as_agent home "$S" herdr-desk decide "pick jsonl files"
OLD=$OUT
run 0 as_agent home "$S" herdr-desk decide "pick one sqlite file" --replaces "$OLD" --tag principle:9
run 0 on home herdr-desk set T1 "done"

view
out_has "alpha fact"
out_has "- [x] [session] T1 session todo"
out_has "pick jsonl files"
out_has "#principle:9"
say "view before any compaction ok"

compact
view
out_has "alpha fact"
out_has "built the x branch"
out_lacks "session todo"
out_lacks "pick jsonl files"
out_has "pick one sqlite file"
out_has "still open todo"
out_has "[session] compacted"
say "view after one compaction ok"

run 0 as_agent home "$S" herdr-desk note "gamma fact"
compact
view
out_lacks "alpha fact"
out_has "gamma fact"
out_has "is the cache safe"
out_has "built the x branch"
out_has "branch todo"
say "view after two compactions ok"

run 0 on home herdr-desk set T2 "done"
run 0 as_agent home "$S" herdr-desk note --merged --branch quick/x --pr 7 --sha abc1234
view
out_lacks "built the x branch"
out_lacks "branch todo"
out_has "[quick/x] merged #7 (abc1234)"
out_has "still open todo"
say "view after merge ok"

view --all
out_has "alpha fact"
out_has "built the x branch"
out_has "session todo"
out_has "branch todo"
out_has "pick jsonl files"
say "--all shows hidden lines ok"
pass
