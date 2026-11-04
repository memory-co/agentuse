"""Immutable external session resources; Python standard library only."""
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import tempfile


def scalar(value):
    value = value.strip()
    if value.startswith('"'):
        return json.loads(value)
    if value.startswith("'"):
        if not value.endswith("'"):
            raise ValueError('unterminated skill metadata string')
        return value[1:-1].replace("''", "'")
    if not value or value.lower() in ('null', 'true', 'false', '~') or re.fullmatch(r'[-+]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][-+]?[0-9]+)?', value) or value[0] in '[{!&*':
        raise ValueError('skill metadata must be a string')
    return value.split(' #', 1)[0].strip()


def metadata(path):
    lines = path.read_text(encoding='utf8').splitlines()
    if not lines or lines[0].strip() != '---':
        raise ValueError('missing skill frontmatter: ' + str(path))
    try:
        end = next(i for i in range(1, len(lines)) if lines[i].strip() == '---')
    except StopIteration:
        raise ValueError('unterminated skill frontmatter: ' + str(path))
    result = {}
    for i in range(1, end):
        match = re.match(r'^(name|description):\s*(.*?)\s*$', lines[i])
        if not match:
            continue
        key, value = match.groups()
        if key in result:
            raise ValueError('duplicate skill metadata: ' + key)
        if value in ('|', '>', '|-', '>-', '|+', '>+'):
            block = []
            for line in lines[i+1:end]:
                if line and not line[0].isspace():
                    break
                block.append(line.strip())
            value = ('\n' if value.startswith('|') else ' ').join(block).strip()
        else:
            continuation = []
            for line in lines[i+1:end]:
                if line and not line[0].isspace():
                    break
                if line.strip() and not line.lstrip().startswith('#'):
                    continuation.append(line.strip())
            value = scalar(' '.join([value]+continuation))
        result[key] = value
    name = result.get('name', '')
    if not isinstance(name, str) or not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_-]{0,63}', name):
        raise ValueError('invalid skill name: ' + str(path))
    if not isinstance(result.get('description'), str) or not result['description'].strip():
        raise ValueError('missing skill description: ' + str(path))
    return result


def copy_skill(source, destination, boundary=None, ancestors=()):
    source = source.resolve(strict=True)
    boundary = boundary or source
    if source != boundary and boundary not in source.parents:
        raise ValueError('skill symlink escapes its directory: ' + str(source))
    mode = source.stat().st_mode
    if stat.S_ISDIR(mode):
        if source in ancestors:
            raise ValueError('cyclic skill symlink: ' + str(source))
        destination.mkdir(mode=0o700)
        for child in sorted(source.iterdir()):
            copy_skill(child, destination/child.name, boundary, ancestors+(source,))
    elif stat.S_ISREG(mode):
        destination.write_bytes(source.read_bytes())
        destination.chmod(0o700 if mode & stat.S_IXUSR else 0o600)
    else:
        raise ValueError('skill resource is not a regular file/directory: ' + str(source))


def inventory(root):
    files = {}
    for path in sorted(root.rglob('*')):
        if path.is_symlink():
            raise ValueError('resource snapshot contains a symlink')
        if path.is_file():
            files[path.relative_to(root).as_posix()] = dict(
                sha256=hashlib.sha256(path.read_bytes()).hexdigest(), executable=bool(path.stat().st_mode & stat.S_IXUSR))
        elif not path.is_dir():
            raise ValueError('invalid snapshot entry')
    return files


def fingerprint(files):
    return hashlib.sha256(json.dumps(files, sort_keys=True).encode()).hexdigest()


def fsync_tree(root):
    for path in root.rglob('*'):
        if path.is_file():
            with path.open('rb') as file:
                os.fsync(file.fileno())
    for path in sorted([p for p in root.rglob('*') if p.is_dir()], reverse=True)+[root]:
        fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)


def prepare(args, destination, cwd, events):
    if (args.agents_md is not None or args.skills_dir is not None) and (events.parent == cwd or cwd in events.parent.parents):
        raise ValueError('event address with resources must be outside cwd')
    destination.mkdir(mode=0o700)
    result = dict(agents=None, roots=[], skills=[])
    if args.agents_md is not None:
        source = Path(args.agents_md).expanduser().resolve(strict=True)
        content = source.read_bytes()
        content.decode('utf8')
        (destination/'agents.md').write_bytes(content)
        result['agents'] = dict(source=str(source), sha256=hashlib.sha256(content).hexdigest())
    seen_roots, names = set(), set()
    for value in args.skills_dir or []:
        source = Path(value).expanduser().resolve(strict=True)
        if not source.is_dir():
            raise ValueError('skills root must be a directory: ' + str(source))
        if source in seen_roots:
            continue
        seen_roots.add(source)
        children = [p for p in sorted(source.iterdir()) if p.is_dir()]
        if not children:
            raise ValueError('empty skills root: ' + str(source))
        root_files = {}
        for child in children:
            # Snapshot first: both metadata and hashes describe exactly the frozen copy.
            temporary = destination/('skill-'+str(len(result['skills'])))
            copy_skill(child, temporary)
            info = metadata(temporary/'SKILL.md')
            key = info['name'].casefold()
            if key in names:
                raise ValueError('duplicate skill name: '+info['name']+' from '+str(child))
            names.add(key)
            skills = destination/'skills'
            skills.mkdir(mode=0o700, exist_ok=True)
            target = skills/info['name']
            temporary.rename(target)
            files = inventory(target)
            root_files[child.name] = files
            result['skills'].append(dict(name=info['name'], description=info['description']))
        result['roots'].append(dict(source=str(source), sha256=fingerprint(root_files)))
    if result['skills']:
        plugin = destination/'native'
        (plugin/'.claude-plugin').mkdir(mode=0o700, parents=True)
        (plugin/'.claude-plugin/plugin.json').write_text(json.dumps(dict(name='harnessctl', version='0.4.0'))+'\n')
        shutil.copytree(destination/'skills', plugin/'skills')
    result['files'] = inventory(destination)
    fsync_tree(destination)
    return result


def validate(config):
    root = Path(config['resources_dir'])
    if not root.is_dir() or inventory(root) != config['resources']['files']:
        raise ValueError('resource snapshot missing or changed; refusing to alter session resources')


def compare(config, args, events):
    cwd = Path(config['cwd'])
    if args.cwd is not None and Path(args.cwd).expanduser().resolve(strict=True) != cwd:
        raise ValueError('cwd is bound to this event path; use a new event file')
    if args.model is not None and args.model != config.get('model'):
        raise ValueError('model is bound to this event path; use a new event file')
    if args.agents_md is None and args.skills_dir is None:
        return
    with tempfile.TemporaryDirectory(prefix='resource-check-', dir=events.parent) as temp:
        candidate = prepare(args, Path(temp)/'resources', cwd, events)
        current = config['resources']
        if args.agents_md is not None and candidate['agents'] != current['agents']:
            raise ValueError('instructions are bound to this event path; use a new event file')
        if args.skills_dir is not None and candidate['roots'] != current['roots']:
            raise ValueError('skills are bound to this event path; use a new event file')
