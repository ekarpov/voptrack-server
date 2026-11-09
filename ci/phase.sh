# Only static phase labels and numeric elapsed/status reach the metrics file.
ci_phase() {
  local label="$1" start=$SECONDS rc=0
  shift
  python3 "$root/ci/timing.py" "$label-start" 0 0
  "$@" || rc=$?
  python3 "$root/ci/timing.py" "$label" "$((SECONDS-start))" "$rc"
  return "$rc"
}
