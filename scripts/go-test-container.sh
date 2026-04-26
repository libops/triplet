#!/usr/bin/env bash

set -euo pipefail

export PATH="/usr/local/go/bin:$PATH"

flags=()
if [ -n "${GO_TEST_FLAGS:-}" ]; then
  # GO_TEST_FLAGS is intentionally split like a shell would split local
  # command-line flags; package patterns remain positional argv from test.sh.
  read -r -a flags <<<"$GO_TEST_FLAGS"
else
  flags=(-v -race)
fi

if [ "$#" -eq 0 ]; then
  set -- ./...
fi

exec /usr/local/go/bin/go test "${flags[@]}" "$@"
