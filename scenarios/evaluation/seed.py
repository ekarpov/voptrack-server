#!/usr/bin/env python3
"""Put something in an evaluation server worth looking at.

An empty server demonstrates nothing: every page reads zero and the point of
looking is lost. This creates two track types shaped the way Voptrack's own
are -- a value with a unit, and a two-value reading sharing one unit -- and a
fortnight of readings for each.

Synthetic and obviously so. It is not the bundled catalogue, which is still a
sketch with open questions recorded in specs/voptrack-tracks-format.md, and
seeding a sketch into a demonstration would misrepresent how settled it is.
"""

import json
import math
import ssl
import sys
import urllib.error
import urllib.request
from datetime import datetime, timedelta, timezone

WEIGHT = "com.example.evaluation.weight"
PRESSURE = "com.example.evaluation.blood_pressure"

TYPES = [
    {
        "id": WEIGHT,
        "parent_id": None,
        "name": "Weight",
        "fields": [
            {"id": "recorded_at", "meta_id": WEIGHT, "name": "Recorded", "type": "date_time"},
            {"id": "weight", "meta_id": WEIGHT, "name": "Weight", "type": "float"},
            {"id": "unit", "meta_id": WEIGHT, "name": "Unit", "type": "text"},
        ],
    },
    {
        "id": PRESSURE,
        "parent_id": None,
        "name": "Blood pressure",
        "fields": [
            {"id": "recorded_at", "meta_id": PRESSURE, "name": "Recorded", "type": "date_time"},
            {"id": "systolic", "meta_id": PRESSURE, "name": "Systolic", "type": "integer"},
            {"id": "diastolic", "meta_id": PRESSURE, "name": "Diastolic", "type": "integer"},
            {"id": "unit", "meta_id": PRESSURE, "name": "Unit", "type": "text"},
        ],
    },
]


def readings(days):
    """A fortnight ending today. Deterministic, so two runs look the same and
    a screenshot stays true."""
    start = datetime.now(timezone.utc).replace(hour=7, minute=30, second=0, microsecond=0)
    for day in range(days, 0, -1):
        # Exactly millisecond precision, which the protocol requires and
        # isoformat() omits entirely when microseconds happen to be zero.
        moment = start - timedelta(days=day)
        at = moment.strftime("%Y-%m-%dT%H:%M:%S.") + f"{moment.microsecond // 1000:03d}Z"
        wobble = math.sin(day / 2.3)
        yield at, round(78.4 + wobble * 0.9, 1), 118 + int(wobble * 5), 76 + int(wobble * 3)


class Server:
    def __init__(self, base, token, ca):
        self.base, self.token = base.rstrip("/"), token
        self.context = ssl.create_default_context(cafile=ca) if ca else None

    def call(self, method, path, body=None):
        data = json.dumps(body).encode() if body is not None else None
        request = urllib.request.Request(self.base + path, data=data, method=method)
        request.add_header("Authorization", "Bearer " + self.token)
        if data:
            request.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(request, context=self.context, timeout=30) as response:
                return response.status, json.loads(response.read() or b"{}")
        except urllib.error.HTTPError as error:
            raw = error.read()
            try:
                return error.code, json.loads(raw or b"{}")
            except json.JSONDecodeError:
                return error.code, {"raw": raw[:200].decode(errors="replace")}


def main():
    if len(sys.argv) < 3:
        print("usage: seed.py BASE_URL TOKEN [CA_FILE]", file=sys.stderr)
        return 2
    base, token = sys.argv[1], sys.argv[2]
    ca = sys.argv[3] if len(sys.argv) > 3 else None
    server = Server(base, token, ca)

    status, body = server.call("GET", "/v1/datasets")
    if status != 200 or not body.get("datasets"):
        print(f"cannot list datasets: {status} {body}", file=sys.stderr)
        return 1
    dataset = body["datasets"][0]["id"]

    for meta in TYPES:
        status, body = server.call("POST", f"/v1/datasets/{dataset}/meta", {"type": meta})
        # 409 is "already there", which is the right answer to running this
        # twice and not a failure.
        if status not in (201, 409):
            print(f"defining {meta['id']}: {status} {body}", file=sys.stderr)
            return 1

    operations = []
    for at, weight, systolic, diastolic in readings(14):
        operations.append({"op": "insert", "type": WEIGHT, "fields": {
            "recorded_at": {"type": "date_time", "value": at},
            "weight": {"type": "float", "value": weight},
            "unit": {"type": "text", "value": "kg"}}})
        operations.append({"op": "insert", "type": PRESSURE, "fields": {
            "recorded_at": {"type": "date_time", "value": at},
            "systolic": {"type": "integer", "value": systolic},
            "diastolic": {"type": "integer", "value": diastolic},
            "unit": {"type": "text", "value": "mmHg"}}})

    status, body = server.call("POST", f"/v1/datasets/{dataset}:transact", {"operations": operations})
    if status != 200:
        print(f"writing readings: {status} {body}", file=sys.stderr)
        return 1

    print(f"seeded {len(TYPES)} types and {len(operations)} readings into {dataset}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
