#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
: "${VOPTRACK_IMAGE:?select a verified immutable image}"
: "${VOPTRACK_HOME_SETUP_DIR:?fresh persistent qualification root}"
test ! -e "$VOPTRACK_HOME_SETUP_DIR"
mkdir -m 700 "$VOPTRACK_HOME_SETUP_DIR"
python3 "$root/release/home-setup/gate.py" "$VOPTRACK_IMAGE" "$VOPTRACK_HOME_SETUP_DIR/native"
python3 "$root/release/home-setup/compose-gate.py" "$VOPTRACK_IMAGE" "$VOPTRACK_HOME_SETUP_DIR/compose"
