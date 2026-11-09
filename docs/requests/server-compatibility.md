# App and server compatibility request

Recorded 2026-11-07 from the app team's request, “Keeping Voptrack and Tessera
servers compatible while both keep changing”. Installed apps update more slowly
than servers; upgrades must preserve apps already released to people.

The App Review freeze and Voptrack upgrade controls below are repository policy.
The original upstream requests and their v0.1.0 disposition are recorded below.
This record does not establish live deployment state or completed qualification. No notice
has been sent and no test invitations have been issued by this documentation work.

## App Review freeze: effective now

Keep the App Review demos on Tessera `v0.1.0-rc.4` until the owner ends the
freeze. Keep their deployment, configuration, accounts and reserved invitations
intact. Qualification uses separate synthetic
accounts and invitations on a separate test server. Freeze release alone does
not waive the notice and qualification requirements below.

## Compatibility required of deployment candidates

Any server a released app may connect to must continue serving protocol `"1"`
at `/v1/` unchanged for as long as released apps use it. A future protocol v2
must be served alongside v1. A server release number, including `v0.1.0` final,
is distinct from a protocol major version and does not authorize retiring v1.

Within v1, changes stay additive: response fields and capabilities may be added;
an absent optional capability is the safe reading and must not be treated as
support. This does not turn required protocol operations into optional features.
Release assessment must flag anything that could change behavior inside v1,
even without a spec-text change: stricter validation, consent or scope behavior,
OAuth flows and error codes. Record compatibility concerns before deployment;
do not bypass them by copying or modifying Tessera internals here.

## Tessera v0.1.0 contract status (2026-11-08)

Published Tessera `v0.1.0` at `6367a78c8ee89b97acbb14c8680b22d961bac8fb`
now specifies the three contracts requested below. The table retains the original
request and records its released disposition. This establishes upstream contract
availability, not completed app acceptance, historical overlap or deployment approval.
See the [pin assessment](../tessera-dependencies.md#v010-consumer-qualification--2026-11-08).

## Original requested Tessera specification agreement

These were requested additions to “Versioning and compatibility” in
`tessera-protocol-v1`. Further protocol changes use the supported upstream
process; do not edit Tessera or contact its team automatically.

| Topic | Requested contract | Status and local alternative |
| --- | --- | --- |
| Selection | A client selects the highest version present in both discovery `protocol_versions` and its own supported versions. | Specified in v0.1.0; record the client/server version intersection during qualification. No shared version must not be treated as compatible. |
| Overlap | Serve protocol majors side by side for at least 12 months after a new protocol major ships, and never remove a version without notice. Continue serving v1 while released apps use it, even if that exceeds the minimum. | Specified in v0.1.0 from the successor protocol major first production release, with leap-day clamping. Retain dated evidence and explicit consumer confirmation; block incompatible candidates. |
| Deprecation | Announce planned version retirement so the app can warn people ahead of time; suggested mechanisms are discovery `deprecated_versions` entries with sunset dates and/or an RFC 8594 `Sunset` header on v1 responses. | v0.1.0 specifies optional discovery `deprecated_versions` with UTC `sunset` and at least 14 days of retirement notice; no HTTP Sunset header is required. Dates never disable routes. Upgrade coordination remains separately required. |

These are compatibility commitments and specification clarifications, not a
request to change existing v1 behavior. A deprecation signal alone does not waive
continued support or the minimum overlap. The two-week upgrade notice below is
not an agreed protocol-retirement notice period.

## Test first, then approve an upgrade

For every new Tessera release from `v0.1.0` final onward:

1. Put the exact candidate on a separate test server before any server real apps
   use. Use isolated synthetic data, accounts and invitations; never repurpose
   the frozen demo servers or their review material.
2. Prepare a qualification package for the app team with the test endpoint, exact Tessera and
   Voptrack revisions, release notes, spec and vector diffs, and 2–3 test
   invitations. Explicitly report an empty diff if there is none. Deliver
   invitation material through an authorized private channel, never tracked
   files or transcripts. Obtain authorization before sending it.
3. The app team runs its vector tests, live integration tests and both walks
   (about half a day). Confirm the intended walks with the team; do not invent
   acceptance coverage. Record its explicit result against the exact candidate.
4. Upgrade an app-facing server only after the app team's confirmation, the
   notice period, applicable Voptrack checks and recovery preparation, and any
   active freeze have all been satisfied. A local pass or half-day wait does
   not substitute for app-team confirmation. Material candidate changes require
   renewed assessment and confirmation.

Keep a candidate record with version/diff evidence, test scope and results,
app-team confirmation, notice date and proposed upgrade date. Preserve existing
forward-only migration and restore-based rollback rules. No new server test or
app acceptance is claimed by this document.

## Reciprocal notice

Give at least two weeks' notice before upgrading any server the app connects to.
The app team requests the same lead time before an app release changes which
protocol features it needs. Record the coordination; repository policy cannot
establish that the other team has accepted or sent a notice. Notice does not
replace compatibility confirmation or authorize changes to a frozen deployment.

## Ownership and follow-up

Voptrack owns the freeze, deployment selection, test-server qualification, upgrade
records and its Console/API compatibility. Tessera owns protocol selection and
advertisement contracts, additive protocol/server behavior, parallel protocol
serving and deprecation mechanisms. The app team owns client version selection,
feature fallback, its acceptance tests and app-release notice.

Track missing upstream capabilities in [Tessera dependencies](../tessera-dependencies.md).
Use [repository rules](../../AGENTS.md) and the
[operations upgrade procedure](../../release/README.md#moving-to-a-new-version)
for implementation work. None of this authorizes a deployment, notification,
publication or Tessera change as part of documenting the request.
