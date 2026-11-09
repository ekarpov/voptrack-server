# Voptrack repository rules

- Tessera owns the protocol, specification, vectors, conformance suite and
  reference server. Use its supported interfaces. Voptrack owns deployment,
  vocabulary and Console integration for Home and Research. Preserve protocol
  v1 and each person's authority. Record upstream gaps in
  [Tessera dependencies](docs/tessera-dependencies.md).
- Use synthetic development and test data. Keep credentials, private records
  and deployment details out of source, test output and reports.
- The [vocabulary](specs/voptrack-vocabulary-v1.json) and
  [illustrative Track catalogue](specs/voptrack-tracks-v1.json) are append-only.
  Preserve existing definitions and record unresolved catalogue findings.
- Keep deployment approval separate from source and image publication. Follow
  the [compatibility and upgrade policy](docs/requests/server-compatibility.md),
  including any active App Review freeze.
- Start with the [agent guides](docs/agents/README.md). For a published install,
  use the runbook for the exact release. See [development](docs/development.md)
  for source checks and [distribution](release/distribution/README.md) for
  release checks.
