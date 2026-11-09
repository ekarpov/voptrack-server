"""The required Tessera version comes only from the tracked module pin."""
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[1]

def parse_pin(text):
    matches = re.findall(r'^[ \t]*github\.com/ekarpov/tessera[ \t]+(v[^\s]+)[ \t]*(?://[^\r\n]*)?$', text, re.M)
    if len(matches) != 1:
        raise ValueError('one explicit Tessera module pin is required')
    return matches[0]

def expected_pin():
    return parse_pin((ROOT / 'go.mod').read_text())

if __name__ == '__main__':
    print(expected_pin())
