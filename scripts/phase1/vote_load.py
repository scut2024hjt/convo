#!/usr/bin/env python3
"""Drive the actual vote API using already-authenticated distinct test users."""
import argparse
import concurrent.futures
import json
import os
import random
import statistics
import time
import urllib.error
import urllib.parse
import urllib.request
from collections import Counter
from pathlib import Path


def vote(args, token, direction):
    payload = json.dumps({'post_id': args.post_id, 'direction': direction}).encode()
    req = urllib.request.Request(args.url + '/api/v1/vote', data=payload, method='POST',
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    start = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=args.timeout) as response:
            body = json.load(response)
        code = str(body.get('code', 'missing'))
        if code == '1000':
            code = 'changed' if body.get('data', {}).get('changed') else 'unchanged'
    except urllib.error.HTTPError as exc: code = 'http_' + str(exc.code)
    except (OSError, ValueError, TimeoutError) as exc: code = type(exc).__name__
    return round((time.perf_counter()-start)*1000,3), code


def worker(args, token, index):
    rng = random.Random(args.seed + index)
    # Requests are sequential per user, avoiding simultaneous changes from a
    # single identity and preserving a meaningful final direction.
    rows = []
    for _ in range(args.requests_per_user):
        direction = rng.choice(['1', '-1', '0', '1'])
        rows.append(vote(args, token, direction))
        if args.interval_ms: time.sleep(args.interval_ms/1000)
    return rows


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--url', default='http://127.0.0.1:9090')
    p.add_argument('--tokens-env', default='CONVO_TEST_VOTE_TOKENS')
    p.add_argument('--post-id', required=True)
    p.add_argument('--seed', type=int, default=20261104)
    p.add_argument('--requests-per-user', type=int, default=20)
    p.add_argument('--interval-ms', type=int, default=150)
    p.add_argument('--timeout', type=int, default=8)
    p.add_argument('--out', type=Path, required=True)
    args = p.parse_args()
    url = urllib.parse.urlsplit(args.url)
    if url.scheme != 'http' or url.hostname not in ('127.0.0.1', 'localhost', '::1'):
        p.error('Only local test endpoints are allowed')
    if not args.post_id.isdigit() or int(args.post_id) <= 0 or int(args.post_id) > (1<<63)-1:
        p.error('post_id must be a positive int64 decimal string')
    if not 1 <= args.requests_per_user <= 10000 or not 0 <= args.interval_ms <= 60000:
        p.error('invalid load settings')
    tokens = [x.strip() for x in os.environ.get(args.tokens_env, '').split(',') if x.strip()]
    if not tokens: p.error(f'Set {args.tokens_env} with distinct test-user JWTs')
    start = time.perf_counter()
    with concurrent.futures.ThreadPoolExecutor(max_workers=min(len(tokens), 100)) as pool:
        rows = [v for i, result in enumerate(pool.map(lambda pair: worker(args, *pair), zip(tokens, range(len(tokens))))) for v in result]
    duration = time.perf_counter()-start
    ms = sorted(r[0] for r in rows)
    report = {'count':len(rows),'users':len(tokens),'seed':args.seed,'duration_s':round(duration,3),
              'rps':round(len(rows)/duration,2), 'p50_ms':ms[(len(ms)-1)//2],
              'p95_ms':ms[int((len(ms)-1)*.95)],'outcomes':dict(Counter(c for _,c in rows)),
              'observations':[{'ms':t,'result':result} for t,result in rows]}
    args.out.parent.mkdir(parents=True,exist_ok=True)
    args.out.write_text(json.dumps(report,indent=2),encoding='utf-8')
    print(json.dumps({k:v for k,v in report.items() if k!='observations'},indent=2))

if __name__ == '__main__': main()
