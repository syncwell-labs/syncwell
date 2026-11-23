"""End-to-end test for the dependency-free Python client.

Run with: python3 -m unittest clients/python/test_client_e2e.py -v
It boots the real Go server from bin/syncwell and exercises two Python clients
(including an offline edit and reconnect).
"""

import os
import pathlib
import subprocess
import sys
import threading
import time
import unittest
import urllib.request

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
from syncwell_client import Syncwell  # noqa: E402

ROOT = pathlib.Path(__file__).resolve().parents[2]
BIN = ROOT / "bin" / "syncwell"
PORT = 8131
BASE = f"ws://127.0.0.1:{PORT}/ws"


def wait_healthy(timeout=10.0):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{PORT}/healthz", timeout=0.5) as res:
                if res.status == 200:
                    return
        except Exception:
            time.sleep(0.1)
    raise RuntimeError("server did not become healthy")


class PythonClientE2E(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not BIN.exists():
            raise unittest.SkipTest(f"missing {BIN}; run make build")
        cls.srv = subprocess.Popen([str(BIN), "-addr", f"127.0.0.1:{PORT}"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        wait_healthy()

    @classmethod
    def tearDownClass(cls):
        cls.srv.kill()
        cls.srv.wait(timeout=5)

    def test_token_url_construction(self):
        # Default: token is NOT put into the URL query parameters
        client_default = Syncwell("ws://localhost:8080/ws", "room1", token="mytoken")
        self.assertNotIn("token=", client_default._url())

        # Opt-in: allow_query_token=True puts token into the URL query parameters
        client_query = Syncwell("ws://localhost:8080/ws", "room1", token="mytoken", allow_query_token=True)
        self.assertIn("token=mytoken", client_query._url())

    def test_follow_owner_resolves_route_without_query_token(self):
        seen = {}

        class Response:
            def __enter__(self):
                return self

            def __exit__(self, *_args):
                return False

            def read(self):
                return b'{"node":"node-b","url":"wss://owner.example/ws"}'

        def open_route(url, timeout):
            seen["url"] = url
            seen["timeout"] = timeout
            return Response()

        client = Syncwell("ws://seed.example/ws", "room one", token="secret", follow_owner=True, route_opener=open_route)
        owner = client._resolve_owner_url()
        self.assertEqual(owner, "wss://owner.example/ws")
        self.assertIn("/route?room=room+one", seen["url"])
        self.assertNotIn("token", seen["url"])
        self.assertNotIn("token=", client._url(owner))

    def test_two_clients_converge_and_offline_edits_survive_reconnect(self):
        a = Syncwell(BASE, "python-e2e", client_id="py-A")
        b = Syncwell(BASE, "python-e2e", client_id="py-B")
        a.connect()
        b.connect()
        self.assertTrue(a.wait_ready(5), "A did not receive a snapshot")
        self.assertTrue(b.wait_ready(5), "B did not receive a snapshot")

        a.set("title", "hello from Python")
        self._eventually(lambda: b.get("title") == "hello from Python", "B did not see A's write")

        # B goes offline, edits locally, reconnects and ships its state.
        b.close()
        time.sleep(0.1)
        b.set("offline", "kept")
        self.assertEqual(b.get("offline"), "kept")
        b.connect()
        self.assertTrue(b.wait_ready(5))
        self._eventually(lambda: a.get("offline") == "kept", "offline edit was not merged after reconnect")

        self.assertEqual(a.live(), b.live())
        a.close()
        b.close()

    def test_concurrent_set_and_receive(self):
        client = Syncwell("ws://localhost:8080/ws", "concurrency-room", client_id="py-concurrent")
        client._ready.set()
        sent_frames = []
        lock = threading.Lock()

        class MockWS:
            def send(self, text):
                with lock:
                    sent_frames.append(text)

            def close(self):
                pass

        client._ws = MockWS()

        errors = []

        def worker_writer(thread_id, n_ops):
            try:
                for i in range(n_ops):
                    client.set(f"k-{thread_id}-{i}", f"val-{i}")
            except Exception as e:
                errors.append(e)

        def worker_receiver(n_frames):
            try:
                for i in range(n_frames):
                    client._receive({
                        "protocol": 1,
                        "type": "op",
                        "key": f"remote-{i}",
                        "register": {
                            "v": f"remote-val-{i}",
                            "t": {"c": i + 1, "n": "remote-node"},
                        },
                    })
            except Exception as e:
                errors.append(e)

        threads = []
        for t_id in range(4):
            threads.append(threading.Thread(target=worker_writer, args=(t_id, 50)))
        for _ in range(2):
            threads.append(threading.Thread(target=worker_receiver, args=(50,)))

        for t in threads:
            t.start()
        for t in threads:
            t.join()

        self.assertEqual(errors, [])
        self.assertGreater(client.clock.counter, 0)
        snapshot = client.snapshot()
        self.assertGreaterEqual(len(snapshot), 250)

    def test_concurrent_writes_live_server(self):
        a = Syncwell(BASE, "python-concurrent-e2e", client_id="py-A")
        b = Syncwell(BASE, "python-concurrent-e2e", client_id="py-B")
        a.connect()
        b.connect()
        self.assertTrue(a.wait_ready(5))
        self.assertTrue(b.wait_ready(5))

        errors = []

        def do_writes(client, prefix, count):
            try:
                for i in range(count):
                    client.set(f"{prefix}-{i}", i)
            except Exception as e:
                errors.append(e)

        t1 = threading.Thread(target=do_writes, args=(a, "a", 30))
        t2 = threading.Thread(target=do_writes, args=(b, "b", 30))
        t1.start()
        t2.start()
        t1.join()
        t2.join()

        self.assertEqual(errors, [])
        self._eventually(lambda: len(a.live()) == 60 and len(b.live()) == 60, "clients did not converge on 60 items")
        self.assertEqual(a.live(), b.live())
        a.close()
        b.close()

    def _eventually(self, predicate, message, timeout=3.0):
        deadline = time.time() + timeout
        while time.time() < deadline:
            if predicate():
                return
            time.sleep(0.05)
        self.fail(message)


if __name__ == "__main__":
    unittest.main()
