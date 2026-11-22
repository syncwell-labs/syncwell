"""Unit tests for Python CRDT engine: Clock and LWWMap."""

import pathlib
import sys
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
from syncwell.crdt import Clock, LWWMap, register_after


class TestClock(unittest.TestCase):
    def test_clock_tick(self):
        clk = Clock("node-1")
        self.assertEqual(clk.counter, 0)
        ts1 = clk.tick()
        self.assertEqual(ts1, {"c": 1, "n": "node-1"})
        ts2 = clk.tick()
        self.assertEqual(ts2, {"c": 2, "n": "node-1"})

    def test_clock_observe(self):
        clk = Clock("node-1")
        clk.observe({"c": 10, "n": "node-2"})
        self.assertEqual(clk.counter, 10)
        ts = clk.tick()
        self.assertEqual(ts, {"c": 11, "n": "node-1"})


class TestLWWMap(unittest.TestCase):
    def test_lww_set_get_delete(self):
        m = LWWMap()
        m.set("k1", "v1", {"c": 1, "n": "a"})
        self.assertEqual(m.get("k1"), "v1")
        self.assertEqual(m.live(), {"k1": "v1"})

        # Higher timestamp delete
        m.delete("k1", {"c": 2, "n": "a"})
        self.assertIsNone(m.get("k1"))
        self.assertEqual(m.live(), {})

        # Lower timestamp write ignored
        m.set("k1", "v0", {"c": 1, "n": "a"})
        self.assertIsNone(m.get("k1"))

    def test_lww_commutativity(self):
        m1 = LWWMap()
        m2 = LWWMap()

        m1.set("key", "val-A", {"c": 5, "n": "node-a"})
        m2.set("key", "val-B", {"c": 10, "n": "node-b"})

        # Merge in both directions
        left = LWWMap()
        left.merge(m1.snapshot())
        left.merge(m2.snapshot())

        right = LWWMap()
        right.merge(m2.snapshot())
        right.merge(m1.snapshot())

        self.assertEqual(left.live(), right.live())
        self.assertEqual(left.get("key"), "val-B")

    def test_lww_equal_timestamp_tiebreak(self):
        # When counters match, higher node wins
        r1 = {"v": "val-1", "t": {"c": 5, "n": "node-a"}}
        r2 = {"v": "val-2", "t": {"c": 5, "n": "node-b"}}
        self.assertTrue(register_after(r2, r1))

        # When counter and node match, delete wins over write
        del_reg = {"d": True, "t": {"c": 5, "n": "node-a"}}
        self.assertTrue(register_after(del_reg, r1))


if __name__ == "__main__":
    unittest.main()
