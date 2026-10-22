"""End-to-end test for the dependency-free Python client.

Run with: python3 -m unittest clients/python/test_client_e2e.py -v
It boots the real Go server from bin/syncwell and exercises two Python clients
(including an offline edit and reconnect).
"""

import os
import pathlib
import subprocess
import sys
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

    def _eventually(self, predicate, message, timeout=3.0):
        deadline = time.time() + timeout
        while time.time() < deadline:
            if predicate():
                return
            time.sleep(0.05)
        self.fail(message)


if __name__ == "__main__":
    unittest.main()
