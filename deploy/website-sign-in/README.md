# Default website sign-in (Tessera rc.4)

Fresh Console-enabled Voptrack Home, Research and Personal installations use
the branded single-form sign-in by default. The form has account, password,
an unchecked **Keep me signed in** choice and **Sign in**. Credentials still go
directly to Tessera; Console is not a password proxy.

This policy is only for the deployment's own website. Separately registered
web/native apps still require ordinary OAuth consent, even if they claim the
Voptrack name. Study participation and exchange consent stay separate.

## Default and operator controls

Init provisions dedicated identity `voptrack-website`, bound to the exact Console
HTTPS origin and `/auth/callback`. Its ceiling is `read write insert` plus only
administration the signed-in person already holds. It grants no schema, erasure
or dataset-creation permission and no access to another person's private records.

`VOPTRACK_WEBSITE_SIGN_IN` controls selection:

- `auto` (default): enable on fresh web installations; preserve an existing
  generated policy, including its absence, on an ordinary init rerun.
- `on`: explicitly select the default policy, including for a legacy installation.
- `off`: disable the owned-website policy and use the separate introduction,
  credential and OAuth-consent flow. This overrides explicit client variables.

App-only deployments (`VOPTRACK_CONSOLE=off`) receive no website policy.
Existing deliberately disabled installations are not silently opted in.
An existing policy whose origin no longer matches requires explicit configuration;
do not retain a stale callback or silently expand a custom authority ceiling.

Advanced operators may still choose an exact dedicated ID and explicit ceiling:

```
VOPTRACK_WEBSITE_CLIENT_ID=my-dedicated-website
VOPTRACK_WEBSITE_DATA_GRANTS=read write insert
VOPTRACK_WEBSITE_ADMINISTER=yes
```

Use an ID that was never publicly registered. Explicit custom IDs still require
an explicit administration setting (`yes` or `no`); the configured grants may
be narrower or empty. These settings are independent of branding.

`VOPTRACK_CONSOLE_URL` takes precedence when binding the origin; otherwise init
uses the public address or private Console port. No wildcard or request-derived
origin is accepted. Init publishes only the selected identity and origin to
`/var/lib/voptrack-public/website-sign-in.json`; Console reads that derived file.
A disabled receipt overrides an old environment ID. Missing receipts retain the
legacy environment path, while malformed or wrong-origin receipts fail closed.

Fresh web installations also receive the packaged private branding input.
Existing custom or deliberately unbranded setups retain their choice.
`VOPTRACK_IDENTITY_BRAND=off` suppresses automatic initial branding; an explicit
input file remains authoritative. See [identity branding](../identity-brand/README.md).

## Applying and compatibility

Stop Console and Tessera, run init, then restart the selected rc.4 deployment.
Keep the chosen environment on subsequent init/recovery operations. These are
website configuration changes, not a Tessera pin upgrade. App Review servers
remain frozen; this source default does not authorize applying a configuration
to them or changing release-client compatibility.

An upgrade from rc.2/rc.3 still requires complete backups and review of
Tessera's release notes. Registry format 5 is forward-only: never launch an
older binary against upgraded data. Replacing only Console is not an upgrade
procedure. Retain matching pre-upgrade data/configuration and recovery material;
rollback requires restoring that set. The owner's App Review freeze and
app-team compatibility/notice process take precedence over this procedure.

The ordinary public client remains available for explicit sensitive authorization
and account-management return links. A refused first-party code exchange does
not fall back to public registration. Changes between policies require a fresh
`/login` request; an old OAuth page may expire during maintenance.

## Session handling and validation

The Console links directly to Tessera's bound state/S256 PKCE request and
validates the `console_session.origin` received only at code exchange. It ignores
remember choices in URL parameters. Unchecked means a browser-session cookie;
checked means the existing encrypted refresh store and an opaque Secure,
HttpOnly, SameSite=Lax, origin-specific cookie, capped at 30 days from sign-in.
Refresh does not extend that deadline. Signing out removes the remembered
session. Passwords and tokens never enter browser cookies.

One website installation can expose multiple authorized datasets. Dataset
projections share one synchronized rotating credential, including after restart,
so refresh is not spent once per dataset. Live Tessera permission checks remain
in place. No shared service or administrator credential replaces the person's
credential.

Focused Go tests cover exact authorization parameters, ordinary consent retention,
missing/wrong-origin/malformed persistence handoffs, shared refresh under concurrent
reads, serialized concurrent callback completion, encrypted remembered-session
restart/rotation, non-sliding expiry and
logout replay refusal. The Console and Research release gates continue to test
the default ordinary flow. Browser/demo and actual-image results are recorded
separately; unit tests alone do not prove browser completion or migration safety.
