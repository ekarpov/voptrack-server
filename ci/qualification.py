"""Release authority is a complete named gate set, never a targeted green run."""
import json
from pathlib import Path
import re
import subprocess
from tessera_pin import expected_pin

ROOT = Path(__file__).resolve().parents[1]

def gate_names():
    return [g['name'] for g in json.loads((ROOT/'ci/gates.json').read_text())['gates']]

def validate_note(text, revision):
    lines=text.splitlines()
    headers={}
    for line in lines:
        if ': ' in line:
            k,v=line.split(': ',1);headers[k]=v
    rows=[(m[1],m[2]) for line in lines if (m:=re.fullmatch(r'(\S+)\s+(PASS|FAIL|SKIP)\s+[0-9]+s',line))]
    if (not lines or lines[0]!='ci: pass' or headers.get('mode')!='full'
            or headers.get('commit')!=revision or headers.get('tessera')!=expected_pin()
            or [n for n,s in rows]!=gate_names() or any(s!='PASS' for n,s in rows)
            or not re.fullmatch(r'sha256:[0-9a-f]{64}',headers.get('image',''))):
        raise ValueError('A complete passing full CI record for this commit is required')

def require_full(revision):
    text=subprocess.check_output(['git','notes','--ref=ci','show',revision],cwd=ROOT,text=True)
    validate_note(text,revision)

if __name__=='__main__':
    import sys
    try:require_full(sys.argv[1])
    except (ValueError,subprocess.CalledProcessError,IndexError):
        sys.exit('No valid full CI qualification for this commit')
