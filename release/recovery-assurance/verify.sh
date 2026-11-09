#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
: "${VOPTRACK_IMAGE:?select the verified immutable rc.2 image}"
: "${VOPTRACK_ASSURANCE_DIR:?fresh persistent private qualification root}"
python3 "$root/release/recovery-assurance/gate.py" "$VOPTRACK_IMAGE" "$VOPTRACK_ASSURANCE_DIR"
