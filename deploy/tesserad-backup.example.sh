#!/bin/sh
# Offline native/systemd backup. Product Compose installations use release/README.md.
# Requires an active dedicated service, flock, private encrypted storage and no
# other producer/provisioner. The destination must support same-filesystem links.
set -eu
umask 077

CONFIG="${TESSERA_CONFIG:-/etc/tessera/server.json}"
KEY="${TESSERA_BACKUP_KEY:-/etc/tessera/backup.key}"
DESTINATION="${TESSERA_BACKUP_DIR:-/var/backups/tessera}"
SERVICE="${TESSERA_SERVICE:-tesserad.service}"
KEEP="${TESSERA_BACKUP_KEEP:-14}"
DATA="${TESSERA_DATA_DIR:-/var/lib/tessera}"

fail() { echo "$1" >&2; exit 1; }
case "$KEEP" in ''|*[!0-9]*|0*) fail 'TESSERA_BACKUP_KEEP must be a positive integer without leading zeros';; esac
# Bound shell arithmetic before any service or retention changes.
[ "${#KEEP}" -le 6 ] || fail 'TESSERA_BACKUP_KEEP exceeds 999999'
mkdir -p "$DESTINATION"
chmod 700 "$DESTINATION"
exec 9>"$DESTINATION/.backup.lock"
flock -n 9 || fail 'another backup owns this destination'
archive="$DESTINATION/tessera-$(date -u +%Y-%m-%dT%H%M%SZ).enc"
[ ! -e "$archive" ] && [ ! -L "$archive" ] &&
    [ ! -e "$archive.retired" ] && [ ! -L "$archive.retired" ] ||
    fail 'backup output already exists; preserve it and retry with a new timestamp'
[ "$(systemctl show --property=ActiveState --value "$SERVICE")" = active ] ||
    fail 'backup requires an active service; no service was started'
stage="$(mktemp -d "$DESTINATION/.backup.XXXXXXXX")"
restart_needed=no
cleanup() {
    result=$?
    trap - EXIT INT TERM
    if [ "$restart_needed" = yes ]; then
        if ! systemctl start "$SERVICE" ||
            [ "$(systemctl show --property=ActiveState --value "$SERVICE")" != active ]; then
            echo 'service restart failed; operator action required' >&2
            result=1
        fi
    fi
    rm -rf -- "$stage" || result=1
    exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Arm recovery before stop: even a failed stop may have interrupted the service.
restart_needed=yes
systemctl stop "$SERVICE"
[ "$(systemctl show --property=ActiveState --value "$SERVICE")" = inactive ] ||
    fail 'service is not confirmed stopped; no backup was taken'
tesserad -config "$CONFIG" -backup "$stage/archive.enc" -backup-key-file "$KEY" >&2
[ -s "$stage/archive.enc" ] || fail 'backup archive is empty'

# Capture exactly one engine's fence while the service remains stopped. This
# evidence covers erasures up to capture, never future erasures after restart.
fence=""
for candidate in "$DATA/dataset.db.retired" "$DATA/postgres-fence.retired"; do
    if [ -f "$candidate" ] && [ ! -L "$candidate" ]; then
        [ -z "$fence" ] || fail 'two retirement fences; resolve the engine before retrying'
        fence="$candidate"
    fi
done
[ -n "$fence" ] || fail 'no retirement fence; backup is incomplete'
cp -p "$fence" "$stage/archive.enc.retired"
chmod 600 "$stage/archive.enc" "$stage/archive.enc.retired"

# No-clobber publication on the same filesystem. The final archive is the
# completion marker: publish its evidence first. Process interruption may leave
# an orphan .retired file; this is not a power-loss durability guarantee.
ln -T "$stage/archive.enc.retired" "$archive.retired"
ln -T "$stage/archive.enc" "$archive"
systemctl start "$SERVICE"
[ "$(systemctl show --property=ActiveState --value "$SERVICE")" = active ] ||
    fail 'service did not become active; operator action required'
restart_needed=no

# Retain KEEP complete pairs by UTC filename order. Never prune the pair just
# produced (including after clock rollback), symlinks, or incomplete old exports.
# Quoted glob iteration also handles spaces/newlines in the destination path.
set -- "$DESTINATION"/tessera-*.enc
count=0
for old do
    if [ -f "$old" ] && [ ! -L "$old" ] &&
        [ -f "$old.retired" ] && [ ! -L "$old.retired" ]; then
        count=$((count + 1))
    fi
done
for old do
    [ "$count" -gt "$KEEP" ] || break
    if [ "$old" != "$archive" ] && [ -f "$old" ] && [ ! -L "$old" ] &&
        [ -f "$old.retired" ] && [ ! -L "$old.retired" ]; then
        rm -- "$old"
        rm -- "$old.retired"
        count=$((count - 1))
    fi
done
printf '%s\n' "$archive"
