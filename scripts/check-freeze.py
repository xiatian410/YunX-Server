#!/usr/bin/env python3
"""Verify the frozen frontend and the precise refinement boundary."""
import hashlib
import json
from pathlib import Path
root = Path(__file__).resolve().parents[1]
def sha(data):
    return hashlib.sha256(data).hexdigest()
manifest = json.loads((root / 'docs/design/freeze.json').read_text())
base = json.loads((root / 'docs/design/baseline-sha256.json').read_text())
initial = manifest
if 'increment' in manifest:
    increment = json.loads((root / manifest['increment']).read_text())
    previous = (root / increment['parent_manifest']).read_bytes()
    assert sha(previous) == increment['parent_sha256'], 'Initial freeze snapshot changed'
    initial = json.loads(previous)
    expected_files = dict(initial['files'])
    for name, change in increment['changes'].items():
        assert expected_files.get(name) == change['before'], f'Invalid increment origin: {name}'
        if change['after'] is None:
            expected_files.pop(name)
            assert not (root / name).exists(), f'Removed asset remains: {name}'
        else:
            expected_files[name] = change['after']
    assert expected_files == manifest['files'], 'Increment does not match current freeze'
for name, expected in manifest['files'].items():
    assert sha((root / name).read_bytes()) == expected, f'Frozen file changed: {name}'
css = (root / f"docs/design/candidate-{manifest['selected']}.css").read_bytes()
actual = (root / 'web/src/style.css').read_bytes()
assert actual.endswith(css), 'Selected candidate is not the exact CSS suffix'
assert sha(actual[:-len(css)]) == base['web/src/style.css'], 'Unreviewed baseline CSS changed'
for name, expected in base.items():
    if name != 'web/src/style.css':
        assert initial['files'][name] == expected, f'Initial frontend differs from baseline: {name}'
sources = json.loads((root / 'docs/icon-sources.json').read_text())
for name, source in sources['files'].items():
    assert sha((root / name).read_bytes()) == source['sha256'], f'Upstream icon bytes changed: {name}'
print(f"Frozen candidate {manifest['selected']}: {len(manifest['files'])} hashes verified; original baseline and documented increment intact.")
