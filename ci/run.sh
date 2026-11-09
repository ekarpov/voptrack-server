#!/usr/bin/env bash
# Local CI: every gate, in the order the GitHub workflow ran them, on this
# machine. GitHub Actions is off for this repository (the owner's decision,
# 2026-10-07: it is not free), so this is what "CI passed" means now.
#
#   ci/run.sh          run everything against HEAD; record the result as a git note
#
# It refuses a dirty working tree, so a result always belongs to one commit.
# Every step runs even after one fails (the table says which), except that
# nothing after a failed image build means anything, so that stops the run.
# Logs: .ci/<commit>/<step>.log, the last five commits kept. The result: a
# note on the commit under refs/notes/ci. Run before every release tag and
# before moving the Tessera pin; nothing enforces it (the owner's choice).
#
# Shares Docker and the disk with everything else on this machine. The app
# team's research-dev and home-dev are left alone: the dev-server steps run
# disposable twins with their own projects, state, ports and networks.
set -uo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
. "$root/ci/select-image.sh"
cd "$root"

# Fail closed: a git that cannot read the repository must not look clean.
status="$(git status --porcelain)" || { echo "ci: git cannot read this repository" >&2; exit 2; }
[ -z "$status" ] || { echo "ci: the working tree has changes; commit or stash them first" >&2; exit 2; }
free=$(df -Pk / | awk 'NR==2 {print int($4 / 1048576)}') # GiB, on macOS and Linux alike
[ "$free" -ge 15 ] || { echo "ci: only ${free} GiB free; need 15" >&2; exit 2; }
lock="$(git rev-parse --path-format=absolute --git-common-dir)/ci.lock"
mkdir "$lock" 2>/dev/null || { echo "ci: another run holds $lock" >&2; exit 2; }
trap 'rm -rf "$lock"' EXIT

sha="$(git rev-parse --verify HEAD)" && [ -n "$sha" ] || { echo "ci: no commit to test" >&2; exit 2; }
logs="$root/.ci/$sha"
mkdir -p "$logs"
export VOPTRACK_STORAGE_ENCRYPTED=yes # synthetic data in disposable twins, destroyed after each step
# Defaults for this Mac; ci/shared-host.env overrides them on the shared host.
: "${VOPTRACK_HOME_DEV_PORT:=8644}" "${VOPTRACK_HOME_DEV_HTTP_PORT:=8680}" "${VOPTRACK_HOME_DEV_CONSOLE_PORT:=9446}" "${VOPTRACK_HOME_DEV_NET:=10.117.50}"
: "${VOPTRACK_RESEARCH_DEV_PORT:=8744}" "${VOPTRACK_RESEARCH_DEV_HTTP_PORT:=8780}" "${VOPTRACK_RESEARCH_DEV_CONSOLE_PORT:=9447}" "${VOPTRACK_RESEARCH_DEV_NET:=10.117.51}"
export VOPTRACK_HOME_DEV_PROJECT=voptrack-ci-home-dev VOPTRACK_HOME_DEV_STATE="$logs/home-dev" \
  VOPTRACK_HOME_DEV_PORT VOPTRACK_HOME_DEV_HTTP_PORT VOPTRACK_HOME_DEV_CONSOLE_PORT VOPTRACK_HOME_DEV_NET
export VOPTRACK_RESEARCH_DEV_PROJECT=voptrack-ci-research-dev VOPTRACK_RESEARCH_DEV_STATE="$logs/research-dev" \
  VOPTRACK_RESEARCH_DEV_PORT VOPTRACK_RESEARCH_DEV_HTTP_PORT VOPTRACK_RESEARCH_DEV_CONSOLE_PORT VOPTRACK_RESEARCH_DEV_NET

home_dev() {
  state="$VOPTRACK_HOME_DEV_STATE"
  ./release/household/dev.sh up && ./release/household/dev.sh token && python3 -c "import json,sys; d=json.load(open(sys.argv[1]+'/home.json')); m=d['members']; assert set(m)=={'sara','ben','lina'} and m['lina']['suspended'] and not m['lina']['shares'] and d['expected_household']['home spending total']['All']=='113.05'" "$state"
  rc=$?; ./release/household/dev.sh down >/dev/null 2>&1; return $rc
}
research_dev() {
  state="$VOPTRACK_RESEARCH_DEV_STATE"
  ./release/research/dev.sh up && ./release/research/dev.sh token && python3 -c "import json,sys; ds=[json.load(open(sys.argv[1]+'/'+n)) for n in ('participant.json','participant-2.json')]; assert all([t['id'] for t in d['installed_types']]==['com.voptrack.subject','com.voptrack.source','com.voptrack.track','study.app_roundtrip.sleep','com.voptrack.track.weight'] and d['refresh_token'] for d in ds) and ds[0]['dataset']!=ds[1]['dataset']" "$state"
  rc=$?; ./release/research/dev.sh down >/dev/null 2>&1; return $rc
}
scenario() { ./scenarios/"$1"/scenario.sh init && ./scenarios/"$1"/scenario.sh verify; rc=$?; ./scenarios/"$1"/scenario.sh down >/dev/null 2>&1; return $rc; }
evaluation() { make try; rc=$?; make try-down >/dev/null 2>&1; return $rc; }

build_image() { make image IMAGE="${VOPTRACK_IMAGE}"; }
# Keep synthetic recovery inputs/partial outputs outside the checkout and normal
# rotating logs. The guided gate consumes exactly the preceding retained sets.
recovery_sets() {
  mkdir -p "$HOME/.local/state/voptrack-ci"
  recovery_root="$(mktemp -d "$HOME/.local/state/voptrack-ci/recovery.XXXXXXXX")" || return 1
  chmod 700 "$recovery_root"
  printf '%s\n' "$recovery_root" > "$logs/recovery-root"
  VOPTRACK_RECOVERY_SET_DIR="$recovery_root/retained" make recovery-set-check
}
guided_recovery() {
  [ -s "$logs/recovery-root" ] || return 1
  recovery_root="$(cat "$logs/recovery-root")"
  VOPTRACK_GUIDED_INPUTS="$recovery_root/retained" VOPTRACK_GUIDED_DIR="$recovery_root/guided" make guided-recovery-check
}


off_machine_backups() {
  [ -s "$logs/recovery-root" ] || return 1
  recovery_root="$(cat "$logs/recovery-root")"
  VOPTRACK_OFF_MACHINE_INPUTS="$recovery_root/retained" VOPTRACK_OFF_MACHINE_DIR="$recovery_root/off-machine" make off-machine-backups-check
}


# The scheduler owns result publication. Functions stay in this shell so existing
# gate commands, cleanup and fixture setup retain their established semantics.
export root logs
export -f home_dev research_dev scenario evaluation build_image recovery_sets guided_recovery off_machine_backups
python3 "$root/ci/runner.py" "$@"
