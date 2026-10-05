#!/usr/bin/env bash
# H1: two notes saves that read the same text: the second is refused stale and the first's text stays. Then
# `edit --append-notes` keeps both lines when the notes change between its read and its write.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
home_up home
run 0 on home herdr-desk add -t "notes under test" -n "the base" --desk

term_start boardA 100 40 home "$BIN/herdr-desk"
term_start boardB 100 40 home "$BIN/herdr-desk"
for b in boardA boardB; do
  term_wait "$b" "notes under test"
  term_keys "$b" Enter
  term_wait "$b" "NOTES"
done
term_keys boardA e
term_type boardA " alpha"
term_keys boardB e
term_type boardB " bravo"

term_keys boardA C-s
wait_task home 1 .task.notes "the base alpha"
ok "board A saved its notes"

# Board B's save is refused against A's text; the refusal loads the task, so B's second ctrl+s replaces it with no
# refresh between.
term_keys boardB C-s
term_wait boardB "changed while you edited"
term_has boardB "bravo" || fail "board B lost its typed text"
[ "$(task_field home 1 .task.notes)" = "the base alpha" ] || fail "board B's refused save changed the notes"
ok "board B's save shows the notes changed and keeps its typed text"
ok "the task holds board A's text"

term_keys boardB C-s
wait_task home 1 .task.notes "the base bravo"
ok "a second ctrl+s on board B replaces it"

# A client whose home rewrites the notes right after the first read: the append is refused stale, reads again,
# and writes once more.
make_client cli home
cat >"$E2E/interfere.sh" <<SH
#!/bin/sh
req=\$(cat)
out=\$(printf '%s' "\$req" | "$REPO/scripts/e2e/ssh-shim.sh" "\$@") || exit \$?
printf '%s\n' "\$out"
case "\$req" in
*'"method":"tasks.get"'*)
  if [ ! -e "$E2E/interfered" ]; then
    : >"$E2E/interfered"
    XDG_CONFIG_HOME="$E2E/home/config" XDG_STATE_HOME="$E2E/home/state" XDG_DATA_HOME="$E2E/home/data" \\
      XDG_CACHE_HOME="$E2E/home/cache" herdr-desk edit 1 --notes "changed in between" >/dev/null
  fi
  ;;
esac
SH
chmod +x "$E2E/interfere.sh"
write_config cli <<TOML
[client]
home = "home"
command = ["$E2E/interfere.sh", "$E2E", "{home}", "herdr-desk", "rpc"]
TOML

run 0 on cli herdr-desk edit 1 --append-notes "the appended line"
[ -e "$E2E/interfered" ] || fail "the notes were never changed between the read and the write"
[ "$(task_field home 1 .task.notes)" = "$(printf 'changed in between\nthe appended line')" ] ||
  fail "the notes are '$(task_field home 1 .task.notes)'"
ok "edit --append-notes after a change retries once and both lines are kept"
pass
