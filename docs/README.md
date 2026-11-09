# Voptrack documentation

Start with a task, then follow the detailed reference when needed. For a
published installation, use the guides and downloads for the exact release.

## Quick guide

| Task | Guide |
| --- | --- |
| Clone, build and install | [Source installation](install-source.md), [source agent runbook](install-source-agent.md) |
| Install prepared images | [Image installation](installation.md), [image agent runbook](install-agent.md) |
| Build and try the current source | [Development](development.md) |
| Use Home | [Home](../release/household/README.md) |
| Create and run a study | [Research](../release/research/README.md) |
| Configure, back up, restore or upgrade | [Operations reference](../release/README.md) |
| Understand the system | [Architecture](architecture.md) |
| Change the repository with an agent | [Agent guides](agents/README.md), [repository rules](../AGENTS.md) |

## Detailed references

| Area | Documents |
| --- | --- |
| Low-level deployment | [Native/custom containers](../deploy/README.md), [manual Tessera operations](operating.md), [protocol scenarios](../scenarios/README.md) |
| Backup and recovery | [Operator decisions](backup-recovery-operations.md), [off-host copies](../release/off-machine/README.md), [input preflight](../release/recovery-set/README.md), [guided recovery](../release/guided-recovery/README.md) |
| Setup and recovery acceptance | [Home setup](../release/home-setup/README.md), [recovery export](../release/recovery/README.md), [kit](../release/kit/README.md), [native settings](../release/native-settings/README.md), [fault supervisor](../release/native-settings/persistfault/README.md), [assurance](../release/recovery-assurance/README.md) |
| Product qualification | [Console](../release/console/README.md), [app-only Home](../release/home-app/README.md), coverage sections in the Home and Research guides |
| Client integration | [Swift protocol integration](swift-integration.md), [standalone OAuth](swift-oauth.md), [invitation retest evidence](requests/tessera-native-invitation-completion.md), [compatibility and upgrade policy](requests/server-compatibility.md) |
| Upstream dependency | [Supported interfaces and qualification ledger](tessera-dependencies.md) |
| Retained Research analytical CSV | [Aggregate suppression and CSV contract](research-suppression.md); analytical browser dashboards are retired |
| Vocabulary and catalogue | [Vocabulary](../specs/voptrack-vocabulary-v1.md), [illustrative tracks format](../specs/voptrack-tracks-format.md), [versioned app exports](../catalogue/README.md) |
| Internal contracts | [Acquisition transport](../internal/acquisitionclient/README.md), [operator branding](../deploy/identity-brand/README.md) |
| Maintainer qualification and distribution | [CI](../ci/README.md), [publication checklist](../release/distribution/README.md) |

## Documentation maintenance

Each gate's README keeps its coverage and exclusions beside the gate. Dated
consumer receipts and versioned catalogues preserve evidence and compatibility;
new summaries should link to them rather than replace them.

Agent installation instructions remain at `docs/install-agent.md` because bundles
package them as `INSTALL-AGENT.md`; the human guide is packaged as `INSTALL.md`.
