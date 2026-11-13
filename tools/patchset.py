#!/usr/bin/env python3
"""Generate sing-box from a pinned upstream and the manifest's ordered Git diffs.

Adapted from Ricky-Hao/new-api 7ca7f4112b06b26222946033d2fb038fe6223c41,
originally Ricky-Hao/LibreChat f02d9b1d81a5e61661d69bea05e9278c162a3fd7.
Copyright (c) 2026 LibreChat. MIT license: tools/LICENSE.LibreChat.
"""
import argparse
import contextlib
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
ACTIVE = {'keep', 'feature', 'local', 'awaiting-upstream'}
STATUSES = ACTIVE | {'upstream', 'replaced'}
TIERS = {'focused', 'project'}


class Failure(Exception):
    pass


def require(condition, message):
    if not condition:
        raise Failure(message)


def inside(root, relative):
    path = Path(relative)
    require(not path.is_absolute() and '..' not in path.parts, f'Unsafe path: {relative}')
    resolved = (root / path).resolve()
    require(resolved.is_relative_to(root.resolve()), f'Path escapes checkout: {relative}')
    return resolved


def git(target, *args, env=None, data=None):
    result = subprocess.run(['git', '-C', str(target), *args], input=data,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env)
    if result.returncode:
        raise Failure(f'git {" ".join(args)} failed in {target}:\n'
                      + result.stderr.decode(errors='replace'))
    return result.stdout


def load_manifest(root=ROOT):
    manifest = json.loads((root / 'manifest.json').read_text())
    require(manifest['format'] == 1, 'Unsupported manifest format')
    require(re.fullmatch(r'[0-9a-f]{40}', manifest['base']), 'Base must be a full commit SHA')
    require(bool(manifest['upstream']), 'Upstream repository missing')
    application_branch(manifest)
    seen = set()
    for entry in manifest['patches']:
        name = entry['id']
        require(re.fullmatch(r'[a-z0-9]+(?:-[a-z0-9]+)*', name), f'Invalid id: {name}')
        require(name not in seen, f'Duplicate id: {name}')
        require(entry['status'] in STATUSES, f'Unknown status: {name}')
        require(set(entry['depends_on']) <= seen, f'Missing or out-of-order dependency: {name}')
        require(entry['status'] not in ACTIVE or entry['patch'], f'Active patch missing: {name}')
        if entry['status'] not in ACTIVE:
            require(entry['patch'] is None and entry['paths'] == [], f'Retired payload: {name}')
            require(re.fullmatch(r'[0-9a-f]{40}', entry['replacement']['commit']),
                    f'Replacement commit missing: {name}')
            require(bool(entry['replacement']['evidence']), f'Replacement evidence missing: {name}')
        seen.add(name)
        if entry['patch']:
            require(inside(root, entry['patch']).is_file(), f'Payload missing: {name}')
        require(inside(root, f'patches/{name}/README.md').is_file(), f'Guide missing: {name}')
        contract = inside(root, entry['tests'])
        require(contract.is_file(), f'Test contract missing: {name}')
        checks = json.loads(contract.read_text())['checks']
        require(bool(checks), f'Empty test contract: {name}')
        for check in checks:
            validate_check(check)
    return manifest


def application_branch(manifest):
    application = manifest['application']
    ref = application['ref']
    if ref == manifest['base']:
        name = f'upstream-{manifest["base"][:9]}'
    else:
        require(re.fullmatch(r'refs/tags/v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?', ref),
                'Application ref must be the full base SHA or an exact release tag')
        name = ref.removeprefix('refs/tags/')
    require(re.fullmatch(re.escape(name) + r'(?:-patchset-(?:[2-9]|[1-9][0-9]+))?',
                         application['branch']),
            f'Application branch must match {name} (or its -patchset-N revision)')
    return application['branch']


def validate_check(check):
    require(bool(check['name']) and check['tier'] in TIERS, 'Invalid check name or tier')
    require(bool(check['requires']), f'No check entry paths: {check["name"]}')
    require(isinstance(check['command'], list) and bool(check['command'])
            and all(isinstance(arg, str) and arg for arg in check['command']),
            f'Command must be a nonempty argument array: {check["name"]}')


def select(manifest, only=None, statuses=None):
    entries = manifest['patches']
    known = {p['id']: p for p in entries}
    wanted = set(only) if only else {p['id'] for p in entries
                                    if not statuses or p['status'] in statuses}
    require(wanted <= known.keys(), f'Unknown patch ids: {sorted(wanted - known.keys())}')
    pending = list(wanted)
    while pending:
        for dependency in known[pending.pop()]['depends_on']:
            if dependency not in wanted:
                wanted.add(dependency)
                pending.append(dependency)
    require(bool(wanted), 'Selection is empty')
    return [p for p in entries if p['id'] in wanted]


def checkout(target):
    target = target.resolve()
    top = Path(git(target, 'rev-parse', '--show-toplevel').decode().strip()).resolve()
    require(top == target, '--target must name the application Git checkout root')
    return target


@contextlib.contextmanager
def operation_lock(root, target, creating=False):
    """Serialize tool writers across linked worktrees and competing target creation."""
    paths = set()
    for repository in (root, target):
        if repository.exists():
            common = git(repository, 'rev-parse', '--path-format=absolute', '--git-common-dir')
            paths.add(Path(common.decode().strip()) / 'patchset.lock')
    if creating:
        target.parent.mkdir(parents=True, exist_ok=True)
        paths.add(target.parent / f'.{target.name}.patchset.lock')
    acquired = []
    try:
        for path in sorted(paths):
            try:
                descriptor = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
            except FileExistsError as error:
                raise Failure(f'Patchset operation locked: {path}; no work overwritten. '
                              'If interrupted, confirm no generator is running before removing it.') from error
            acquired.append(path)
            with os.fdopen(descriptor, 'w') as lock:
                lock.write(f'pid={os.getpid()}\n')
        yield
    finally:
        for path in reversed(acquired):
            path.unlink()


def fixed_base(target, base):
    head = git(target, 'rev-parse', 'HEAD').decode().strip()
    require(head == base, f'Target HEAD {head} differs from fixed base {base}; '
            'nothing applied. Adapt the patch set explicitly; the tool never changes base.')


def clean_base(target, base):
    require(not git(target, 'status', '--porcelain', '--untracked-files=all'),
            f'Target has changes or untracked files: {target}; nothing applied. '
            'Repeated application is refused. Use a fresh checkout for a new selection.')
    fixed_base(target, base)


def applicable(target, entries, base='HEAD'):
    result = []
    for entry in entries:
        if entry['status'] in ACTIVE:
            result.append(entry)
            continue
        commit = entry['replacement']['commit']
        git(target, 'merge-base', '--is-ancestor', commit, base)
        print(f'SKIP {entry["id"]}: {entry["status"]}, included commit {commit}')
    return result


def payload(root, target, entry):
    path = inside(root, entry['patch'])
    stats = git(target, 'apply', '--numstat', '-z', str(path)).split(b'\0')
    paths = []
    for stat in filter(None, stats):
        _, _, name = stat.split(b'\t', 2)
        relative = name.decode()
        require('.git' not in Path(relative).parts, f'Git metadata in payload: {relative}')
        inside(target, relative)
        paths.append(relative)
    require(paths and len(paths) == len(set(paths)), f'Empty or duplicate diff paths: {entry["id"]}')
    require(set(paths) == set(entry['paths']), f'Payload path inventory differs: {entry["id"]}')
    return path.read_bytes()


def prepare(root, target, manifest, entries, history=None):
    entries = applicable(target, entries, manifest['base'])
    # Preflight the entire sequence with a private index. A later conflict cannot
    # leave earlier patches in the user's index or working tree.
    git_dir = git(target, 'rev-parse', '--absolute-git-dir').decode().strip()
    with tempfile.TemporaryDirectory(prefix='patchset-', dir=git_dir) as temporary:
        env = {**os.environ, 'GIT_INDEX_FILE': str(Path(temporary) / 'index')}
        git(target, 'read-tree', manifest['base'], env=env)
        for entry in entries:
            try:
                git(target, 'apply', '--cached', '--binary', '--whitespace=nowarn', '-',
                    env=env, data=payload(root, target, entry))
                if history is not None:
                    history.append((f'patch: {entry["id"]}',
                                    git(target, 'write-tree', env=env).decode().strip()))
            except Failure as error:
                raise Failure(f'Preflight stopped at {entry["id"]}. Target unchanged.\n{error}') from error
        result_tree = git(target, 'write-tree', env=env).decode().strip()
        combined = git(target, 'diff', '--binary', '--full-index', '--no-ext-diff',
                       '--no-renames', manifest['base'], result_tree)
    return entries, result_tree, combined


def apply(root, target, manifest, entries, write=False):
    clean_base(target, manifest['base'])
    entries, result_tree, combined = prepare(root, target, manifest, entries)
    if write and combined:
        clean_base(target, manifest['base'])
        git(target, 'apply', '--index', '--binary', '--whitespace=nowarn', '-', data=combined)
    action = 'APPLIED (staged)' if write else 'CHECKED (no application files changed)'
    print(f'{action}: {len(entries)} patches; tree {result_tree}')
    return result_tree


def verify(root, target, manifest, entries):
    if git(target, 'rev-parse', 'HEAD').decode().strip() != manifest['base']:
        return verify_branch(root, target, manifest, manifest['patches'])
    fixed_base(target, manifest['base'])
    git(target, 'diff', '--exit-code')
    require(not git(target, 'ls-files', '--others', '--exclude-standard'),
            'Generated checkout has untracked files; preserve and write back your work first')
    _, expected, _ = prepare(root, target, manifest, entries)
    actual = git(target, 'write-tree').decode().strip()
    require(actual == expected, f'Generated tree {actual} differs from manifest tree {expected}; '
            'preserve and write back your work first')
    print(f'VERIFIED: base {manifest["base"]}; tree {actual}')
    return actual


def generate(root, target, manifest, entries, source=None):
    if target.exists():
        return verify(root, checkout(target), manifest, entries)
    target.parent.mkdir(parents=True, exist_ok=True)
    # An independent clone can be renamed safely; failed generation never exposes
    # a partially patched application or borrows a worktree's index.
    with tempfile.TemporaryDirectory(prefix='patchset-generate-', dir=target.parent) as temporary:
        application = Path(temporary) / 'application'
        git(target.parent, 'clone', '--no-checkout', '--no-hardlinks',
            str(source or manifest['upstream']), str(application))
        git(application, 'checkout', '--detach', manifest['base'])
        apply(root, application, manifest, entries)
        tree = apply(root, application, manifest, entries, write=True)
        application.rename(target)
    print(f'GENERATED: {target}; base {manifest["base"]}; tree {tree}')
    return tree


def branch_commit(root, target, manifest, entries):
    require(entries == manifest['patches'], 'Application branches require the complete manifest')
    ref = manifest['application']['ref']
    resolved = git(target, 'rev-parse', '--verify', f'{ref}^{{commit}}').decode().strip()
    require(resolved == manifest['base'], f'Upstream ref {ref} does not resolve to fixed base')
    history = []
    _, patched_tree, _ = prepare(root, target, manifest, entries, history)
    source = hashlib.sha256(json.dumps(manifest, sort_keys=True).encode())
    for entry in entries:
        if entry['patch']:
            source.update(inside(root, entry['patch']).read_bytes())
    guide = root / 'tools/application-AGENTS.md'
    source.update(guide.read_bytes())
    git_dir = git(target, 'rev-parse', '--absolute-git-dir').decode().strip()
    with tempfile.TemporaryDirectory(prefix='patchset-branch-', dir=git_dir) as temporary:
        env = {**os.environ, 'GIT_INDEX_FILE': str(Path(temporary) / 'index')}
        git(target, 'read-tree', patched_tree, env=env)
        # Keep upstream workflow bytes and attribution without registering extra
        # Actions entry points when this application branch is published.
        files = git(target, 'ls-tree', '-r', '-z', patched_tree, '--', '.github/workflows/')
        for item in filter(None, files.split(b'\0')):
            metadata, path = item.split(b'\t', 1)
            mode, kind, blob = metadata.decode().split()
            require(kind == 'blob', 'Unsupported upstream workflow entry')
            original = path.decode()
            archive = original.replace('.github/workflows/', '.github/upstream-workflows/', 1)
            require(not git(target, 'ls-tree', patched_tree, '--', archive),
                    f'Upstream workflow archive collision: {archive}')
            git(target, 'update-index', '--add', '--cacheinfo', mode, blob, archive, env=env)
            git(target, 'update-index', '--force-remove', '--', original, env=env)
        # Maintenance instructions are generated alongside workflow archival,
        # not a sixth business feature. Preserve future upstream guidance.
        require(not git(target, 'ls-tree', patched_tree, '--', 'AGENTS.md'),
                'Upstream AGENTS.md exists; adapt maintenance guidance explicitly')
        blob = git(target, 'hash-object', '-w', '--stdin', data=guide.read_bytes()).decode().strip()
        git(target, 'update-index', '--add', '--cacheinfo', '100644', blob, 'AGENTS.md', env=env)
        tree = git(target, 'write-tree', env=env).decode().strip()
    name = application_branch(manifest)
    history.append((f'maintenance: materialize {name}\n\n'
                    f'Upstream: {manifest["upstream"]}\nUpstream-Ref: {ref}\n'
                    f'Upstream-Base: {manifest["base"]}\nPatchset-Source: {source.hexdigest()}\n'
                    f'Patched-Tree: {patched_tree}\n'
                    'Workflows: .github/upstream-workflows/ (inactive; CI belongs to main)\n'
                    'Guidance: tools/application-AGENTS.md -> AGENTS.md', tree))
    # Fixed generator identity and upstream timestamp make independent CI jobs
    # reproduce the same commits, without reading or changing a user's identity.
    date = git(target, 'show', '-s', '--format=%ct', manifest['base']).decode().strip() + ' +0000'
    env = {**os.environ, 'GIT_AUTHOR_NAME': 'sing-box patchset generator',
           'GIT_AUTHOR_EMAIL': 'patchset@sing-box.invalid', 'GIT_AUTHOR_DATE': date,
           'GIT_COMMITTER_NAME': 'sing-box patchset generator',
           'GIT_COMMITTER_EMAIL': 'patchset@sing-box.invalid', 'GIT_COMMITTER_DATE': date}
    commit = manifest['base']
    for message, stage_tree in history:
        commit = git(target, '-c', 'i18n.commitEncoding=UTF-8', 'commit-tree', stage_tree, '-p', commit,
                     data=(message + '\n').encode(), env=env).decode().strip()
    return commit, tree


def verify_branch(root, target, manifest, entries):
    name = application_branch(manifest)
    require(git(target, 'symbolic-ref', 'HEAD').decode().strip() == f'refs/heads/{name}',
            f'Target must be on application branch {name}')
    require(not git(target, 'status', '--porcelain', '--untracked-files=all'),
            'Application branch has user changes; preserve and write back your work first')
    expected, tree = branch_commit(root, target, manifest, entries)
    require(git(target, 'rev-parse', 'HEAD').decode().strip() == expected,
            'Application commit differs from manifest; preserve existing branch and use a new revision')
    print(f'VERIFIED BRANCH: {name}; commit {expected}; base {manifest["base"]}; tree {tree}')
    return tree


def branch(root, target, manifest, entries, source=None):
    checkout(root)
    name = application_branch(manifest)
    if target.exists():
        target = checkout(target)
        require(git(root, 'rev-parse', '--path-format=absolute', '--git-common-dir')
                == git(target, 'rev-parse', '--path-format=absolute', '--git-common-dir'),
                'Existing target is not an application worktree of this maintenance repository; '
                'preserve it and choose an unused target')
        return verify_branch(root, target, manifest, entries)
    target.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='patchset-branch-', dir=target.parent) as temporary:
        application = Path(temporary) / 'application'
        git(target.parent, 'clone', '--no-checkout', '--no-hardlinks',
            str(source or manifest['upstream']), str(application))
        git(application, 'checkout', '--detach', manifest['base'])
        commit, tree = branch_commit(root, application, manifest, entries)
        existing = git(root, 'for-each-ref', '--format=%(objectname)', f'refs/heads/{name}').decode().strip()
        require(not existing or existing == commit,
                f'Existing branch {name} differs; nothing overwritten. Use a new -patchset-N revision')
        refspecs = [commit]
        ref = manifest['application']['ref']
        if ref.startswith('refs/tags/'):
            # Linked worktrees share refs; retain the validated release tag for
            # offline verification. An unforced fetch rejects conflicting tags.
            refspecs.append(f'{ref}:{ref}')
        git(root, 'fetch', '--no-tags', '--no-write-fetch-head', str(application), *refspecs)
        if not existing:
            git(root, 'update-ref', f'refs/heads/{name}', commit, '0' * 40)
    git(root, 'worktree', 'add', str(target), name)
    print(f'BRANCHED: {name}; commit {commit}; base {manifest["base"]}; tree {tree}; target {target}')
    return tree


def contracts(root, target, entries, tier):
    checks = []
    seen = set()
    for entry in entries:
        contract = json.loads(inside(root, entry['tests']).read_text())
        for check in contract['checks']:
            validate_check(check)
            if tier != 'all' and check['tier'] != tier:
                continue
            key = json.dumps(check, sort_keys=True)
            if key in seen:
                continue
            seen.add(key)
            cwd = inside(target, check['cwd'])
            require(cwd.is_dir(), f'Check directory missing: {cwd}')
            for path in check['requires']:
                require(inside(target, path).is_file(), f'Test/check entry missing: {path}')
            checks.append(check)
    require(bool(checks), f'No checks in tier {tier}; nothing was run')
    return checks


def test(root, target, entries, tier='focused'):
    for check in contracts(root, target, entries, tier):
        print(f'RUN: {check["name"]} ({check["cwd"]})', flush=True)
        subprocess.run(check['command'], cwd=inside(target, check['cwd']), check=True)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=ROOT, help=argparse.SUPPRESS)
    parser.add_argument('action', choices=['list', 'check', 'apply', 'generate', 'branch', 'verify', 'test'])
    parser.add_argument('--target', type=Path)
    parser.add_argument('--source', type=Path, help='generate/branch from a local Git repository (offline)')
    selection = parser.add_mutually_exclusive_group()
    selection.add_argument('--only', nargs='+', metavar='ID', help='include ids and their dependencies')
    selection.add_argument('--status', nargs='+', choices=sorted(STATUSES))
    parser.add_argument('--tier', choices=[*sorted(TIERS), 'all'], default='focused')
    args = parser.parse_args(argv)
    root = args.root.resolve()
    manifest = load_manifest(root)
    entries = select(manifest, args.only, args.status)
    if args.action == 'list':
        for entry in entries:
            print(f'{entry["id"]}\t{entry["status"]}\t'
                  f'deps={",".join(entry["depends_on"]) or "-"}\t{entry["patch"] or "no payload"}')
        return
    require(args.target is not None, '--target is required')
    target = args.target.resolve()
    require(target != root, 'Use an application checkout, not the patch repository')
    require(args.source is None or args.action in {'generate', 'branch'},
            '--source is only for generate/branch')
    with operation_lock(root, target, args.action in {'generate', 'branch'}):
        if args.action in {'generate', 'branch'}:
            if args.action == 'branch':
                require(not args.only and not args.status, 'Application branches require the complete manifest')
            action = branch if args.action == 'branch' else generate
            action(root, target, manifest, entries, args.source.resolve() if args.source else None)
            return
        target = checkout(target)
        if args.action in {'test', 'verify'}:
            verify(root, target, manifest, entries)
            if args.action == 'test':
                test(root, target, entries, args.tier)
        else:
            apply(root, target, manifest, entries, args.action == 'apply')


if __name__ == '__main__':
    try:
        main()
    except (Failure, OSError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        print(f'ERROR: {error}', file=sys.stderr)
        sys.exit(1)
