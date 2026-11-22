"""Backward compatibility module for syncwell_client.

All implementations now live under the `syncwell` package.
"""

from syncwell import (
    Clock,
    LWWMap,
    Syncwell,
    SyncwellClient,
    SyncwellError,
    canonical_json,
    register_after,
)

__all__ = [
    "SyncwellClient",
    "Syncwell",
    "Clock",
    "LWWMap",
    "SyncwellError",
    "canonical_json",
    "register_after",
]
