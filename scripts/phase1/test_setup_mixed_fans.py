"""Offline checks: SQL cardinalities and verified-count manifest contract."""
import json
import os
import re
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import setup_mixed_fans as setup


class FanGeneratorTests(unittest.TestCase):
    def test_seed_for_two_and_501_is_deterministic_and_distinct(self):
        one, meta = setup.build_seed_sql('try01', 42, 101, 102, 103, 2, 501)
        two, meta2 = setup.build_seed_sql('try01', 42, 101, 102, 103, 2, 501)
        self.assertEqual(one, two)
        self.assertEqual(meta, meta2)
        self.assertEqual(meta['generated_synthetic_users'], 501)
        self.assertEqual(meta['normal_synthetic_fans'], 1)
        self.assertEqual(meta['popular_synthetic_fans'], 500)
        user_rows = re.findall(r"\(720\d+,'mf_try01_\d+','invalid-test-password'\)", one)
        self.assertEqual(len(user_rows), 501)
        follow_pairs = re.findall(r'\((720\d+),(102|103)\)', one)
        self.assertEqual(len(follow_pairs), 501)
        self.assertEqual(len({pair[0] for pair in follow_pairs}), 501)
        self.assertEqual(sum(author == '102' for _, author in follow_pairs), 1)
        self.assertEqual(sum(author == '103' for _, author in follow_pairs), 500)
        self.assertNotIn('(101,102)', one)  # reader relationship is created by API
        self.assertIn('START TRANSACTION;', one)
        self.assertTrue(one.endswith('COMMIT;\n'))

    def test_reject_invalid_ids_and_namespace(self):
        with self.assertRaises(ValueError):
            setup.build_seed_sql('bad-name', 1, 101, 102, 103, 2, 501)
        with self.assertRaises(ValueError):
            setup.build_seed_sql('good', 1, 101, 101, 103, 2, 501)
        with self.assertRaises(ValueError):
            setup.build_seed_sql('good', 1, 101, 102, 103, 0, 501)

    def test_live_count_mismatch_blocks_manifest(self):
        accounts = {
            'reader': {'username': 'mf_test_reader', 'id': 101, 'token': 'test'},
            'normal': {'username': 'mf_test_normal', 'id': 102, 'token': 'test'},
            'popular': {'username': 'mf_test_popular', 'id': 103, 'token': 'test'},
        }
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory) / 'fans.json'
            def fake_compose(_args, command, sql=None):
                return '127.0.0.1:19091'
            def fake_mysql(_args, statement):
                if statement.startswith('SELECT'):
                    return '2' if '102' in statement else '500'
                return ''
            with patch.dict(os.environ, {'CONVO_TEST_ACCOUNT_PASSWORD': 'password123'}), \
                 patch.object(setup, 'run_compose', side_effect=fake_compose), \
                 patch.object(setup, 'mysql_query', side_effect=fake_mysql), \
                 patch.object(setup, 'register_login', side_effect=list(accounts.values())), \
                 patch.object(setup, 'api_call', return_value={'code': 1000, 'data': {'following': True}}):
                result = setup.main(['--url', 'http://127.0.0.1:19091',
                                     '--project', 'convo-p1-demo', '--namespace', 'try01',
                                     '--out', str(out)])
            self.assertEqual(result, 1)
            self.assertFalse(out.exists())
            self.assertTrue(out.with_suffix('.sql').exists())

    def test_valid_live_counts_write_nonsecret_manifest(self):
        accounts = [
            {'username': f'mf_demo_{role}', 'id': n, 'token': f'secret_{role}'}
            for n, role in ((101, 'reader'), (102, 'normal'), (103, 'popular'))]
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory) / 'fans.json'
            def fake_mysql(_args, statement):
                if statement.startswith('SELECT'):
                    return '2' if '102' in statement else '501'
                return ''
            with patch.dict(os.environ, {'CONVO_TEST_ACCOUNT_PASSWORD': 'password123'}), \
                 patch.object(setup, 'run_compose', return_value='127.0.0.1:19091'), \
                 patch.object(setup, 'mysql_query', side_effect=fake_mysql), \
                 patch.object(setup, 'register_login', side_effect=accounts), \
                 patch.object(setup, 'api_call', return_value={'code': 1000, 'data': {'following': True}}):
                result = setup.main(['--url', 'http://127.0.0.1:19091', '--project', 'convo-p1-demo',
                                     '--namespace', 'try01', '--seed', '42', '--out', str(out)])
            self.assertEqual(result, 0)
            report = json.loads(out.read_text())
            self.assertEqual(report['actual_fans'], {'normal': 2, 'popular': 501})
            self.assertEqual(report['seed'], 42)
            self.assertNotIn('secret_', out.read_text())
            self.assertNotIn('password123', out.read_text())


if __name__ == '__main__':
    unittest.main()
