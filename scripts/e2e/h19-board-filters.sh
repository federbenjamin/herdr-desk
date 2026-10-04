#!/usr/bin/env bash
# H19: search, the project and thread filters, the done drawer, and the keys overlay.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
mkdir -p "$E2E/work/alpha" "$E2E/work/beta"
run 0 on home desk add -t "apple in alpha" -p "$E2E/work/alpha" --thread red
run 0 on home desk add -t "banana in beta" -p "$E2E/work/beta" --thread blue
run 0 on home desk add -t "cherry without a project" --desk --thread red
run 0 on home desk add -t "durian is finished" --desk
run 0 on home desk set 4 done

term_start board 100 30 home "$BIN/desk"
term_wait board "apple in alpha"
term_has board "banana in beta" || fail "the board lacks banana at the start"
term_has board "durian is finished" && fail "the done task shows before d"

# header <name>: the first line of the screen.
header() { term_screen "$1" | head -n 1; }

term_keys board /
term_wait board "search:"
term_type board "banana"
term_wait_gone board "apple in alpha"
term_keys board Enter
term_has board "banana in beta" || fail "the matching row is gone"
term_has board "cherry without a project" && fail "a row that does not match is shown"
ok "/ hides the rows that do not match"

term_esc board
term_wait board "apple in alpha"
term_has board "cherry without a project" || fail "esc did not bring the rows back"
ok "esc clears the search"

# only <name> <shown-title> <hidden-title...>: the screen shows the first and none of the rest.
only() {
  local name=$1 shown=$2
  shift 2
  term_wait "$name" "$shown"
  for t in "$@"; do
    term_has "$name" "$t" && fail "'$t' is shown beside '$shown'"
  done
  return 0
}

term_keys board p
term_wait_gone board "desk  all"
case "$(header board)" in
  *"desk  alpha ▾"*) only board "apple in alpha" "banana in beta" "cherry without a project" ;;
  *"desk  beta ▾"*) only board "banana in beta" "apple in alpha" "cherry without a project" ;;
  *) fail "p left the header as '$(header board)'" ;;
esac
ok "p shows one project's tasks"
for _ in 1 2 3 4; do
  case "$(header board)" in *"desk  all ▾"*) break ;; esac
  term_keys board p
  sleep 0.3
done
case "$(header board)" in *"desk  all ▾"*) ;; *) fail "p did not come back to all: '$(header board)'" ;; esac

term_keys board t
term_wait_gone board "thread: all"
case "$(header board)" in
  *"thread: red ▾"*) only board "apple in alpha" "banana in beta" ;;
  *"thread: blue ▾"*) only board "banana in beta" "apple in alpha" "cherry without a project" ;;
  *) fail "t left the header as '$(header board)'" ;;
esac
ok "t shows one thread's tasks"
for _ in 1 2 3 4; do
  case "$(header board)" in *"thread: all ▾"*) break ;; esac
  term_keys board t
  sleep 0.3
done
case "$(header board)" in *"thread: all ▾"*) ;; *) fail "t did not come back to all: '$(header board)'" ;; esac

term_keys board d
term_wait board "DONE"
term_wait board "durian is finished"
ok "d shows DONE with the done task"
term_keys board d
term_wait_gone board "DONE"
term_has board "durian is finished" && fail "the done task stays after the second d"
ok "d again hides it"

term_keys board "?"
term_wait board "KEYS"
ok "? shows KEYS"

term_keys board q
pass
