#!/usr/bin/env bash
# Prove a backup can be restored, which is the only thing that makes it one.
#
# Native rather than containerised, deliberately: backup and restore are
# `tesserad` subcommands and need no proxy, no image and no network. This runs
# the same binary the systemd path in deploy/README.md runs, which is closer to
# what an operator does than a container would be.
#
# It exists because none of this had ever been executed. The documentation
# named two artifacts and restore needs three.
set -euo pipefail

work="$(mktemp -d)"
port="${VOPTRACK_BACKUP_PORT:-7843}"
trap 'pkill -f "tesserad -config $work/" >/dev/null 2>&1 || true; rm -rf "$work"' EXIT

say() { printf '  %-34s %s\n' "$1" "$2"; }
fail() { printf '\nFAIL: %s\n' "$1" >&2; exit 1; }

openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj "/CN=localhost" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" \
  -keyout "$work/k.pem" -out "$work/c.pem" 2>/dev/null
chmod 600 "$work/k.pem"
mkdir -p "$work/data"; chmod 700 "$work/data"
config() { sed "s|DATA|$1|" > "$2" <<JSON
{
  "listen": "127.0.0.1:${port}",
  "issuer": "https://localhost:${port}",
  "data_dir": "DATA",
  "tls_certificate": "$work/c.pem",
  "tls_key": "$work/k.pem",
  "engine": "sqlite",
  "content": true,
  "encrypted_storage_confirmed": true
}
JSON
}
config "$work/data" "$work/server.json"
config "$work/restored" "$work/restore.json"

head -c 32 /dev/urandom > "$work/backup.key"; chmod 600 "$work/backup.key"
openssl rand -base64 18 > "$work/pw"; chmod 600 "$work/pw"

wait_up() { for _ in $(seq 1 40); do curl -sk "https://localhost:${port}/.well-known/tessera" >/dev/null 2>&1 && return 0; sleep 1; done; return 1; }

echo "Backup and restore"

# A person must exist before a backup can be taken at all: the archive carries
# identity.json, which is written by the first provisioning. Without it the
# backup fails with dataset_unavailable, naming the wrong thing entirely.
go tool tesserad -config "$work/server.json" -add-person owner \
  -grants read,write,schema,erase -password-file "$work/pw" >/dev/null
say "a deployment with somebody in it" "ready"

# Exercise the actual wrapper with the pinned native binary. No real service is
# controlled: a synthetic systemctl records state while no source server runs.
mkdir -p "$work/bin" "$work/exports"
cat > "$work/bin/tesserad" <<'SH'
#!/bin/sh
exec go tool tesserad "$@"
SH
cat > "$work/bin/systemctl" <<'SH'
#!/bin/sh
case "$1" in
    show) cat "$BACKUP_GATE_STATE";;
    stop) printf inactive > "$BACKUP_GATE_STATE";;
    start) printf active > "$BACKUP_GATE_STATE";;
    *) exit 1;;
esac
SH
chmod 700 "$work/bin/tesserad" "$work/bin/systemctl"
printf active > "$work/service-state"
archive="$(PATH="$work/bin:$PATH" BACKUP_GATE_STATE="$work/service-state" \
  TESSERA_CONFIG="$work/server.json" TESSERA_BACKUP_KEY="$work/backup.key" \
  TESSERA_DATA_DIR="$work/data" TESSERA_BACKUP_DIR="$work/exports" \
  TESSERA_SERVICE=voptrack-backup-gate.service TESSERA_BACKUP_KEEP=14 \
  sh deploy/tesserad-backup.example.sh 2>"$work/backup.log")" || fail "native backup wrapper failed"
[ -s "$archive" ] || fail "the archive is empty"
say "backup taken through wrapper" "$(wc -c < "$archive" | tr -d ' ') bytes"

fence="$archive.retired"
[ -f "$fence" ] || fail "wrapper did not publish retirement evidence"
cmp "$work/data/dataset.db.retired" "$fence" || fail "wrapper changed retirement evidence"
say "captured retirement fence" "matches stopped SQLite source"

# Restoring without the fence must fail. This is why the documentation names
# three artifacts rather than two, and if it ever stops failing the
# documentation can be simplified -- so the check is worth having either way.
if go tool tesserad -config "$work/restore.json" -restore "$archive" \
     -backup-key-file "$work/backup.key" >/dev/null 2>&1; then
  fail "restore succeeded without a retirement fence; the documentation over-states what is needed"
fi
rm -rf "$work/restored"
say "restore without the fence" "refused, as documented"

go tool tesserad -config "$work/restore.json" -restore "$archive" \
  -backup-key-file "$work/backup.key" -retirement-fence "$fence" >/dev/null 2>&1 \
  || fail "restore with the archive, key and fence — the documented procedure — did not work"
say "restore with all three" "restored"

# A restore nobody has started is not a restore. The server must come up and
# the person the backup carried must still be able to sign in.
( go tool tesserad -config "$work/restore.json" >"$work/log" 2>&1 & )
wait_up || fail "the restored deployment did not start; see $work/log"
say "restored deployment answers" "$(curl -sk -o /dev/null -w '%{http_code}' "https://localhost:${port}/.well-known/tessera")"

status="$(curl -sk -o /dev/null -w '%{http_code}' -u "owner:$(cat "$work/pw")" "https://localhost:${port}/admin")"
[ "$status" = "200" ] || fail "the owner the backup carried cannot sign in to the restored deployment ($status)"
say "its administrator still signs in" "200"

echo
echo "ok — a backup taken here can be restored and used"
