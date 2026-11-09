#!/usr/bin/env python3
"""After the Console lost its volume: the studies come back from the server as
it starts; the researcher signs in once, and consent records and joining are
there too.

Usage: reopen.py BASE CA OWNER_PASSWORD_FILE before|after
"""
import html, http.cookiejar, re, ssl, sys, urllib.error, urllib.parse, urllib.request

import sys as _ci_sys
from pathlib import Path as _CiPath
_ci_sys.path.insert(0, str(_CiPath(__file__).resolve().parents[2] / 'ci'))
from timing import retry_auth, timed_request, RetryBudget, phase, event

base, ca, owner_pw, phase = sys.argv[1].rstrip("/"), sys.argv[2], open(sys.argv[3]).read().strip(), sys.argv[4]
ctx = ssl.create_default_context(cafile=ca)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None


def browser():
    return urllib.request.build_opener(urllib.request.HTTPSHandler(context=ctx), NoRedirect,
                                       urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))


@retry_auth
def request(opener, method, url, form=None):
    data = urllib.parse.urlencode(form).encode() if form is not None else None
    h = {"Content-Type": "application/x-www-form-urlencoded"} if form is not None else {}
    r = urllib.request.Request(url if url.startswith("http") else base + url, data=data, method=method, headers=h)
    try:
        resp = opener.open(r, timeout=30)
        return resp.status, resp.read().decode(), resp.headers
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace"), e.headers


def follow(opener, url, form=None, method="GET"):
    for _ in range(10):
        st, body, headers = request(opener, method, url, form=form)
        if st in (301, 302, 303) and headers.get("Location"):
            url, form, method = urllib.parse.urljoin(base + "/", headers["Location"]), None, "GET"
            continue
        if st == 200 and urllib.parse.urlparse(url).path == "/login" and 'class="web-login"' in body:
            token = re.search(r'name="csrf" value="([^"]+)"', body)
            if not token:
                fail("Console login introduction has no CSRF token")
            form, method = {"csrf": token.group(1)}, "POST"
            continue
        return st, body, url


field = lambda p, n: re.search(r'name="%s"\s+value="([^"]*)"' % n, p).group(1)


def fail(message):
    print(f"\nFAIL: {message}", file=sys.stderr)
    sys.exit(1)


st, page, _ = request(browser(), "GET", "/study/sleep-mood")
if phase == "before":
    # Read from the server as the Console started: nobody has signed in.
    if st != 200 or not re.search(r"data-consent-version>[2-9]<", page):
        fail(f"with its volume gone, the Console did not bring the study back from the server ({st})")
    sys.exit(0)

researcher = browser()
st, page, _ = follow(researcher, "/login")
st, approval, _ = request(researcher, "POST", "/oauth/authorize", form={
    "flow": field(page, "flow"), "csrf": field(page, "csrf"), "username": "owner", "password": owner_pw, "decision": "continue"})
st, _, headers = request(researcher, "POST", "/oauth/authorize", form={
    "flow": field(page, "flow"), "csrf": field(approval, "csrf"), "decision": "allow", "administer": "yes"})
follow(researcher, headers["Location"])

st, page, _ = request(browser(), "GET", "/study/sleep-mood")
if st != 200 or not re.search(r"data-consent-version>[2-9]<", page):
    fail(f"after the researcher signed in, the study page answered {st} without its consent version 2")
st, page, _ = request(researcher, "GET", "/studies/sleep-mood/participants")
consents = re.findall(r"<td data-account>([^<]+)", page)
if len(consents) < 2:
    fail(f"the consent records did not come back: {consents}")
# Joining needs the study's issuer secret, which was lost with the volume and
# is replaced at the researcher's sign-in.
person = browser()
st, page, _ = follow(person, "/study/sleep-mood/join")
form = {"csrf": field(page, "csrf"), "version": field(page, "version"), "code": field(page, "code"), "agree": "yes"}
st, redeem, url = follow(person, "/study/sleep-mood/join", form=form, method="POST")
if "/oauth/invitation" not in url:
    fail(f"joining after the restore did not reach the account page ({st} {url})")
print(f"  {'the Console, its volume lost':<46} back from the server: {len(consents)} consents, joining works")

st, policy, _ = request(researcher, "GET", "/studies/sleep-mood/policy")
if st != 200 or field(policy, "revision") != "4" or 'name="threshold" inputmode="numeric" value="2"' not in policy:
    fail("suppression: policy did not return from the Console state store")
print("  suppression policy restored                    K=2, revision=4")

st,overview,_=request(researcher,"GET","/studies/sleep-mood/dashboard?period=all")
if st!=200 or 'data-monitoring' not in overview or 'data-value>' in overview:
    fail("collection overview did not survive state restoration")
print("  collection overview restored                  metadata and participant administration available")

# The unfinished setup is in the same backed-up Console document as its study.
st, draft, _ = request(researcher, "GET", "/studies/unfinished-study/setup/review")
if st != 200 or "Unpublished synthetic consent" not in draft or "Guided purpose" not in draft or 'data-published-version>0<' not in draft:
    fail("setup: unfinished progress or unpublished consent lost on restore")
if len(sys.argv) > 5 and sys.argv[5] == "archive":
    for action in ("publish", "open"):
        st, draft, _ = request(researcher, "GET", "/studies/unfinished-study/setup/review")
        st, body, headers = request(researcher, "POST", "/studies/unfinished-study/setup/review", {"csrf": field(draft, "csrf"), "revision": field(draft, "revision"), "action": action})
        if st != 303:
            fail(f"setup: restored draft cannot {action} ({st})")
    st, completed, _ = request(researcher, "GET", "/studies/unfinished-study/setup/share")
    if st != 200 or "data-setup-complete" not in completed:
        fail("setup: restored draft did not open")
print("  unfinished study setup restored               private consent and progress resumed")
