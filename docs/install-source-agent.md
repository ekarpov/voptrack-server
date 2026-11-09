# Agent runbook: build and install from source

Use this runbook when the user chooses to clone the public repository, build on
the host and run Home or Research. It is an installation task, not permission to
change code or an existing server. Read the matching [human guide](install-source.md)
and its host and IP certificate links before changing the host.

## Agree the installation

Obtain the exact published tag or reviewed full commit, authorized host access,
Home or Research, installation directory, stable project name, address, time
zone, Console mode and confirmation of encrypted storage. Both repositories are
public. Do not request GitHub credentials, set global SSH rewrites or pass secrets
into Docker builds. Use host credentials only through the user's access mechanism.

## Follow the steps

1. Inspect OS, CPU, Docker/Compose, Go, disk, encryption, occupied ports, DNS when using a domain, and
   network ranges. The human recipe targets fresh Ubuntu x86-64. Preserve other
   workloads. Install host packages only within the user's authorized scope.
2. Run the copy-and-paste steps in `install-source.md` sections 1–2. Check the Go
   archive checksum, clone, detach at the exact tag/commit, record its full SHA,
   download public modules and build. A failed build is not permission to use an
   old local tag. Verify the built image reports the tracked Tessera pin.
3. Follow section 3 to create a new installation directory. Preserve the source
   checkout separately. Fix the product and the built image identity in the
   copied Compose file. Use private permissions for `.env`; validate values.
   Do not reuse another project's volumes, change its product or reset markers.
4. Before startup, choose a public domain and public certificate or a reachable
   IP and local certificate. Follow the IP guide linked by the human guide for
   interface binding, root export and checks with that root. Keep TLS verification
   on and wait for health. The user installs and approves trust on the phone.
5. Have the user retrieve the initial password in their private terminal and
   complete interactive sign-in. Verify Home/Manage Home or Research/Studies,
   app connection and participant/member access separately as authorized. For
   local HTTPS, have the user verify Safari with the installed root and verify
   app joining separately. Home invitations support separate in-app trust.
6. Test a normal restart, preserve files and volume names, and follow the exact
   revision's backup/recovery guide. A synthetic gate is not the user's restore
   drill. Use a fresh isolated target for recovery testing.

## Stop and report

Preserve partial state if the version cannot be fetched, build/version checking
fails, storage encryption is unconfirmed, permissions are missing, ports conflict
or TLS checks fail. Do not disable TLS, broaden access or delete data to continue.
No frozen App Review demo may be changed by this task.

Report source SHA, Tessera version, image ID, host type, installation directory,
project name, Compose file, HTTPS address, checks performed and remaining actions.
Report secret file paths only. Never print passwords, invitation URLs, tokens,
backup keys, account identifiers or record contents into the conversation.
This runbook does not authorize publication, branch cleanup or host changes
outside the installation. Before an app-facing upgrade, follow the project's
notice, compatibility-confirmation and active-freeze rules.
