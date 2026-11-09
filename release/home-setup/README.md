# Protected first-run Home setup

Built against Tessera alpha.39.
This opt-in first-use workflow is separate from ordinary Console and from
Research installation. Research keeps its existing operator-led deployment and
study wizard. Real AWS, separate-host backup acceptance and physical custody
remain pending; this feature neither performs nor attests them.

## Host-authorized entry

Use release/compose.yaml with this overlay in a NEW project with empty volumes.
Do not deploy this first-use workflow to an existing service. The proposed host sequence:

    docker compose -f release/compose.yaml -f release/home-setup/compose.yaml up -d setup
    umask 077
    docker compose -f release/compose.yaml -f release/home-setup/compose.yaml run --rm setup-admin access > /private/home-setup-access.json

After completing browser setup, the host starts the normal services:

    docker compose -f release/compose.yaml -f release/home-setup/compose.yaml up -d --wait

The file contains a single-use code, setup origin and TLS certificate fingerprint.
Keep it private; never paste it into chat or ordinary logs. Compare that
fingerprint with the browser's certificate before entering the code. The setup
listener terminates TLS itself and is published on host loopback only, default
https://localhost:9673. If changing the setup port, set both
VOPTRACK_SETUP_PORT and the matching VOPTRACK_SETUP_ORIGIN. Remote operators use an explicitly approved SSH tunnel
to that loopback listener. A private network address alone grants no authority.
No secret is put in a URL, daemon log or ordinary Console page.

The hostname/public HTTPS origin remains host configuration, since routing and
TLS need it before browser setup. Setup cannot choose an arbitrary command,
account, file, destination or existing target. Only the Home mode, display name,
time zone and disk-encryption attestation are browser settings. Public Console,
home-network-only Console and app-only are explicit choices fixed at first init.
App-only still uses this temporary setup service; it closes on completion.

Normal services depend on setup's successful exit. A durable public pending
marker also blocks supported server/Console launch helpers. Completed setup
reconciles only that marker and exits without listening on later starts.
Never remove pending/state files to force startup around an interrupted setup.

## Browser journey and authority

The host code expires after 30 minutes, permits at most five guesses and is
durably consumed before issuing an authorized browser cookie. Sessions expire
after at most 15 minutes and never extend on refresh. Host reauthorization is
explicit:

    docker compose -f release/compose.yaml -f release/home-setup/compose.yaml run --rm setup-admin authorize
    docker compose -f release/compose.yaml -f release/home-setup/compose.yaml run --rm setup-admin access > /private/home-setup-access.json

It is allowed only before initialization or when already initialized and waiting
for material custody. A new grant invalidates earlier browser authority.
Completed or interrupted initialization cannot be reauthorized to run again.

TLS, exact Host/Origin, secure SameSite cookies, bounded sessions/forms, CSRF,
duplicate-field refusal, absolute expiry and a single-use confirmation protect
the flow. Navigation never creates the owner. Review binds saved choices, target
directory identities, server origin and attempt. Target freshness is checked
again before acceptance and under exclusive maintenance before mutation.

The native initializer and native offline password-reset command supply the
existing supported behavior. There is no protocol fork or unattended admin token.
The chosen password is used through a private temporary file, never an argument,
saved draft, recovery package, daemon log or bootstrap password. The old random
bootstrap password is retired; init password refuses after setup resets it.

The one-use setup service has initializer authority over only its fresh mounted
volumes. It has no Docker socket, cloud credential or general host command API.
Ordinary Console keeps its existing authority and mounts. Owner setup does not
grant the owner access to members' private stores.

## Recovery material and completion

The initial material export uses the existing offline host export format. It
contains bootstrap/service/CA secrets and the backup key, but no completed data
backup, current retirement evidence or independently established producer trust.
It is explicitly labelled initial recovery material rather than a complete
committed recovery set. Downloading a separate key creates a copy of the same
key; it does not make the package key-free.

Download both, then explicitly attest they were kept outside this machine and
apart from archive storage. A completed HTTP write is not proof of external
custody, encryption or recovery readiness. After startup, existing backup
readiness/kit approval and native preflight establish the recovery-set materials.
Do not mark real off-machine protection verified from this local setup.

The package and separate key copy come from the same immutable export, even if
a mounted key changes between downloads.

Completion closes the privileged listener and links to app connection using the
existing public connect contract and rsc.io/qr. Console modes also link to member
invitations and backup readiness; normal sign-in and current approval still apply.
No-web mode creates no Home/Console-state services and routes no ordinary Console.

Persisted choices are loaded on supported init/restart and used by Console and
generated Caddy routing. Newly prepared committed recovery kits carry validated
optional setup choices/password-retirement metadata. Old kits remain supported.
Restoring onto a fresh host still requires its current explicit storage-encryption
attestation; a saved statement about the old host cannot supply that approval.
Use this slice's matching Voptrack image or a later compatible one for restores.
The Tessera alpha.39 pin alone does not make older deployment helpers understand
saved setup choices or password retirement.

## Failure, retry and evidence

An interrupted draft loses its confirmation and browser session; host approval
is needed to continue. A stop after accepted/provisioning/password records
stopped, preserves all partial targets and blocks service start. It never infers
that an owner was successfully initialized or re-runs a possibly completed
mutation. Inspect preserved state and active processes on the host. A fresh
attempt needs separately approved fresh volumes; keep the old ones and evidence.

A completed journal with a remaining pending marker can remove only the exact
attempt's marker, then exit. Other marker contents or substituted target roots
are refused. No setup grants are valid after completion. Later valid host routing/port changes
can pass the completed service without reopening it; target identities must still
match. Unfinished attempts remain bound to their reviewed origins.

## Qualification coverage and exclusions

The missing-completion-guard durability regression injects a parent-directory
sync failure, so it runs under root as well as ordinary users. A compiling Go
overlay that skipped that sync failed at the intended assertion; the normal
race-enabled package checks passed. This is unit fault injection, not a
simulation of power loss or filesystem durability on the deployment host.

Local synthetic tests cover host authority, CSRF, capability replay, expiry,
choice/target binding, existing-target preservation, restart stop semantics,
custody gates, completion closure, stored choices and language completeness.
The actual-image gate verifies the image's alpha.39 binary, exercise public/
private/app-only fresh Homes, password authentication, startup blocking, native
initialization, retirement of the old password and restart persistence. Affected ordinary release/Home/Research/private/app-only gates are also covered.

New gate assertions require compiling isolated intended defects, actual fault
images, intended failures and intact replays. Retain all logs, source/image
identities, partial targets and before/after inventories. Never prune existing
services, volumes, images or qualification evidence.

Browser review covers seven languages, phone/desktop, light/dark, keyboard and
failure/progress states. This is local synthetic qualification; physical power
loss, real browser certificate onboarding, real app/device pairing, public DNS/
certificates, AWS/IAM, independent-host custody and deployment remain unqualified.
Consult docs/home-first-run-setup.md for exact executed evidence, the observed
Docker lifecycle/network exception, and the final review handoff.

The owner-authorized source review permits direct merge into local dev after
local checks. Push, deployment and upgrade require separate authorization.

## Qualification on a shared test host

Run inventory-sensitive setup qualification without concurrent Docker mutations,
under the host's heavy-work lock where applicable. The native gate accepts
`VOPTRACK_HOME_SETUP_PORT`. The Compose gate accepts
`VOPTRACK_HOME_SETUP_COMPOSE_PORT_BASE` (four consecutive ports), plus
`VOPTRACK_HOME_SETUP_COMPOSE_SUBNET`, `VOPTRACK_HOME_SETUP_COMPOSE_POOL` and
`VOPTRACK_HOME_SETUP_COMPOSE_ADDRESS`. Select only host-approved, unused loopback
ports and a consistent approved Docker network. Defaults preserve local testing.
No existing listener or network is stopped/replaced to make a gate pass.
These overrides change fixture placement, not workflow assertions or authority.
