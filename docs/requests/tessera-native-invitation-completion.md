# Native invitation account completion: iOS retest acceptance

Status, 2026-11-06: the app team reports successful native account completion,
study setup, and synthetic record write/sync/read-back on iOS. Interrupted-join
recovery and resumed authorization scope remain pending. No server-side defect
was found in this round, and no server fix or redeployment is indicated.

## App-team retest, 2026-11-06

Consumer-reported acceptance used Voptrack 1.0 (1), iOS 26.5 Simulator (iPhone),
Release review builds `caba3315` (10:18-10:29 EET, UTC+2) and `ac0c88f9`
(11:01-11:03 EET; includes the app-side fix), against the existing rc.4 study
demo. These results were supplied by the app team, not independently rerun here.

- Account creation → Continue to Voptrack → native callback: PASS at 10:24:39,
  one tap; callback delivered and study setup resumed.
- Callback → completed study setup: PASS; study dataset connected and Sleep
  and Weight Tracks installed.
- Write permission → synthetic record → sync/read-back: PASS at 11:02:49;
  the study Weight record was read back after an app restart at 11:03.
  The earlier record-sheet failure was app-side: an unreachable, unrelated
  Home on the test profile blocked the sheet. The app team fixed it.
- Fresh `scope=read write`: PASS; See and Add/change/delete were preselected,
  with a matching summary.
- Interrupted join → Sign in again, including resumed `scope=read write`:
  pending, using the already-issued separate invitation. No new invitation
  is needed for this pending test.
- Branding and Return to app: verified in light and dark.

This supersedes the unresolved fresh-join finding below for the reported builds
and environment. It does not qualify interrupted recovery, resumed scope,
physical devices or other builds. Record values and participant/invitation
material are omitted.

## Historical investigation

The following preserves the earlier failure and HTTP investigation as dated
evidence. The account-created recovery page is distinct from the OAuth consent
completion page (Return to app).

## Consumer report

The app team tested Voptrack 1.0 (1), Release configuration from `786280d4`,
on iOS 26.5 Simulator using ASWebAuthenticationSession. The study demo runs
Tessera v0.1.0-rc.4. At 2026-11-05 18:57 UTC they followed:

Invitation → Join Study → Agree and Join → set password → Create account →
decline Save Password → recovery-code page → Continue to the native app.

The participant account was created and its invitation consumed. Two taps about
12 seconds apart produced tap-recognition messages but no navigation-policy
event, redirect, callback, error or page change. The account/recovery page stayed
visible. A separate rc.3 attempt reportedly returned on a second tap after
declining Save Password; that is not rc.4 acceptance. Account identifiers,
passwords, recovery codes and invitation material are deliberately omitted here.

## Evidence checked here

The published dependency is Tessera v0.1.0-rc.4 at
`5691f26bf50f52b625b18fb2b9e2fb9959b59d3b`.

A dedicated synthetic live HTTP probe, separate from the reported participant
and reserved review invitations, registered the exact native return URI, joined
the synthetic demo study, and submitted the new account password once. It found:

- Account creation returned 201 with the recovery page, without an HTTP redirect.
- Exactly one anchor pointed to the registered `com.voprak.voptrack:/joined`.
- The private-use URL was not replaced with Go's dead-link template marker.
- Strict CSP retained `default-src 'none'` and `form-action 'self'`.
- An unregistered return target was not offered (checked by GET only).
- Existing participants and reserved invitations were not modified or consumed.

The probe created one separate synthetic diagnostic account. Its credentials and
raw recovery page remain private; the sanitized receipt contains booleans and
HTTP status only. It created no study records. A second, separate retest
invitation was issued and verified by landing-page GET only; it remains private
and was not joined by this check.

Source inspection of the local app checkout at `30440aea0` confirms that it asks
for the `/joined` return and selects the same custom callback scheme for
ASWebAuthenticationSession. Reported commit `786280d4` is not present in that
checkout, so the exact tested implementation remains consumer-reported. This
inspection is not proof of runtime session lifetime, system dispatch, or a
particular released build. The screenshot/report describes the pre-branding
attempt; branding checks on the later OAuth page do not close this finding.

## Minimum upstream/consumer investigation

Reproduce this **account-created recovery page** in an actual iOS
ASWebAuthenticationSession, including the Save Password prompt and its dismissal.
Use a fresh disposable participant per accepted join. Check the registered return
URI, the rendered anchor destination, hit testing/user activation after password
manager dismissal, and callback delivery to the active authentication session.
Retain sanitized observations; do not collect passwords, recovery codes, tokens,
invitation codes or full callback query strings in shared diagnostics.

The required behavior is an explicit user action that returns to the validated
registered native destination after the person has had the opportunity to retain
their recovery code. Preserve strict CSP, exact registered-return validation,
single-use invitations and recovery-code handling. Do not automatically skip the
recovery screen, inject a script, broadly allow native schemes in CSP, proxy-rewrite
identity HTML, or bypass TLS as an unverified workaround.

An iOS reproduction must distinguish:

1. A wrong/dead destination emitted by the server.
2. A correct anchor whose tap never initiates navigation.
3. Navigation initiated but not received by the expected authentication session.
4. Callback received but study setup not resumed.

The historical HTTP evidence rules out the first case for the synthetic probe, not for
every failed device session, and does not distinguish the remaining cases. A
general identity-page behavior fix belongs in Tessera; a session/resume defect
belongs in the app. No upstream or app repository was modified from here.

## Original local alternative and retest plan

At the time of the original investigation, the app team reported interrupted-join
recovery on `016c7ba0`, not yet live-tested:
close the sheet, use Sign in again as the same participant with `scope=read write`,
then resume the pending study installation. Do not POST the study join again or
connect it as ordinary storage. Reopening an issued account page must not be
described as making a consumed invitation reusable.

Once a candidate is ready, use the separate unused private invitation and report
each result independently with the exact app build and server version:

1. Fresh invitation → account creation → Continue to the native app.
2. Callback received → original study setup completed.
3. Dataset write access confirmed → synthetic record created and synced.
4. `scope=read write` retained through both fresh and resumed authorization.
5. Close the sheet after account creation → Sign in again resumes that same
   participant without a second invitation redemption.

A successful sign-in, HTTP anchor check, desktop WebKit check or branded OAuth
Return to app check cannot substitute for these iOS results. At the time of this
original plan, the full fresh-study-join path remained unqualified; the dated
app-team retest above records subsequent acceptance and the remaining checks.
