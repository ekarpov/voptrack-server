#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
: "${VOPTRACK_IMAGE:?select a verified immutable image}"
: "${VOPTRACK_OFF_MACHINE_INPUTS:?retained synthetic Home/Research sets}"
: "${VOPTRACK_OFF_MACHINE_DIR:?fresh persistent qualification root}"
python3 "$root/release/off-machine/gate.py" "$VOPTRACK_IMAGE" "$VOPTRACK_OFF_MACHINE_INPUTS" "$VOPTRACK_OFF_MACHINE_DIR"
