# Agent guide: extending Voptrack

Use this workflow for product features, integration and deployment changes.
[AGENTS.md](../../AGENTS.md) remains authoritative.

## Quick guide

1. Locate the component with the [architecture map](architecture.md).
2. Read the relevant implementation, callers, tests and feature README.
3. Keep the change within Voptrack's authority and supported Tessera interfaces.
4. Update the user guide and the gate's coverage/limits alongside the change.
5. Run focused checks, review the diff, then follow the delivery policy.

## Choose the owning layer

Protocol, storage and conformance belong to Tessera. Record a missing supported
capability, the minimum upstream request and local alternatives in
[Tessera dependencies](../tessera-dependencies.md). General usefulness alone does
not block a local integration through supported interfaces. Do not copy internals,
patch the dependency repository or contact its team automatically.

Voptrack owns Home/Research semantics, Console, vocabulary, configuration and
recovery integration. Preserve per-person authority. New handlers must check
access, protect mutations and avoid exposing private values in errors or logs.
Inspect existing OAuth/session and CSRF handling before adding routes.

## Common changes

| Change | Inspect and preserve |
| --- | --- |
| Console page | `cmd/console/main.go` routes, handler, templates, `static/console.css`, language catalogues and focused tests; keep server rendering, CSP and all language keys |
| App-facing study behavior | `cmd/console/api.go`, [Swift integration](../swift-integration.md), [OAuth](../swift-oauth.md); compare real consumer code before claiming compatibility |
| Research aggregates | `cmd/console/suppression.go`, [suppression contract](../research-suppression.md); retain threshold enforcement for panels and CSV |
| Connect-app QR | `cmd/console/connect.go`; keep `rsc.io/qr`, explicit local-CA trust and secret-free connect metadata |
| Vocabulary or illustrative catalogue | Append new entries only; never alter existing JSON entries or invent annotation keys to pass checks |
| Generated app catalogue | Refresh from an app export with pinned provenance; preserve published versions, never hand-edit generated files |
| Configuration or image | `deploy/voptrack-init`, `cmd/deployctl/`, `release/compose.yaml`, `deploy/Containerfile`; maintain lock, ownership and credential boundaries |
| Backup or recovery | Owning `internal/` package and feature README; preserve opaque transfer, independent inputs, fresh targets and interruption state |

## Validate and document

For documentation-only work, check local links, command consistency and policy
preservation. No unrelated image build is needed. Code changes use focused tests
and the relevant gate; [development checks](../development.md#checks) and the
[publication checklist](../../release/distribution/README.md#1-select-and-qualify-the-source)
define broader requirements. Full CI is required for release milestones and
Tessera pin changes. `make catalog-check` intentionally reports unresolved sketch
questions outside `make check`; do not silence it.

When adding or removing a gate assertion, update its coverage and exclusions.
Follow the project's planted-defect proof requirements. Never describe an
unexecuted path as verified, a partial CI run as full, or an unpublished source
feature as available to clients. Preserve dated evidence and independent-review
stops. Use synthetic data and report credential paths, never values.
