# Protocol deployment scenarios

These are developer/operator examples and disposable conformance gates around
Tessera. To install current Voptrack Home or Research, start with the
[installation guide](../docs/installation.md) or [source development](../docs/development.md).

## Quick guide

| Scenario | Purpose | Important limit |
| --- | --- | --- |
| [Evaluation](evaluation/README.md) | Throwaway synthetic demonstration | Localhost only; teardown destroys its volume |
| [Single user](single-user/README.md) | One server behind Caddy | Read its TLS, backup and real-host exclusions |
| [Multiple users](multi-user/README.md) | Separate dataset access and isolation | Follow the scenario's exact provisioning and coverage limits |
| [Household](household/README.md) | Shared-store protocol arrangement | Owner can reach created stores; this is **not current Voptrack Home** |

## How to use them

Read the selected README before starting or tearing down anything. Use an
explicit candidate `VOPTRACK_IMAGE`, synthetic records and isolated projects.
Verification writes data; never point a disposable gate at personal storage.

The household scenario predates current Home's sealed-custodian private-store
model. Its owner access and shared-store design are intentional properties of
that example. Do not use it as the Home installation or privacy guide. Each
scenario retains its own evidence and exclusions because conformance alone does
not prove product behavior, public HTTPS or recovery readiness.
