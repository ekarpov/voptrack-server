#!/usr/bin/env bash
# Prove Home comes back after its machine is lost, from what the owner kept:
# a household is built, the owner takes the recovery kit and a backup with
# init's own commands, every volume is destroyed, and init restores onto fresh
# volumes. Members, sharing, suspension, Home's totals and everyone's sign-in
# must all be back. Removes everything it creates, volumes included.
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
. "$root/ci/select-image.sh"
. "$root/ci/phase.sh"
work="$(mktemp -d)"
port="${VOPTRACK_RECOVERY_HTTPS_PORT:-7343}"
cp "$root/release/compose.yaml" "$work/"
before="${VOPTRACK_GATE_PROJECT:-voptrack-recovery-gate}-lost" after="${VOPTRACK_GATE_PROJECT:-voptrack-recovery-gate}-new"
dc() { local p="$1"; shift; ( cd "$work" && docker compose -p "$p" "$@" ); }
cleanup() { ci_phase cleanup dc "$before" down -v --remove-orphans >/dev/null 2>&1 || true; ci_phase cleanup dc "$after" down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$work"; }
trap cleanup EXIT
# The scheduler signals the process group; exit through the cleanup trap.
trap 'exit 143' TERM
trap 'exit 130' INT
fail() { printf '\nFAIL: %s\n' "$1" >&2; dc "$after" logs --tail 20 init console >&2 2>/dev/null || true; exit 1; }
say() { printf '  %-46s %s\n' "$1" "$2"; }
export VOPTRACK_HTTPS_PORT="$port" VOPTRACK_HTTP_PORT="${VOPTRACK_RECOVERY_HTTP_PORT:-7380}"
export VOPTRACK_PUBLIC_URL="https://localhost:${port}" VOPTRACK_STORAGE_ENCRYPTED=yes VOPTRACK_VARIANT=household
export VOPTRACK_CONSOLE_PORT="${VOPTRACK_RECOVERY_CONSOLE_PORT:-7344}"
echo "Home, rebuilt on a new machine from what the owner kept"

ci_phase bootstrap dc "$before" up -d --wait >/dev/null 2>&1 || fail "the first deployment did not become healthy"
dc "$before" run --rm -T init password > "$work/owner" 2>/dev/null; chmod 600 "$work/owner"
dc "$before" exec -T caddy cat /var/lib/voptrack-public/ca.crt > "$work/ca.crt"
pin_before="$(dc "$before" exec -T caddy cat /var/lib/voptrack-public/tls-pin)"
[ -n "$pin_before" ] || fail "the Home announced no pin"
ci_phase verification python3 "$root/release/household/seed_dev.py" "$VOPTRACK_PUBLIC_URL" "$work/ca.crt" "$work/owner" "$work/seed" >/dev/null \
  || fail "the household could not be set up"
# A backup that holds the household: the server takes one at every start.
dc "$before" restart tesserad >/dev/null 2>&1 && dc "$before" up -d --no-deps --wait tesserad console caddy >/dev/null 2>&1 || fail "the restart failed"
# A header-valid unfinished file is newer than an existing completed startup
# archive. Export must never choose it by mtime/name/header/stable size.
bad=tessera-backup-99991231T235959.999999999Z.enc
dc "$before" exec -T tesserad sh -c 'printf "TESSERA-BACKUP-1\nunfinished" > /var/lib/voptrack-backups/tessera-backup-99991231T235959.999999999Z.enc'
mkdir -m 700 "$work/restore"
if ci_phase export dc "$before" run --rm -T init take-backup > "$work/restore/refused.partial" 2>/dev/null; then fail "export allowed live writers"; fi
[ ! -s "$work/restore/refused.partial" ] || fail "refused export emitted a bundle"
dc "$before" stop console tesserad >/dev/null 2>&1 || fail "could not quiesce deployment"
# Establish the older fixture by a successful supported offline command, not
# by inferring completion from its filename/header/mtime. Then make the partial
# file newest again so the reviewed selection defect is deterministic.
dc "$before" run --rm -T --entrypoint /usr/local/bin/tesserad init \
  -config /var/lib/voptrack-config/server.json \
  -backup /var/lib/voptrack-backups/tessera-backup-20000101T000000.000000000Z.enc \
  -backup-key-file /var/lib/voptrack-config/backup.key >/dev/null 2>&1 || fail "completed older fixture could not be created"
dc "$before" run --rm -T --entrypoint sh init -c 'printf "TESSERA-BACKUP-1\\nunfinished" > /var/lib/voptrack-backups/tessera-backup-99991231T235959.999999999Z.enc' >/dev/null


# Missing current evidence must fail before Tessera can recreate anything.
dc "$before" run --rm -T --entrypoint sh init -c 'mv /var/lib/tessera/dataset.db.retired /var/lib/tessera/fence-gate-saved' >/dev/null
if ci_phase export dc "$before" run --rm -T init take-backup > "$work/restore/missing.partial" 2>/dev/null; then fail "export accepted missing current fence"; fi
[ ! -s "$work/restore/missing.partial" ] || fail "missing-fence refusal emitted output"
dc "$before" run --rm -T --entrypoint sh init -c 'test ! -e /var/lib/tessera/dataset.db.retired && mv /var/lib/tessera/fence-gate-saved /var/lib/tessera/dataset.db.retired' >/dev/null || fail "missing fence was silently regenerated"
# Make interrupted sensitive delivery exceed pipe capacity deterministically.
dc "$before" run --rm -T --entrypoint sh init -c 'dd if=/dev/urandom of=/var/lib/voptrack-console/export-gate-padding bs=1048576 count=1 2>/dev/null' >/dev/null
if ci_phase export dc "$before" run --rm -T init recovery-kit 2>/dev/null | head -c 32 > "$work/restore/interrupted.partial"; then fail "interrupted kit export reported success"; fi
dc "$before" run --rm -T --entrypoint sh init -c 'rm /var/lib/voptrack-console/export-gate-padding' >/dev/null
rm -f "$work/restore/interrupted.partial" "$work/restore/missing.partial" "$work/restore/refused.partial"
say "missing fence and interrupted delivery" "refused; partial output discarded"
ci_phase export dc "$before" run --rm -T init recovery-kit > "$work/restore/kit.tar" 2>/dev/null || fail "init recovery-kit failed"
ci_phase export dc "$before" run --rm -T init take-backup > "$work/restore/backup.tar" 2>/dev/null || fail "init take-backup failed"
if tar -tf "$work/restore/backup.tar" | grep -q "$bad"; then fail "export selected unfinished newest archive"; fi
say "unfinished newest online archive" "ignored; exact fresh offline snapshot exported"
# Rotation/replacement of online files cannot substitute the offline snapshot.
dc "$before" run --rm -T --entrypoint sh init -c 'mv /var/lib/voptrack-backups/tessera-backup-99991231T235959.999999999Z.enc /var/lib/voptrack-backups/rotated-gate.partial; ln -s /var/lib/voptrack-config/backup.key /var/lib/voptrack-backups/tessera-backup-99991231T235959.999999999Z.enc' >/dev/null
ci_phase export dc "$before" run --rm -T init take-backup > "$work/restore/rotation.tar" 2>/dev/null || fail "online rotation/replacement prevented a fresh export"
if tar -tf "$work/restore/rotation.tar" | grep -Eq "$bad|rotated-gate"; then fail "online replacement entered export"; fi
rm -f "$work/restore/rotation.tar"
say "online rotation and symlink replacement" "ignored by fresh completed export"

tar -tf "$work/restore/backup.tar" | grep -q 'dataset.db.retired$' || fail "the backup does not carry the retirement fence"
say "the owner keeps a kit and a backup" "$(du -k "$work/restore/kit.tar" | cut -f1) KiB and $(du -k "$work/restore/backup.tar" | cut -f1) KiB"

dc "$before" down -v >/dev/null 2>&1
say "the machine is lost" "every volume gone"

dc "$after" run --rm -T init restore-kit < "$work/restore/kit.tar" >/dev/null 2>&1 || fail "init restore-kit failed"
early="$(ci_phase restore dc "$after" up -d --wait 2>&1)" && fail "the stack started with a restore pending: an empty server"
dc "$after" down >/dev/null 2>&1
out="$(dc "$after" run --rm -T init restore < "$work/restore/backup.tar" 2>&1)" || fail "init restore failed: $out"
grep -q "restored from" <<<"$out" || fail "init restore did not restore an archive: $out"
ci_phase restore dc "$after" up -d --wait >/dev/null 2>&1 || fail "the rebuilt deployment did not become healthy"
dc "$after" exec -T caddy cat /var/lib/voptrack-public/ca.crt > "$work/ca.crt"
# The key every phone pinned must survive the move, or every app would have to
# be paired again: the CA travels in the recovery kit.
[ "$(dc "$after" exec -T caddy cat /var/lib/voptrack-public/tls-pin)" = "$pin_before" ] \
  || fail "the rebuilt Home announces a different pin: every app would refuse it"
say "the local CA pin" "the same on the new machine"
live="$(dc "$after" run --rm -T init restore < "$work/restore/backup.tar" 2>&1)" && fail "restore allowed live producers"
grep -q "maintenance unavailable" <<<"$live" || fail "live restore did not hit the maintenance guard"
dc "$after" stop console tesserad >/dev/null 2>&1 || fail "could not quiesce one-shot checks"
again="$(dc "$after" run --rm -T init restore < "$work/restore/backup.tar" 2>&1)" && fail "a second restore over a live deployment was accepted"
grep -q "no restore pending" <<<"$again" || fail "a second restore was refused for the wrong reason: $again"
again="$(dc "$after" run --rm -T init restore-kit < "$work/restore/kit.tar" 2>&1)" && fail "a kit over a live deployment was accepted"
grep -q "already exists" <<<"$again" || fail "a kit over a live deployment was refused for the wrong reason: $again"
dc "$after" up -d --no-deps --wait tesserad console caddy >/dev/null 2>&1 || fail "could not restart after one-shot checks"
say "restore-kit, then restore, on fresh volumes" "restored; neither runs twice"

ci_phase verification python3 "$root/release/recovery/check.py" "$VOPTRACK_PUBLIC_URL" "$work/ca.crt" "$work/owner" "$work/seed/home.json" \
  || fail "the household did not come back"
echo
echo "ok — Home came back from a recovery kit and a backup, on fresh volumes"
