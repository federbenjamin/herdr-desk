#!/bin/sh
# checks.sh: the one place desk's checks live. It runs go vet, the coverage gate, the gofmt check,
# and then shellcheck, stopping at the first that fails. With --no-coverage it runs go test in place
# of the coverage gate.
set -eu

coverage=1
case "${1:-}" in
  "") ;;
  --no-coverage) coverage=0 ;;
  *)
    echo "usage: sh scripts/checks.sh [--no-coverage]" >&2
    exit 2
    ;;
esac

cd "$(dirname "$0")/.."

go vet ./...
if [ "$coverage" -eq 1 ]; then
  sh scripts/coverage.sh
else
  go test ./...
fi
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  echo "checks: gofmt would change:" >&2
  echo "$unformatted" >&2
  exit 1
fi
shellcheck scripts/*.sh scripts/e2e/*.sh
