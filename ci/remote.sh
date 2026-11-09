#!/usr/bin/env bash
# A tracked, immutable source bundle per run; only heavy execution shares a lock.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
. "$root/ci/select-image.sh"
cd "$root"
[ -f ci/remote.local.env ] && . ci/remote.local.env
. ci/remote-paths.sh
host="${VOPTRACK_CI_HOST:?set VOPTRACK_CI_HOST}"
key="${VOPTRACK_CI_KEY:?set VOPTRACK_CI_KEY}"
ssh_opts=(-i "$key" -o IdentitiesOnly=yes -o IdentityAgent=none -o BatchMode=yes)
while [ "$#" -gt 0 ]; do
 case "$1" in
  --steps) CI_STEPS="${2:?--steps needs gate names}"; shift 2;;
  --workers) CI_WORKERS="${2:?--workers needs 1 or 2}"; shift 2;;
  *) echo 'ci-remote: unknown argument' >&2; exit 2;;
 esac
done
python3 ci/runner.py --plan --steps "${CI_STEPS:-}" --workers "${CI_WORKERS:-2}" >/dev/null

[ -z "$(git status --porcelain)" ] || { echo 'ci-remote: commit changes first' >&2; exit 2; }
sha="$(git rev-parse HEAD)"
run="$(python3 -c 'import secrets; print(secrets.token_hex(6))')"
job="$ci_root/runs/$sha-$run"
proxy="$(mktemp -d)"
trap 'rm -rf "$proxy"' EXIT
./deploy/modproxy.sh "$proxy/modules" >/dev/null
# Include version tags/history for the append-only vocabulary guard, never local
# notes, ignored credentials, untracked files or another worktree's Git indirection.
git bundle create "$proxy/source.bundle" HEAD --tags
ssh -n "${ssh_opts[@]}" "$host" "umask 077; mkdir -p '$ci_root/runs'; test ! -e '$job'; mkdir -m700 '$job' '$job/modules'"
printf '{"owner":"voptrack-immutable-ci-v1","sha":"%s"}\n' "$sha" > "$proxy/owner.json"
scp -q "${ssh_opts[@]}" "$proxy/source.bundle" "$proxy/owner.json" ci/workspace.py "$host:$job/"
rsync -ac --link-dest="$ci_root/modules" --no-times --no-perms --no-owner --no-group -e "ssh ${ssh_opts[*]}" "$proxy/modules/" "$host:$job/modules/"
printf -v image_q '%q' "$VOPTRACK_IMAGE"
printf -v steps_q '%q' "${CI_STEPS:-}"
printf -v workers_q '%q' "${CI_WORKERS:-2}"
echo "ci-remote: immutable run ${sha:0:12}-$run"
set +e
ssh -n "${ssh_opts[@]}" "$host" "set -e; umask 077
git init -q '$job/tree'
git -C '$job/tree' fetch -q '$job/source.bundle' 'HEAD:refs/heads/candidate' '+refs/tags/*:refs/tags/*'
git -C '$job/tree' checkout -q --detach '$sha'
rm '$job/source.bundle'
cd '$job/tree'
set -a; . ci/shared-host.env; set +a
export IMAGE=$image_q VOPTRACK_IMAGE=$image_q CI_STEPS=$steps_q CI_WORKERS=$workers_q
export PATH=/usr/local/go/bin:\$PATH GOPROXY=file://$job/modules,https://proxy.golang.org GONOPROXY= GONOSUMDB='github.com/ekarpov/*' GOPRIVATE=
mkdir -p '$job/evidence'
set +e
flock '$ci_lock' ./ci/run.sh >'$job/runner.log' 2>&1
status=\$?
cat '$job/runner.log'
printf '%s\n' \$status >'$job/completed'
if [ -d '.ci/$sha' ]; then cp -a '.ci/$sha/.' '$job/evidence/'; fi
exit \$status"
rc=$?
set -e
mkdir -p ".ci/$sha"
printf '%s\n' "$job" > ".ci/$sha/remote-job-$run"
mkdir -p "$proxy/results"
rsync -a -e "ssh ${ssh_opts[*]}" "$host:$job/evidence/" "$proxy/results/" || { echo 'ci-remote: result retrieval failed' >&2; exit 1; }
scp -q "${ssh_opts[@]}" "$host:$job/runner.log" ".ci/$sha/runner-$run.log"
ssh -n "${ssh_opts[@]}" "$host" "python3 '$job/workspace.py' '$job' '$ci_root/runs'"
if [ ! -d "$proxy/results/runs" ]; then
  echo 'ci-remote: stopped before gate execution; runner log retained' >&2
  [ "$rc" -ne 0 ] || rc=1
  exit "$rc"
fi
cp -a "$proxy/results/." ".ci/$sha/"
# Only a full run has a canonical summary; partial results stay under runs/.
if [ -z "${CI_STEPS:-}" ]; then
  test -f ".ci/$sha/summary.txt"
  git notes --ref=ci add -f -F ".ci/$sha/summary.txt" "$sha"
  echo "ci-remote: full result recorded on ${sha:0:12}"
else
  echo 'ci-remote: partial result retained; full qualification unchanged'
fi
exit "$rc"
