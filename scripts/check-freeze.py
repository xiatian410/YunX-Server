#!/usr/bin/env python3
"""Validate the frozen sources, append-only visual history, and icon provenance."""
import hashlib
import json
from pathlib import Path
root = Path(__file__).resolve().parents[1]
def sha(data):
    return hashlib.sha256(data).hexdigest()
def history(manifest, depth=0):
    assert depth < 20, 'Freeze history cycle'
    if 'increment' not in manifest:
        return manifest
    increment = json.loads((root / manifest['increment']).read_text())
    previous = (root / increment['parent_manifest']).read_bytes()
    assert sha(previous) == increment['parent_sha256'], 'Previous freeze snapshot changed'
    parent = json.loads(previous)
    initial = history(parent, depth + 1)
    expected_files = dict(parent['files'])
    for name, change in increment['changes'].items():
        assert expected_files.get(name) == change['before'], f'Invalid increment origin: {name}'
        if change['after'] is None:
            expected_files.pop(name)
        else:
            expected_files[name] = change['after']
    assert expected_files == manifest['files'], 'Increment does not match current freeze'
    return initial
manifest = json.loads((root / 'docs/design/freeze.json').read_text())
base = json.loads((root / 'docs/design/baseline-sha256.json').read_text())
initial = history(manifest)
for name, expected in manifest['files'].items():
    assert sha((root / name).read_bytes()) == expected, f'Frozen file changed: {name}'
actual = (root / 'web/src/style.css').read_bytes()
if 'css_base' in manifest:
    original = (root / manifest['css_base']).read_bytes()
    suffix = (root / manifest['css_additions']).read_bytes()
    assert actual == original + suffix, 'Unreviewed CSS change beyond the increment'
else:
    original = actual
# A user-authorized layout generation preserves the initial CSS as a frozen snapshot.
# Validate both the current draft + named refinement and the original baseline.
if 'initial_css_snapshot' in manifest:
    snapshot = manifest['initial_css_snapshot']
    assert snapshot in manifest['files'], 'Initial CSS snapshot must itself be frozen'
    original = (root / snapshot).read_bytes()
css = (root / f"docs/design/candidate-{initial['selected']}.css").read_bytes()
assert original.endswith(css), 'Initial selected refinement changed'
assert sha(original[:-len(css)]) == base['web/src/style.css'], 'Initial CSS baseline changed'
for name, expected in base.items():
    if name != 'web/src/style.css':
        assert initial['files'][name] == expected, f'Initial frontend differs from baseline: {name}'
sources = json.loads((root / 'docs/icon-sources.json').read_text())
for name, source in sources['files'].items():
    assert sha((root / name).read_bytes()) == source['sha256'], f'Upstream icon bytes changed: {name}'
print(f"Frozen frontend: {len(manifest['files'])} hashes verified; original baseline and increment history intact.")
