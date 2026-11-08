#!/usr/bin/env python3
"""Offline T01 repair + verified clean T02 input. No network; source is read-only.
Read this text using the GitHub connector, write it locally, then execute Python.
Do not try to download this script from inside the network-restricted container.
"""
import argparse, base64, hashlib, json, lzma, os, re, shutil, subprocess, tempfile
from pathlib import Path

PAYLOAD_SHA256 = '53b472451ac0cbfa4b180a0f9f6f717d3838475e04165636fafc03d55f571178'
PAYLOAD_B64 = """
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
"""

def digest(data):
    return hashlib.sha256(data).hexdigest()

def load_payload():
    packed = base64.b64decode(PAYLOAD_B64, validate=False)
    if digest(packed) != PAYLOAD_SHA256:
        raise SystemExit('SCRIPT_TEXT_CORRUPTED: payload checksum mismatch; re-read exact text.')
    data = json.loads(lzma.decompress(packed))
    encoded = json.dumps(data['source_files'], ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode()
    if digest(encoded) != data['snapshot_sha256']:
        raise SystemExit('MANIFEST_CORRUPTED: canonical source identity mismatch.')
    return data

def inspect_source(source, data):
    errors, exact = [], 0
    for f in data['source_files']:
        p = source / f['path']
        if p.is_symlink():
            errors.append(f['path'] + ': symlink is not an accepted input')
            continue
        current = digest(p.read_bytes()) if p.is_file() else None
        if current == f['sha256']:
            exact += 1
        elif f['path'] == 'docs/T01_ACCEPTANCE.md' and current is None:
            continue
        elif f['path'] in data['before_hashes'] and current == data['before_hashes'][f['path']]:
            continue
        else:
            errors.append(f['path'] + ': missing or different from accepted original/fixed T01')
    return errors, exact

def candidates(output):
    roots = [Path.cwd(), Path('/mnt/data'), Path('/home/oai/share')]
    seen, result, visited = set(), [], 0
    skip = {'.git', 'node_modules', '__pycache__', '.cache', '.venv', 'venv', 'vendor', 'site-packages'}
    for root in roots:
        if not root.is_dir() or root == Path('/'):
            continue
        for current, dirs, files in os.walk(root):
            current = Path(current)
            visited += 1
            depth = len(current.relative_to(root).parts)
            dirs[:] = [d for d in dirs if d not in skip and not (current / d).is_symlink() and (current / d).resolve() != output]
            if depth >= 6 or visited >= 5000:
                dirs[:] = []
            if 'go.mod' in files and (current / 'dao/mysql/content_delivery.go').is_file():
                key = current.resolve()
                if key not in seen:
                    seen.add(key)
                    result.append(key)
            if visited >= 5000:
                break
    return result

def verify_exact(root, data):
    errors = []
    for f in data['source_files']:
        p = root / f['path']
        if not p.is_file() or p.is_symlink():
            errors.append(f['path'] + ': missing or symlink')
        elif digest(p.read_bytes()) != f['sha256'] or p.stat().st_size != f['bytes']:
            errors.append(f['path'] + ': content mismatch')
        elif (p.stat().st_mode & 0o777) != int(f['mode'], 8):
            errors.append(f['path'] + ': mode mismatch')
    if errors:
        raise SystemExit('TARGET_VERIFICATION_FAILED:\n' + '\n'.join(errors))

def git(root, *args, input_data=None):
    env = os.environ.copy()
    env.update({'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_GLOBAL': os.devnull, 'GIT_TERMINAL_PROMPT': '0'})
    for name in ['GIT_DIR', 'GIT_WORK_TREE', 'GIT_INDEX_FILE']:
        env.pop(name, None)
    command = ['git', '-c', 'core.hooksPath=/dev/null', '-c', 'core.autocrlf=false', '-c', 'commit.gpgSign=false', '-c', 'tag.gpgSign=false'] + list(args)
    return subprocess.run(command, cwd=root, env=env, input=input_data, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout.decode().strip()

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', type=Path, help='Existing original or accepted T01 source root containing go.mod')
    parser.add_argument('--output', type=Path, default=(Path('/mnt/data') if Path('/mnt/data').is_dir() else Path.cwd()) / 'convo-t02-offline')
    args = parser.parse_args()
    data = load_payload()
    output = args.output.resolve()
    if output.exists():
        verify_exact(output, data)
        baseline = git(output, 'rev-parse', 'refs/tags/t02-input')
        print(json.dumps({'status': 'already_verified', 'output': str(output), 'source_files': len(data['source_files']), 'snapshot_id': data['snapshot_id'], 'local_input_commit': baseline, 'network_required': False}))
        return
    found = [args.source.resolve()] if args.source else candidates(output)
    valid, diagnostics = [], {}
    for source in found:
        errors, exact = inspect_source(source, data)
        if errors:
            diagnostics[str(source)] = errors
        else:
            valid.append((exact, str(source), source))
    if not valid:
        print(json.dumps({'status': 'source_not_available_or_mismatched', 'details': diagnostics, 'required': 'An existing original T01 delivery (161 files) or accepted T01 snapshot (163 files). No source files were changed.'}, ensure_ascii=False, indent=2))
        raise SystemExit(2)
    source = sorted(valid, reverse=True)[0][2]
    if source == output or source in output.parents:
        raise SystemExit('Choose an output outside the original source directory.')
    output.parent.mkdir(parents=True, exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix='.t02-input-build-', dir=output.parent))
    try:
        for f in data['source_files']:
            src, dst = source / f['path'], staging / f['path']
            if src.is_file():
                dst.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(src, dst)
        for part in re.split(r'(?=^diff --git )', data['patch'], flags=re.M):
            if not part:
                continue
            match = re.match(r'diff --git a/(\S+) b/(\S+)', part)
            path = match.group(2)
            if path not in data['before_hashes']:
                raise SystemExit('Unexpected patch path: ' + path)
            expected = next(f for f in data['source_files'] if f['path'] == path)
            target = staging / path
            if target.is_file() and digest(target.read_bytes()) == expected['sha256']:
                continue
            git(staging, 'apply', '--check', '--whitespace=nowarn', '-', input_data=part.encode())
            git(staging, 'apply', '--whitespace=nowarn', '-', input_data=part.encode())
        (staging / 'docs/T01_ACCEPTANCE.md').write_text(data['acceptance'])
        for f in data['source_files']:
            (staging / f['path']).chmod(int(f['mode'], 8))
        verify_exact(staging, data)
        git(staging, 'init', '-q')
        git(staging, 'add', '-A')
        git(staging, '-c', 'user.name=WebDelivery', '-c', 'user.email=web-delivery@example.invalid', 'commit', '-q', '-m', 'T02 offline input: verified T01 snapshot')
        git(staging, 'tag', 't02-input')
        baseline = git(staging, 'rev-parse', 'HEAD')
        staging.rename(output)
    finally:
        if staging.exists():
            shutil.rmtree(staging)
    report = {'status': 'verified', 'original_source': str(source), 'output': str(output), 'source_files': len(data['source_files']), 'snapshot_id': data['snapshot_id'], 'snapshot_sha256': data['snapshot_sha256'], 'upstream_source_commit': data['source_commit'], 'local_input_commit': baseline, 'local_tag': 't02-input', 'same_application_source': True, 'network_required': False, 'original_source_unchanged': True, 'note': 'Local commit SHA differs from upstream; all 163 application-source files match the accepted manifest. Extra handoff docs and six omitted legacy host configs are not deletions.'}
    (output.parent / 'convo-t02-offline-input-report.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(report, ensure_ascii=False, indent=2))

if __name__ == '__main__':
    main()
