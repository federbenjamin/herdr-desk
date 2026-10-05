#!/usr/bin/env bash
# H9: the release config builds the four binaries, their checksums, and the brew cask.
# Needs the network once: goreleaser itself is fetched through `go run`.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

DIST="$REPO/dist"
trap 'rm -rf "$DIST"; cleanup' EXIT

(cd "$REPO" && go run github.com/goreleaser/goreleaser/v2@latest check) || fail "goreleaser check"
(cd "$REPO" && go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean --skip=publish) ||
  fail "goreleaser snapshot release"

for target in darwin_arm64 darwin_amd64 linux_arm64 linux_amd64; do
  found=$(find "$DIST" -maxdepth 1 -name "herdr-desk_*_${target}.tar.gz" | head -n 1)
  [ -n "$found" ] || fail "no archive for $target"
  say "archive: $(basename "$found")"
  grep -q "$(basename "$found")" "$DIST/checksums.txt" || fail "checksums.txt lacks $(basename "$found")"
done
say "checksums.txt: $(wc -l <"$DIST/checksums.txt" | tr -d ' ') lines"

cask=$(find "$DIST" -name 'herdr-desk.rb' | head -n 1)
[ -n "$cask" ] || fail "no rendered cask"
grep -q 'cask "herdr-desk" do' "$cask" || fail "the rendered file is not a herdr-desk cask"
say "cask: ${cask#"$DIST"/}"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  arm64 | aarch64) ARCH=arm64 ;;
  *) ARCH=amd64 ;;
esac
mkdir -p "$E2E/x"
tar -xzf "$(find "$DIST" -maxdepth 1 -name "herdr-desk_*_${OS}_${ARCH}.tar.gz" | head -n 1)" -C "$E2E/x"
run 0 "$E2E/x/herdr-desk" version
case "$OUT" in "herdr-desk dev") fail "the release build did not set the version" ;; herdr-desk\ *) ;; *) fail "version printed '$OUT'" ;; esac
pass
