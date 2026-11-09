#!/usr/bin/env bash
# Prove the household variant, built the way its README tells an owner to build
# it: from an empty directory, through Tessera's own /admin, one form at a time.
#
# The model is PLACEMENT. Each member has a private store; the Family store is
# shared by everyone in it. What goes in Family is shared, what stays in a
# personal store is not. Type-scoped sharing is deliberately not used: it
# narrows one sign-in rather than the account (Tessera finding 29, open), so it
# is not a privacy boundary yet.
#
# Everything created is removed on exit, volumes included.
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
. "$root/ci/select-image.sh"
. "$root/ci/phase.sh"
work="$(mktemp -d)"
project="${VOPTRACK_GATE_PROJECT:-voptrack-household-gate}"
port="${VOPTRACK_HOUSEHOLD_HTTPS_PORT:-7643}"
cp "$root/release/compose.yaml" "$work/"

dc() { ( cd "$work" && docker compose -p "$project" "$@" ); }
cleanup() { ci_phase cleanup dc down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$work"; }
trap cleanup EXIT
# The scheduler signals the process group; exit through the cleanup trap.
trap 'exit 143' TERM
trap 'exit 130' INT
say()  { printf '  %-44s %s\n' "$1" "$2"; }
fail() { printf '\nFAIL: %s\n' "$1" >&2; dc logs --tail 20 >&2 2>/dev/null || true; exit 1; }

export VOPTRACK_HTTPS_PORT="$port" VOPTRACK_HTTP_PORT="${VOPTRACK_HOUSEHOLD_HTTP_PORT:-7680}"
export VOPTRACK_PUBLIC_URL="https://localhost:${port}" VOPTRACK_STORAGE_ENCRYPTED=yes
export VOPTRACK_VARIANT=household
base="$VOPTRACK_PUBLIC_URL"

echo "Household, by placement"
if ! ci_phase bootstrap dc up -d --wait >"$work/startup.log" 2>&1; then
  tail -n 40 "$work/startup.log" >&2
  fail "the household deployment did not become healthy"
fi
dc run --rm -T init password > "$work/owner" 2>/dev/null; chmod 600 "$work/owner"
dc exec -T caddy cat /var/lib/voptrack-public/ca.crt > "$work/ca.crt"
say "first start, household variant" "healthy"

as() { local who="$1" pw="$2"; shift 2; curl -s --cacert "$work/ca.crt" -u "$who:$(cat "$pw")" "$@"; }
csrf() { as owner "$work/owner" "$base/admin/$1" | grep -oE 'name="csrf" value="[^"]*"' | head -1 | sed 's/.*value="//;s/"//'; }
outcome() { sed -n 's/.*done=\([a-z-]*\).*/\1/p'; }
store_id() { as owner "$work/owner" "$base/admin/datasets" | grep -B1 ">$1<" | grep -oE 's-[0-9a-f]{32}' | head -1; }

# The owner starts with no store of their own -- not even the Console's, which
# only its service holds. Anything else means the variant did not take effect
# and every step below is testing the wrong deployment.
listing="$(as owner "$work/owner" "$base/admin/datasets")"
if grep -qE 'data-label="Name">' <<<"$listing"; then fail "the household owner already holds a store before creating any"; fi
say "owner starts with no store" "as the variant says"

# Step 1 of the README: create the stores, named.
for name in Family Sara Ben; do
  got="$(as owner "$work/owner" -o /dev/null -w '%{redirect_url}' -X POST \
    --data-urlencode "csrf=$(csrf datasets)" --data-urlencode "display_name=$name" \
    "$base/admin/datasets" | outcome)"
  [ "$got" = created ] || fail "creating the $name store: $got"
done
family="$(store_id Family)"; sara="$(store_id Sara)"; ben="$(store_id Ben)"
[ -n "$family" ] && [ -n "$sara" ] && [ -n "$ben" ] || fail "a store created in /admin is not listed"
say "Family, Sara, Ben created in /admin" "named, all held by the owner"

# Step 2: each member gets their own store in full -- which makes them its
# administrator and nobody else's -- and read, write and schema on Family.
# Schema because the app defines its own track types in whatever store it uses.
add() {
  local person="$1" store="$2" pw="$3"; shift 3
  local args=() g
  for g in "$@"; do args+=(-d "$g=yes"); done
  as owner "$work/owner" -o /dev/null -w '%{redirect_url}' -X POST \
    --data-urlencode "csrf=$(csrf principals)" --data-urlencode "person=$person" \
    --data-urlencode "password=$pw" --data-urlencode "dataset=$store" "${args[@]}" \
    "$base/admin/principals" | outcome
}
for member in sara ben; do
  openssl rand -base64 18 | tr -d '\n' > "$work/$member"; chmod 600 "$work/$member"
  own="$sara"; [ "$member" = ben ] && own="$ben"
  [ "$(add "$member" "$own" "$(cat "$work/$member")" read write schema erase)" = added ] \
    || fail "adding $member to their own store"
  # Adding an EXISTING account to a second store still demands the password
  # field, and ignores it. The README warns about that; this proves the member's
  # real password is untouched by it.
  [ "$(add "$member" "$family" "ignored-for-an-existing-account" read write schema)" = added ] \
    || fail "adding $member to Family"
done
say "Sara and Ben: own store + Family" "added"

status="$(as sara "$work/sara" -o /dev/null -w '%{http_code}' "$base/admin")"
[ "$status" = 200 ] || fail "Sara's password stopped working after she was added to Family ($status)"
say "second 'password' left the first alone" "Sara still signs in"

# Scoped administration: Sara administers her own store and nothing else, so she
# can manage it without the owner and cannot see anybody else's.
mine="$(as sara "$work/sara" "$base/admin/datasets" | grep -oE 'data-label="Name">[^<]*' | sed 's/.*>//' | tr '\n' ' ')"
[ "$mine" = "Sara " ] || fail "Sara's administration shows [$mine], not only her own store"
say "Sara administers" "only Sara"

# Tokens for the member-side checks. Minting takes the database lock, so the
# producers stop for it, exactly as the README tells an operator.
dc stop console tesserad >/dev/null 2>&1 || fail "could not quiesce for minting"
mint() {
  dc run --rm -T --no-deps --entrypoint /usr/local/bin/tesserad init \
    -config /var/lib/voptrack-config/server.json -add-token "$1" -dataset "$2" \
    -secret-output "/var/lib/voptrack-bootstrap/$3" >/dev/null 2>&1
}
mint sara "$family" sara-family.tok || fail "minting Sara's Family token"
mint ben  "$family" ben-family.tok  || fail "minting Ben's Family token"
# The account-level boundary: Ben does not hold Sara's store, so no credential
# for it can be made for him at all. This is the property type-scoped sharing
# lacks, and why placement is the model.
if mint ben "$sara" ben-sara.tok; then fail "a token for Ben on Sara's personal store was issued"; fi
say "a credential for Ben on Sara's store" "cannot be issued"
for t in sara-family ben-family; do
  dc run --rm -T --no-deps --entrypoint cat init "/var/lib/voptrack-bootstrap/$t.tok" > "$work/$t.tok" 2>/dev/null
done
dc up -d --no-deps --wait tesserad console caddy >/dev/null 2>&1 || fail "the server did not come back after minting"

( cd "$root" && go run ./cmd/verify -url "$base" -ca "$work/ca.crt" -conformance=false \
    -token "$(cat "$work/sara-family.tok")" -shared-token "$(cat "$work/ben-family.tok")" \
    -isolation -foreign-dataset "$ben" \
    -signin-user sara -signin-password-file "$work/sara" \
    -admin-user owner -admin-password-file "$work/owner" ) > "$work/verify.log" 2>&1 \
  || { cat "$work/verify.log" >&2; fail "the member-side checks failed"; }
for check in sharing isolation "signing in" administration; do
  grep -Eq "^[[:space:]]*ok[[:space:]]+$check\$" "$work/verify.log" \
    || { cat "$work/verify.log" >&2; fail "\"$check\" was not reported as passing"; }
done
say "Family shared both ways (Sara ↔ Ben)" "ok"
say "Ben's store concealed from Sara's credential" "ok"
say "Sara signs in through the app's flow" "ok"

# The variant is fixed at first start, and saying otherwise must be refused
# rather than silently ignored.
dc stop console tesserad >/dev/null 2>&1 || fail "could not quiesce for variant validation"
if VOPTRACK_VARIANT=standard ci_phase bootstrap dc up -d --wait >/dev/null 2>&1; then
  fail "the deployment restarted as a different variant"
fi
init_log="$(dc logs init 2>/dev/null || true)"   # not piped: see release/verify.sh
grep -q "cannot be changed after first start" <<<"$init_log" \
  || fail "changing the variant was refused without saying why"
say "restarting as another variant" "refused, with the reason"

echo
echo "ok — a household built through /admin keeps personal stores private and Family shared"
