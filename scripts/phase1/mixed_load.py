#!/usr/bin/env python3
"""Mixed Feed reads and real POST /post writes. Local isolated API only.

The reader MUST follow the writer beforehand. Visibility is measured by polling
first Feed page after a successful write. This is not a write-time-fanout test.
"""
import argparse
import base64
import binascii
import concurrent.futures
import json
import os
import re
import statistics
import time
import urllib.error
import urllib.parse
import urllib.request
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path


def call(url, token, path, method='GET', data=None):
    req = urllib.request.Request(url + path, data=json.dumps(data).encode() if data is not None else None,
        method=method, headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    begin = time.perf_counter()
    try:
        with urllib.request.urlopen(req,timeout=10) as response:
            result = json.load(response)
        if result.get('code') != 1000:
            return (time.perf_counter()-begin)*1000, None, 'business_' + str(result.get('code'))
        return (time.perf_counter()-begin)*1000, result.get('data'), None
    except urllib.error.HTTPError as exc:
        return (time.perf_counter()-begin)*1000, None, 'http_' + str(exc.code)
    except (OSError, ValueError, TimeoutError) as exc:
        return (time.perf_counter()-begin)*1000, None, type(exc).__name__


def positive_int64_text(value):
    """Preserve BIGINT precision: never compare IDs through JSON floats."""
    if (not isinstance(value, str) or not re.fullmatch(r'[1-9][0-9]*', value)
            or int(value) > (1 << 63) - 1):
        raise ValueError('author ID must be a positive decimal int64 string')
    return value


def jwt_subject(token):
    """Read the subject for an early role guard; the API verifies JWT signatures.

    This check rejects a valid token for the wrong writer *before* any posts.
    It does not treat unsigned claims as proof of server authentication.
    """
    parts = token.split('.')
    if len(parts) != 3 or not all(parts):
        raise ValueError('writer token must be a compact JWT')
    try:
        payload = json.loads(base64.urlsafe_b64decode(
            parts[1] + '=' * (-len(parts[1]) % 4)))
    except (binascii.Error, UnicodeDecodeError, ValueError) as exc:
        raise ValueError('writer token has an invalid JWT payload') from exc
    if not isinstance(payload, dict):
        raise ValueError('writer token JWT payload must be an object')
    return positive_int64_text(payload.get('sub'))


def validate_writer_role(token, role, expected_id):
    expected = positive_int64_text(expected_id)
    subject = jwt_subject(token)
    if subject != expected:
        raise ValueError(f'writer identity mismatch: --writer-role {role} expects '
                         f'author ID {expected}, but JWT subject is {subject}')


def percentile(times, p):
    if not times: return None
    return round(sorted(times)[int((len(times)-1)*p)],3)


def reader(args, token, until):
    rows = []
    while time.perf_counter() < until:
        ms, _, err = call(args.url, token, '/api/v1/feed?limit=20')
        rows.append((ms,err))
    return rows


def writer(args, token, reader_token, until):
    rows = []
    i = 0
    while time.perf_counter() < until:
        i += 1
        title = f'Phase1 mixed {args.run_id} #{i}'
        ms, result, err = call(args.url,token,'/api/v1/post',method='POST',
            data={'community_id':args.community_id,'title':title,'content':'Mixed benchmark post. Test environment only.'})
        visibility_ms = None
        if not err and result:
            post_id = result['post_id']
            begin = time.perf_counter()
            while time.perf_counter()-begin < args.visibility_timeout:
                _,feed,feed_err = call(args.url,reader_token,'/api/v1/feed?limit=50')
                if not feed_err and any(p['id'] == post_id for p in feed.get('items',[])):
                    visibility_ms = round((time.perf_counter()-begin)*1000,3)
                    break
                time.sleep(.15)
        rows.append({'ms':round(ms,3),'error':err,'visible_ms':visibility_ms})
        if args.write_interval_ms: time.sleep(args.write_interval_ms/1000)
    return rows


def main():
    p=argparse.ArgumentParser()
    p.add_argument('--url',default='http://127.0.0.1:9090')
    p.add_argument('--reader-token-env',default='CONVO_TEST_TOKEN')
    p.add_argument('--writer-token-env',default='CONVO_TEST_WRITER_TOKEN')
    p.add_argument('--run-id',required=True)
    p.add_argument('--fans-manifest',type=Path,help='Verified setup_mixed_fans.py JSON report')
    p.add_argument('--writer-role',choices=('normal','popular'),help='Which API writer is being measured')
    p.add_argument('--community-id',type=int,default=1)
    p.add_argument('--seconds',type=int,default=30)
    p.add_argument('--readers',type=int,default=4)
    p.add_argument('--write-interval-ms',type=int,default=1500)
    p.add_argument('--visibility-timeout',type=int,default=5)
    p.add_argument('--out',type=Path,required=True)
    args=p.parse_args()
    url=urllib.parse.urlsplit(args.url)
    if url.scheme!='http' or url.hostname not in ('localhost','127.0.0.1','::1'):
        p.error('Only localhost targets are allowed')
    if not 1<=args.seconds<=600 or not 1<=args.readers<=100 or not 0<=args.write_interval_ms<=60000:
        p.error('invalid workload limits')
    fans = None
    if bool(args.fans_manifest) != bool(args.writer_role):
        p.error('--fans-manifest and --writer-role must be set together')
    if args.fans_manifest:
        try:
            manifest = json.loads(args.fans_manifest.read_text(encoding='utf-8'))
            if manifest['api_origin'] != args.url or manifest['mode'] != 'read_time_pull':
                p.error('fan manifest does not belong to this API / pull-mode experiment')
            fans = {'role': args.writer_role, 'writer_id': manifest['accounts'][args.writer_role]['id'],
                    'actual_fans': manifest['actual_fans'][args.writer_role],
                    'seed': manifest['seed'], 'namespace': manifest['namespace'],
                    'project': manifest['project']}
        except (OSError, KeyError, TypeError, ValueError) as exc:
            p.error(f'invalid fan manifest: {exc}')
    reader_token=os.environ.get(args.reader_token_env)
    writer_token=os.environ.get(args.writer_token_env)
    if not reader_token or not writer_token: p.error('Set reader and writer token env vars')
    if fans:
        try:
            validate_writer_role(writer_token, args.writer_role, fans['writer_id'])
        except ValueError as exc:
            p.error(str(exc))
    start=datetime.now(timezone.utc).isoformat()
    begin=time.perf_counter()
    until=begin+args.seconds
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.readers+1) as pool:
        readers=[pool.submit(reader,args,reader_token,until) for _ in range(args.readers)]
        writer_future=pool.submit(writer,args,writer_token,reader_token,until)
        read_rows=[row for task in readers for row in task.result()]
        writes=writer_future.result()
    duration=time.perf_counter()-begin
    read_errors=Counter(err for _,err in read_rows if err)
    report={
        'started':start,'elapsed_s':round(duration,3),'run_id':args.run_id,
        'readers':args.readers, 'seconds':args.seconds,
        'write_interval_ms':args.write_interval_ms,
        'visibility_timeout_s':args.visibility_timeout,
        'community_id':args.community_id,
        'fans':fans, 'read_total':len(read_rows),
        'read_rps':round(len(read_rows)/duration,2), 'read_errors':dict(read_errors),
        'read_p50_ms':percentile([ms for ms,_ in read_rows],.5),
        'read_p95_ms':percentile([ms for ms,_ in read_rows],.95),
        'post_total':len(writes),'post_errors':dict(Counter(row['error'] for row in writes if row['error'])),
        'post_p50_ms':percentile([row['ms'] for row in writes],.5),
        'post_p95_ms':percentile([row['ms'] for row in writes],.95),
        'visibility_p50_ms':percentile([row['visible_ms'] for row in writes if row['visible_ms'] is not None],.5),
        'visibility_p95_ms':percentile([row['visible_ms'] for row in writes if row['visible_ms'] is not None],.95),
        'not_visible_within_deadline':sum(row['visible_ms'] is None and row['error'] is None for row in writes),
        'read_observations':[{'ms':round(ms,3),'error':err} for ms,err in read_rows],
        'write_observations':writes,
    }
    args.out.parent.mkdir(parents=True,exist_ok=True)
    args.out.write_text(json.dumps(report,indent=2),encoding='utf-8')
    print(json.dumps({k:v for k,v in report.items() if not k.endswith('observations')},indent=2))
    if read_errors or report['post_errors'] or report['not_visible_within_deadline']:
        raise SystemExit(1)

if __name__=='__main__': main()
