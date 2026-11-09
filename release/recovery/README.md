# Recovery export and fresh-volume recovery gate

`make recovery-check` uses isolated disposable Home deployments and synthetic
records. Both services run with shared deployment maintenance locks. The gate
requires export to fail with no output while producers are live, then stops both.
It proves a missing retirement fence is refused before it can be silently
recreated and that interrupted kit delivery exits unsuccessfully. Partial outputs
are discarded. A header-valid unfinished newest online archive is deliberately
left beside an older fixture whose completion is established by successful
exit of the supported offline Tessera backup command (plus startup archives). `take-backup` must ignore it and export a new
completed offline snapshot, not infer completion from file properties.

The gate also rotates/replaces that online file with a symlink to prove it is not
selected for export. A successful bundle plus separate kit restores on fresh
volumes. The original CA pin, owner/member credentials, distinct stores, sharing,
suspension and exact Home totals survive. Live restore is refused by the guard;
with producers stopped, repeated restore/restore-kit are refused by the existing
one-shot/fresh-target safeguards.

## Completion and authority

Host access authorizes this CLI operation; it supplies no browser capability.
The helper holds the exclusive deployment guard throughout a supported offline
Tessera backup command, current-fence capture and staged delivery. Running
services, configuration generation and supported mutating init commands
participate in that guard. The fresh archive is private until command completion;
no online archive lookup or rotation race participates in selection. The current
fence must exist before Tessera opens and remain unchanged during the snapshot.
The kit stays separate and contains no changing retirement fence.

## What this does not prove

No browser sensitive-export authorization, recent authentication, arbitrary
host-process control, remote copy/off-machine custody, public-domain TLS, actual
phone operation, screen-reader use or independent linguistic review. A trusted
host can bypass wrappers or replace files/lock inodes; adoption requires stopping
old unwrapped services before initializing/starting the new image. The supported
workflow excludes unmanaged concurrent host mutations. Console's online archive
downloads identify format only and are not completion/integrity proof.

### First-release sign-in fixture (2026-11-08)

The restored-household browser check supports default first-party sign-in and
ordinary consent. It validates callback origin, original owner/member passwords
and suspended-member refusal. For first-party member sign-in it independently
uses that member's ordinary app OAuth grant to confirm the original dataset ID,
rather than inferring restored identity from the Home page. Record totals,
private access and retained sharing assertions are unchanged.
