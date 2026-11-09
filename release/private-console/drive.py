#!/usr/bin/env python3
"""The Console kept on the home network, seen from both sides.

Usage: drive.py PUBLIC CONSOLE CA OWNER_PASSWORD_FILE
"""
import http.cookiejar, re, ssl, sys, urllib.error, urllib.parse, urllib.request
from html.parser import HTMLParser

public, console, ca = sys.argv[1].rstrip("/"), sys.argv[2].rstrip("/"), sys.argv[3]
owner_pw = open(sys.argv[4]).read().strip()
ctx = ssl.create_default_context(cafile=ca)


def fail(message):
    print(f"\nFAIL: {message}", file=sys.stderr)
    sys.exit(1)


def say(what, outcome):
    print(f"  {what:<46} {outcome}")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None


def browser():
    return urllib.request.build_opener(urllib.request.HTTPSHandler(context=ctx), NoRedirect,
                                       urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))


def request(opener, method, url, form=None):
    data = urllib.parse.urlencode(form).encode() if form is not None else None
    h = {"Content-Type": "application/x-www-form-urlencoded"} if form is not None else {}
    try:
        resp = opener.open(urllib.request.Request(url, data=data, method=method, headers=h))
        return resp.status, resp.read().decode(), resp.headers
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace"), e.headers


class WebsiteForm(HTMLParser):
    def __init__(self, text):
        super().__init__(); self.fields = {}; self.refresh = None; self.feed(text)
    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == "input" and attrs.get("type") == "hidden" and attrs.get("name"):
            self.fields[attrs["name"]] = attrs.get("value", "")
        if tag == "button" and attrs.get("name") and attrs["name"] not in self.fields:
            self.fields[attrs["name"]] = attrs.get("value", "")
        if tag == "meta" and attrs.get("http-equiv", "").lower() == "refresh":
            self.refresh = attrs.get("content", "")


def follow(opener, url, form=None, method="GET"):
    for _ in range(10):
        st, body, headers = request(opener, method, url, form=form)
        if st in (301, 302, 303) and headers.get("Location"):
            url, form, method = urllib.parse.urljoin(url, headers["Location"]), None, "GET"
            continue
        if st == 200 and urllib.parse.urlparse(url).path == "/login" and 'class="web-login"' in body:
            form = dict(WebsiteForm(body).fields, remember="off")
            method = "POST"
            continue
        return st, body, url
    fail("too many redirects")


field = lambda p, n: re.search(r'name="%s"\s+value="([^"]*)"' % n, p).group(1)

b = browser()
for path in ("/", "/members", "/login"):
    st, _, _ = request(b, "GET", public + path)
    if st != 404:
        fail(f"the public address serves the Console's {path} ({st}), want 404")
st, _, _ = request(b, "GET", public + "/.well-known/oauth-authorization-server")
if st != 200:
    fail(f"the public address no longer serves Tessera's discovery ({st})")
say("the public address", "Tessera only; the Console answers 404")

st, page, _ = request(b, "GET", console + "/")
if st != 200 or "/login" not in page:
    fail(f"the Console's own port does not serve it ({st})")
st, page, url = follow(b, console + "/login")
if not url.startswith(public + "/oauth/authorize"):
    fail(f"signing in did not go to Tessera's public page ({url})")
if 'name="remember"' in page:
    st, result, headers = request(b, "POST", public+"/oauth/authorize", form=dict(WebsiteForm(page).fields, username="owner", password=owner_pw))
    back = headers.get("Location", "") if st == 303 else ""
    if st == 200:
        refresh = WebsiteForm(result).refresh
        if not refresh or not re.search(r"url=", refresh, re.I):fail("website sign-in did not complete")
        back = re.split(r"url=", refresh, flags=re.I)[1].strip().strip(chr(34)+chr(39))
else:
    st, approval, _ = request(b, "POST", public + "/oauth/authorize", form={
        "flow": field(page, "flow"), "csrf": field(page, "csrf"), "username": "owner", "password": owner_pw, "decision": "continue"})
    st, _, headers = request(b, "POST", public + "/oauth/authorize", form={
        "flow": field(page, "flow"), "csrf": field(approval, "csrf"), "decision": "allow", "administer": "yes"})
    back = headers.get("Location", "")
if urllib.parse.urlparse(back)._replace(query="", fragment="") != urllib.parse.urlparse(console + "/auth/callback"):
    fail(f"Tessera sent the owner back to {back.split('?')[0]}, not the Console's own address")
st, page, url = follow(b, back)
if st != 200 or urllib.parse.urlparse(url).path not in ("/", "/home") or 'href="/manage"' not in page:
    fail(f"the owner's sign-in did not land on Home ({st} {url})")
say("the owner signs in, across the two", "Tessera's page, back to the Console's port")
