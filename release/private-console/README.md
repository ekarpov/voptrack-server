# Private Console gate

`make private-console-check` starts a disposable synthetic Home. The public
listener must expose discovery/OAuth while refusing Console pages. The private
loopback listener must render Console, send sign-in to the public Tessera
authorization origin, require its own callback, and land on Home with Manage
Home navigation. The browser driver handles ordinary OAuth consent and configured
first-party sign-in, submitting the introductory form with persistence off.

This does not qualify public DNS/TLS, actual LAN reachability, real devices,
remembered-session persistence or an existing deployment. The first-release fixture
alignment on 8 November 2026 preserves these authority and listener assertions;
the former Members landing-page expectation is replaced by the accepted Home UI.

The callback comparison requires the exact scheme, host, port and path; a path
merely starting with `/auth/callback` does not pass. Query data is the OAuth
response, not part of the callback address. This does not establish phone
dispatch or browser trust outside the synthetic gate.
