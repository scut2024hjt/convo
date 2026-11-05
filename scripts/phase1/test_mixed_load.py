"""Writer-to-manifest binding tests; local HTTP fixture, no Docker/MySQL needed."""
import base64
import json
import os
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SCRIPT = Path(__file__).with_name('mixed_load.py')
NORMAL_ID = '9007199254740993'
POPULAR_ID = '9007199254740994'


def fake_jwt(subject):
    """Fixture only: the script reads claims; production API validates signature."""
    def encode(obj):
        return base64.urlsafe_b64encode(json.dumps(obj).encode()).rstrip(b'=').decode()
    return '.'.join((encode({'alg': 'HS256', 'typ': 'JWT'}),
                     encode({'sub': subject, 'iss': 'convo'}), 'fixture_signature'))


class Handler(BaseHTTPRequestHandler):
    get_count = 0
    post_count = 0
    lock = threading.Lock()

    def do_GET(self):
        with self.lock:
            type(self).get_count += 1
        time.sleep(0.005)
        self.respond({'code': 1000, 'data': {'items': [{'id': 'posted-fixture'}], 'next_cursor': None}})

    def do_POST(self):
        with self.lock:
            type(self).post_count += 1
        length = int(self.headers.get('Content-Length', 0))
        self.rfile.read(length)
        self.respond({'code': 1000, 'data': {'post_id': 'posted-fixture'}})

    def respond(self, obj):
        contents = json.dumps(obj).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(contents)))
        self.end_headers()
        self.wfile.write(contents)

    def log_message(self, *_):
        pass


class MixedLoadIdentityTests(unittest.TestCase):
    def setUp(self):
        Handler.get_count = 0
        Handler.post_count = 0
        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = f'http://127.0.0.1:{self.server.server_port}'

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)

    def invoke(self, role, writer_id):
        with tempfile.TemporaryDirectory() as folder:
            folder = Path(folder)
            manifest = folder / 'fans.json'
            manifest.write_text(json.dumps({
                'api_origin': self.url, 'mode': 'read_time_pull',
                'project': 'convo-p1-fixture', 'seed': 42, 'namespace': 'fixture01',
                'accounts': {'normal': {'id': NORMAL_ID}, 'popular': {'id': POPULAR_ID}},
                'actual_fans': {'normal': 2, 'popular': 501},
            }))
            result = folder / 'mixed.json'
            cmd = [sys.executable, str(SCRIPT), '--url', self.url,
                   '--run-id', 'fixture', '--fans-manifest', str(manifest),
                   '--writer-role', role, '--seconds', '1', '--readers', '1',
                   '--write-interval-ms', '1000', '--visibility-timeout', '1',
                   '--out', str(result)]
            proc = subprocess.run(cmd, capture_output=True, text=True,
                                  env={**os.environ, 'CONVO_TEST_TOKEN': 'reader-fixture',
                                       'CONVO_TEST_WRITER_TOKEN': fake_jwt(writer_id)},
                                  timeout=15)
            return proc, json.loads(result.read_text()) if result.exists() else None

    def test_wrong_writer_fails_before_first_request_or_report(self):
        proc, report = self.invoke('popular', NORMAL_ID)
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn('writer identity mismatch', proc.stderr)
        self.assertIn('--writer-role popular', proc.stderr)
        self.assertIsNone(report)
        self.assertEqual(Handler.post_count, 0)
        self.assertEqual(Handler.get_count, 0)

    def test_normal_writer_matches_two_fan_manifest(self):
        proc, report = self.invoke('normal', NORMAL_ID)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(report['fans']['role'], 'normal')
        self.assertEqual(report['fans']['writer_id'], NORMAL_ID)
        self.assertEqual(report['fans']['actual_fans'], 2)
        self.assertGreaterEqual(report['post_total'], 1)
        self.assertGreaterEqual(Handler.post_count, 1)

    def test_popular_writer_matches_501_fan_manifest(self):
        proc, report = self.invoke('popular', POPULAR_ID)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(report['fans']['role'], 'popular')
        self.assertEqual(report['fans']['writer_id'], POPULAR_ID)
        self.assertEqual(report['fans']['actual_fans'], 501)
        self.assertGreaterEqual(report['post_total'], 1)
        self.assertGreaterEqual(Handler.post_count, 1)

    def test_invalid_writer_subject_fails_before_http(self):
        proc, report = self.invoke('normal', 'not-a-jwt')
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn('author ID must be a positive decimal int64 string', proc.stderr)
        self.assertIsNone(report)
        self.assertEqual(Handler.post_count, 0)
        self.assertEqual(Handler.get_count, 0)


if __name__ == '__main__':
    unittest.main()
