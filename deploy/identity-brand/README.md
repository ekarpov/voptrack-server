# Operator identity branding

Fresh Console-enabled deployments create the packaged product branding by default.
Existing unbranded deployments retain that choice on init reruns;
`VOPTRACK_IDENTITY_BRAND=off` prevents automatic initial input creation.
Custom branding is configured by writing `identity-brand.json` in the deployment's **config volume**,
owned by UID 10001, mode 0600. Start from `home.json` or `study.json` here. Stop
both Console and Tessera, write the input, run init, then start services; use the
normal supported maintenance workflow. Init regenerates server configuration.
Without this input on an existing installation, branding stays off. Invalid/private-file checks fail
closed; Tessera validates the final palette and immutable PNG at startup.

This Voptrack interface exposes only name and the documented light/dark
`accent`, `surface`, and `text` fields. It deliberately exposes no web-client
allowlist or first-party exception. Native OAuth continues using Return to app.
Names must be nonblank, control-free, and at most 200 UTF-8 bytes; input is at
most 16 KiB and must be a private regular file. The image supplies the logo at
`/usr/local/share/voptrack/identity-logo.png`, rather than accepting arbitrary
operator paths. Recovery kits include the operator input and bind it to the
rendered configuration; use the matching image when restoring.

Tessera rc.4 has a single logo and three palette fields per theme. The packaged
PNG preserves the alpha of the app's transparent navy mark, area-downsampled to
512 px without background compositing. `provenance.json` records its source and
resulting hashes; `resize-mark.go` reproduces the conversion. The same source
produces transparent browser PNG fallbacks and the touch icon. The old white
backing and navy-backed app-icon exports are retired.

Because Tessera cannot select a different logo for dark mode, the supplied
identity-page palettes both use the light backdrop. This keeps the navy mark
readable without adding a box or altering the artwork. Console pages still use
the app's transparent light/dark SVG variants. Custom operator palettes must keep
their selected logo readable; there is no theme-logo or custom CSS hook in rc.4.
The light accent-text `#0C6A61` is used for Tessera's single accent field to meet
4.5:1 contrast against `#F2F2F7`; `#11857A` does not meet that threshold.

## Coverage and limits

Go tests cover unknown/privileged input refusal, private-file/symlink/size
checks, generated JSON, legacy unbranded operation, branded kit export/restore,
and pending configuration not reporting ready. `release/identity-brand/verify.py`
checks the actual image in disposable Home/Research deployments: both palettes,
validated PNG response, strict native CSP, default-off and startup refusal for
invalid contrast. The preflight also refuses the actual image whose public logo
directory lacks traversal permission. The compiled missing-injection control must fail the brand
assertion before restoring the positive image. This does not qualify iOS app
dispatch, device screenshots, accessibility beyond upstream contrast checks,
or arbitrary external assets.

## Identity-page favicon

`make-favicon.py` wraps the transparent 16/32px PNGs in an ICO container. Init
publishes that immutable asset in the public volume. Both Caddy entry points
serve `/favicon.ico` directly, including when the Console is private or off.
Browsers can discover it on Tessera sign-in pages without rewriting identity
HTML, altering CSP or changing protocol routes. Console HTML also declares it
as a fallback beside its existing SVG/PNG favicon links.


The custom-brand gate explicitly selects `VOPTRACK_IDENTITY_BRAND=off` for its
initial unbranded control, then qualifies explicit palettes and invalid inputs.
Fresh default branding for all three web products is separately qualified by
`release/console/website_defaults.py`. Branding never selects website authority.
