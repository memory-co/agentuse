#!/usr/bin/env python3
"""Build a reproducible standalone distribution and SHA-256 checksum."""
import argparse
import gzip
import hashlib
from pathlib import Path
import tarfile


def build(output):
    root = Path(__file__).resolve().parent
    output.mkdir(parents=True, exist_ok=True)
    target = output / 'harnessctl-0.1.0-linux.tar.gz'
    files = [root / name for name in ('README.md', 'LICENSE', 'package.py')]
    for directory in ('lib', 'harness'):
        files += sorted(p for p in (root / directory).rglob('*')
                        if p.is_file() and '__pycache__' not in p.parts and p.suffix != '.pyc')
    with target.open('wb') as raw:
        with gzip.GzipFile(filename='', fileobj=raw, mode='wb', mtime=0) as zipped:
            with tarfile.open(fileobj=zipped, mode='w') as archive:
                for path in files:
                    if path.is_symlink():
                        raise ValueError('distribution may not contain symlinks: ' + str(path))
                    info = archive.gettarinfo(str(path), arcname='harnessctl/' + path.relative_to(root).as_posix())
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
    print(build(parser.parse_args().output_dir))
