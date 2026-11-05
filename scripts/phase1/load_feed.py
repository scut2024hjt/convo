#!/usr/bin/env python3
"""Measure real Feed HTTP keyset pages; localhost only; JSON raw archive."""
import argparse
import concurrent.futures
from collections import Counter
import json
import os
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from pathlib import Path


def percentile(samples, q):
    if not samples: return None
    values = sorted(samples)
    return round(values[int((len(values) - 1) * q)] * 1000, 3)


def request(url, token, timeout):
    started = time.perf_counter()
    req = urllib.request.Request(url, headers={'Authorization': 'Bearer ' + token})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            body = json.load(response)
            status = response.status
    except urllib.error.HTTPError as exc:
        return time.perf_counter()-started, None, f'http_{exc.code}'
    except (OSError, ValueError, TimeoutError) as exc:
        return time.perf_counter()-started, None, type(exc).__name__
    if status != 200 or body.get('code') != 1000:
        return time.perf_counter()-started, None, f'business_{body.get("code")}'
    return time.perf_counter()-started, body.get('data'), None


def run_worker(index, args, token):
    """Return partial samples plus an explicit terminal outcome for this worker.

    A failed request is not the same as a successful last page. Both stop this
    worker and are recorded so that the complete report can still be written.
    """
    observations = []
    cursor = None
    for n in range(args.per_worker):
        page = n % args.depth + 1
        if page == 1:
            cursor = None
        elif cursor is None:
            return {'worker': index, 'completed': False,
                    'failure_reason': 'insufficient_depth',
                    'failure_detail': f'no next_cursor for requested page {page}/{args.depth}',
                    'observations': observations}
        query = {'limit': str(args.limit)}
        if cursor:
            query['cursor'] = cursor
        url = args.url + '/api/v1/feed?' + urllib.parse.urlencode(query)
        elapsed, data, err = request(url, token, args.timeout)
        observations.append({'ms': round(elapsed*1000, 3), 'error': err,
                             'depth': page, 'items': len(data.get('items', [])) if data else None})
        if err:
            return {'worker': index, 'completed': False,
                    'failure_reason': 'request_failed', 'failure_detail': err,
                    'observations': observations}
        cursor = data.get('next_cursor') if isinstance(data, dict) else None
        if page < args.depth and cursor is None:
            return {'worker': index, 'completed': False,
                    'failure_reason': 'insufficient_depth',
                    'failure_detail': f'no next_cursor for requested page {page+1}/{args.depth}',
                    'observations': observations}
    return {'worker': index, 'completed': True, 'failure_reason': None,
            'failure_detail': None, 'observations': observations}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--url', default='http://127.0.0.1:9090')
    parser.add_argument('--token-env', default='CONVO_TEST_TOKEN')
    parser.add_argument('--concurrency', type=int, default=5)
    parser.add_argument('--per-worker', type=int, default=50)
    parser.add_argument('--depth', type=int, default=1)
    parser.add_argument('--limit', type=int, default=20)
    parser.add_argument('--warmup', type=int, default=5)
    parser.add_argument('--timeout', type=float, default=10)
    parser.add_argument('--out', type=Path, required=True)
    args = parser.parse_args()
    parsed = urllib.parse.urlsplit(args.url)
    if parsed.scheme != 'http' or parsed.hostname not in ('127.0.0.1', 'localhost', '::1') or parsed.username:
        parser.error('Only local HTTP test targets are allowed')
    if not 1 <= args.concurrency <= 100 or not 1 <= args.per_worker <= 100000 or not 1 <= args.depth <= 200 or not 1 <= args.limit <= 50:
        parser.error('invalid load settings')
    if args.per_worker < args.depth:
        parser.error('--per-worker must be >= --depth to complete a full deep-page cycle')
    token = os.environ.get(args.token_env)
    if not token: parser.error(f'Set {args.token_env} in the environment')
    for _ in range(args.warmup):
        request(args.url + '/api/v1/feed?limit=' + str(args.limit), token, args.timeout)
    started_at = datetime.now(timezone.utc).isoformat()
    start = time.perf_counter()
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.concurrency) as pool:
        futures = [pool.submit(run_worker, i, args, token) for i in range(args.concurrency)]
        worker_results = []
        for i, future in enumerate(futures):
            try:
                worker_results.append(future.result())
            except Exception as exc:
                worker_results.append({'worker': i, 'completed': False,
                                       'failure_reason': 'unexpected_worker_error',
                                       'failure_detail': type(exc).__name__, 'observations': []})
    seconds = time.perf_counter() - start
    observations = [row for worker in worker_results for row in worker['observations']]
    times = [row['ms']/1000 for row in observations]
    failures = [row for row in observations if row['error']]
    incomplete = [worker for worker in worker_results if not worker['completed']]
    error_counts = dict(Counter(row['error'] for row in failures))
    reasons = dict(Counter(worker['failure_reason'] for worker in incomplete))
    report = {'start': started_at, 'duration_s': round(seconds, 3), 'target': args.url,
              'concurrency': args.concurrency, 'per_worker': args.per_worker, 'depth': args.depth,
              'limit': args.limit, 'warmup': args.warmup,
              'requested_samples': args.concurrency * args.per_worker,
              'requests': len(observations), 'actual_samples': len(observations),
              'errors': len(failures), 'error_counts': error_counts,
              'error_rate': round(len(failures)/len(observations), 6) if observations else None,
              'rps': round(len(observations)/seconds, 2) if seconds else None,
              'p50_ms': percentile(times, .50), 'p95_ms': percentile(times, .95),
              'completed': not incomplete,
              'failure_reason': incomplete[0]['failure_reason'] if incomplete else None,
              'incomplete_reasons': reasons, 'workers': [
                  {k: v for k, v in worker.items() if k != 'observations'} for worker in worker_results
              ], 'observations': observations}
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(report, indent=2), encoding='utf-8')
    print(json.dumps({k: v for k, v in report.items() if k not in ('observations', 'workers')}, indent=2))
    if not report['completed'] or failures:
        raise SystemExit(1)

if __name__ == '__main__':
    main()
