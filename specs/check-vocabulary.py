#!/usr/bin/env python3
"""Enforce that the vocabulary only ever grows.

A published meaning is permanent. That is not a style preference: datasets record
`dimension: mass` and nothing records which version of this document was meant, so a
changed entry retroactively alters what every stored record says. Additions are free;
edits and removals are not, and no reviewer catches a changed constant reliably.

Usage:  check-vocabulary.py [BASE_REF]      (default: HEAD)
        check-vocabulary.py --history

Compares the working-tree vocabulary against BASE_REF and fails on anything but
additions, plus a version that did not increase.

--history compares it against every version committed since append-only began
to be enforced (the commit that added this script), and HEAD.
That is what CI runs: comparing a clean checkout against HEAD compares a commit
with itself, so a changed meaning that was committed would pass. An entry must
match every version it ever appeared in, so no later commit can rewrite it
either. It refuses a shallow clone, which would silently have no history.
"""

import json
import subprocess
import sys
from pathlib import Path

VOCABULARY = Path(__file__).with_name("voptrack-vocabulary-v1.json")

# Sections whose entries are identified by "id" and must never change or disappear.
KEYED = ["field_roles", "type_roles", "meanings", "dimensions", "units", "annotation_keys"]


def load(ref: str | None):
    if ref is None:
        return json.loads(VOCABULARY.read_text())
    rel = VOCABULARY.relative_to(Path(__file__).resolve().parents[1])
    out = subprocess.run(["git", "show", f"{ref}:{rel}"], capture_output=True, text=True,
                         cwd=VOCABULARY.parents[1])
    if out.returncode != 0:
        return None  # Not present in the base: everything is an addition.
    return json.loads(out.stdout)


def entries(doc, section):
    return {e["id"]: e for e in doc.get(section, [])}


def compare(old, new) -> list[str]:
    problems: list[str] = []
    for section in KEYED:
        was, now = entries(old, section), entries(new, section)
        for ident, entry in was.items():
            if ident not in now:
                problems.append(f"{section}: '{ident}' was removed — names are permanent")
            elif now[ident] != entry:
                changed = sorted({k for k in set(entry) | set(now[ident])
                                  if entry.get(k) != now[ident].get(k)})
                # Prose may be clarified; anything a consumer computes with may not.
                hard = [k for k in changed if k not in ("doc",)]
                if hard:
                    problems.append(
                        f"{section}: '{ident}' changed {hard} — publish a new name instead")

    if str(new.get("version")) < str(old.get("version")):
        problems.append(f"version went backwards: {old.get('version')} -> {new.get('version')}")
    if problems and str(new.get("version")) == str(old.get("version")):
        problems.append("version unchanged despite edits — additions bump the minor version")
    return problems


def git(*args: str) -> str:
    out = subprocess.run(["git", *args], capture_output=True, text=True, cwd=VOCABULARY.parents[1])
    if out.returncode != 0:
        raise SystemExit(f"vocabulary: git {' '.join(args)} failed: {out.stderr.strip()}")
    return out.stdout.strip()


def main() -> int:
    arg = sys.argv[1] if len(sys.argv) > 1 else "HEAD"
    new = load(None)
    if arg == "--history":
        if git("rev-parse", "--is-shallow-repository") == "true":
            print("vocabulary: a shallow clone has no history to compare against; "
                  "fetch it whole (actions/checkout: fetch-depth: 0)", file=sys.stderr)
            return 1
        root = Path(__file__).resolve().parents[1]
        rel = str(VOCABULARY.relative_to(root))
        # Append-only binds from the commit that began enforcing it: edits
        # made before then (9f8f4a4, adopting a partner's changes) predate the
        # promise, and are not violations of it.
        since = git("log", "--diff-filter=A", "--format=%H", "--",
                    str(Path(__file__).resolve().relative_to(root))).split()[-1]
        # After the first public release squashed the history, that commit is
        # the root and has no parent: compare against everything there is.
        has_parent = len(git("rev-list", "--parents", "-n", "1", since).split()) > 1
        span = f"{since}^..HEAD" if has_parent else "HEAD"
        refs = ["HEAD"] + git("log", "--format=%H", span, "--", rel).split()
    else:
        refs = [arg]

    problems: list[str] = []
    compared = 0
    for ref in refs:
        old = load(ref)
        if old is None:
            continue  # not present there: everything is an addition
        compared += 1
        problems += [f"{p} (against {ref[:12]})" for p in compare(old, new)]

    for p in sorted(set(problems)):
        print(f"vocabulary: {p}", file=sys.stderr)
    if problems:
        return 1
    print(f"vocabulary: append-only respected against {compared} committed version(s)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
