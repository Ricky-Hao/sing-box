"""Real Git regression tests, adapted from new-api / LibreChat in SOURCES.md.

Copyright (c) 2026 LibreChat. MIT license: tools/LICENSE.LibreChat.
"""
import contextlib
import copy
import io
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import patchset
import ci as ci_tools
import check_assets

CLI = Path(__file__).with_name('patchset.py').resolve()


class PatchsetTests(unittest.TestCase):
    def setUp(self):
        work = CLI.parents[1] / '.work'
        work.mkdir(exist_ok=True)
        temporary = tempfile.TemporaryDirectory(prefix='tool-test-', dir=work)
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name) / 'catalog'
        self.target = Path(temporary.name) / 'application'
        self.root.mkdir()
        (self.root / 'tools').mkdir()
        (self.root / 'tools/application-AGENTS.md').write_text('# Generated application rules\n')
        self.target.mkdir()
        self.git('init', '-q')
        self.git('config', 'user.name', 'Patchset test')
        self.git('config', 'user.email', 'test@example.invalid')
        self.git('config', 'commit.gpgsign', 'false')
        self.write('file.txt', 'base\n')
        self.write('.gitignore', 'node_modules/\nignored.txt\n')
        self.write('web/check.test.ts', '// runner fixture\n')
        self.git('add', '.')
        self.git('commit', '-qm', 'base')
        self.base = self.git('rev-parse', 'HEAD').decode().strip()
        self.manifest = {'format': 1, 'upstream': str(self.target),
                         'base': self.base, 'patches': [],
                         'application': {'ref': self.base, 'branch': f'upstream-{self.base[:9]}'}}
        self.add_patch('first', 'base', 'first')
        self.add_patch('second', 'first', 'second', ['first'])
        self.save()
        self.maintenance_git('init', '-q')

    def git(self, *args):
        return subprocess.check_output(['git', '-C', str(self.target), *args],
                                       stderr=subprocess.PIPE)

    def write(self, path, text):
        destination = self.target / path
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_text(text)

    def add_patch(self, name, before, after, dependencies=None):
        directory = self.root / 'patches' / name
        directory.mkdir(parents=True)
        (directory / 'README.md').write_text(f'# {name}\n')
        (directory / 'changes.patch').write_text(
            'diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n'
            f'@@ -1 +1 @@\n-{before}\n+{after}\n')
        entry = {'id': name, 'status': 'keep', 'depends_on': dependencies or [],
                 'patch': f'patches/{name}/changes.patch', 'paths': ['file.txt'],
                 'tests': f'patches/{name}/tests.json'}
        self.manifest['patches'].append(entry)
        self.set_checks(entry, [{'name': 'content contract', 'tier': 'focused', 'cwd': '.',
                                'requires': ['file.txt'], 'command': [sys.executable, '-c',
                                "from pathlib import Path; assert Path('file.txt').read_text() == 'second\\n'"]}])
        return entry

    def set_checks(self, entry, checks):
        (self.root / entry['tests']).write_text(json.dumps({'checks': checks}))

    def save(self):
        (self.root / 'manifest.json').write_text(json.dumps(self.manifest))

    def cli(self, action, *args, target=None, root=None):
        return subprocess.run([sys.executable, str(CLI), '--root', str(root or self.root), action,
                               '--target', str(target or self.target), *args],
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)

    def assert_clean(self):
        self.assertEqual(self.git('status', '--porcelain'), b'')
        self.assertEqual((self.target / 'file.txt').read_text(), 'base\n')

    def maintenance_git(self, *args):
        return subprocess.check_output(['git', '-C', str(self.root), *args], stderr=subprocess.PIPE)

    def branch_fixture(self):
        self.write('.github/workflows/release.yml',
                   'name: QuantumNous new-api release\non: [push, workflow_dispatch]\n')
        self.git('add', '.github')
        self.git('commit', '-qm', 'upstream workflows')
        self.base = self.git('rev-parse', 'HEAD').decode().strip()
        self.manifest.update(base=self.base,
                             application={'ref': self.base, 'branch': f'upstream-{self.base[:9]}'})
        self.save()
        self.maintenance_git('init', '-q')
        self.maintenance_git('config', 'user.name', 'Patchset test')
        self.maintenance_git('config', 'user.email', 'test@example.invalid')
        self.maintenance_git('config', 'commit.gpgsign', 'false')
        self.maintenance_git('add', '.')
        self.maintenance_git('commit', '-qm', 'maintenance catalog')
        return self.target.parent / 'version-worktree'

    def test_branch_has_ordered_implementation_commits_and_inactive_upstream_workflows(self):
        generated = self.branch_fixture()
        maintenance_head = self.maintenance_git('rev-parse', 'HEAD')
        (self.root / 'human.txt').write_text('staged maintenance work\n')
        self.maintenance_git('add', 'human.txt')
        maintenance_status = self.maintenance_git('status', '--porcelain')
        result = self.cli('branch', target=generated)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((generated / '.git').is_file())
        name = self.manifest['application']['branch']
        self.assertEqual(patchset.git(generated, 'symbolic-ref', '--short', 'HEAD').decode().strip(), name)
        commits = self.maintenance_git('rev-list', '--reverse', f'{self.base}..{name}').decode().splitlines()
        self.assertEqual(len(commits), 3)
        self.assertEqual(self.maintenance_git('show', f'{commits[0]}:file.txt'), b'first\n')
        self.assertEqual(self.maintenance_git('show', f'{commits[1]}:file.txt'), b'second\n')
        self.assertEqual((generated / 'file.txt').read_text(), 'second\n')
        self.assertEqual((generated / 'AGENTS.md').read_bytes(),
                         (self.root / 'tools/application-AGENTS.md').read_bytes())
        self.assertFalse((generated / '.github/workflows').exists())
        self.assertEqual((generated / '.github/upstream-workflows/release.yml').read_bytes(),
                         self.git('show', 'HEAD:.github/workflows/release.yml'))
        message = self.maintenance_git('show', '-s', '--format=%B', name).decode()
        self.assertIn(f'Upstream-Base: {self.base}', message)
        self.assertIn('Patchset-Source:', message)
        self.assertEqual(patchset.git(generated, 'status', '--porcelain'), b'')
        self.assertEqual(self.maintenance_git('rev-parse', 'HEAD'), maintenance_head)
        self.assertEqual(self.maintenance_git('status', '--porcelain'), maintenance_status)
        self.assert_clean()

    def test_branch_rebuilds_same_commits_and_reuses_only_matching_existing_branch(self):
        generated = self.branch_fixture()
        self.assertEqual(self.cli('branch', target=generated).returncode, 0)
        head = patchset.git(generated, 'rev-parse', 'HEAD')
        for action in ('branch', 'verify', 'test'):
            result = self.cli(action, target=generated)
            self.assertEqual(result.returncode, 0, result.stderr)
        other_target = generated.with_name('already-checked-out')
        result = self.cli('branch', target=other_target)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('worktree', result.stderr)
        self.assertFalse(other_target.exists())
        independent = self.root.with_name('fresh-maintenance')
        subprocess.run(['git', 'clone', '-q', '--no-hardlinks', str(self.root), str(independent)], check=True)
        fresh_target = generated.with_name('fresh-version-worktree')
        result = subprocess.run([sys.executable, str(CLI), '--root', str(independent), 'branch',
                                 '--target', str(fresh_target), '--source', str(self.target)],
                                text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(patchset.git(fresh_target, 'rev-parse', 'HEAD'), head)
        self.assertEqual(patchset.git(generated, 'rev-parse', 'HEAD'), head)

    def test_branch_preserves_user_index_worktree_untracked_files_and_commits(self):
        generated = self.branch_fixture()
        self.assertEqual(self.cli('branch', target=generated).returncode, 0)
        (generated / 'file.txt').write_text('user staged\n')
        patchset.git(generated, 'add', 'file.txt')
        (generated / 'file.txt').write_text('user unstaged\n')
        (generated / 'untracked.txt').write_text('user untracked\n')
        status = patchset.git(generated, 'status', '--porcelain')
        head = patchset.git(generated, 'rev-parse', 'HEAD')
        for action in ('branch', 'verify'):
            result = self.cli(action, target=generated)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('user changes', result.stderr)
        self.assertEqual(patchset.git(generated, 'status', '--porcelain'), status)
        self.assertEqual(patchset.git(generated, 'show', ':file.txt'), b'user staged\n')
        self.assertEqual((generated / 'file.txt').read_text(), 'user unstaged\n')
        self.assertEqual((generated / 'untracked.txt').read_text(), 'user untracked\n')
        self.assertEqual(patchset.git(generated, 'rev-parse', 'HEAD'), head)

    def test_branch_collision_and_old_detached_checkout_are_never_overwritten(self):
        generated = self.branch_fixture()
        name = self.manifest['application']['branch']
        self.maintenance_git('branch', name)
        before = self.maintenance_git('rev-parse', name)
        result = self.cli('branch', target=generated)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Existing branch', result.stderr)
        self.assertEqual(self.maintenance_git('rev-parse', name), before)
        self.assertFalse(generated.exists())
        self.assertEqual(self.cli('apply').returncode, 0)
        status = self.git('status', '--porcelain')
        result = self.cli('branch')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('not an application worktree', result.stderr)
        self.assertEqual(self.git('status', '--porcelain'), status)

    def test_branch_rejects_unrecorded_user_commit_even_with_unchanged_tree(self):
        generated = self.branch_fixture()
        self.assertEqual(self.cli('branch', target=generated).returncode, 0)
        patchset.git(generated, 'commit', '--allow-empty', '-qm', 'user commit')
        head = patchset.git(generated, 'rev-parse', 'HEAD')
        for action in ('branch', 'verify'):
            result = self.cli(action, target=generated)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('Application commit differs', result.stderr)
        self.assertEqual(patchset.git(generated, 'rev-parse', 'HEAD'), head)
        self.assertEqual(patchset.git(generated, 'status', '--porcelain'), b'')

    def test_branch_refuses_archive_collision_and_partial_patch_selection(self):
        generated = self.branch_fixture()
        result = self.cli('branch', '--only', 'first', target=generated)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('complete manifest', result.stderr)
        self.write('.github/upstream-workflows/release.yml', 'existing upstream file\n')
        self.git('add', '.github')
        self.git('commit', '-qm', 'existing archive path')
        self.base = self.git('rev-parse', 'HEAD').decode().strip()
        self.manifest.update(base=self.base,
                             application={'ref': self.base, 'branch': f'upstream-{self.base[:9]}'})
        self.save()
        result = self.cli('branch', target=generated)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('archive collision', result.stderr)
        self.assertFalse(generated.exists())
        self.assertEqual(self.maintenance_git('branch', '--list', self.manifest['application']['branch']), b'')
        self.assertEqual((self.target / '.github/upstream-workflows/release.yml').read_text(),
                         'existing upstream file\n')

    def test_exact_release_ref_is_verified_and_misleading_version_names_are_refused(self):
        generated = self.branch_fixture()
        self.git('tag', '-a', 'v1.2.3-rc.1', '-m', 'release fixture')
        self.manifest['application'] = {'ref': 'refs/tags/v1.2.3-rc.1', 'branch': 'v1.2.3-rc.1'}
        self.save()
        result = self.cli('branch', target=generated)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.git('commit', '--allow-empty', '-qm', 'not selected release')
        self.git('tag', 'v1.2.4')
        self.manifest['application'] = {'ref': 'refs/tags/v1.2.4', 'branch': 'v1.2.4'}
        self.save()
        result = self.cli('branch', target=generated.with_name('wrong-release'))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('does not resolve to fixed base', result.stderr)
        self.assertEqual(self.maintenance_git('branch', '--list', 'v1.2.4'), b'')
        self.manifest['application'] = {'ref': self.base, 'branch': 'v1.2.3-rc.1'}
        self.save()
        result = self.cli('branch', target=generated.with_name('misnamed'))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Application branch must match', result.stderr)

    def release_fixture(self, annotated=True):
        generated = self.branch_fixture()
        tag = 'v1.2.3-rc.1'
        self.git('tag', *(['-a', tag, '-m', 'release fixture'] if annotated else [tag]))
        self.git('tag', 'v9.9.9')
        self.manifest['application'] = {'ref': f'refs/tags/{tag}', 'branch': tag}
        self.save()
        self.maintenance_git('add', 'manifest.json')
        self.maintenance_git('commit', '-qm', 'select release fixture')
        return generated, tag

    def release_lifecycle(self, annotated):
        generated, tag = self.release_fixture(annotated)
        ref = f'refs/tags/{tag}'
        tag_object = self.git('rev-parse', ref)
        self.assertEqual(self.maintenance_git('tag', '--list'), b'')
        result = self.cli('branch', target=generated)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.maintenance_git('rev-parse', ref), tag_object)
        self.assertEqual(self.maintenance_git('tag', '--list').decode().splitlines(), [tag])
        head = patchset.git(generated, 'rev-parse', 'HEAD')
        for action in ('verify', 'branch', 'test'):
            with self.subTest(action=action):
                result = self.cli(action, target=generated)
                self.assertEqual(result.returncode, 0, result.stderr)
                if action == 'test':
                    self.assertEqual(result.stdout.count('RUN: content contract'), 1)
        self.assertEqual(patchset.git(generated, 'rev-parse', 'HEAD'), head)
        self.assertEqual(patchset.git(generated, 'status', '--porcelain'), b'')
        self.assertEqual(self.maintenance_git('status', '--porcelain'), b'')

        fresh = self.root.with_name('release-maintenance-clone')
        subprocess.run(['git', 'clone', '-q', '--no-tags', '--no-hardlinks', str(self.root), str(fresh)],
                       check=True)
        self.assertEqual(patchset.git(fresh, 'tag', '--list'), b'')
        rebuilt = generated.with_name('rebuilt-release')
        result = self.cli('branch', '--source', str(self.target), target=rebuilt, root=fresh)
        self.assertEqual(result.returncode, 0, result.stderr)
        for action in ('branch', 'verify', 'test'):
            result = self.cli(action, target=rebuilt, root=fresh)
            self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(patchset.git(rebuilt, 'rev-parse', 'HEAD'), head)
        self.assertEqual(patchset.git(rebuilt, 'rev-parse', ref), tag_object)

    def test_annotated_release_branch_can_be_reused_verified_and_tested(self):
        self.release_lifecycle(annotated=True)

    def test_lightweight_release_branch_can_be_reused_verified_and_tested(self):
        self.release_lifecycle(annotated=False)

    def test_release_tag_conflict_preserves_existing_refs_and_work(self):
        generated, tag = self.release_fixture()
        self.maintenance_git('tag', '-a', tag, '-m', 'local tag to preserve')
        (self.root / 'human.txt').write_text('staged maintenance work\n')
        self.maintenance_git('add', 'human.txt')
        refs = self.maintenance_git('show-ref')
        status = self.maintenance_git('status', '--porcelain')
        result = self.cli('branch', target=generated)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('would clobber existing tag', result.stderr)
        self.assertEqual(self.maintenance_git('show-ref'), refs)
        self.assertEqual(self.maintenance_git('status', '--porcelain'), status)
        self.assertFalse(generated.exists())

    def test_concurrent_release_tag_creation_is_not_overwritten(self):
        generated, tag = self.release_fixture()
        original_git = patchset.git
        concurrent_refs = []

        def concurrent_tag(target, *args, **kwargs):
            if target == self.root and args[0] == 'fetch':
                self.maintenance_git('tag', tag)
                concurrent_refs.append(self.maintenance_git('show-ref'))
            return original_git(target, *args, **kwargs)

        with mock.patch.object(patchset, 'git', side_effect=concurrent_tag):
            with self.assertRaisesRegex(patchset.Failure, 'would clobber existing tag'):
                patchset.branch(self.root, generated, self.manifest, self.manifest['patches'])
        self.assertEqual(len(concurrent_refs), 1)
        self.assertEqual(self.maintenance_git('show-ref'), concurrent_refs[0])
        self.assertFalse(generated.exists())

    def test_release_verification_rejects_missing_or_moved_tag(self):
        generated, tag = self.release_fixture()
        result = self.cli('branch', target=generated)
        self.assertEqual(result.returncode, 0, result.stderr)
        head = patchset.git(generated, 'rev-parse', 'HEAD')
        ref = f'refs/tags/{tag}'
        self.maintenance_git('update-ref', '-d', ref, self.git('rev-parse', ref).decode().strip())
        for moved in (False, True):
            if moved:
                self.maintenance_git('tag', tag)
            for action in ('verify', 'branch', 'test'):
                with self.subTest(moved=moved, action=action):
                    result = self.cli(action, target=generated)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn('does not resolve to fixed base' if moved else 'rev-parse --verify',
                                  result.stderr)
                    self.assertNotIn('RUN:', result.stdout)
        self.assertEqual(patchset.git(generated, 'rev-parse', 'HEAD'), head)
        self.assertEqual(patchset.git(generated, 'status', '--porcelain'), b'')

    def test_branch_later_failure_creates_no_branch_or_partial_worktree(self):
        generated = self.branch_fixture()
        path = self.root / self.manifest['patches'][1]['patch']
        path.write_text(path.read_text().replace('-first\n', '-conflict\n'))
        result = self.cli('branch', target=generated)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Preflight stopped at second', result.stderr)
        self.assertFalse(generated.exists())
        self.assertEqual(self.maintenance_git('branch', '--list', self.manifest['application']['branch']), b'')
        self.assert_clean()

    def test_new_patch_revision_keeps_previous_version_branch_intact(self):
        generated = self.branch_fixture()
        second = self.manifest['patches'].pop()
        self.save()
        self.assertEqual(self.cli('branch', target=generated).returncode, 0)
        old_name = self.manifest['application']['branch']
        old_head = self.maintenance_git('rev-parse', old_name)
        self.manifest['patches'].append(second)
        self.manifest['application']['branch'] += '-patchset-2'
        self.save()
        updated = generated.with_name('second-patch-version')
        result = self.cli('branch', target=updated)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((updated / 'file.txt').read_text(), 'second\n')
        self.assertEqual((generated / 'file.txt').read_text(), 'first\n')
        self.assertEqual(self.maintenance_git('rev-parse', old_name), old_head)
        self.assertEqual(self.cli('test', target=updated).returncode, 0)

    def test_selection_includes_transitive_dependencies_once_in_manifest_order(self):
        self.add_patch('third', 'second', 'third', ['second'])
        chosen = patchset.select(self.manifest, ['third', 'first', 'third'])
        self.assertEqual([p['id'] for p in chosen], ['first', 'second', 'third'])
        self.manifest['patches'][2]['status'] = 'local'
        self.assertEqual(patchset.select(self.manifest, statuses=['local']), chosen)
        with self.assertRaisesRegex(patchset.Failure, 'Unknown patch ids'):
            patchset.select(self.manifest, ['typo'])
        self.save()
        result = self.cli('apply', '--only', 'third')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.target / 'file.txt').read_text(), 'third\n')

    def test_check_preserves_target_and_apply_stages_complete_selection(self):
        result = self.cli('check', '--only', 'second')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_clean()
        result = self.cli('apply', '--only', 'second')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.target / 'file.txt').read_text(), 'second\n')
        self.assertEqual(self.git('show', ':file.txt'), b'second\n')
        self.assertEqual(self.git('rev-parse', 'HEAD').decode().strip(), self.base)
        duplicate = self.cli('apply')
        self.assertNotEqual(duplicate.returncode, 0)
        self.assertIn('Repeated application is refused', duplicate.stderr)
        self.assertEqual((self.target / 'file.txt').read_text(), 'second\n')

    def test_later_conflict_does_not_leave_earlier_changes(self):
        path = self.root / self.manifest['patches'][1]['patch']
        path.write_text(path.read_text().replace('-first\n', '-not-the-base\n'))
        for action in ('check', 'apply'):
            with self.subTest(action=action):
                result = self.cli(action)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('Preflight stopped at second', result.stderr)
                self.assert_clean()

    def test_dirty_index_worktree_and_untracked_files_are_preserved(self):
        self.write('file.txt', 'staged user work\n')
        self.git('add', 'file.txt')
        self.write('file.txt', 'unstaged user work\n')
        self.write('untracked.txt', 'untracked user work\n')
        before = self.git('status', '--porcelain')
        result = self.cli('apply')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Target has changes', result.stderr)
        self.assertEqual(self.git('status', '--porcelain'), before)
        self.assertEqual(self.git('show', ':file.txt'), b'staged user work\n')
        self.assertEqual((self.target / 'file.txt').read_text(), 'unstaged user work\n')
        self.assertEqual((self.target / 'untracked.txt').read_text(), 'untracked user work\n')

    def test_wrong_base_is_refused_without_switching_head(self):
        self.git('commit', '--allow-empty', '-qm', 'different base')
        head = self.git('rev-parse', 'HEAD')
        result = self.cli('apply')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('differs from fixed base', result.stderr)
        self.assertEqual(self.git('rev-parse', 'HEAD'), head)
        self.assert_clean()

    def test_inventory_mismatch_and_duplicate_ids_are_rejected(self):
        self.manifest['patches'][1]['paths'] = ['other.txt']
        self.save()
        result = self.cli('apply')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Payload path inventory differs', result.stderr)
        self.assert_clean()
        self.manifest['patches'].append(copy.deepcopy(self.manifest['patches'][0]))
        self.save()
        with self.assertRaisesRegex(patchset.Failure, 'Duplicate id'):
            patchset.load_manifest(self.root)

    def test_dependency_order_and_empty_contract_are_rejected(self):
        self.manifest['patches'].reverse()
        self.save()
        with self.assertRaisesRegex(patchset.Failure, 'out-of-order dependency'):
            patchset.load_manifest(self.root)
        self.manifest['patches'].reverse()
        self.set_checks(self.manifest['patches'][0], [])
        self.save()
        with self.assertRaisesRegex(patchset.Failure, 'Empty test contract'):
            patchset.load_manifest(self.root)

    def test_skip_requires_documented_replacement_in_selected_target(self):
        entry = self.manifest['patches'][0]
        entry.update(status='upstream', patch=None, paths=[],
                     replacement={'commit': self.base, 'evidence': 'reviewed base helper'})
        self.save()
        with contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(patchset.applicable(self.target, [entry]), [])
        entry['replacement']['evidence'] = ''
        self.save()
        with self.assertRaisesRegex(patchset.Failure, 'Replacement evidence missing'):
            patchset.load_manifest(self.root)
        entry['replacement'].update(commit='0' * 40, evidence='absent commit')
        with self.assertRaises(patchset.Failure):
            patchset.applicable(self.target, [entry])
        self.assert_clean()

    def test_generate_is_independent_repeatable_and_protects_edits(self):
        generated = self.target.parent / 'generated'
        result = self.cli('generate', '--source', str(self.target), target=generated)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((generated / '.git').is_dir())
        self.assertEqual((generated / 'file.txt').read_text(), 'second\n')
        for action in ('generate', 'verify', 'test'):
            result = self.cli(action, target=generated)
            self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_clean()
        (generated / 'file.txt').write_text('user edit\n')
        result = self.cli('generate', target=generated)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((generated / 'file.txt').read_text(), 'user edit\n')
        subprocess.run(['git', '-C', str(generated), 'add', 'file.txt'], check=True)
        result = self.cli('generate', target=generated)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('differs from manifest tree', result.stderr)
        self.assertEqual((generated / 'file.txt').read_text(), 'user edit\n')

    def test_failed_generation_does_not_expose_partial_target(self):
        path = self.root / self.manifest['patches'][1]['patch']
        path.write_text(path.read_text().replace('-first\n', '-conflict\n'))
        generated = self.target.parent / 'generated'
        result = self.cli('generate', target=generated)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Preflight stopped at second', result.stderr)
        self.assertFalse(generated.exists())
        self.assert_clean()

    def test_verify_refuses_extra_untracked_work(self):
        self.assertEqual(self.cli('apply').returncode, 0)
        self.write('untracked.txt', 'user work\n')
        result = self.cli('verify')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('untracked files', result.stderr)
        self.assertEqual((self.target / 'untracked.txt').read_text(), 'user work\n')

    def test_apply_preserves_ignored_file_collision_without_partial_changes(self):
        entry = self.manifest['patches'][1]
        entry['paths'].append('ignored.txt')
        with (self.root / entry['patch']).open('a') as patch:
            patch.write('diff --git a/ignored.txt b/ignored.txt\nnew file mode 100644\n'
                        '--- /dev/null\n+++ b/ignored.txt\n@@ -0,0 +1 @@\n+new file\n')
        self.save()
        self.write('ignored.txt', 'user data\n')
        result = self.cli('apply')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.target / 'ignored.txt').read_text(), 'user data\n')
        self.assert_clean()

    def test_command_uses_application_cwd_deduplicates_and_propagates_failure(self):
        check = {'name': 'cwd contract', 'tier': 'focused', 'cwd': 'web',
                 'requires': ['web/check.test.ts'], 'command': [sys.executable, '-c',
                 "from pathlib import Path; assert Path('check.test.ts').is_file(); print('executed once')"]}
        for entry in self.manifest['patches']:
            self.set_checks(entry, [check])
        self.assertEqual(self.cli('apply').returncode, 0)
        result = self.cli('test')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.count('executed once'), 1)
        check['command'] = [sys.executable, '-c', 'raise SystemExit(9)']
        self.set_checks(self.manifest['patches'][0], [check])
        result = self.cli('test')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('exit status 9', result.stderr)
        result = self.cli('test', '--tier', 'project')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('No checks in tier', result.stderr)
        check['requires'] = ['missing.go']
        self.set_checks(self.manifest['patches'][0], [check])
        result = self.cli('test')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('entry missing', result.stderr)

    def test_operation_lock_blocks_another_process_and_releases_after_failure(self):
        common = Path(self.maintenance_git('rev-parse', '--absolute-git-dir').decode().strip())
        with self.assertRaisesRegex(RuntimeError, 'interrupted'):
            with patchset.operation_lock(self.root, self.target):
                original = (common / 'patchset.lock').read_bytes()
                result = self.cli('apply')
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('operation locked', result.stderr)
                self.assertEqual((common / 'patchset.lock').read_bytes(), original)
                self.assert_clean()
                raise RuntimeError('interrupted')
        self.assertFalse((common / 'patchset.lock').exists())
        self.assertEqual(self.cli('check').returncode, 0)

    def test_creation_lock_protects_same_target_from_independent_repositories(self):
        destination = self.target.with_name('contended')
        lock = destination.parent / '.contended.patchset.lock'
        with patchset.operation_lock(self.root, destination, creating=True):
            with self.assertRaisesRegex(patchset.Failure, 'operation locked'):
                with patchset.operation_lock(self.target, destination, creating=True):
                    self.fail('Competing creator acquired a live target lock')
            self.assertTrue(lock.exists())
            self.assertFalse(destination.exists())
        self.assertFalse(lock.exists())
        self.assert_clean()

    def test_upstream_guidance_is_preserved_and_template_changes_need_new_revision(self):
        generated = self.branch_fixture()
        self.write('AGENTS.md', '# Upstream instructions\n')
        self.git('add', 'AGENTS.md')
        self.git('commit', '-qm', 'upstream guidance')
        self.manifest['base'] = self.git('rev-parse', 'HEAD').decode().strip()
        self.manifest['application'] = {'ref': self.manifest['base'],
                                        'branch': f'upstream-{self.manifest["base"][:9]}'}
        self.save()
        result = self.cli('branch', target=generated)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Upstream AGENTS.md exists', result.stderr)
        self.assertFalse(generated.exists())
        self.assertEqual((self.target / 'AGENTS.md').read_text(), '# Upstream instructions\n')

        # Use a separate normal base; existing snapshot remains immutable.
        self.manifest['base'] = self.base
        self.manifest['application'] = {'ref': self.base, 'branch': f'upstream-{self.base[:9]}'}
        self.save()
        self.assertEqual(self.cli('branch', target=generated).returncode, 0)
        before = patchset.git(generated, 'rev-parse', 'HEAD')
        (self.root / 'tools/application-AGENTS.md').write_text('# Revised instructions\n')
        result = self.cli('branch', target=generated)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('new revision', result.stderr)
        self.assertEqual(patchset.git(generated, 'rev-parse', 'HEAD'), before)

    def test_selected_contract_can_run_on_verified_full_application_branch(self):
        generated = self.branch_fixture()
        self.assertEqual(self.cli('branch', target=generated).returncode, 0)
        result = self.cli('test', '--only', 'first', target=generated)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_ci_requires_new_snapshot_name_for_generation_changes_only(self):
        (self.root / 'tools/patchset.py').write_text('# generator fixture\n')
        self.branch_fixture()
        previous = self.maintenance_git('rev-parse', 'HEAD').decode().strip()
        self.assertFalse(ci_tools.release_needed(self.root, self.manifest, previous))
        (self.root / 'patches/first/README.md').write_text('# More documentation\n')
        self.set_checks(self.manifest['patches'][0], [{
            'name': 'updated contract', 'tier': 'focused', 'cwd': '.', 'requires': ['file.txt'],
            'command': [sys.executable, '-c', 'pass']}])
        self.assertFalse(ci_tools.release_needed(self.root, self.manifest, previous))
        (self.root / 'tools/application-AGENTS.md').write_text('# New guidance\n')
        with self.assertRaisesRegex(patchset.Failure, 'choose a new application.branch'):
            ci_tools.release_needed(self.root, self.manifest, previous)
        self.manifest['application']['branch'] += '-patchset-2'
        self.assertTrue(ci_tools.release_needed(self.root, self.manifest, previous))

    def test_release_asset_policy_accepts_only_complete_narrow_outputs(self):
        assets = self.target.with_name('assets')
        assets.mkdir()
        expected = []
        for system in ('linux', 'windows', 'darwin', 'freebsd'):
            for arch in ('amd64', 'arm64'):
                extension = 'zip' if system == 'windows' else 'tar.gz'
                expected.append(f'sing-box-1.14.4-{system}-{arch}.{extension}')
                if system == 'linux':
                    expected.append(f'sing-box-1.14.4-linux-{arch}-musl.tar.gz')
        expected += ['SFA-1.14.4-arm64-v8a.apk', 'SFA-version-metadata.json']
        for name in expected:
            (assets / name).write_bytes(b'fixture')
        (assets / 'SFA-version-metadata.json').write_text('{"version_code":747,"version_name":"1.14.4"}')
        with contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(check_assets.check(assets, '1.14.4'), sorted(expected))
        forbidden = assets / 'sing-box-1.14.4-linux-amd64-glibc.tar.gz'
        forbidden.write_bytes(b'forbidden')
        with self.assertRaisesRegex(ValueError, 'extra='):
            check_assets.check(assets, '1.14.4')
        forbidden.unlink()
        (assets / expected[0]).unlink()
        with self.assertRaisesRegex(ValueError, 'missing='):
            check_assets.check(assets, '1.14.4')

    def test_paths_cannot_escape_checkout(self):
        for path in ('../outside', '/absolute'):
            with self.assertRaises(patchset.Failure):
                patchset.inside(self.target, path)
        (self.target / 'escape').symlink_to(self.root, target_is_directory=True)
        with self.assertRaises(patchset.Failure):
            patchset.inside(self.target, 'escape/manifest.json')


if __name__ == '__main__':
    unittest.main()
