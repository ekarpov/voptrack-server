# Agent guides

Task-oriented guides for agents working on Voptrack. Repository changes follow
[AGENTS.md](../../AGENTS.md). These pages provide
navigation and implementation context; they do not grant host or publication
authority.

## Quick guide

| Task | Read |
| --- | --- |
| Build and install from public source | [Source runbook](../install-source-agent.md), [human guide](../install-source.md) |
| Install a published release on a user's host | [Public installation runbook](../install-agent.md) and matching [human guide](../installation.md) |
| Set up a source checkout or synthetic fixture | [Development](../development.md) |
| Find components and authority boundaries | [Architecture map](architecture.md) |
| Choose deployment and recovery paths | [Deployment options](deployment.md) |
| Add or change a feature | [Extending Voptrack](extending.md) |

## Working sequence

Read only the guides relevant to the task, inspect the implementation and its
callers, make the change, then use the applicable validation policy. Keep exact
version and evidence limits in reports. Source implementation, a passing local
gate and a released client are separate facts.

For public installation, use the exact release's `docs/install-agent.md`, also
shipped as `INSTALL-AGENT.md`. A development checkout's instructions cannot
substitute for a published, qualified release.
