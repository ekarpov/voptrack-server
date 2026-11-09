#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
: "${VOPTRACK_IMAGE:?select a verified immutable candidate image}"
: "${VOPTRACK_GUIDED_INPUTS:?retained synthetic Home and Research parent}"
: "${VOPTRACK_GUIDED_DIR:?new persistent private qualification directory}"
# No global pruning, automatic cleanup of failed targets, or remote operations.
python3 "$root/release/guided-recovery/gate.py" "$VOPTRACK_IMAGE" "$VOPTRACK_GUIDED_INPUTS" "$VOPTRACK_GUIDED_DIR"
