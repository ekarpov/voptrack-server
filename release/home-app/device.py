#!/usr/bin/env python3
"""Join on a device, as the Voptrack app does (Tessera alpha.32): no browser,
no password. Used by the gates.

  device.py redeem BASE CA LINK [ACCOUNT]   -> JSON {access_token, refresh_token, account, dataset_id, preview}
  device.py status BASE CA TOKEN PATH        -> the HTTP status of GET PATH with that token

LINK is an invitation link (…/oauth/invitation?code=…[&pin=…]); its code is
previewed, then redeemed at /oauth/token with the invitation grant for a public
client registered here. A code that cannot be used prints {"error": …}.
"""
import json, ssl, sys, urllib.error, urllib.parse, urllib.request

GRANT = "urn:tessera:params:oauth:grant-type:invitation"


def call(ctx, method, url, form=None, js=None, token=None):
    data, headers = None, {"Accept": "application/json"}
    if form is not None:
        data, headers["Content-Type"] = urllib.parse.urlencode(form).encode(), "application/x-www-form-urlencoded"
    if js is not None:
        data, headers["Content-Type"] = json.dumps(js).encode(), "application/json"
    if token:
        headers["Authorization"] = "Bearer " + token
    try:
        resp = urllib.request.urlopen(urllib.request.Request(url, data=data, method=method, headers=headers), context=ctx)
        return resp.status, resp.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace")


def redeem(base, ctx, link, account=None):
    code = dict(urllib.parse.parse_qsl(urllib.parse.urlparse(link).query)).get("code", "")
    # An invitation previews; a device code (another device, or the owner's
    # from the host) may not, and is redeemed all the same.
    st, body = call(ctx, "GET", base + "/oauth/invitation/preview?" + urllib.parse.urlencode({"code": code}))
    preview = json.loads(body) if st == 200 else {"status": st}
    st, body = call(ctx, "POST", base + "/oauth/register", js={
        "client_name": "Voptrack (gate)", "redirect_uris": ["com.voptrack.gate:/oauth/callback"],
        # An ordinary public client: Tessera's registration names only
        # authorization_code and refresh_token; any registered public client may
        # then use the invitation grant.
        "token_endpoint_auth_method": "none", "grant_types": ["authorization_code", "refresh_token"],
        "response_types": ["code"]})
    if st not in (200, 201):
        return {"error": f"register {st}"}
    form = {"grant_type": GRANT, "code": code, "client_id": json.loads(body)["client_id"]}
    if account:
        form["account"] = account
    st, body = call(ctx, "POST", base + "/oauth/token", form=form)
    if st != 200:
        return {"error": f"token {st}", "status": st}
    out = json.loads(body)
    out["preview"] = preview
    return out


if __name__ == "__main__":
    cmd, base, ca = sys.argv[1], sys.argv[2].rstrip("/"), sys.argv[3]
    ctx = ssl.create_default_context(cafile=ca)
    if cmd == "redeem":
        print(json.dumps(redeem(base, ctx, sys.argv[4], sys.argv[5] if len(sys.argv) > 5 else None)))
    elif cmd == "status":
        print(call(ctx, "GET", base + sys.argv[5], token=sys.argv[4])[0])
