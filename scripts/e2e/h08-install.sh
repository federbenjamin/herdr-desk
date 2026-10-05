#!/usr/bin/env bash
# H8: the install script installs a checksum-verified prebuilt binary, falls back to a source
# build on a bad checksum or a missing release, and says so clearly when it can do neither.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build

V=9.9.9
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  arm64 | aarch64) ARCH=arm64 ;;
  x86_64 | amd64) ARCH=amd64 ;;
  *) fail "unmapped arch $(uname -m)" ;;
esac
ASSET="herdr-desk_${V}_${OS}_${ARCH}.tar.gz"
REL="$E2E/rel/v$V"
mkdir -p "$REL"
tar -czf "$REL/$ASSET" -C "$BIN" herdr-desk
(cd "$REL" && shasum -a 256 "$ASSET" >checksums.txt)

# A PATH with no herdr-desk on it, so the script must install one: every folder that holds a herdr-desk is
# dropped, the test's own and any the machine has installed.
CLEAN_PATH=$(tr ':' '\n' <<<"$PATH" | while read -r d; do [ -x "$d/herdr-desk" ] || printf '%s\n' "$d"; done | paste -sd: -)

# install <name> <base-url> [extra env...]: run the script with its own output folders.
install() {
  local name=$1 base=$2
  shift 2
  run "${WANT:-0}" env PATH="$CLEAN_PATH" DESK_REPO_ROOT="$REPO" DESK_VERSION="$V" DESK_BASE_URL="$base" \
    DESK_OUT="$E2E/$name/out/herdr-desk" DESK_INSTALL_DIR="$E2E/$name/inst" "$@" sh "$REPO/scripts/fetch-or-build.sh"
}

install pre "file://$E2E/rel"
[ -x "$E2E/pre/out/herdr-desk" ] || fail "no binary at the out path"
[ -x "$E2E/pre/inst/herdr-desk" ] || fail "no binary in the install dir"
cmp -s "$E2E/pre/out/herdr-desk" "$BIN/herdr-desk" || fail "the installed binary is not the released one"
run 0 "$E2E/pre/inst/herdr-desk" version
say "prebuilt ok"

mkdir -p "$E2E/bad/v$V"
cp "$REL/$ASSET" "$E2E/bad/v$V/"
printf '%064d  %s\n' 0 "$ASSET" >"$E2E/bad/v$V/checksums.txt"
install mis "file://$E2E/bad"
case "$OUT$ERR" in *"checksum"*) ;; *) fail "no word about the checksum mismatch" ;; esac
run 0 "$E2E/mis/out/herdr-desk" version
case "$OUT" in *"herdr-desk $V+src"*) ;; *) fail "a source build does not say it is one: $OUT" ;; esac
say "checksum mismatch fell back ok"

mkdir -p "$E2E/empty"
install src "file://$E2E/empty"
run 0 "$E2E/src/out/herdr-desk" version
case "$OUT" in *"herdr-desk $V+src"*) ;; *) fail "a source build does not say it is one: $OUT" ;; esac
say "source build ok"

WANT=1 install none "file://$E2E/empty" DESK_GO="$E2E/no-such-go"
case "$OUT$ERR" in *"go"*) ;; *) fail "the error does not name go" ;; esac
[ ! -e "$E2E/none/out/herdr-desk" ] || fail "a binary appeared with no release and no go"
say "no go, no release: clear error ok"
pass
