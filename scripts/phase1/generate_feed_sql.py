#!/usr/bin/env python3
"""Deterministic, namespaced Feed SQL fixture. Dedicated test MySQL ONLY.

Never pipe this into the primary database. Creation of large historical posts
bypasses the Redis post index; offline rebuild is required before app restart.
"""
import argparse
import hashlib
import json
import random
import re
from datetime import datetime, timedelta
from pathlib import Path


def sql_quote(value):
    return "'" + str(value).replace("'", "''") + "'"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--namespace', required=True, help='Unique run identifier: letters/numbers/_')
    parser.add_argument('--reader-id', type=int, required=True, help='Existing API-created reader ID')
    parser.add_argument('--mode', choices=['fixture', 'load'], default='load')
    parser.add_argument('--users', type=int, default=100)
    parser.add_argument('--posts', type=int, default=1000)
    parser.add_argument('--follow-count', type=int, default=5)
    parser.add_argument('--seed', type=int, default=20261104)
    parser.add_argument('--out', type=Path, required=True)
    args = parser.parse_args()
    if not re.fullmatch('[A-Za-z0-9_]{1,24}', args.namespace):
        parser.error('--namespace must contain only letters, digits and _')
    if args.reader_id <= 0 or args.reader_id > (1 << 63)-1:
        parser.error('reader id out of range')
    if not 1 <= args.users <= 5000 or not 1 <= args.posts <= 250000:
        parser.error('invalid data scale')
    if not 0 <= args.follow_count <= args.users:
        parser.error('follow-count must be between 0 and users')
    if args.mode == 'fixture':
        args.users, args.posts, args.follow_count = 3, 7, 2
    rng = random.Random(args.seed)
    prefix = int.from_bytes(hashlib.sha256(args.namespace.encode()).digest()[:4], 'big') % 100000
    user_base = 7000000000000000000 + prefix * 100000
    post_base = 7100000000000000000 + prefix * 100000
    if args.reader_id in range(user_base, user_base + args.users):
        parser.error('reader id overlaps generated users')
    authors = [user_base + i for i in range(1, args.users + 1)]
    out = []
    def append(statement): out.append(statement + ';\n')
    out.append('-- TEST ONLY. Generate under an isolated MySQL volume; do not import into production.\n')
    out.append(f'-- namespace={args.namespace} seed={args.seed} reader={args.reader_id} users={args.users} posts={args.posts} follows={args.follow_count}\n')
    append('START TRANSACTION')
    user_rows = []
    for i, user_id in enumerate(authors, 1):
        # Password deliberately unusable. Authors are synthetic; reader logs in via API.
        user_rows.append(f'({user_id},{sql_quote("p1_" + args.namespace + "_" + str(i))},{sql_quote("invalid-test-password")})')
    for i in range(0, len(user_rows), 500):
        append('INSERT INTO user (user_id,username,password) VALUES ' + ','.join(user_rows[i:i+500]))
    follow_rows = [f'({args.reader_id},{author})' for author in authors[:args.follow_count]]
    for i in range(0, len(follow_rows), 500):
        append('INSERT INTO user_follow (follower_id,followee_id) VALUES ' + ','.join(follow_rows[i:i+500]))
    post_rows = []
    expected = []
    base_date = datetime(2026, 1, 1, 12, 0, 0)
    for i in range(1, args.posts + 1):
        post_id = post_base + i
        if args.mode == 'fixture':
            # 1/2/3: B/C, same timestamp, order by post ID DESC
            # 4: unfollowed D, 5: own A, 6: invisible B, 7: older C.
            ids = [authors[0], authors[1], authors[0], authors[2], args.reader_id, authors[0], authors[1]]
            author = ids[i-1]
            status = 0 if i == 6 else 1
            instant = base_date if i < 7 else base_date - timedelta(days=1)
        else:
            # A high-output author and repeated timestamps exercise skew/ties.
            author = authors[0] if rng.random() < .25 else rng.choice(authors)
            status = 0 if i % 101 == 0 else 1
            instant = base_date - timedelta(seconds=i // 4)
        date_str = instant.strftime('%Y-%m-%d %H:%M:%S')
        post_rows.append(f'({post_id},{sql_quote("Phase1 " + args.namespace + " #" + str(i))},{sql_quote("Generated in dedicated test stack")},{author},1,{status},{sql_quote(date_str)},{sql_quote(date_str)})')
        if status == 1 and author in authors[:args.follow_count]:
            expected.append((instant, post_id))
    for i in range(0, len(post_rows), 500):
        append('INSERT INTO post (post_id,title,content,author_id,community_id,status,create_time,update_time) VALUES ' + ','.join(post_rows[i:i+500]))
    append('COMMIT')
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(''.join(out), encoding='utf-8')
    expected.sort(reverse=True)
    manifest = {'mode': args.mode, 'namespace': args.namespace, 'seed': args.seed,
                'reader_id': str(args.reader_id), 'author_ids': [str(i) for i in authors], 'generated_users': args.users,
                'generated_posts': args.posts, 'follow_count': args.follow_count,
                'expected_feed_ids': [str(p[1]) for p in expected] if args.mode == 'fixture' else None}
    args.out.with_suffix('.json').write_text(json.dumps(manifest, indent=2), encoding='utf-8')
    print('Generated', args.out, '(dedicated MySQL ONLY)', 'and manifest', args.out.with_suffix('.json'))

if __name__ == '__main__':
    main()
