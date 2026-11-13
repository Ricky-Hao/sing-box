#!/usr/bin/env python3
"""Check the complete release asset policy before publishing."""
import argparse
import json
from pathlib import Path


def check(directory, version):
    expected = {f'sing-box-{version}-{system}-{arch}.{extension}'
                for system, extension in [('linux', 'tar.gz'), ('windows', 'zip'),
                                          ('darwin', 'tar.gz'), ('freebsd', 'tar.gz')]
                for arch in ('amd64', 'arm64')}
    expected |= {f'sing-box-{version}-linux-{arch}-musl.tar.gz' for arch in ('amd64', 'arm64')}
    files = {p.name for p in directory.iterdir() if p.is_file()}
    apks = {name for name in files if name.startswith('SFA-') and 'arm64-v8a' in name and name.endswith('.apk')}
    if len(apks) != 1:
        raise ValueError(f'Expected one arm64-v8a SFA APK, found {sorted(apks)}')
    expected |= apks | {'SFA-version-metadata.json'}
    if files != expected or any(not p.is_file() for p in directory.iterdir()):
        raise ValueError(f'Release assets differ: missing={sorted(expected-files)}, extra={sorted(files-expected)}')
    if any((directory / name).stat().st_size == 0 for name in files):
        raise ValueError('Empty release asset')
    metadata = json.loads((directory / 'SFA-version-metadata.json').read_text())
    if not isinstance(metadata['version_code'], int) or not metadata['version_name']:
        raise ValueError('Invalid SFA version metadata')
    print('\n'.join(sorted(files)))
    return sorted(files)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory', type=Path)
    parser.add_argument('--version', required=True)
    args = parser.parse_args()
    check(args.directory, args.version)
