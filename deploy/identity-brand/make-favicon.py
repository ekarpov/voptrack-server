#!/usr/bin/env python3
"""Wrap the existing transparent PNG sizes in a browser-compatible ICO.

ICO directory layout / PNG entries:
https://chromium.googlesource.com/chromium/src/+/refs/heads/main/tools/resources/ico_tools.py
"""
from pathlib import Path
import struct

root = Path(__file__).resolve().parents[2]
static = root / 'cmd/console/static'
frames = [(size, (static / f'favicon-{size}.png').read_bytes()) for size in (16, 32)]
offset = 6 + 16 * len(frames)
entries = []
for size, png in frames:
    if png[:8] != b'\x89PNG\r\n\x1a\n' or struct.unpack('>II', png[16:24]) != (size, size):
        raise SystemExit('Invalid favicon PNG input')
    entries.append(struct.pack('<BBBBHHII', size, size, 0, 0, 1, 32, len(png), offset))
    offset += len(png)
(static / 'favicon.ico').write_bytes(struct.pack('<HHH', 0, 1, len(frames)) + b''.join(entries) + b''.join(png for _, png in frames))
