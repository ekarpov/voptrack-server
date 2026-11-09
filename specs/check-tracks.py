#!/usr/bin/env python3
"""Validate the bundled track catalog.

A type ID and its field IDs are written into datasets, so a catalog that is wrong
produces types a server refuses or, worse, types two platforms disagree about. The
rules mirrored from the protocol are marked; the rest are Voptrack's own.

See voptrack-tracks-format.md. Exits non-zero and prints every problem it found,
rather than the first, because fixing these one round trip at a time is the thing
a validator exists to prevent.
"""

import json
import re
import sys
from pathlib import Path

HERE = Path(__file__).parent
CATALOG = HERE / "voptrack-tracks-v1.json"
VOCABULARY = HERE / "voptrack-vocabulary-v1.json"

# Mirrored from pkg/protocol/identifiers.go in the Tessera repository. If these
# drift the catalog starts passing here and failing at the server, so they are
# copied deliberately and named, not approximated.
META_ID = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$")
FIELD_ID = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")
RESERVED = set("""add all alter and as autoincrement between case check collate commit constraint create default
deferrable delete distinct drop else escape except exists foreign from group having if in index
insert intersect into is isnull join limit not nothing notnull null on or order primary references
returning select set table then to transaction union unique update using values when where""".split())
# pkg/protocol/value.go. There is no "number". `fixed` is an exact decimal
# carried as a canonical string, added in Tessera v0.1.0-alpha.10 -- the type
# for money, where float would lose cents.
STORAGE_TYPES = {"integer", "float", "fixed", "text", "boolean", "date_time", "media", "json", "blob", "reference"}

problems = []


def problem(where, message):
    problems.append(f"{where}: {message}")


def identifier(kind, value, where):
    if not isinstance(value, str) or not 1 <= len(value.encode("utf-8")) <= 255:
        problem(where, f"{kind} identifier must be 1-255 bytes of text")
        return
    pattern = META_ID if kind == "type" else FIELD_ID
    if not pattern.fullmatch(value):
        problem(where, f"{kind} identifier {value!r} does not match the protocol's grammar")
    if value.lower() in RESERVED:
        problem(where, f"{kind} identifier {value!r} is a reserved word")
    if kind == "field" and value in ("id", "meta_id"):
        problem(where, f"field identifier {value!r} is reserved by the record shape")


def vocabulary_names(vocabulary):
    def ids(section):
        entries = vocabulary.get(section, [])
        if isinstance(entries, dict):
            return set(entries)
        return {e.get("id") for e in entries if isinstance(e, dict)} | {e for e in entries if isinstance(e, str)}

    units = {}
    for entry in vocabulary.get("units", []):
        if isinstance(entry, dict) and entry.get("id"):
            units[entry["id"]] = entry.get("dimension") or entry.get("dimensionID")
    return {
        "roles": ids("field_roles") | ids("type_roles"),
        "dimensions": ids("dimensions"),
        "meanings": ids("meanings"),
        "annotation_keys": ids("annotation_keys"),
        "units": units,
    }


def check_field(field, type_id, known, field_ids):
    where = f"{type_id}.{field.get('id', '?')}"
    identifier("field", field.get("id"), where)
    if field.get("id") in field_ids:
        problem(where, "duplicate field identifier within the type")
    field_ids.add(field.get("id"))

    storage = field.get("storage_type")
    if storage not in STORAGE_TYPES:
        hint = " — the protocol has integer and float, not number" if storage == "number" else ""
        problem(where, f"storage_type {storage!r} is not a protocol type{hint}")

    for role in field.get("roles", []):
        if role not in known["roles"]:
            problem(where, f"role {role!r} is not in the vocabulary")

    annotations = field.get("annotations", {})
    for key in annotations:
        if key not in known["annotation_keys"]:
            problem(where, f"annotation key {key!r} is not in the vocabulary")

    dimension = annotations.get("dimension")
    if dimension is not None and dimension not in known["dimensions"]:
        problem(where, f"dimension {dimension!r} is not in the vocabulary")

    meaning = annotations.get("meaning")
    if meaning is not None and meaning not in known["meanings"]:
        problem(where, f"meaning {meaning!r} is not in the vocabulary")

    named_units = list(annotations.get("allowed_units", []))
    if annotations.get("unit"):
        named_units.append(annotations["unit"])
    for unit in named_units:
        if unit not in known["units"]:
            problem(where, f"unit {unit!r} is not in the vocabulary")
        elif dimension and known["units"][unit] and known["units"][unit] != dimension:
            problem(where, f"unit {unit!r} belongs to {known['units'][unit]}, not {dimension}")

    source = annotations.get("unit_source")
    if source == "fixed" and not annotations.get("unit"):
        problem(where, 'unit_source "fixed" needs a unit')
    if source == "record" and not annotations.get("unit_field"):
        problem(where, 'unit_source "record" needs a unit_field')
    return annotations.get("unit_field") if source == "record" else None


def main():
    try:
        catalog = json.loads(CATALOG.read_text())
        vocabulary = json.loads(VOCABULARY.read_text())
    except (OSError, json.JSONDecodeError) as error:
        print(f"tracks: cannot read the catalog or vocabulary: {error}")
        return 1

    known = vocabulary_names(vocabulary)
    types = catalog.get("types", [])
    if not isinstance(types, list):
        print("tracks: 'types' must be a list")
        return 1

    by_id = {}
    for entry in types:
        type_id = entry.get("id")
        identifier("type", type_id, type_id or "<missing id>")
        if type_id in by_id:
            problem(type_id, "duplicate type identifier")
        by_id[type_id] = entry
        for role in entry.get("roles", []):
            if role not in known["roles"]:
                problem(type_id, f"role {role!r} is not in the vocabulary")

    for type_id, entry in by_id.items():
        field_ids = set()
        unit_fields = []
        for field in entry.get("fields", []):
            unit_fields.append(check_field(field, type_id, known, field_ids))
        for unit_field in filter(None, unit_fields):
            if unit_field not in field_ids:
                problem(type_id, f"unit_field {unit_field!r} is not a field of this type")

        # Ancestry: the parent must exist, the chain must terminate, and a subtype
        # never redeclares an inherited field — Tessera merges effective metadata
        # from the ancestry and cannot resolve two declarations of one field.
        seen, parent, inherited = {type_id}, entry.get("parent"), set()
        while parent:
            if parent not in by_id:
                problem(type_id, f"parent {parent!r} is not a type in this catalog")
                break
            if parent in seen:
                problem(type_id, f"inheritance cycle through {parent!r}")
                break
            seen.add(parent)
            inherited |= {f.get("id") for f in by_id[parent].get("fields", [])}
            parent = by_id[parent].get("parent")
        for redeclared in sorted(field_ids & inherited):
            problem(type_id, f"redeclares inherited field {redeclared!r}")

    if problems:
        print(f"tracks: {len(problems)} problem(s)")
        for line in problems:
            print(f"  {line}")
        return 1
    print(f"tracks: {len(by_id)} type(s) valid against the protocol and the vocabulary")
    return 0


if __name__ == "__main__":
    sys.exit(main())
