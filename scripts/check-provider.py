#!/usr/bin/env python3
"""Check included upstream-source hashes against the reviewed local adaptations."""
import hashlib
import json
from pathlib import Path
root=Path(__file__).resolve().parents[1]
manifest=json.loads((root/'provider/UPSTREAM.json').read_text())
for name,entry in manifest['files'].items():
    actual=hashlib.sha256((root/name).read_bytes()).hexdigest()
    assert actual==entry['local_sha256'], f'Provider source drift: {name}'
    assert entry['modified']==(entry['sha256']!=actual), f'Incorrect provenance: {name}'
print(f"Provider provenance: {len(manifest['files'])} source hashes verified.")
