---
name: voptrack-server
description: Work on Voptrack Server deployment, Console, vocabulary, Tessera integration, public pull requests, and release verification.
---

# Voptrack Server

Follow the [repository rules](../../../AGENTS.md). Start from the applicable
[agent guide](../../../docs/agents/README.md) and load the sections relevant to
the task.

## Windows and WSL

Run repository shell commands in Ubuntu WSL, including Git, Go, Python, and Make.
Use PowerShell to launch WSL. Keep scripts and source files LF as specified in
`.gitattributes`. Use the Go version in [development setup](../../../docs/development.md).

## Public delivery

Use the [public development workflow](../../../CONTRIBUTING.md#public-development-workflow)
for branching, review and merges. Start from `origin/main` and keep the PR scoped
to the requested work. Summarize only checks that ran and preserve private
evidence separately. Before pushing or uploading, review both the content and
its metadata for private details.

## Validation

- For documentation changes, check links and anchors, consistency, and
  preservation of existing requirements.
- For code or configuration changes, run the focused tests and relevant
  integration gates. See [development checks](../../../docs/development.md#checks)
  and the gate coverage in [CI](../../../ci/README.md). Report checks that
  could not run and the resulting coverage gap.
- Before a release or Tessera pin change, run the full suite on the exact
  clean commit. Use `make ci` on a suitable Linux host or `make ci-remote`
  with authorized host access. A release needs the passing record for its
  final commit. See [distribution](../../../release/distribution/README.md).
- When adding or changing a gate assertion, preserve the existing coverage
  and update its README. For a behavior check, prove that the named assertion
  catches a compiling defect, then restore the code and replay the check.

## Build and operate

Use `make image` to build with the pinned Tessera module. Verify the version
inside the built image. Use the [CI settings guide](../../../ci/README.md#remote-host-settings)
for remote work. Keep local host settings, credentials, raw logs, and CI notes
private. Include sanitized test summaries with public release artifacts.

For a user's published installation, use the versioned
[source runbook](../../../docs/install-source-agent.md) or
[image runbook](../../../docs/install-agent.md). Use synthetic data for development
and qualification. Follow the [app compatibility policy](../../../docs/requests/server-compatibility.md)
for deployments, including the active App Review freeze, consumer acceptance,
notice, and recovery requirements. Release publication is a separate action.

## Tessera and consumers

Use Tessera's supported interfaces and keep protocol or reference server changes
upstream. Record missing guarantees and local alternatives in
[Tessera dependencies](../../../docs/tessera-dependencies.md). Identify the
exact upstream release or source revision and compare the actual interfaces
before claiming consumer compatibility. Obtain communication authorization
before sending findings upstream.
