#!/usr/bin/env python3
"""After a rebuild: is the household the one that was lost?

Usage: check.py BASE CA OWNER_PASSWORD_FILE HOME_JSON
"""
import http.cookiejar, json, re, ssl, sys, urllib.error, urllib.parse, urllib.request
from html.parser import HTMLParser
from pathlib import Path

base, ca, owner_pw, home = sys.argv[1].rstrip("/"), sys.argv[2], open(sys.argv[3]).read().strip(), json.load(open(sys.argv[4]))
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


def request(b, method, url, form=None):
    data = urllib.parse.urlencode(form).encode() if form is not None else None
    try:
        r = b.open(urllib.request.Request(url if url.startswith("http") else base + url, data=data, method=method))
        return r.status, r.read().decode(), r.headers
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace"), e.headers


def follow(b, url):
    for _ in range(10):
        st, body, h = request(b, "GET", url)
        if st in (302, 303):
            url = urllib.parse.urljoin(base + "/", h["Location"])
            continue
        return st, body, url


field = lambda p, n: re.search(r'name="%s"\s+value="([^"]*)"' % n, p).group(1)


def own_store(approval, known=None):
    """The person's own store on the approval page. With household sharing the
    chooser also lists stores others share with them -- by bare id, in no fixed
    order -- so a script that knows its store picks it by id. Before that, the
    person's own store is the only one there."""
    options = re.findall(r'<option\s+value="([^"]+)"', approval)
    if known and known in options:
        return known
    return options[0] if options else None


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


def sign_in(person, password, grants, known=None):
    b = browser()
    st, page, url = follow(b, "/login")
    if 'name="remember"' in page:
        st, result, headers = request(b, "POST", "/oauth/authorize", form=dict(WebsiteForm(page).fields, username=person, password=password))
        callback = headers.get("Location") if st == 303 else None
        if st == 200:
            refresh = WebsiteForm(result).refresh
            if refresh and re.search(r"url=", refresh, re.I):
                callback = re.split(r"url=", refresh, flags=re.I)[1].strip().strip(chr(34)+chr(39))
        if not callback:
            return None, st
        parsed = urllib.parse.urlparse(urllib.parse.urljoin(base+"/", callback))
        if parsed.netloc != urllib.parse.urlparse(base).netloc or parsed.path != "/auth/callback":
            fail("restored website callback origin refused")
        if follow(b, callback)[0] != 200:
            return None, st
        if known:
            # Independently confirm the same account still owns the original
            # dataset; a successful Home page alone cannot establish this.
            source=Path(__file__).resolve().parents[1]/'console'/'drive.py'
            g={'__file__':str(source),'__name__':'recovery_browser_helpers'}
            exec(compile(source.read_text().split('# ---- the owner invites the household from the Console')[0],str(source),'exec'),g)
            g['stores']={person:known}
            token=g['app_token'](person,password,{'read':'yes','store':True})
            status,raw,_=g['api'](token,'GET','/v1/datasets')
            if status!=200 or known not in [d['id'] for d in json.loads(raw)['datasets']]:
                fail("restored member dataset identity changed")
        return b, known
    st, approval, _ = request(b, "POST", "/oauth/authorize", form={
        "flow": field(page, "flow"), "csrf": field(page, "csrf"), "username": person, "password": password, "decision": "continue"})
    if st != 200:
        return None, st
    form = {"flow": field(page, "flow"), "csrf": field(approval, "csrf"), "decision": "allow"}
    option = own_store(approval, known)
    if grants.pop("store", False) and option:
        form["dataset"] = option
    form.update(grants)
    st, _, h = request(b, "POST", "/oauth/authorize", form=form)
    follow(b, h["Location"])
    return b, option


owner, _ = sign_in("owner", owner_pw, {"administer": "yes"})
if not owner:
    fail("the owner's original password does not sign in")
st, page, _ = request(owner, "GET", "/members")
listed = dict(re.findall(r'<td data-member>([^<]+)</td><td data-state>([^<]+)</td>', page))
if listed != {"sara": "active", "ben": "active", "lina": "suspended"}:
    fail(f"Members after the rebuild lists {listed}; want Sara and Ben active, Lina suspended")
say("the owner, with the original password", "Sara, Ben; Lina still suspended")
q = urllib.parse.urlencode({"scope": "home", "type": "org.voptrack.gate.spending", "field": "amount", "measure": "sum", "group": "store"})
st, page, _ = request(owner, "GET", "/panel?" + q)
per = dict(re.findall(r'<tr><td>([^<]*)</td><td class="num" data-value>([^<]*)</td>', page))
want = home["expected_household"]["home spending per member"]
if per != want:
    fail(f"Home per member after the rebuild is {per}; want {want}, the household before")
say("Home's totals", f"{per} -- as before, Lina's still absent")
for name in ("sara", "ben"):
    m = home["members"][name]
    b, dataset = sign_in(name, m["password"], {"store": True, "read": "yes"}, m["dataset"])
    if not b or dataset != m["dataset"]:
        fail(f"{name} does not sign in to the same store after the rebuild ({dataset} vs {m['dataset']})")
b, st = sign_in("lina", home["members"]["lina"]["password"], {"store": True, "read": "yes"})
if b:
    fail("Lina, suspended before the loss, signed in after the rebuild")
say("members sign in with their own passwords", "the same stores; Lina refused")
