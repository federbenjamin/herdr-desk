#!/usr/bin/env bash
# H16 [real pair]: a task added on the client shows on the home, a second call within a minute takes at most 0.15 s
# more than a reused ssh of `true` between the same two machines (the pair's own floor), twelve adds from both machines give twelve numbers, and a client board's refresh with six blocked tasks takes
# under 3 s. Run on the home: E2E_CLIENT=<ssh target of the client> E2E_HOME=<target the client uses for this machine>
# bash scripts/e2e/p01-pair.sh
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
pair_up

run 0 cn add -t "added on the client" --desk
[ "$OUT" = T1 ] || fail "the client's add printed '$OUT', want T1"
run 0 on home herdr-desk list
out_has "added on the client"
ok "the home lists the client's task"

# The add above opened the connection the next two calls reuse (ControlPersist is 60 s). The floor is the same ssh
# command with `true` in place of `herdr-desk rpc`: what the pair's reused connection costs with no herdr-desk in it.
rssh "$CN_ENV python3 $RDIR/timed.py $RDIR/floor.txt $(floor_command)" || fail "the reused ssh of true failed"
FLOOR=$(rssh "cat $RDIR/floor.txt")
ctimed list
OVER=$(awk -v t="$CT" -v f="$FLOOR" 'BEGIN { printf "%.3f", t - f }')
say "the second call took $CT s, $OVER s over the ssh floor of $FLOOR s (at most 0.15 over)"
awk -v o="$OVER" 'BEGIN { exit !(o <= 0.15) }' || fail "the second call took $CT s, $OVER s over the ssh floor of $FLOOR s"
ok "the second call is within 0.15 s of the ssh floor"

adders=()
for i in 1 2 3 4 5 6; do
  on home herdr-desk add -t "from the home $i" --desk >"$E2E/add.home$i.out" 2>"$E2E/add.home$i.err" &
  adders+=("$!")
  cn add -t "from the client $i" --desk >"$E2E/add.cli$i.out" 2>"$E2E/add.cli$i.err" &
  adders+=("$!")
done
failed=0
for p in "${adders[@]}"; do
  wait "$p" || failed=$((failed + 1))
done
[ "$failed" = 0 ] || fail "$failed of 12 adds did not exit 0: $(cat "$E2E"/add.*.err)"
got=$(cat "$E2E"/add.*.out | sed 's/^T//' | sort -n | tr '\n' ' ')
want=$(seq 2 13 | tr '\n' ' ')
[ "$got" = "$want" ] || fail "the numbers are '$got', want '$want'"
ok "12 adds from both machines gave T2 to T13, each once"

# Six blocked tasks, so the refresh has six details to fetch.
for n in 2 3 4 5 6 7; do run 0 on home herdr-desk set "T$n" blocked; done
cat >"$E2E/refresh.py" <<'PY'
import concurrent.futures, subprocess, sys, time

# refresh.py <file> <herdr-desk> <task numbers...>: one refresh as the board makes it (board.go's refresh): the
# status, the list, the runs, then each task's detail at once. Writes the seconds to <file>.
out, bin_, tasks = sys.argv[1], sys.argv[2], sys.argv[3:]
def call(*args):
    subprocess.run([bin_, *args], stdout=subprocess.DEVNULL, check=True)
start = time.perf_counter()
call("ticker", "status")
call("list", "--json")
call("runs")
with concurrent.futures.ThreadPoolExecutor() as pool:
    list(pool.map(lambda n: call("show", "T" + n, "--json"), tasks))
open(out, "w").write("%.3f\n" % (time.perf_counter() - start))
PY
scp -q -o BatchMode=yes "$E2E/refresh.py" "$E2E_CLIENT:$RDIR/refresh.py" || fail "cannot copy the refresh timer"
rssh "$CN_ENV python3 $RDIR/refresh.py $RDIR/timed.txt $RDIR/herdr-desk 2 3 4 5 6 7" || fail "the refresh failed"
CT=$(rssh "cat $RDIR/timed.txt")
under "$CT" 3 || fail "one client board refresh with 6 blocked tasks took $CT s"
ok "one client board refresh with 6 blocked tasks took $CT s (under 3)"
pass
