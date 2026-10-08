#!/usr/bin/env bash
# H41: `context` prints the desk: start_runs, the caps and today's count, the roots with their about (scratch last), the
# models, the board by section, the live runs, and what changed since the cursor. The cursor moves only when the
# recorded coordinator's session reads it.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
APP="$WORK/app"
DOCS="$WORK/docs"
mkdir -p "$APP" "$DOCS"
add_root "$APP" in-place "the web app"
add_root "$DOCS" self "the written docs"
RC_CAP=2
RC_DAY=7
runner_up home

run 0 on home herdr-desk add -t "needs an answer" --desk
run 0 on home herdr-desk set T1 blocked
run 0 on home herdr-desk add -t "runs now" --desk
run 0 on home herdr-desk run start T2 --root "$APP" --model sonnet
run 0 on home herdr-desk add -t "waits its turn" --desk
run 0 on home herdr-desk coordinator
COORD=$(sqlite3 "$DB" "SELECT session FROM coordinator")

run 0 on home herdr-desk context
out_has "start_runs propose"
ok "start_runs propose"
out_has "cap 2 · today 1 of max_runs_per_day 7"
ok "caps and today's count"
out_has "$APP  in-place  the web app"
out_has "$DOCS  self  the written docs"
LINES=$(grep -n -e "^  $APP " -e "^  $DOCS " -e "^  $SCRATCH " <<<"$OUT" | cut -d: -f2 | tr -s ' ' | cut -d' ' -f2 | tr '\n' ' ')
[ "$LINES" = "$APP $DOCS $SCRATCH " ] || fail "the roots are listed as '$LINES', not the roots in order and the scratch root last"
ok "roots with about, scratch last"
out_has "models: sonnet, opus"
ok "models"
POS_N=$(grep -n '^NEEDS YOU' <<<"$OUT" | cut -d: -f1)
POS_M=$(grep -n '^IN MOTION' <<<"$OUT" | cut -d: -f1)
POS_D=$(grep -n '^ON DECK' <<<"$OUT" | cut -d: -f1)
{ [ -n "$POS_N" ] && [ "$POS_N" -lt "$POS_M" ] && [ "$POS_M" -lt "$POS_D" ]; } || fail "the sections are not NEEDS YOU, IN MOTION, ON DECK in order"
sed -n "${POS_N},$((POS_M - 1))p" <<<"$OUT" | grep -F "needs an answer" >/dev/null || fail "T1 is not under NEEDS YOU"
sed -n "${POS_M},$((POS_D - 1))p" <<<"$OUT" | grep -F "runs now" >/dev/null || fail "T2 is not under IN MOTION"
sed -n "${POS_D},\$p" <<<"$OUT" | grep -F "waits its turn" >/dev/null || fail "T3 is not under ON DECK"
ok "NEEDS YOU, IN MOTION, ON DECK"
out_has "run 1  T2  running  $APP  in-place  sonnet"
ok "live runs"

# A person's context shows the changes and leaves the cursor; the coordinator's moves it.
[ "$(sqlite3 "$DB" "SELECT cursor FROM coordinator")" = 0 ] || fail "the cursor starts at $(sqlite3 "$DB" "SELECT cursor FROM coordinator")"
out_has "changes since e0:"
out_has "needs an answer"
LAST=$(sqlite3 "$DB" "SELECT max(id) FROM events")
[ "$(sqlite3 "$DB" "SELECT cursor FROM coordinator")" = 0 ] || fail "a person's context moved the cursor"
ok "a person's context leaves the cursor"

run 0 as_agent home "$COORD" herdr-desk context
out_has "changes since e0:"
out_has "needs an answer"
out_has "e$LAST "
[ "$(sqlite3 "$DB" "SELECT cursor FROM coordinator")" = "$LAST" ] || fail "the coordinator's context left the cursor at $(sqlite3 "$DB" "SELECT cursor FROM coordinator"), not $LAST"
ok "changes since the last context"

run 0 on home herdr-desk add -t "added after the read" --desk
run 0 as_agent home "$COORD" herdr-desk context
out_has "changes since e$LAST:"
out_has "added after the read"
section=$(sed -n '/^changes since/,$p' <<<"$OUT")
case "$section" in *"needs an answer"* | *"  e$LAST  "*) fail "the second context's changes repeat an old change" ;; *) ;; esac
ok "the coordinator's second context shows only what is new"
pass
