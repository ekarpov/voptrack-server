#!/usr/bin/env bash
# Explicit separate-host replay of private synthetic retained sets. No deployment upgrade.
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
retained="${1:?private retained parent containing household and research}"
image="${2:?immutable image digest}"
[[ "$image" =~ ^sha256:[0-9a-f]{64}$ ]] || exit 2
[ -f "$root/ci/remote.local.env" ] && . "$root/ci/remote.local.env"
. "$root/ci/remote-paths.sh"
: "${VOPTRACK_CI_HOST:?}" "${VOPTRACK_CI_KEY:?}"
ssh_opts=(-i "$VOPTRACK_CI_KEY" -o IdentitiesOnly=yes -o IdentityAgent=none -o BatchMode=yes)
task="voptrack-recovery-set-$(python3 -c 'import secrets;print(secrets.token_hex(8))')"
dest="$ci_root/$task"
mkdir -m700 -p "$retained/remote-evidence"
remote_stream() { ssh "${ssh_opts[@]}" "$VOPTRACK_CI_HOST" "$@"; }
remote() { remote_stream "$@" </dev/null; }
# Compare daemon identity as well as OS identity. Keep these observations private.
cat /etc/machine-id | sha256sum > "$retained/remote-evidence/local-machine.sha256"
docker info --format '{{.ID}}' | sha256sum > "$retained/remote-evidence/local-daemon.sha256"
remote 'cat /etc/machine-id | sha256sum' > "$retained/remote-evidence/remote-machine.sha256"
remote "docker info --format '{{.ID}}' | sha256sum" > "$retained/remote-evidence/remote-daemon.sha256"
! cmp -s "$retained/remote-evidence/local-machine.sha256" "$retained/remote-evidence/remote-machine.sha256"
! cmp -s "$retained/remote-evidence/local-daemon.sha256" "$retained/remote-evidence/remote-daemon.sha256"
remote "umask 077; mkdir -p '$ci_root'; test ! -e '$dest'; mkdir '$dest'; mkdir '$dest/source' '$dest/retained'"
git -C "$root" archive HEAD | remote_stream "tar -xf - -C '$dest/source'"
rsync -a --no-owner --no-group -e "ssh ${ssh_opts[*]}" "$retained/household" "$retained/research" "$VOPTRACK_CI_HOST:$dest/retained/"
# Loading by content ID does not alter the host's default deployment tag.
docker save "$image" | remote_stream 'docker load' > "$retained/remote-evidence/image-load.log"
# Some Docker stores rewrite non-runtime metadata during load. Verify every
# filesystem layer and the complete runtime configuration before executing it.
loaded_image=$(sed -nE 's/^Loaded image ID: (sha256:[0-9a-f]{64})$/\1/p' "$retained/remote-evidence/image-load.log")
[[ "$loaded_image" =~ ^sha256:[0-9a-f]{64}$ ]] || { echo 'image load identity unavailable' >&2; exit 1; }
docker image inspect "$image" > "$retained/remote-evidence/source-image.json"
remote "docker image inspect '$loaded_image'" > "$retained/remote-evidence/loaded-image.json"
python3 "$root/release/recovery-set/image_identity.py" "$retained/remote-evidence/source-image.json" "$retained/remote-evidence/loaded-image.json" > "$retained/remote-evidence/runtime-image.json"
printf '%s\n' "$dest" > "$retained/remote-evidence/workdir"
remote "python3 '$dest/source/release/recovery-set/inventory.py' '$dest/before.json'"
expected=$(cd "$root" && go list -m -f '{{.Version}}' github.com/ekarpov/tessera)
remote "cd '$dest/source'; flock '$ci_lock' bash release/recovery-set/replay.sh '$dest/retained' '$loaded_image' '$task' '$expected'" > "$retained/remote-evidence/drill.log" 2>&1
remote "python3 '$dest/source/release/recovery-set/inventory.py' '$dest/after.json'"
rsync -a --no-owner --no-group -e "ssh ${ssh_opts[*]}" "$VOPTRACK_CI_HOST:$dest/before.json" "$VOPTRACK_CI_HOST:$dest/after.json" "$retained/remote-evidence/"
rsync -a --no-owner --no-group -e "ssh ${ssh_opts[*]}" "$VOPTRACK_CI_HOST:$dest/retained/" "$retained/remote-evidence/readback/"
python3 - "$retained" <<'PYVERIFY'
import hashlib,json,pathlib,sys
root=pathlib.Path(sys.argv[1]);readback=root/'remote-evidence/readback'
for variant in ['household','research']:
 for name in ['inputs/selection.json','inputs/backup.tar','inputs/evidence.tar','inputs/kit.tar','inputs/backup-settings-export.json','trust/backup-lineage.json','key/backup.key','source-removed.json']:
  assert hashlib.sha256((root/variant/name).read_bytes()).digest()==hashlib.sha256((readback/variant/name).read_bytes()).digest(),'retained-input readback differs'
 assert json.loads((readback/variant/'drill-receipt.json').read_text())['restored']
before=json.loads((root/'remote-evidence/before.json').read_text());after=json.loads((root/'remote-evidence/after.json').read_text())
assert before==after,'remote resources changed; inspect private inventory'
(root/'remote-evidence/verified.json').write_text(json.dumps({'separate_os_and_docker_daemon':True,'retained_bytes_read_back_exactly':True,'home_and_research_recovery':'pass','original_remote_resources_unchanged':True,'physical_custody_attested':False})+'\n')
PYVERIFY
printf 'Independent-host Home/Research replay passed; private receipts retained.\n'
