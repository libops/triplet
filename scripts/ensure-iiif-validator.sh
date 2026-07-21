#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VENV_DIR="${IIIF_VALIDATOR_VENV:-$ROOT_DIR/.cache/iiif-validator}"
VALIDATOR="$VENV_DIR/bin/iiif-validate.py"
PACKAGE_VERSION="${IIIF_VALIDATOR_VERSION:-1.0.5}"

check_runtime() {
  # The installed entry point imports python-magic before it processes CLI
  # arguments, so --help verifies both the Python package and native libmagic
  # without embedding Python source in this shell script.
  if "$VALIDATOR" --help >/dev/null 2>&1; then
    return 0
  fi

  cat >&2 <<'EOF'
iiif-validator is installed, but Python cannot load libmagic.

Install the native libmagic library, then rerun this command:
  macOS:         brew install libmagic
  Debian/Ubuntu: sudo apt-get install libmagic1
EOF
  exit 1
}

if [ -x "$VALIDATOR" ]; then
  check_runtime
  printf '%s\n' "$VALIDATOR"
  exit 0
fi

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required to install iiif-validator" >&2
  exit 1
fi

echo "Installing iiif-validator==$PACKAGE_VERSION into $VENV_DIR" >&2
if ! python3 -m venv "$VENV_DIR"; then
  cat >&2 <<'EOF'
Failed to create the Python virtualenv for iiif-validator.

On Debian/Ubuntu, install python3-venv. On macOS, install Python from Homebrew
or python.org. The validator also requires libmagic at runtime
(Ubuntu: libmagic1, macOS: brew install libmagic).
EOF
  exit 1
fi

"$VENV_DIR/bin/python" -m pip install --upgrade pip >&2
"$VENV_DIR/bin/python" -m pip install "iiif-validator==$PACKAGE_VERSION" >&2

if [ ! -x "$VALIDATOR" ]; then
  echo "iiif-validator installed but $VALIDATOR was not created" >&2
  exit 1
fi

check_runtime
printf '%s\n' "$VALIDATOR"
