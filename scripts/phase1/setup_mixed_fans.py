#!/usr/bin/env python3
"""API-backed writers, deterministic synthetic fans, real MySQL fan counts.

Run ONLY in an explicitly named, disposable `convo-p1-*` Compose project.
The API creates real, login-capable reader/writer accounts; the synthetic fan
accounts deliberately cannot log in. No JWT or password is written to results.
"""
import argparse
import hashlib
import json
import os
import random
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

MAX_ID = (1 << 63) - 1


def validate_local_url(url):
    parsed = urllib.parse.urlsplit(url)
    if (parsed.scheme != 'http' or parsed.hostname not in ('127.0.0.1', 'localhost', '::1')
            or parsed.username or parsed.password or parsed.query or parsed.fragment or parsed.path.rstrip('/')):
        raise ValueError('API URL must be a local HTTP origin, without credentials or path')
    return parsed.port or 80


def api_call(origin, route, method, data=None, token=None):
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    req = urllib.request.Request(
        origin + '/api/v1' + route,
        data=json.dumps(data).encode() if data is not None else None,
        method=method, headers=headers)
    with urllib.request.urlopen(req, timeout=10) as resp:
        result = json.load(resp)
    return result


def check_success(result, action, allowed=(1000,)):
    code = result.get('code')
    if code not in allowed:
        raise RuntimeError(f'{action}: business_code={code}, msg={result.get("msg")}')
    return result.get('data')


def register_login(origin, username, password):
    check_success(api_call(origin, '/signup', 'POST', {
        'username': username, 'password': password, 're_password': password,
    }), 'signup '+username, allowed=(1000, 1002))
    login = check_success(api_call(origin, '/login', 'POST', {
        'username': username, 'password': password,
    }), 'login '+username)
    user_id = int(login['user_id'])
    if user_id <= 0 or user_id > MAX_ID:
        raise ValueError('invalid API returned ID')
    return {'username': username, 'id': user_id, 'token': login['token']}


def build_seed_sql(namespace, seed, reader_id, normal_id, popular_id, normal_fans, popular_fans):
    """Count the API-created reader as one follower of EACH writer."""
    ids = [reader_id, normal_id, popular_id]
    if len(set(ids)) != 3 or min(ids) <= 0 or max(ids) > MAX_ID:
        raise ValueError('reader and writers must have three distinct positive int64 IDs')
    if not re.fullmatch(r'[a-zA-Z0-9_]{1,20}', namespace):
        raise ValueError('namespace must be 1-20 alphanumeric or underscore characters')
    if not (1 <= normal_fans <= 10000 and 1 <= popular_fans <= 10000):
        raise ValueError('fan counts must be in 1..10000 (includes API reader)')
    total = normal_fans + popular_fans - 2
    digest = hashlib.sha256(namespace.encode()).digest()
    offset = int.from_bytes(digest[:4], 'big') % 1000000
    base = 7200000000000000000 + offset * 100000
    synthetic_ids = list(range(base + 1, base + total + 1))
    if set(ids).intersection(synthetic_ids):
        raise ValueError('synthetic IDs conflict with API-created IDs')
    random.Random(seed).shuffle(synthetic_ids)
    normal_synthetic = synthetic_ids[:normal_fans-1]
    popular_synthetic = synthetic_ids[normal_fans-1:]
    sql = [f'-- isolated TEST ONLY, namespace={namespace} seed={seed}\n', 'START TRANSACTION;\n']
    ordered_ids = sorted(synthetic_ids)
    for n in range(0, len(ordered_ids), 400):
        batch = ordered_ids[n:n+400]
        rows = [f"({i},'mf_{namespace}_{j}','invalid-test-password')"
                for j, i in enumerate(batch, n+1)]
        sql.append('INSERT INTO user (user_id, username, password) VALUES\n  ' + ',\n  '.join(rows) + ';\n')
    relations = [(fan, normal_id) for fan in normal_synthetic] + [
        (fan, popular_id) for fan in popular_synthetic]
    for n in range(0, len(relations), 400):
        batch = relations[n:n+400]
        sql.append('INSERT INTO user_follow (follower_id, followee_id) VALUES\n  ' +
                   ',\n  '.join(f'({f},{w})' for f, w in batch) + ';\n')
    sql.append('COMMIT;\n')
    return ''.join(sql), {'generated_synthetic_users': total,
                           'normal_synthetic_fans': len(normal_synthetic),
                           'popular_synthetic_fans': len(popular_synthetic)}


def run_compose(args, command, sql=None):
    cmd = ['docker', 'compose', '-f', str(args.compose_file), '-p', args.project,
           *command]
    proc = subprocess.run(cmd, input=sql, text=True, capture_output=True, timeout=120)
    if proc.returncode:
        raise RuntimeError(f'Compose operation failed (exit {proc.returncode}): {proc.stderr[:1000]}')
    return proc.stdout.strip()


def mysql_query(args, statement):
    # The container already has MYSQL_ROOT_PASSWORD. No secret is inserted into
    # shell history, command arguments, generated SQL, or JSON artifacts.
    return run_compose(args, ['exec', '-T', 'mysql8019', 'sh', '-c',
        'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql --user=root --database=convo '
        '--batch --skip-column-names --default-character-set=utf8mb4'], sql=statement)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--url', required=True, help='Isolated local API origin, e.g. http://127.0.0.1:19091')
    parser.add_argument('--project', required=True, help='Explicit disposable Compose project: convo-p1-*')
    parser.add_argument('--compose-file', type=Path, default=Path('docker-compose.yaml'))
    parser.add_argument('--namespace', required=True, help='Unique experiment namespace')
    parser.add_argument('--seed', type=int, default=20261104)
    parser.add_argument('--normal-fans', type=int, default=2)
    parser.add_argument('--popular-fans', type=int, default=501)
    parser.add_argument('--password-env', default='CONVO_TEST_ACCOUNT_PASSWORD')
    parser.add_argument('--out', type=Path, required=True, help='Nonsecret JSON manifest')
    args = parser.parse_args(argv)
    if not re.fullmatch(r'convo-p1-[a-z0-9_-]+', args.project):
        parser.error('--project must be explicitly named convo-p1-* (isolated test only)')
    try:
        port = validate_local_url(args.url)
    except ValueError as exc:
        parser.error(str(exc))
    password = os.environ.get(args.password_env)
    if not password:
        parser.error(f'set {args.password_env}; password is never saved')
    try:
        # Ensure a running service in the *specified* project and verify port.
        published = run_compose(args, ['port', 'convo_app', '9090'])
        if int(published.rsplit(':', 1)[-1]) != port:
            raise RuntimeError('API port does not match the isolated Compose project')
        usernames = {role: f'mf_{args.namespace}_{role}' for role in ('reader', 'normal', 'popular')}
        accounts = {role: register_login(args.url, username, password)
                    for role, username in usernames.items()}
        reader = accounts['reader']
        for role in ('normal', 'popular'):
            author_id = accounts[role]['id']
            check_success(api_call(args.url, f'/users/{author_id}/follow', 'PUT',
                                   token=reader['token']), 'follow '+role)
        seed_sql, counts = build_seed_sql(args.namespace, args.seed, reader['id'],
            accounts['normal']['id'], accounts['popular']['id'],
            args.normal_fans, args.popular_fans)
        args.out.parent.mkdir(parents=True, exist_ok=True)
        sql_file = args.out.with_suffix('.sql')
        sql_file.write_text(seed_sql, encoding='utf-8')
        if seed_sql.count('INSERT INTO user ('):
            mysql_query(args, seed_sql)
        verified = {}
        for role, target in (('normal', args.normal_fans), ('popular', args.popular_fans)):
            raw = mysql_query(args,
                f'SELECT COUNT(*) FROM user_follow WHERE followee_id = {accounts[role]["id"]};\n')
            count = int(raw)
            if count != target:
                raise RuntimeError(f'{role}: expected {target} followers, found {count}')
            verified[role] = count
        report = {'project': args.project, 'namespace': args.namespace, 'seed': args.seed,
                  'mode': 'read_time_pull', 'api_origin': args.url,
                  'fan_counts_include_reader': True,
                  'accounts': {role: {'username': acc['username'], 'id': str(acc['id'])}
                               for role, acc in accounts.items()},
                  'actual_fans': verified, 'desired_fans': {
                      'normal': args.normal_fans, 'popular': args.popular_fans},
                  **counts, 'sql_file': str(sql_file)}
        args.out.write_text(json.dumps(report, indent=2), encoding='utf-8')
        print(json.dumps(report, indent=2))
    except (OSError, RuntimeError, ValueError, KeyError, urllib.error.URLError,
            subprocess.TimeoutExpired) as exc:
        print(f'setup failed: {exc}', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
