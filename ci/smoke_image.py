#!/usr/bin/env python3
"""Fail shared prerequisites before expensive integration; no serving or credentials."""
import json
import re
import subprocess
import sys
from tessera_pin import expected_pin

DIGEST = re.compile(r'^sha256:[0-9a-f]{64}$')

def capture(*args):
    return subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)

def verify(image, run=capture):
    inspected = run('docker','image','inspect',image,'--format','{{.Id}}')
    identifier = inspected.stdout.strip()
    if inspected.returncode or not DIGEST.fullmatch(identifier):
        raise ValueError('image identity unavailable')
    version = run('docker','run','--rm','--network','none','--entrypoint','/usr/local/bin/tesserad',identifier,'-version')
    if version.returncode or version.stdout.strip() != expected_pin():
        raise ValueError('image does not contain the required Tessera pin')
    executable = run('docker','run','--rm','--network','none','--entrypoint','sh',identifier,'-c','test -x /usr/local/bin/voptrack-init')
    if executable.returncode:
        raise ValueError('image bootstrap is not executable')
    refusal = run('docker','run','--rm','--network','none','--env','VOPTRACK_STORAGE_ENCRYPTED=','--entrypoint','/usr/local/bin/voptrack-init',identifier)
    if not refusal.returncode or 'VOPTRACK_STORAGE_ENCRYPTED is not' not in refusal.stderr + refusal.stdout:
        raise ValueError('image bootstrap did not enforce its storage prerequisite')
    logo = run('docker','run','--rm','--network','none','--entrypoint','sh',identifier,'-c','test -r /usr/local/share/voptrack/identity-logo.png')
    if logo.returncode:
        raise ValueError('image operator logo is not readable by the server user')
    return {'image':identifier,'tessera':version.stdout.strip(),'bootstrap_executable':True,'storage_refusal':True,'operator_logo_readable':True}

if __name__ == '__main__':
    try:
        print(json.dumps(verify(sys.argv[1]),sort_keys=True))
    except (ValueError,IndexError):
        print('Image preflight failed.',file=sys.stderr)
        raise
