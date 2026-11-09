#!/usr/bin/env bash
# Run only against a private retained test set on the selected recovery host.
set -euo pipefail
retained="${1:?}" image="${2:?}" task="${3:?}" expected="${4:?}"
test "$(docker run --rm --entrypoint tesserad "$image" -version)" = "$expected"
python3 - <<'PY'
import socket
with socket.socket() as stream:stream.bind(('127.0.0.1',30843))
PY
for variant in household research; do
 python3 release/recovery-set/drill.py "$retained/$variant" "$image" "$task-$variant" 30843
done
