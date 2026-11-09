> Update, 6 November 2026: verified pinned Tessera rc.4 provides the requested
> first-party Console form and bound remember-me handoff. This document preserves
> the original request; the remaining work is Voptrack integration and local
> qualification. See [operator setup](../../deploy/website-sign-in/README.md).

# Request: first-party website sign-in and operator branding

Status: prepared for owner review; not sent.
Evidence baseline: Voptrack's local demo pins Tessera v0.1.0-rc.2. Confirm which
parts are already available on the team's current branch before implementing.

## Problem

Voptrack Home and Research use OAuth/PKCE for both native apps and their website.
Their web Console has Voptrack's logo, colors and typography, but signing in moves
the user to generic identity/consent pages with different styling and app-centric
wording. The visual change makes the website feel disconnected and makes its own
sign-in look like an unrelated application's permission request.

The pinned identity pages have embedded CSS allowed by a CSP hash. Voptrack has
not copied those pages, proxied credentials, injected styles, changed CSP, or
modified Tessera to work around the lack of a public branding interface.

## Built-in website sign-in without the application consent picker

For the deployment's own web Console, show one compact, branded sign-in card:

- The transparent Voptrack mark and a short “Sign in” heading.
- Username and password fields, with password-manager/autofill support.
- An unchecked “Keep me signed in” checkbox (up to 30 days).
- One “Sign in” submit button.

The person enters credentials, optionally checks the box, submits once, and enters
the website. Remove the separate Console welcome/remember-me page and its
“Continue to sign in” handoff. Avoid the introductory paragraphs, repeated
explanations and OAuth implementation details on the ordinary website sign-in
screen. Put only necessary inline validation, expiry or recovery messages beside
the relevant controls; keep the narrow layout compact and keyboard accessible.

Credentials must still go directly to Tessera's own sign-in handler. Provide a
supported contract for carrying the user's remember-me choice to the Console as
part of the validated sign-in transaction, bound to that flow and Console origin.
Do not require the Console to proxy passwords or scrape/modify Tessera forms.
The choice controls browser-session persistence only; it does not grant additional
rights or extend Tessera's token authority. Voptrack can retain its encrypted
remembered-session storage behind this combined screen.

Do not show “Allow access to your data”, a dataset selector, or the generic
application permission checkboxes during ordinary built-in website sign-in.
An owner should not have to approve “Manage accounts for you” to use their own
administration UI. A member should not have to choose a personal dataset merely
to see Home's shared dashboard. Dataset selection can remain a normal website
navigation action when the person has multiple readable stores.

Please provide a supported, server-configured first-party Console sign-in mode.
Reusing OAuth/PKCE internally is acceptable; the public-client authorization
picker need not be part of the website experience. The trusted designation must
come from the operator/server, be bound to the exact registered Console origin
and redirect, and never be something a dynamically registered client can claim
for itself. Other OAuth clients keep the existing explicit consent flow.

This changes how website access is established, not what the account may access.
Keep the sealed-custodian boundary: Home owners do not gain members' private
records; members do not gain other people's unshared data; Research access stays
within the researcher's authority. Recheck current role/account status and honor
suspension, revocation and expiry. Sensitive operations retain their explicit
product confirmations and any required fresh authentication. Do not simulate
this feature by hiding controls, automatically submitting the current consent
form or using broader server credentials in Voptrack.

## Operator branding

Provide an optional, operator-controlled identity-page brand configuration:

- A local logo asset and accessible product/site name, with a bounded asset size
  and safe supported formats. No arbitrary remote asset loading is needed.
- A small palette (accent, surface and text colors), with accessible defaults.
- Consistent branding on sign-in, consent, cancellation, expiry and return pages.
- Clear website-appropriate labels when the registered OAuth client is a web
  Console, while preserving a clear destination for native app authorization.

An operator-supplied brand definition, rendered by Tessera's own templates, is
sufficient. Arbitrary template HTML, CSS or JavaScript overrides are unnecessary.
The client-supplied name must remain visibly distinguished from trusted operator
identity. Example wording for the built-in website: “Sign in to Voptrack Home”.
External application authorization should still clearly explain its requested access.

## Boundaries and acceptance

Credential verification, OAuth state, PKCE, CSRF, registered redirect validation,
code single-use, expiry and permission enforcement remain Tessera-owned. Branding
must not grant rights beyond the operator-authorized first-party policy or the
account’s current authority. Preserve consent choices for other clients and
fresh authorization where an action requires it.
Unbranded deployments keep working with safe defaults. No per-client arbitrary
asset URLs or trusted HTML are introduced. Maintain CSP's current protection
level using Tessera-generated asset policies/hashes as appropriate.

Please provide a documented configuration/API contract and browser coverage for
sign-in/consent/error states at desktop and narrow widths, keyboard navigation,
password-manager/autofill semantics and readable contrast. Verify that existing
native app callbacks and ordinary OAuth web clients keep their existing consent
and grant semantics. Test that an untrusted client cannot opt into the first-party
mode, including by copying its display name, origin parameters or requested rights.

## Voptrack-side work already separated

The web Console's opt-in remembered session belongs to Voptrack: an opaque cookie
and protected durable refresh credentials, capped at 30 days and still subject
to Tessera expiry, revocation and current account authority. It does not require
Tessera to persist a browser password or extend any OAuth grant. The storage implementation remains Voptrack-owned; the combined login needs
the supported remember-choice handoff described above.
