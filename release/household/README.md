# Voptrack Home

A server for a household. Every member has a **personal store** that all their
devices record into, private to them. Each member chooses which of their tracks
the household sees together in **Home**, and can change their mind at any time.
People record through the Voptrack app. The web Console provides household
views, sharing controls, record histories and household management.

**Status: development.** No image is published yet; real-home and device
qualification remain pending. Local gates cover synthetic deployments.

## Quick guide

1. [Install Home](../../docs/installation.md), choosing the Console and TLS mode
   before first start. For current source, use [development](../../docs/development.md).
2. Sign in as **owner** at your configured HTTPS address. Fresh installations
   use the single owned-website sign-in form; ordinary app OAuth remains separate.
3. [Invite the household](#add-the-household), then connect each person's app.
4. Choose [Sharing with Home](#sharing-with-home), then use the household views
   below. Saved views are outside the first-release web UI.
5. Retain [backup and recovery material](../README.md#backups) off-host.

## Set it up

Use the installation guide's persistent settings and matching image/Compose
revision. For local Home, follow its CA-pin invitation and browser trust steps;
public DNS and router forwarding are unnecessary. Read the owner's access and
rebind limits below before inviting people.

## An invitation lets someone into the household

Send an invitation link only to the person it is for. Whoever redeems it
becomes a member, and a member reads what everyone shares with Home. (Once
household sharing moves to Tessera's group readers, that includes shares made
before they joined.) The server issues at most 100 invitations over its life,
and unredeemed ones can be stopped: they are the controls.

To run Home with no web interface at all, the app doing everything, see
[how to choose](../README.md#choose-how-to-install).

## Keep the Console at home

To keep the web interface off the internet, while the app still syncs from
anywhere, add `VOPTRACK_CONSOLE_PRIVATE=yes` and the machine's home address as
`VOPTRACK_CONSOLE_BIND`. See [how to choose](../README.md#choose-how-to-install).

## Add the household

Open **Manage Home → Members & invitations**, then choose **Invite a member**.
You get a one-time link; send it to one person over a channel you trust. They choose their name and password — you
never see the password — and get a store of their own, named after them.

They connect the Voptrack app from **Connect the app**, which shows a code to
scan. A local-TLS Home's connect links also carry its CA public-key pin, which
the person explicitly approves in the app. Use the
[local-CA invitation instructions](../../docs/installation.md#local-home-join-the-app-with-an-approved-ca-pin)
for joining without a system-wide CA profile; browser Console access needs
separate browser/system trust.

## Who can see what

| | Sara's store | Ben's store | Home |
| --- | --- | --- | --- |
| Sara | everything | **nothing** | what everyone shared |
| Ben | **nothing** | everything | what everyone shared |
| Owner | **nothing** | **nothing** | what everyone shared |

**The owner cannot read anyone's personal store.** Members' stores are held by a
sealed account nobody can sign in to; the owner runs the server and invites
people, and cannot add themselves to a member's store.

**Said plainly: whoever runs the machine can read the database files.** This
protects a member's records from the owner *using Voptrack*, not from somebody
with the disk.

## Sharing with Home

On **Sharing with Home**, a member ticks the tracks the household sees together —
spending, say, and not their mood diary. Home's dashboards add up exactly what
was ticked, across everyone. Unticking stops Home reading that track at once.

Choosing who reads a store requires that member's own administration authority.
The default owned-website policy includes only administration the signed-in
person already holds. If an ordinary OAuth connection is used instead, approve
**Administer on your behalf** for the member's own store; the owner cannot grant
themselves access to it.

Home is a service that reads, never writes, and reads only what was ticked.

## Household views

**Home** summarizes shared household activity. **Spending** combines readable
member transactions, keeping currencies separate. Named household destinations,
such as Rex, appear when their shared data is available. **Add to household**
provides the addition entry point; **Browse records** opens detailed histories.
The default Home does not show the combined technical entry table.

Period presets, particular months, custom dates, members and relevant household
or category choices use shared selection controls. **Search entries** is separate:
applied search stays visible and can be cleared while retaining the selection.

**My data**, when available, lists data connected to the current sign-in. It does
not assume every connected dataset belongs only to that person. Saved views are
removed from both first-release product UIs. Existing definitions remain stored;
Home's library and create/open/remove routes are unavailable. Panel/CSV readers
remain under their existing authority.

The owner uses **Manage Home** for members/invitations and **Server & backups**.
Backups have a concise summary, with downloads, schedule and recovery details in
expandable sections. Stop track sharing is not offered in the web UI; see the
[operator capability](../README.md#what-a-homes-owner-can-do-with-members-tracks).

Numeric summaries require supported Track semantics. Unknown types keep their
individual history; legacy questions that could mix currencies or units link to
Track history instead of presenting a misleading aggregate. Categories and named
subjects come from app-defined objects, never inferred text. Common Track creation
and editing remain subject to the agreed app catalogue and household dataset contract.

## Leaving

**Download my data**, under **Account & data**, gives a member every track and
record in their store as JSON, read with their own sign-in. Attachments are listed, not
included.

**Remove my data** erases every record and track in a member's store. The member
signs in again and ticks **Erase this dataset** on Tessera's own screen; the
Console shows what will be destroyed and erases only when they confirm.

Erased data stays inside the server's backup archives until they are replaced
(seven by default, so a week at the default schedule). It also stays in any
copy of an archive kept elsewhere. Restoring a backup does not bring it back.
Tell members this before they rely on erasure.

**Delete my account**, beside it, happens on Tessera's own page: the member
types their password there, sees exactly what will be erased, and confirms. Their
store goes with the account, although the sealed custodian co-holds it.

## Lost phones and forgotten passwords

Under **Manage Home → Members & invitations**, the owner can **suspend** a
member: every device of theirs is refused at once and nothing in their store changes. **Reinstate** undoes it.

Nobody can reset a member's password, the owner included, because nobody else
administers a member's store. Each member is shown a **recovery code** once,
when they accept their invitation. Tell them to keep it somewhere safe: it is
the only way back into their account if they forget their password.

## What this has not been shown to do

- **Run in a real home.** Gates use synthetic localhost TLS. Public-certificate
  installs need domain/reachability checks; local-only Home needs explicit CA
  trust and LAN reachability, with no public forwarding required.
- **Independent off-host protection.** Local collection and replication tests
  do not establish independent custody or a real destination. Retain matching
  recovery material and verify recovery; see [backups](../README.md#backups).
- **The app's half of scanning the code** is with the app's maintainers.

## Maintenance coverage

The placement gate (`make household-check`) stops both producers before its
host token fixture, then starts explicit services with `--no-deps --wait`.
Its immutable-variant negative test also stops both first, so the asserted
variant error cannot be masked by maintenance refusal. The app-only gate covers
Console-off guard refusal, first owner-device creation and lost-phone replacement;
see [its coverage](../home-app/README.md). These remain disposable local
checks, not real-device or off-machine custody evidence.

### First-release development fixture (2026-11-08)

The development seeder supports both ordinary OAuth consent and the configured
first-party Console sign-in, with persistence off and same-origin callback
validation. This aligns the recovery/Home-development fixture with the accepted
UI; it does not add real-device or remembered-session acceptance.
