#!/usr/bin/env python3
"""FEED-1/2 fixture assertions against the live local API.

Run with a dedicated database seeded via generate_feed_sql.py --mode fixture.
Use an API-registered reader ID given in its manifest. No destructive SQL.
"""
import argparse
import concurrent.futures
import json
import os
import urllib.parse
import urllib.request
from pathlib import Path


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--url', default='http://127.0.0.1:9090')
    p.add_argument('--token-env', default='CONVO_TEST_TOKEN')
    p.add_argument('--manifest', required=True, type=Path)
    args = p.parse_args()
    parsed = urllib.parse.urlsplit(args.url)
    if parsed.scheme != 'http' or parsed.hostname not in ('127.0.0.1', 'localhost', '::1'):
        p.error('Local isolated test endpoint only')
    token = os.environ.get(args.token_env)
    if not token: p.error('Set reader token environment variable')
    m = json.loads(args.manifest.read_text())
    if m['mode'] != 'fixture': p.error('Expected mode=fixture manifest')
    if len(m['author_ids']) < 3: p.error('Expected B,C,D fixture authors')

    def request(path, method='GET', with_auth=True):
        headers = {'Authorization': 'Bearer ' + token} if with_auth else {}
        req = urllib.request.Request(args.url + '/api/v1' + path, headers=headers, method=method)
        with urllib.request.urlopen(req, timeout=10) as response:
            return json.load(response)

    b, c, d = m['author_ids'][:3]
    reader = m['reader_id']
    assert request('/feed', with_auth=False)['code'] == 1006
    assert request('/users/0/follow')['code'] == 1001
    assert request('/users/9223372036854775808/follow')['code'] == 1001
    assert request('/users/9223372036854775807/follow', 'PUT')['code'] == 1003
    assert request(f'/users/{reader}/follow', 'PUT')['code'] == 1013
    assert request(f'/users/{reader}/follow')['data']['following'] is False
    assert request(f'/users/{b}/follow')['data']['following'] is True
    assert request('/feed?limit=0')['code'] == 1001
    assert request('/feed?cursor=!invalid!')['code'] == 1001

    # Concurrent idempotent PUTs must not change the visible relationship.
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
        result = list(pool.map(lambda _: request(f'/users/{b}/follow', 'PUT'), range(16)))
    assert all(item['code'] == 1000 and item['data']['following'] for item in result)

    def page_all():
        all_ids = []
        cursor = None
        cursors = set()
        for _ in range(100):
            query = '/feed?limit=2' + ('&cursor=' + urllib.parse.quote(cursor, safe='') if cursor else '')
            response = request(query)
            assert response['code'] == 1000, response
            page = response['data']
            for item in page['items']:
                assert isinstance(item['id'], str) and isinstance(item['author_id'], str)
                all_ids.append(item['id'])
            cursor = page['next_cursor']
            if cursor is None: return all_ids
            assert cursor not in cursors, 'Repeated cursor'
            cursors.add(cursor)
        raise AssertionError('Too many pages')

    expected = m['expected_feed_ids']
    observed = page_all()
    assert observed == expected, {'expected': expected, 'actual': observed}
    assert len(observed) == len(set(observed))
    detail = request('/post/' + observed[0])
    assert detail['code'] == 1000 and detail['data']['author_id_str'] in (b,c)

    assert request(f'/users/{b}/follow', 'DELETE')['code'] == 1000
    assert request(f'/users/{b}/follow', 'DELETE')['code'] == 1000
    assert request(f'/users/{b}/follow')['data']['following'] is False
    after = page_all()
    # Keep only C's posts; D, self, and invisible B must still be excluded.
    expected_after = [expected[1], expected[-1]]
    assert after == expected_after, {'expected':expected_after,'actual':after}
    assert request(f'/users/{b}/follow','PUT')['code'] == 1000
    assert page_all() == expected, 'Re-follow must include old posts'
    assert request(f'/users/{d}/follow')['data']['following'] is False
    print('FEED-1/2 fixture assertions passed:', len(observed), 'posts, large IDs, pagination, follow/unfollow')

if __name__ == '__main__': main()
