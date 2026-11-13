#!/usr/bin/env python3
"""Rebuild the application and expose exact identities to validation/build jobs."""
import argparse
import hashlib
import json
import os
import subprocess
import sys
from pathlib import Path

import patchset


def generation_input(root, manifest, read):
    digest = hashlib.sha256(json.dumps(manifest, sort_keys=True).encode())
    paths = ['tools/patchset.py', 'tools/application-AGENTS.md']
    paths += [entry['patch'] for entry in manifest['patches'] if entry['patch']]
    for path in paths:
        digest.update(path.encode() + b'\0' + read(path))
    return digest.digest()


def release_needed(root, manifest, previous):
    if not previous:
        return False
    previous = patchset.git(root, 'rev-parse', '--verify', f'{previous}^{{commit}}').decode().strip()
    exists = patchset.git(root, 'ls-tree', previous, '--', 'manifest.json')
    if not exists:
        return True  # First migration from a source repository.
    read = lambda path: patchset.git(root, 'show', f'{previous}:{path}')
    old = json.loads(read('manifest.json'))
    changed = generation_input(root, manifest, lambda p: patchset.inside(root, p).read_bytes()) != \
        generation_input(root, old, read)
    same_name = manifest['application']['branch'] == old['application']['branch']
    patchset.require(not (changed and same_name),
                     'Generation inputs changed: preserve the previous snapshot and choose a new application.branch')
    return changed


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--target', type=Path, default=Path('.work/application'))
    parser.add_argument('--source', type=Path)
    parser.add_argument('--previous', help='Previous main commit; used to enforce immutable snapshot names')
    parser.add_argument('--maintenance', help='Require this exact clean maintenance SHA in CI')
    parser.add_argument('--expect-commit')
    parser.add_argument('--expect-tree')
    args = parser.parse_args()
    root = patchset.ROOT
    manifest = patchset.load_manifest(root)
    maintenance = patchset.git(root, 'rev-parse', 'HEAD').decode().strip()
    if args.maintenance:
        patchset.require(maintenance == args.maintenance, 'Wrong maintenance checkout')
        patchset.require(not patchset.git(root, 'status', '--porcelain', '--untracked-files=all'),
                         'CI maintenance checkout is dirty')
    release = release_needed(root, manifest, args.previous)
    target = args.target.resolve()
    with patchset.operation_lock(root, target, creating=True):
        tree = patchset.branch(root, target, manifest, manifest['patches'], args.source)
        patchset.verify_branch(root, target, manifest, manifest['patches'])
    commit = patchset.git(target, 'rev-parse', 'HEAD').decode().strip()
    patchset.require(not args.expect_commit or commit == args.expect_commit, 'Application commit changed between jobs')
    patchset.require(not args.expect_tree or tree == args.expect_tree, 'Application tree changed between jobs')
    name = patchset.application_branch(manifest)
    values = {'maintenance': maintenance, 'base': manifest['base'], 'branch': name,
              'commit': commit, 'tree': tree, 'version': name.removeprefix('v'),
              'tag': f'patchset-{name}', 'release': str(release).lower(),
              'groups': json.dumps([p['id'] for p in manifest['patches']], separators=(',', ':'))}
    output = ''.join(f'{key}={value}\n' for key, value in values.items())
    print(output, end='')
    if os.environ.get('GITHUB_OUTPUT'):
        with open(os.environ['GITHUB_OUTPUT'], 'a') as stream:
            stream.write(output)


if __name__ == '__main__':
    try:
        main()
    except (patchset.Failure, OSError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        print(f'ERROR: {error}', file=sys.stderr)
        sys.exit(1)
