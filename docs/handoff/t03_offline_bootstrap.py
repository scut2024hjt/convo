#!/usr/bin/env python3
"""Offline accepted T02 repairs + verified clean T03 input. No network; source is read-only.
Read this text using the GitHub connector, write it locally, then execute Python.
Do not try to download this script from inside the network-restricted container.
"""
import argparse, base64, hashlib, json, lzma, os, re, shutil, subprocess, tempfile
from pathlib import Path

PAYLOAD_SHA256 = '215f76e078f1afd6e59378d18c7539ece0979d945bbe4bb55adc597016547cdf'
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
        elif f['path'] in data['extra_files'] and current is None:
            continue
        elif f['path'] in data['before_hashes'] and current == data['before_hashes'][f['path']]:
            continue
        else:
            errors.append(f['path'] + ': missing or different from accepted original/fixed T02')
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
    parser.add_argument('--source', type=Path, help='Existing original or accepted T02 source root containing go.mod')
    parser.add_argument('--output', type=Path, default=(Path('/mnt/data') if Path('/mnt/data').is_dir() else Path.cwd()) / 'convo-t03-offline')
    args = parser.parse_args()
    data = load_payload()
    output = args.output.resolve()
    if output.exists():
        verify_exact(output, data)
        baseline = git(output, 'rev-parse', 'refs/tags/t03-input')
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
        print(json.dumps({'status': 'source_not_available_or_mismatched', 'details': diagnostics, 'required': 'An existing original T02 delivery (176 files) or accepted T02 snapshot (178 files plus acceptance records). No source files were changed.'}, ensure_ascii=False, indent=2))
        raise SystemExit(2)
    source = sorted(valid, reverse=True)[0][2]
    if source == output or source in output.parents:
        raise SystemExit('Choose an output outside the original source directory.')
    output.parent.mkdir(parents=True, exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix='.t03-input-build-', dir=output.parent))
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
        for rel, content in data['extra_files'].items():
            dst = staging / rel
            dst.parent.mkdir(parents=True, exist_ok=True)
            dst.write_text(content)
        for f in data['source_files']:
            (staging / f['path']).chmod(int(f['mode'], 8))
        verify_exact(staging, data)
        git(staging, 'init', '-q')
        git(staging, 'add', '-A')
        git(staging, '-c', 'user.name=WebDelivery', '-c', 'user.email=web-delivery@example.invalid', 'commit', '-q', '-m', 'T03 offline input: verified T02 snapshot')
        git(staging, 'tag', 't03-input')
        baseline = git(staging, 'rev-parse', 'HEAD')
        staging.rename(output)
    finally:
        if staging.exists():
            shutil.rmtree(staging)
    report = {'status': 'verified', 'original_source': str(source), 'output': str(output), 'source_files': len(data['source_files']), 'snapshot_id': data['snapshot_id'], 'snapshot_sha256': data['snapshot_sha256'], 'upstream_source_commit': data['source_commit'], 'local_input_commit': baseline, 'local_tag': 't03-input', 'same_application_source': True, 'network_required': False, 'original_source_unchanged': True, 'note': 'Local commit SHA differs from upstream; all 180 input-source files match the accepted manifest. Extra handoff docs and six omitted legacy host configs are not deletions.'}
    (output.parent / 'convo-t03-offline-input-report.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(report, ensure_ascii=False, indent=2))

if __name__ == '__main__':
    main()
