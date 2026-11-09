# Contributing to Voptrack Server

Voptrack Server owns deployment, vocabulary and Console integration for Home
and Research. Use Tessera's supported interfaces for protocol and server behavior.
Start with the [repository rules](AGENTS.md) and the [development guide](docs/development.md).

## Public development workflow

1. Start a short-lived topic branch from current `origin/main`. Use
   `codex/<topic>` for agent-created branches. Bring any work from the private
   archive over as a new, reviewed change on the public baseline.
2. Review the staged files and commit messages before pushing. Use synthetic data.
   Keep credentials, private client details, deployment addresses, record values
   and raw operator evidence in protected storage. Apply the same review to PRs,
   issues, comments, attachments, CI output and release notes.
3. Open a PR against `main`. Use a draft for substantial work still in progress.
   Explain the problem, the resulting behavior, the checks run, and any material
   limits. Bring the branch up to date when needed, run the checks that cover the
   final changes, and resolve review conversations.
4. Obtain maintainer authorization before merging. Use squash merge by default
   and keep the final commit message focused on the change. Remove merged topic
   branches under the agreed delivery workflow. Preserve shared history and
   published tags.

Keep `main` releasable. Maintainers configure PR review, resolved conversations,
and any required checks in GitHub separately. GitHub Actions is currently
disabled, so report the checks that actually ran and include the tested commit.
A skipped or unavailable check leaves that coverage open.

## Checks and releases

Use the [server skill](.agents/skills/voptrack-server/SKILL.md#validation) to
choose checks. Follow the [distribution guide](release/distribution/README.md)
for release qualification. Run the complete 23-gate suite on the clean commit
on `main` that will be tagged. A PR result applies to the commit it tested.
Release tagging, publication and deployment each require authorization for the
specific action. Follow the [compatibility policy](docs/requests/server-compatibility.md)
before changing an app-facing deployment.

For a security issue, request a private contact channel. Keep reproductions,
credentials and affected data out of public reports.
