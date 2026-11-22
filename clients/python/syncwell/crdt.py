"""Pure Python CRDT primitives for Syncwell.

Implements zero-allocation Lamport logical clocks and join-semilattice
Last-Write-Wins Maps (LWWMap) adhering to Syncwell CRDT commutativity,
associativity, and idempotence laws.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any, Dict, Optional, Tuple


def canonical_json(value: Any) -> str:
    """Deterministic JSON formatting used for deterministic content tie-breaking."""
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def register_after(a: Dict[str, Any], b: Dict[str, Any]) -> bool:
    """Deterministic total order for LWW register conflict resolution:

    1. Higher logical counter wins.
    2. Higher lexicographical node ID wins.
    3. Delete (tombstone) beats write on equal timestamp.
    4. Deterministic content tie-break (canonical JSON).
    """
    ta, tb = a.get("t", {}), b.get("t", {})
    ca, cb = ta.get("c", 0), tb.get("c", 0)
    if ca != cb:
        return ca > cb
    na, nb = ta.get("n", ""), tb.get("n", "")
    if na != nb:
        return na > nb
    da, db = bool(a.get("d")), bool(b.get("d"))
    if da != db:
        return da
    return canonical_json(a.get("v")) > canonical_json(b.get("v"))


class Clock:
    """Lamport logical clock per node replica."""

    def __init__(self, node_id: str):
        self.node_id = str(node_id)
        self.counter = 0

    def tick(self) -> Dict[str, Any]:
        """Advance the clock and return a new timestamp dictionary."""
        self.counter += 1
        return {"c": self.counter, "n": self.node_id}

    def observe(self, ts: Optional[Dict[str, Any]]) -> None:
        """Observe a remote timestamp and ensure our clock stays strictly monotonic."""
        if isinstance(ts, dict):
            c = ts.get("c")
            if isinstance(c, int) and c > self.counter:
                self.counter = c


class LWWMap:
    """Last-Writer-Wins Map with tombstone-based deletion."""

    def __init__(self):
        self.entries: Dict[str, Dict[str, Any]] = {}

    def get(self, key: str, fallback: Any = None) -> Any:
        """Get live value or fallback if absent or deleted."""
        reg = self.entries.get(key)
        if reg is not None and not reg.get("d"):
            return reg.get("v")
        return fallback

    def set(self, key: str, value: Any, ts: Dict[str, Any]) -> bool:
        """Set a value with timestamp."""
        return self.merge_register(key, {"v": value, "t": ts})

    def delete(self, key: str, ts: Dict[str, Any]) -> bool:
        """Delete a key by inserting a tombstone."""
        return self.merge_register(key, {"d": True, "t": ts})

    def merge_register(self, key: str, register: Dict[str, Any]) -> bool:
        """Merge a single incoming register according to LWW total order rules."""
        cur = self.entries.get(key)
        if cur is None or register_after(register, cur):
            self.entries[key] = dict(register)
            return True
        return False

    def merge(self, state: Dict[str, Dict[str, Any]]) -> None:
        """Merge a complete dictionary of registers."""
        for k, reg in state.items():
            self.merge_register(k, reg)

    def live(self) -> Dict[str, Any]:
        """Return non-tombstone dictionary view."""
        return {k: reg.get("v") for k, reg in self.entries.items() if not reg.get("d")}

    def snapshot(self) -> Dict[str, Dict[str, Any]]:
        """Return full register state dictionary including tombstones."""
        return {k: dict(reg) for k, reg in self.entries.items()}
