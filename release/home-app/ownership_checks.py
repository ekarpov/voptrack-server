"""Public-interface device ownership gate; never infer ownership from a label.

The fixture knows every pre-existing store. A new member must reach exactly one
new store with write/schema and administer approval, while other stores remain
read-only filtered shares. Rebinding must retain that independently observed ID.
No private identity/storage files or widened credentials are used.
"""
import json
import uuid
import urllib.parse


class OwnershipMismatch(Exception):
    """Only fixed assertion text; no response bodies or identities."""


def check(call, redeem, owner, writer, peer, existing, spending, weight, fail, say):
    shared_store, private_store = existing
    shared = "/admin/api/v1/datasets/{}/readers/issuer:home-members"
    spend = spending["type"]["id"]
    private_type = weight["type"]["id"]

    def expect(status, wanted, assertion):
        if status not in wanted:
            fail(f"device ownership: {assertion} (HTTP {status})")

    def query(token, dataset, track):
        status, body, _ = call("POST", f"/v1/datasets/{dataset}/objects/{track}:query",
                               js={"query": {}}, token=token)
        return status, json.loads(body) if status == 200 else None

    def member(name):
        status, body, _ = call("POST", "/admin/api/v1/issuers/home-members/invitations",
                               js={}, token=owner)
        expect(status, (200, 201), "fixture invitation refused")
        invitation = json.loads(body).get("redeem_url")
        if not invitation:
            fail("device ownership: fixture invitation missing")
        joined = redeem(invitation, name)
        if "error" in joined or joined.get("account") != name or not joined.get("access_token"):
            fail("device ownership: invitation redemption refused")
        preview = joined.get("preview", {})
        if preview.get("administer") is not True or preview.get("access", {}).get("erase") is not False:
            fail("device ownership: preview must promise administration without erase approval")
        return joined, invitation

    known = set(existing)

    def ownership(joined, expected=None):
        token = joined["access_token"]
        status, body, _ = call("GET", "/v1/datasets", token=token)
        expect(status, (200,), "dataset listing refused")
        datasets = json.loads(body).get("datasets", [])
        fresh = [d for d in datasets if d.get("id") not in known]
        if expected is None:
            # Exclude independently known peers first, without consulting the
            # redemption's dataset_id or a mutable/display name.
            if len(fresh) != 1:
                if not fresh and joined.get("dataset_id") in known:
                    raise OwnershipMismatch("primary bound to a pre-existing peer store; new own store absent")
                raise OwnershipMismatch("joined installation does not reach exactly one new own store")
            expected = fresh[0]["id"]
        own = [d for d in datasets if d.get("id") == expected]
        if joined.get("dataset_id") != expected or len(own) != 1:
            raise OwnershipMismatch("primary dataset is not the independently identified own store")
        access = own[0].get("access", {})
        if any(access.get(k) is not True for k in ("read", "write", "schema")) or access.get("erase") is not False:
            raise OwnershipMismatch("own read/write/schema approval missing or erase unexpectedly approved")
        for dataset in datasets:
            if dataset["id"] == expected:
                continue
            grants = dataset.get("access", {})
            if dataset["id"] != shared_store or grants.get("read") is not True or any(
                    grants.get(k) for k in ("write", "schema", "erase", "insert")):
                raise OwnershipMismatch("unexpected store or excessive borrowed-store authority")
        status, body, _ = call("GET", "/admin/api/v1/overview", token=token)
        expect(status, (200,), "device lacks promised own-store administration")
        overview = json.loads(body)
        if overview.get("server_administrator") is not False or {
                d["id"] for d in overview.get("datasets", [])} != {expected}:
            raise OwnershipMismatch("administration is not confined to the own store")
        return expected

    # Positive control without pre-existing shares: ownership, schema/write,
    # private reads, owner/peer isolation and share/revoke all run on alpha.38.
    status, _, _ = call("DELETE", shared.format(shared_store), token=writer)
    expect(status, (204,), "fixture could not remove existing group share")
    joined, invitation = member("ownership-control")
    try:
        own = ownership(joined)
    except OwnershipMismatch as error:
        fail(f"device ownership control: {error}")
    token = joined["access_token"]
    for definition, field, kind, value in ((spending, "amount", "fixed", "1.23"),
                                            (weight, "kg", "float", 71.5)):
        track = definition["type"]["id"]
        status, _, _ = call("POST", f"/v1/datasets/{own}/meta", js=definition, token=token)
        expect(status, (201,), "device cannot define its own track")
        status, _, _ = call("POST", f"/v1/datasets/{own}:transact", js={"operations": [
            {"op": "insert", "type": track, "fields": {field: {"type": kind, "value": value}}}]},
            token=token, headers={"Idempotency-Key": str(uuid.uuid4())})
        expect(status, (200,), "device cannot write its own record")
        status, result = query(token, own, track)
        expect(status, (200,), "device cannot read its own record")
        if len(result.get("records", [])) != 1:
            fail("device ownership: own record readback differs")
        for other in (owner, writer, peer):
            expect(query(other, own, track)[0], (403, 404), "unshared own record accessible to another account")
    status, _, _ = call("PUT", shared.format(own), js={"types": [spend]}, token=token)
    expect(status, (204,), "device cannot share its own selected track")
    status, result = query(peer, own, spend)
    expect(status, (200,), "peer cannot read explicitly shared track")
    if len(result.get("records", [])) != 1:
        fail("device ownership: shared record readback differs")
    expect(query(peer, own, private_type)[0], (403,), "peer can read unshared track")
    for other in (owner, writer, peer):
        status, _, _ = call("PUT", shared.format(own), js={"types": [private_type]}, token=other)
        expect(status, (403, 404), "another account can change device member's sharing")
    status, _, _ = call("POST", f"/v1/datasets/{own}:transact", js={"operations": [
        {"op": "insert", "type": spend, "fields": {"amount": {"type": "fixed", "value": "9.99"}}}]},
        token=peer, headers={"Idempotency-Key": str(uuid.uuid4())})
    expect(status, (403,), "read-only peer can write the device member's record")
    status, result = query(token, own, spend)
    expect(status, (200,), "own record readback after refused foreign write failed")
    if len(result.get("records", [])) != 1:
        fail("device ownership: foreign write changed own records")
    status, _, _ = call("DELETE", shared.format(own), token=token)
    expect(status, (204,), "device cannot revoke its share")
    expect(query(peer, own, spend)[0], (404,), "peer retains reading after share removal")
    replay = redeem(invitation, "ownership-replay")
    if replay.get("status") not in (400, 404):
        fail("device ownership: invitation redeemed twice")
    known.add(own)
    say("device ownership control (no earlier shares)", "new own store; schema/write/read; peers/owner isolated; filtered share and revoke")

    status, _, _ = call("PUT", shared.format(shared_store), js={"types": [spend]}, token=writer)
    expect(status, (204,), "fixture could not restore a filtered group share")
    status, _, _ = call("POST", f"/v1/datasets/{shared_store}:transact", js={"operations": [
        {"op": "insert", "type": spend, "fields": {"amount": {"type": "fixed", "value": "2.34"}}}]},
        token=writer, headers={"Idempotency-Key": str(uuid.uuid4())})
    expect(status, (200,), "fixture cannot write peer's shared record")
    status, result = query(token, shared_store, spend)
    expect(status, (200,), "device cannot read a later group share")
    if len(result.get("records", [])) != 1:
        fail("device ownership: peer shared record readback differs")
    status, _, _ = call("POST", f"/v1/datasets/{shared_store}:transact", js={"operations": [
        {"op": "insert", "type": spend, "fields": {"amount": {"type": "fixed", "value": "9.99"}}}]},
        token=token, headers={"Idempotency-Key": str(uuid.uuid4())})
    expect(status, (403,), "device can write another member's shared track")
    status, result = query(writer, shared_store, spend)
    expect(status, (200,), "peer record readback after refused write failed")
    if len(result.get("records", [])) != 1:
        fail("device ownership: foreign write changed peer records")
    expect(query(token, shared_store, private_type)[0], (403,), "device can read peer's private track")
    expect(query(token, private_store, spend)[0], (404,), "device can discover an unshared peer store")
    for path, method, data in ((f"/v1/datasets/{shared_store}/meta", "POST", weight),
                                (shared.format(shared_store), "PUT", {"types": [private_type]}),
                                (shared.format(private_store), "PUT", {"types": [spend]})):
        status, _, _ = call(method, path, js=data, token=token)
        expect(status, (403, 404), "device can mutate another member's schema or sharing")
    say("device ownership isolation with a later share", "borrowed track readable; private track/store and foreign mutation refused")

    # The old gate accepted a borrowed store merely because it was listed.
    # Independent fresh-store inference now rejects that exact failure. Repeat
    # several fresh joins because alpha.38's dataset map selection is unordered.
    failures = []
    for index in range(4):
        probe, _ = member(f"ownership-probe-{index}")
        try:
            probe_own = ownership(probe)
        except OwnershipMismatch as error:
            failures.append(f"fresh join: {error}")
            say("device ownership fresh-join assertion FAILED", str(error))
            continue
        for other_store in known - {shared_store}:
            expect(query(probe["access_token"], other_store, spend)[0], (404,),
                   "new member can discover an unshared existing store")
        known.add(probe_own)
    if not failures:
        say("device joining with an existing group share", "four new distinct own stores; borrowed access never chosen as primary")

    status, body, _ = call("POST", "/admin/api/v1/issuers/home-members/invitations",
                           js={"rebind": "ownership-control"}, token=owner)
    expect(status, (200, 201), "fixture rebinding invitation refused")
    rebinding = json.loads(body)
    rebind_link = rebinding.get("redeem_url") or (
        "/oauth/invitation?" + urllib.parse.urlencode({"code": rebinding.get("invitation", "")}))
    rebound = redeem(rebind_link)
    if "error" in rebound or rebound.get("account") != "ownership-control":
        fail("device ownership: rebinding redemption refused")
    try:
        ownership(rebound, own)
    except OwnershipMismatch as error:
        failures.append(f"rebinding: {error}")
        say("device ownership rebinding assertion FAILED", str(error))
    expect(call("GET", "/v1/datasets", token=token)[0], (401,), "old device still authorized after rebinding")
    if query(rebound["access_token"], own, spend)[0] != 200:
        failures.append("rebinding: rebound device lost own records")
        say("device ownership rebinding assertion FAILED", "rebound device lost own records")
    if failures:
        fail("device ownership: " + "; ".join(failures))
    say("device ownership after rebinding with shares", "same independently known own store and authority; old device 401")
