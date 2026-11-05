"""Subprocess tests against a local HTTP fixture, without real backend services."""
import json
import os
import subprocess
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SCRIPT = Path(__file__).with_name('load_feed.py')


class Handler(BaseHTTPRequestHandler):
    response_code = 1005
    next_cursor = None
    requests = 0

    def do_GET(self):
        type(self).requests += 1
        body = {'code': self.response_code, 'data': {'items': [{'id': '123'}],
                                                    'next_cursor': self.next_cursor}}
        content = json.dumps(body).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(content)))
        self.end_headers()
        self.wfile.write(content)

    def log_message(self, *_):
        pass


class LoadFeedFailureTests(unittest.TestCase):
    def setUp(self):
        Handler.requests = 0
        Handler.next_cursor = None
        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)

    def invoke(self, code):
        Handler.response_code = code
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'feed.json'
            cmd = [sys.executable, str(SCRIPT), '--url',
                   f'http://127.0.0.1:{self.server.server_port}',
                   '--warmup', '0', '--concurrency', '1', '--per-worker', '2',
                   '--depth', '2', '--out', str(output)]
            proc = subprocess.run(cmd, capture_output=True, text=True,
                                  env={**os.environ, 'CONVO_TEST_TOKEN': 'fixture'}, timeout=10)
            self.assertTrue(output.is_file(), proc.stderr)
            return proc, json.loads(output.read_text())

    def test_business_error_retains_first_sample(self):
        proc, data = self.invoke(1005)
        self.assertNotEqual(proc.returncode, 0)
        self.assertEqual(data['failure_reason'], 'request_failed')
        self.assertEqual(data['error_counts'], {'business_1005': 1})
        self.assertEqual(data['requests'], 1)
        self.assertEqual(data['observations'][0]['error'], 'business_1005')
        self.assertFalse(data['completed'])

    def test_per_worker_below_depth_rejected_before_http(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'feed.json'
            cmd = [sys.executable, str(SCRIPT), '--url',
                   f'http://127.0.0.1:{self.server.server_port}',
                   '--warmup', '5', '--concurrency', '1', '--per-worker', '1',
                   '--depth', '2', '--out', str(output)]
            proc = subprocess.run(cmd, capture_output=True, text=True,
                                  env={**os.environ, 'CONVO_TEST_TOKEN': 'fixture'}, timeout=10)
            self.assertNotEqual(proc.returncode, 0)
            self.assertIn('--per-worker must be >= --depth', proc.stderr)
            self.assertEqual(Handler.requests, 0)
            self.assertFalse(output.exists(), 'invalid configuration must not create a success report')

    def test_insufficient_depth_is_not_request_failure(self):
        Handler.next_cursor = None
        proc, data = self.invoke(1000)
        self.assertNotEqual(proc.returncode, 0)
        self.assertEqual(data['failure_reason'], 'insufficient_depth')
        self.assertEqual(data['requests'], 1)
        self.assertEqual(data['errors'], 0)
        self.assertFalse(data['completed'])


if __name__ == '__main__':
    unittest.main()
