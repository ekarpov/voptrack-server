# App-only Home coverage

`make home-app-check` creates a disposable Home with Console off. It proves
that no web interface or cross-member service credential is installed, product
and TLS-pin discovery, explicit owner/member OAuth authority, app group sharing,
suspension/reinstatement, quotas, moderation, device invitation/rebinding and
owner-device replacement. It exercises APIs as an app would; it does not run
the Swift app or test its interface.

The moderation scenario shares
Spending and Weight from Sara's store, stops only Spending with the explicit
single-dataset selector, checks that Ben loses Spending and keeps Weight, and
checks that the owner cannot read either track. Sara's readers listing records
the stopped type, owner and timestamp. Sara explicitly re-shares; Ben reads it
again and the stopped notice clears.

The Console workflow, other-store isolation, Home service/cache behavior,
named non-Home/whole-store exceptions and confirmation replay are covered in
[the Console gate](../console/README.md). App-only household-group shares do not
imply a Home-service grant or registration in Console's Home sources. The
Console gate checks and discloses that discovery limit; this gate has no Home
service to discover anything through.

## Device ownership gate

`ownership_checks.py`, called by the ordinary `make home-app-check` journey,
replaces the old returned-ID-substring assertion. It runs a no-earlier-share
control: infer the only newly visible store after excluding independently known
fixture stores; require exact primary-ID equality, own read/write/schema without
erase approval, and administration confined to that store. Real synthetic writes
and query readback exercise owner/peer isolation, selected-track group sharing,
private-track refusal, unauthorized sharing mutation and immediate share removal.
The device then receives a filtered group share and must not gain foreign schema
or sharing authority or discover an unshared peer store.

Four fresh device joins with an already present group share must each reach one
new distinct own store, with no primary binding to a borrowed store. Rebinding
must retain the control's independently observed own store/administration and
records, and revoke the old device. Every assertion is required; a failure is not
converted into an expected pass. Preview and single-use invitation checks remain.
Failures print assertion labels/status codes, never response bodies, identifiers,
record values or tokens.

## What this does not cover

No real-device, QR scanning, Swift UI, real-domain/TLS or independent
accessibility/translation verification is claimed. Existing affected installations
are not repaired by upgrading. Old refresh refusal is upstream evidence, not an
independent assertion in this gate; this is not a PostgreSQL-specific ownership
reproduction.
Unshare is not atomic with an independent app re-share: no compare-and-execute,
rollback, permanent sharing ban or idempotency support is supplied locally.
The earlier maintenance-only correction retained its then-current Tessera pin.

## App Review connection workflow

For a fresh writable demo member, select its own named dataset during OAuth and
approve **See this data**, **Add, change and delete entries**, and **Change how
this data is organised**. Track installation needs Schema approval as well as
Write for records; contributor-only insertion is insufficient. Whole-dataset
erasure and server/account administration are not needed for ordinary recording.
After Allow, use Tessera rc.3's Return to app link. In the app, add a track, choose
the connected Home as its storage, and record synthetic data. Connecting storage
alone does not register existing remote subjects or tracks in the app.
