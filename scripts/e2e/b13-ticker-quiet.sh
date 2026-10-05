#!/usr/bin/env bash
# H34: with no live run and no pane left open, a minute of the ticker makes no herdr call. The ticker ticks at once and
# again after a minute, so 65 seconds hold two ticks. The fake herdr logs every call it gets.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
runner_up home
run 0 on home herdr-desk add -t "nothing runs" --desk
ticker_up home
[ "$(on home herdr-desk runs --json)" = "[]" ] || fail "a run is live"
CALLS="$E2E/herdr/calls.log"

sleep 65
ticker_running home || fail "the ticker is not running after 65 s"
[ ! -s "$CALLS" ] || fail "the ticker called herdr: $(cat "$CALLS")"
ok "no herdr call in 65 s"
pass
