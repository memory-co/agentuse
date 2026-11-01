#!/usr/bin/env python3
"""Build independently distributable adapter archives and SHA-256 checksums."""
import argparse
import gzip
import hashlib
import json
from pathlib import Path
import tarfile

ADAPTERS = ('codex', 'claudecode')


def build(output, adapter):
    if adapter not in ADAPTERS:
        raise ValueError('unknown adapter: ' + adapter)
    root = Path(__file__).resolve().parent / 'harness' / adapter
    manifest = json.loads((root / 'manifest.json').read_text())
    output.mkdir(parents=True, exist_ok=True)
    target = output / f"harnessctl-{adapter}-{manifest['version']}-linux.tar.gz"
    files = sorted(p for p in root.rglob('*')
                   if p.is_file() and '__pycache__' not in p.parts and p.suffix != '.pyc')
    with target.open('wb') as raw:
        with gzip.GzipFile(filename='', fileobj=raw, mode='wb', mtime=0) as zipped:
            with tarfile.open(fileobj=zipped, mode='w') as archive:
                for path in files:
                    if path.is_symlink():
                        raise ValueError('distribution may not contain symlinks: ' + str(path))
                    info = archive.gettarinfo(str(path), arcname=adapter + '/' + path.relative_to(root).as_posix())
                    info.uid = info.gid = info.mtime = 0
                    info.uname = info.gname = ''
                    with path.open('rb') as source:
                        archive.addfile(info, source)
    digest = hashlib.sha256(target.read_bytes()).hexdigest()
    target.with_suffix(target.suffix + '.sha256').write_text(digest + '  ' + target.name + '\n')
    return target


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output-dir', type=Path, default=Path(__file__).resolve().parent / 'dist')
    parser.add_argument('--adapter', choices=ADAPTERS, help='omit to build each adapter separately')
    args = parser.parse_args()
    for adapter in (args.adapter,) if args.adapter else ADAPTERS:
        print(build(args.output_dir, adapter))
