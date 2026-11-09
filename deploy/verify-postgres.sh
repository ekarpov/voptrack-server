#!/usr/bin/env bash
# Prove the PostgreSQL path works, because deploy/ advertises it and until now
# it had been stood up once, by hand, months of commits ago.
#
# One manual run is the weakest evidence this repository accepts. Tessera found
# the same shape upstream: their `make check` skipped every PostgreSQL case and
# still printed ok, so a run covering one engine read exactly like a run
# covering two, and a backup-and-restore case that passed once had failed on
# every run since without anybody noticing, because it usually did not run.
#
# The server runs natively and only PostgreSQL is containerised. That is
# deliberate: it is the same binary the systemd path runs, and it keeps the
# thing under test separate from the thing supporting it.
#
# The DSN here uses sslmode=disable over loopback to a throwaway container. A
# real deployment must not: the DSN carries the database password.
set -euo pipefail

work="$(mktemp -d)"
port="${VOPTRACK_PG_SERVER_PORT:-7844}"
pgport="${VOPTRACK_PG_PORT:-7845}"
pg_run_id="$(openssl rand -hex 12)"
name="voptrack-gate-postgres-$pg_run_id"
pgpass="gate-$(openssl rand -hex 8)"

cleanup() {
  pkill -f "tesserad -config $work/" >/dev/null 2>&1 || true
  # Even failed starts may create a container. Remove it only with our label.
  if [ "$(docker inspect --format '{{ index .Config.Labels "voptrack.gate.postgres" }}' "$name" 2>/dev/null)" = "$pg_run_id" ]; then
    docker rm -fv "$name" >/dev/null 2>&1 || true
  fi
  rm -rf "$work"
}
trap cleanup EXIT

say()  { printf '  %-40s %s\n' "$1" "$2"; }
fail() { printf '\nFAIL: %s\n' "$1" >&2; exit 1; }

command -v docker >/dev/null || fail "docker is needed to run a disposable PostgreSQL"

echo "PostgreSQL deployment"

# --------------------------------------------------------------------------
# A disposable database, and a SECOND one for the restore.
#
# Tessera's own change list says the restore DSN must name a disposable
# database rather than merely a fresh one: a restore refuses a database that
# already holds a deployment, so reusing the first one fails on the second run
# and passes on the first. That is the trap this gate is built to avoid, so it
# is asserted below rather than merely avoided.
# --------------------------------------------------------------------------
# -v as well as -f: the postgres image declares a volume, so without it every
# run leaves an anonymous volume of database files behind -- on a host that
# may have little disk to spare.
docker run -d --name "$name" --label "voptrack.gate.postgres=$pg_run_id" -p "127.0.0.1:${pgport}:5432" \
  -e POSTGRES_PASSWORD="$pgpass" -e POSTGRES_USER=tessera -e POSTGRES_DB=tessera \
  postgres:17-alpine >/dev/null
# pg_isready is NOT enough, and trusting it is what made the first version of
# this gate fail in CI while passing twice here. The official image runs initdb
# against a temporary server that listens on the unix socket only, and
# pg_isready talks to that socket -- so it answers "ready" during initialisation,
# before the real server has started on TCP. Locally the image was already
# pulled and the race never opened; on a runner pulling it fresh, it did.
#
# So wait for a query to succeed over TCP, which the init phase cannot answer,
# and retry rather than probe once.
ready=""
for _ in $(seq 1 90); do
  if docker exec -e PGPASSWORD="$pgpass" "$name" \
       psql -h 127.0.0.1 -U tessera -d tessera -tAc 'SELECT 1' 2>/dev/null | grep -q '^1$'; then
    ready=yes; break
  fi
  sleep 1
done
[ -n "$ready" ] || fail "PostgreSQL never answered a query over TCP; see: docker logs $name"

created=""
for _ in $(seq 1 15); do
  if docker exec -e PGPASSWORD="$pgpass" "$name" \
       psql -h 127.0.0.1 -U tessera -d tessera -c 'CREATE DATABASE tessera_restore' >/dev/null 2>&1; then
    created=yes; break
  fi
  sleep 2
done
[ -n "$created" ] || fail "could not create the second, disposable database for the restore"
say "disposable PostgreSQL, two databases" "ready"

openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj "/CN=localhost" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" \
  -keyout "$work/k.pem" -out "$work/c.pem" 2>/dev/null
chmod 600 "$work/k.pem"
mkdir -p "$work/data"; chmod 700 "$work/data"

dsn() { printf 'postgres://tessera:%s@127.0.0.1:%s/%s?sslmode=disable\n' "$pgpass" "$pgport" "$1"; }
dsn tessera         > "$work/postgres.dsn"
dsn tessera_restore > "$work/restore.dsn"

config() { cat > "$2" <<JSON
{
  "listen": "127.0.0.1:${port}",
  "issuer": "https://localhost:${port}",
  "data_dir": "$1",
  "tls_certificate": "$work/c.pem",
  "tls_key": "$work/k.pem",
  "engine": "postgres",
  "postgres_dsn_file": "$3",
  "content": true,
  "encrypted_storage_confirmed": true
}
JSON
}
config "$work/data"     "$work/server.json"  "$work/postgres.dsn"
config "$work/restored" "$work/restore.json" "$work/restore.dsn"

# --------------------------------------------------------------------------
# The DSN is a credential file, so it must be 0600. This is a regression test
# for something found the one time this path was stood up by hand: the server
# refuses to start and the message does not name the file it means.
# --------------------------------------------------------------------------
chmod 644 "$work/postgres.dsn"
if go tool tesserad -config "$work/server.json" -add-person probe \
     -grants read -password-file /dev/null >"$work/perm.log" 2>&1; then
  fail "a world-readable DSN was accepted; deploy/README.md says it is refused"
fi
# The diagnostic names the file, its mode and the required mode. It did not
# always: it used to say only "credential files must be private regular
# files", which left an operator with two candidate files and no hint. Assert
# the useful part so a regression to the vague form fails here.
grep -q "postgres.dsn" "$work/perm.log" && grep -q "0600" "$work/perm.log" \
  || fail "a 0644 DSN was refused, but the message does not name the file and the mode it wants: $(tr -d '\n' < "$work/perm.log")"
say "world-readable DSN" "refused, and the message names it"
chmod 600 "$work/postgres.dsn" "$work/restore.dsn"

openssl rand -base64 18 > "$work/pw"; chmod 600 "$work/pw"
provision() {
  go tool tesserad -config "$work/server.json" -add-person "$1" \
    -grants "$2" -password-file "$work/pw" >/dev/null 2>&1 || fail "provisioning $1"
}
provision owner        read,write,schema,erase
provision gate-full    read,write,schema
provision gate-refused write
for who in gate-full gate-refused; do
  go tool tesserad -config "$work/server.json" -add-token "$who" \
    -secret-output "$work/$who.token" >/dev/null 2>&1 || fail "minting a token for $who"
done
say "people and tokens on PostgreSQL" "owner, gate-full, gate-refused"

wait_up() { for _ in $(seq 1 40); do curl -sk "https://localhost:${port}/.well-known/tessera" >/dev/null 2>&1 && return 0; sleep 1; done; return 1; }

( go tool tesserad -config "$work/server.json" >"$work/log" 2>&1 & )
wait_up || fail "the PostgreSQL deployment did not start; see $work/log"
say "deployment answers" "$(curl -sk -o /dev/null -w '%{http_code}' "https://localhost:${port}/.well-known/tessera")"

# --------------------------------------------------------------------------
# The whole conformance suite, against PostgreSQL rather than SQLite. This is
# the point of the gate: the engine is what changed, and the suite is what can
# tell whether it behaves.
# --------------------------------------------------------------------------
echo
go run ./cmd/verify -url "https://localhost:${port}" \
  -token "$(tr -d '\r\n' < "$work/gate-full.token")" \
  -refused-token "$(tr -d '\r\n' < "$work/gate-refused.token")" \
  -ca "$work/c.pem" \
  -admin-user owner -admin-password-file "$work/pw" \
  || fail "the conformance gate did not pass on PostgreSQL"
echo

pkill -f "tesserad -config $work/server.json" >/dev/null 2>&1 || true
sleep 2

# --------------------------------------------------------------------------
# Backup and restore, on this engine. The fence is NOT the file the
# documentation names: SQLite writes dataset.db.retired and PostgreSQL writes
# postgres-fence.retired, both in the data directory.
# --------------------------------------------------------------------------
head -c 32 /dev/urandom > "$work/backup.key"; chmod 600 "$work/backup.key"
go tool tesserad -config "$work/server.json" -backup "$work/archive.enc" \
  -backup-key-file "$work/backup.key" >/dev/null 2>&1 \
  || fail "taking a backup on PostgreSQL"
[ -s "$work/archive.enc" ] || fail "the archive is empty"
say "backup taken" "$(wc -c < "$work/archive.enc" | tr -d ' ') bytes"

fence="$work/data/postgres-fence.retired"
if [ ! -f "$fence" ]; then
  ls -la "$work/data" >&2
  fail "no retirement fence at $fence"
fi
say "retirement fence present" "postgres-fence.retired"

if [ -f "$work/data/dataset.db.retired" ]; then
  fail "dataset.db.retired exists on PostgreSQL; the documentation's single fence name would be correct"
fi
say "the fence the docs name is absent" "dataset.db.retired, as expected on this engine"

if go tool tesserad -config "$work/restore.json" -restore "$work/archive.enc" \
     -backup-key-file "$work/backup.key" >/dev/null 2>&1; then
  fail "restore succeeded without a retirement fence"
fi
rm -rf "$work/restored"
say "restore without the fence" "refused, as documented"

go tool tesserad -config "$work/restore.json" -restore "$work/archive.enc" \
  -backup-key-file "$work/backup.key" -retirement-fence "$fence" >"$work/restore.log" 2>&1 \
  || fail "restore into a disposable database with all three did not work: $(tr -d '\n' < "$work/restore.log")"
say "restore into a disposable database" "restored"

# The documented procedure names a FRESH database. Tessera says it must be
# disposable, because a restore refuses one that already holds a deployment.
# Asserted so the requirement is evidence rather than a sentence.
rm -rf "$work/second"
sed "s|$work/restored|$work/second|" "$work/restore.json" > "$work/second.json"
if go tool tesserad -config "$work/second.json" -restore "$work/archive.enc" \
     -backup-key-file "$work/backup.key" -retirement-fence "$fence" >/dev/null 2>&1; then
  fail "restoring twice into the same database succeeded; Tessera documents it as refused"
fi
say "restore into a database already holding one" "refused — it must be disposable"

( go tool tesserad -config "$work/restore.json" >"$work/restored.log" 2>&1 & )
wait_up || fail "the restored PostgreSQL deployment did not start; see $work/restored.log"
say "restored deployment answers" "$(curl -sk -o /dev/null -w '%{http_code}' "https://localhost:${port}/.well-known/tessera")"

status="$(curl -sk -o /dev/null -w '%{http_code}' -u "owner:$(cat "$work/pw")" "https://localhost:${port}/admin")"
[ "$status" = "200" ] || fail "the owner the backup carried cannot sign in to the restored deployment ($status)"
say "its administrator still signs in" "200"

echo
echo "ok — PostgreSQL serves the protocol, and a backup taken from it can be restored"
