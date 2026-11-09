# Agent guide: deployment options

Choose a deployment before editing configuration. The public
[installation runbook](../install-agent.md) governs a user's published-release
installation; [operations](../../release/README.md) governs lifecycle details.
Public release and fresh-host qualification remain pending.

## Quick guide

1. Establish source development versus a published-release installation.
2. Choose product, Console exposure and TLS trust before first start.
3. Use images and Compose files from the same exact revision.
4. Verify access and recovery separately; retain the exact checks performed.

## Supported product shapes

| Shape | Configuration | Constraints |
| --- | --- | --- |
| Home with Console | `VOPTRACK_VARIANT=household` | Private member stores, selected sharing; public or local HTTPS |
| Research with local HTTPS | Research plus `VOPTRACK_TLS=local` and an explicit network bind | Reachable IP or private name and trust installed on participants’ devices. Use a public domain for open recruitment. |
| Home on the LAN | Home plus `VOPTRACK_TLS=local` and explicit LAN bind | Trusted invitation/CA pin for app; separate browser trust |
| Home with private Console | Home plus `VOPTRACK_CONSOLE_PRIVATE=yes` and reviewed Console bind | Forward only 80/443; Console listener stays private |
| App-only Home | Home plus `VOPTRACK_CONSOLE=off` | Host first-owner-device flow; app handles member tasks |
| Guided Home setup | Fresh Home plus matching setup overlay | [Protected setup](../../release/home-setup/README.md); preserve interrupted state |
| Research | `VOPTRACK_VARIANT=research` | Public Console for study information and joining |
| Personal server | `VOPTRACK_VARIANT=standard` | Single-person product shape; see operations reference |

Product and Console mode are fixed at first start. Attest disk encryption only
when it is established. Local TLS does not require a public domain, but it does
require explicit trust and reachability. Stock Caddy has no configured DNS-provider
modules; VPN naming alone does not configure HTTPS.

## Delivery and alternatives

Published bundles select pinned GHCR images or verified image archives. Archive
installs use the generated `compose.offline.yaml` for every command with pulls
disabled. Neither path is currently qualified for public installation. Never
replace an unavailable release with `latest` or an unsolicited source build.

Source installations use the [source runbook](../install-source-agent.md).
Authorized source development uses [build instructions](../development.md),
`release/compose.yaml`, and explicit candidate image selection. Cross-store image
transfer requires the [identity procedure](../../release/README.md#transfer-an-image-to-another-installation-host).

[Low-level deployment](../../deploy/README.md) covers native/systemd examples
(unexecuted), custom PostgreSQL Compose and Kubernetes constraints (no chart).
[Scenarios](../../scenarios/README.md) exercise protocol arrangements; their
household scenario is not current Home and has a different owner-access model.

## Lifecycle and acceptance

Check the [App Review freeze and compatibility policy](../requests/server-compatibility.md)
first. General lifecycle procedures do not authorize changes to frozen demos or
bypass app-team qualification and notice before app-facing upgrades.

Use the exact Compose file(s), stable project name and persistent volumes.
Offline provisioning takes the exclusive lock; supported maintenance stops both
Console and Tessera. Never clear markers or delete volumes to bypass a failure.
`down` preserves volumes; `down -v` destroys them.

Use [backup/recovery observations](../backup-recovery-operations.md) and the
[recovery contracts](../../release/README.md#backups). Keep keys separate from
archives and retain independently held matching material. Restore into a fresh
isolated target. Back up before and after upgrades; migrations are forward-only.

A local gate does not establish public DNS/TLS, real-device behavior, independent
custody or fresh-host acceptance. Publication follows the
[maintainer checklist](../../release/distribution/README.md), including its owner
review stops. Do not publish as part of documentation or installation work.
