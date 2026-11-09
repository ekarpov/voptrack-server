#!/usr/bin/env python3
"""Find publication-review candidates; print locations/categories, never matched text.

Default: tracked working-tree text plus all commit messages reachable from HEAD.
--ref scans that immutable tree and its reachable messages instead. Use the
squashed release commit for the release gate. No findings are auto-approved.
Binary payloads and semantic privacy review remain outside this lexical check.
"""
import argparse
from collections import defaultdict
import ipaddress
import json
from pathlib import Path
import re
import subprocess
import sys

PATTERNS = {
    'internal-tool': re.compile(r'\b(?:codex|claude|chatgpt|openai|gemini|anthropic|opencode|sol)\b', re.I),
    'internal-document': re.compile(r'\b(?:hand[- ]?off|execution prompt|agent guidance)\b', re.I),
    'local-path': re.compile(r'(?:(?<![A-Za-z0-9])[A-Za-z]:[\\/]|/(?:Users|home|root|mnt)/|~/|/private/)'),
    'key-name': re.compile(r'\b(?:id_rsa|id_ed25519|tessera_dev|VOPTRACK_CI_KEY)\b|\.ssh[/\\][\w.-]+'),
}
IPV4 = re.compile(r'(?<![\w.:])(?:\d{1,3}\.){3}\d{1,3}(?![\w.])')
IPV6 = re.compile(r'(?<![\w:])(?:[0-9a-fA-F]{0,4}:){2,}[0-9a-fA-F:]{0,4}(?![\w:])')
DOCUMENTATION = [ipaddress.ip_network(n) for n in
                 ('192.0.2.0/24', '198.51.100.0/24', '203.0.113.0/24', '2001:db8::/32')]


def git(root, *args):
    p = subprocess.run(['git', *args], cwd=root, capture_output=True)
    if p.returncode:
        raise ValueError('Git could not read the selected publication inputs')
    return p.stdout


def categories(text):
    found = {name for name, pattern in PATTERNS.items() if pattern.search(text)}
    for match in [*IPV4.finditer(text), *IPV6.finditer(text)]:
        try:
            address = ipaddress.ip_address(match.group())
        except ValueError:
            continue
        if address.is_loopback or address.is_unspecified:
            continue
        if any(address.version == n.version and address in n for n in DOCUMENTATION):
            continue
        found.add('host-address')
    return found


def scan(root, ref=None):
    root = root.resolve()
    if Path(git(root, 'rev-parse', '--show-toplevel').decode().strip()).resolve() != root:
        raise ValueError('--root must be the repository root')
    if git(root, 'rev-parse', '--is-shallow-repository').strip() != b'false':
        raise ValueError('Full selected history is required; shallow publication scans are refused')
    commit = git(root, 'rev-parse', '--verify', '--end-of-options', (ref or 'HEAD')+'^{commit}').decode().strip()
    if ref:
        names = []
        for entry in git(root, 'ls-tree', '-r', '-z', commit).split(b'\0'):
            if not entry:
                continue
            metadata, name = entry.split(b'\t', 1)
            mode, kind, _ = metadata.split()
            if kind != b'blob' or mode not in (b'100644', b'100755'):
                raise ValueError('Unsupported tracked file mode requires separate publication review')
            names.append(name)
    else:
        names = git(root, 'ls-files', '-z').split(b'\0')
    findings = []
    for raw_name in sorted(set(names)):
        if not raw_name: continue
        name = raw_name.decode('utf-8', errors='strict')
        for category in categories(name) | ({'internal-document'} if
                name == 'AGENTS.md' or name.startswith('.agents/') or
                re.search(r'(?:handoff|hand-off|prompt)', name, re.I) else set()):
            findings.append({'path': name, 'line': 0, 'category': category})
        if ref:
            raw = git(root, 'show', f'{commit}:{name}')
        else:
            path = root / name
            if path.is_symlink() or not path.resolve().is_relative_to(root):
                raise ValueError('Tracked symlinks require separate publication review')
            raw = path.read_bytes()
        if b'\0' in raw: continue
        text = raw.decode('utf-8', errors='replace')
        for number, line in enumerate(text.splitlines(), 1):
            for category in sorted(categories(line)):
                findings.append({'path': name, 'line': number, 'category': category})
    messages = git(root, 'log', '--format=%H%x00%B%x00', commit).decode('utf-8', errors='replace').split('\0')
    for i in range(0, len(messages)-1, 2):
        sha, message = messages[i].strip(), messages[i+1]
        for number, line in enumerate(message.splitlines(), 1):
            for category in sorted(categories(line)):
                findings.append({'path': 'commit:'+sha, 'line': number, 'category': category})
    return {'commit': commit, 'tree': 'committed' if ref else 'working', 'findings': findings}


def recommendation(path):
    if path.startswith('.agents/') or path == 'AGENTS.md':
        return 'Owner decides', 'Agent guidance may be private; retain internally before any removal.'
    if path == 'CLAUDE.md':
        return 'Owner decides', 'Alternate agent-guidance entry point; keep consistent with the AGENTS.md decision.'
    if re.search(r'(?:handoff|hand-off|prompt)', path, re.I):
        return 'Remove from public tree', 'Internal coordination artifact; preserve an internal copy.'
    if path.startswith('docs/'):
        return 'Rewrite or retain privately', 'Separate public contracts from internal history and coordination.'
    if path in ('specs/check-public.py', 'specs/test_public.py'):
        return 'Keep, review narrow exceptions', 'Checker definitions and defect fixtures necessarily name prohibited patterns.'
    return 'Rewrite; keep supported behavior', 'Review matches; example networks, test fixtures and configurable paths may be intentional.'


def inventory(result):
    groups = defaultdict(list)
    messages = set()
    for finding in result['findings']:
        if finding['path'].startswith('commit:'):
            messages.add(finding['path'])
        else:
            groups[finding['path']].append(finding)
    lines = ['# Public-readiness decisions', '',
             'Review candidates only; no files or findings are approved or removed.',
             'Locations and categories are shown without matched contents. The scan is lexical,',
             'skips binary contents and cannot certify privacy or catch every address/name.', '',
             f"Scan: `{result['commit'][:12]}` with {result['tree']} tree; {len(messages)} reachable commit messages flagged.",
             'Internal history must remain private; recheck the single squashed release commit.', '',
             '| File | Categories and lines | Recommendation | Reason |', '| --- | --- | --- | --- |']
    for path, findings in sorted(groups.items()):
        locations = defaultdict(list)
        for f in findings: locations[f['category']].append(str(f['line']) if f['line'] else 'name')
        summary = '; '.join(k+': '+', '.join(v[:8])+(' …' if len(v)>8 else '') for k,v in sorted(locations.items()))
        action, reason = recommendation(path)
        safe = path.replace('|', '\\|').replace('`', '').replace('\n',' ').replace('\r',' ')
        lines.append(f'| `{safe}` | {summary} | {action} | {reason} |')
    return '\n'.join(lines)+'\n'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument('--ref', help='Scan this committed tree and all reachable commit messages')
    parser.add_argument('--json', action='store_true', help='Emit locations/categories without matched text')
    parser.add_argument('--report', type=Path, help='Write the per-file owner decision table')
    args = parser.parse_args()
    try:
        result = scan(args.root, args.ref)
        if args.report: args.report.write_text(inventory(result))
    except (OSError, ValueError, UnicodeError):
        print('public-check: cannot completely read selected inputs; no passing result', file=sys.stderr)
        return 2
    if args.json:
        print(json.dumps(result, indent=2))
    else:
        for f in result['findings']:
            print(f"{f['path']}:{f['line']}: {f['category']}")
        print(f"public-check: {len(result['findings'])} review candidates; no automatic approvals")
    return 1 if result['findings'] else 0


if __name__ == '__main__':
    raise SystemExit(main())
