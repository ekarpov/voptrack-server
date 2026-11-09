#!/usr/bin/env python3
"""Consent records are immutable: prove it against the server itself.

Usage: immutable.py BASE CA SECRET_FILE DATASET_FILE

Signs in as the Console's own service -- the strongest holder there is -- reads
the study's consent records, and tries to change who agreed to one. Tessera
must refuse with 409 immutable_field, and the record must be unchanged.
"""
import base64, json, ssl, sys, urllib.error, urllib.parse, urllib.request

base, ca = sys.argv[1].rstrip("/"), sys.argv[2]
secret, dataset = open(sys.argv[3]).read().strip(), open(sys.argv[4]).read().strip()
ctx = ssl.create_default_context(cafile=ca)


def fail(message):
    print(f"\nFAIL: {message}", file=sys.stderr)
    sys.exit(1)


def call(method, path, body=None, token=None, form=None, basic=None):
    h, data = {}, None
    if form is not None:
        data, h["Content-Type"] = urllib.parse.urlencode(form).encode(), "application/x-www-form-urlencoded"
    elif body is not None:
        data, h["Content-Type"] = json.dumps(body).encode(), "application/json"
    if token:
        h["Authorization"] = "Bearer " + token
    if basic:
        h["Authorization"] = "Basic " + base64.b64encode(basic.encode()).decode()
    try:
        r = urllib.request.urlopen(urllib.request.Request(base + path, data=data, method=method, headers=h), context=ctx)
        return r.status, json.loads(r.read() or b"{}")
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw)
        except ValueError:
            return e.code, {}


st, tok = call("POST", "/oauth/token", form={"grant_type": "client_credentials", "dataset": dataset},
               basic="console-state:" + secret)
if st != 200:
    fail(f"the Console's service could not sign in ({st})")
token = tok["access_token"]
objects = f"/v1/datasets/{dataset}/objects/voptrack.console.consent"
st, rs = call("POST", objects + ":query", {"query": {"fields": ["study", "account", "version"],
              "filter": {"field": {"id": "study", "exact": {"type": "text", "value": "sleep-mood"}}}}}, token=token)
records = rs.get("records", []) if st == 200 else []
if len(records) < 2:
    fail(f"the study's consents are not records of their own ({st}, {len(records)} found)")
target = records[0]["id"]["id"]
order = [f["id"] for f in rs["meta"]["fields"]]
before = dict(zip(order, [v["value"] for v in records[0]["values"]]))
st, answer = call("PATCH", f"{objects}/{target}", {"fields": {"account": {"type": "text", "value": "p-somebody-else"}}},
                  token=token)
details = answer.get("error", {}).get("details", {}) if isinstance(answer, dict) else {}
if st != 409 or details.get("reason") != "immutable_field" or "account" not in details.get("fields", []):
    fail(f"changing who agreed answered {st} {answer}; want 409 immutable_field naming account")
st, one = call("GET", f"{objects}/{target}?fields=" + ",".join(order), token=token)
after = dict(zip([f["id"] for f in one["meta"]["fields"]], [v["value"] for v in one["records"][0]["values"]]))
if after != before:
    fail(f"the consent record changed: {before} became {after}")
print(f"  {'consent records, even to the Console itself':<46} immutable: 409, and unchanged")

# Withdrawals too: the walk-through withdrew one participant in the Console.
withdrawals = f"/v1/datasets/{dataset}/objects/voptrack.console.withdrawal"
st, ws = call("POST", withdrawals + ":query", {"query": {"fields": ["study", "account", "at"],
              "filter": {"field": {"id": "study", "exact": {"type": "text", "value": "sleep-mood"}}}}}, token=token)
wrecs = ws.get("records", []) if st == 200 else []
if not wrecs:
    fail(f"the study's withdrawal is not a record of its own ({st})")
wid = wrecs[0]["id"]["id"]
st, answer = call("PATCH", f"{withdrawals}/{wid}", {"fields": {"at": {"type": "date_time", "value": "2020-01-01T00:00:00.000Z"}}},
                  token=token)
details = answer.get("error", {}).get("details", {}) if isinstance(answer, dict) else {}
if st != 409 or details.get("reason") != "immutable_field" or "at" not in details.get("fields", []):
    fail(f"back-dating a withdrawal answered {st} {answer}; want 409 immutable_field naming at")
print(f"  {'withdrawal records, likewise':<46} immutable: back-dating refused")
