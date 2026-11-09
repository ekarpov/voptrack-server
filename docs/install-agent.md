# Agent runbook: install prepared images

Use this runbook when the user wants ready-made Home or Research images. No Go,
source build or GitHub account is needed. Read the matching
[human installation guide](installation.md), also included as `INSTALL.md`, and
its host and IP certificate links before changing the host.

## Agree the installation

Obtain the exact published release, authorized host access, Home or Research,
small registry download or offline download, fresh directory, stable project
name, address, time zone and Console mode. Confirm encrypted storage with the
host owner. Both source repositories are public; no private repository access is
needed. A missing release is a stop, not permission to use `latest` or build from
source without the user's choosing that other path.

## Follow the steps

1. Inspect OS, CPU, Docker/Compose versions, storage, ports, DNS when using a domain, and network
   overlap. Use the platform list on the exact release. Preserve other services.
2. Follow `INSTALL.md` sections 1–2 to prepare the host and download the selected
   product/version/platform from GitHub Releases. Verify the outer checksum
   before extraction, then all inner checksums. Retain `release.json` and notices.
3. Follow section 3. Use a new project and private `.env`. Product and exact image
   are fixed in Compose. Never point a new product at existing data or CA volumes.
   Home defaults to its web Console. Keep the Research Console enabled and
   reachable by participants. Choose a public domain for open recruitment or an
   IP with a local certificate for a group that can install the root. Follow the
   linked IP guide for network settings before first start.
4. Small download: section 4 pulls the pinned public GitHub Packages images and
   starts them. No login is needed. Offline download: run `load.py`, retain the
   equivalence receipt and use `-f compose.offline.yaml` for every command with
   pulls disabled. Do not substitute an online file or skip a loader refusal.
5. Follow section 5 to verify health and both discovery endpoints over HTTPS.
   For local HTTPS, export the installation root and use it with `curl --cacert`.
   The user installs the verified root on the phone and checks Safari at the
   configured address. Home invitations also support separate in-app trust.
   Have the user retrieve the initial password privately, sign in and approve
   any new app CA pin. Do not approve trust on the user's behalf.
6. Verify the Home or Research navigation, real app connection and authorized
   member/participant access separately. A researcher login is not participant
   acceptance. No real people may be enrolled just to create test fixtures.
7. Follow section 6 to test a normal restart and retain off-host recovery material
   and a separately kept key. Arrange a host reboot if it affects other workloads.
   Test recovery only on a fresh isolated target. Use the operations guide at the
   manifest's exact source revision.

## Stop and report

Keep partial state if publication is missing, files or identities differ, the
CPU is unsupported, encryption is unconfirmed, host permissions are missing,
ports conflict or HTTPS fails. Never reset a server or delete volumes as a remedy.
Never touch frozen review demos or their reserved invitations.

Report release, source SHA, product, platform, image digest/loaded ID, directory,
project name, exact Compose file, HTTPS address, actual checks and unperformed
user/app/recovery steps. Report secret paths only; never print passwords,
invitation URLs, tokens, backup keys, account identifiers or record contents.

This runbook authorizes only the requested installation. For upgrades preserve
backups and matching recovery material, use the same project/volumes and follow
app notice/confirmation and freeze rules. `down -v` is destructive. Publication,
unrelated host changes and deleting an installation need their own authorization.
