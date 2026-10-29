"""Minimal, dependency-free Python client for the Syncwell realtime server.

This is the polyglot proof: it speaks the same JSON-over-WebSocket protocol and
runs the same LWW merge rule as the Go server and the JavaScript SDK. The
WebSocket implementation uses only the standard library, so ``pip install`` is
not required.

    from syncwell_client import Syncwell
    # Default: token sent via Authorization header (no query string).
    # Use allow_query_token=True for legacy server compatibility.
    sw = Syncwell("ws://localhost:8080/ws", "my-room", client_id="py-1",
                  allow_query_token=False)
    sw.connect()
    sw.set("title", "hello from Python")
    print(sw.live())
    sw.close()
"""

from __future__ import annotations

import base64
import hashlib
import json
import os
import socket
import ssl
import struct
import threading
import time
import urllib.request
from urllib.parse import urlencode, urlparse

PROTOCOL_VERSION = 1


class SyncwellError(RuntimeError):
    def __init__(self, code: str, message: str, cause: Exception | None = None):
        super().__init__(message)
        self.code = code
        self.cause = cause


def _canonical(value) -> str:
    """Deterministic JSON used for equal-timestamp tie-breaking."""
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def _register_after(a: dict, b: dict) -> bool:
    ta, tb = a.get("t", {}), b.get("t", {})
    if ta.get("c", 0) != tb.get("c", 0):
        return ta.get("c", 0) > tb.get("c", 0)
    if ta.get("n", "") != tb.get("n", ""):
        return ta.get("n", "") > tb.get("n", "")
    if bool(a.get("d")) != bool(b.get("d")):
        return bool(a.get("d"))
    return _canonical(a.get("v")) > _canonical(b.get("v"))


class _WebSocket:
    """Tiny RFC 6455 client: text frames, ping/pong, close, fragmentation."""

    def __init__(self, url: str, timeout: float = 10.0, headers: dict | None = None):
        parsed = urlparse(url)
        if parsed.scheme not in ("ws", "wss"):
            raise SyncwellError("BAD_URL", f"unsupported scheme {parsed.scheme!r}")
        self.secure = parsed.scheme == "wss"
        self.host = parsed.hostname or "localhost"
        self.port = parsed.port or (443 if self.secure else 80)
        self.path = parsed.path or "/"
        if parsed.query:
            self.path += "?" + parsed.query
        self.timeout = timeout
        self.sock = None
        self._buffer = b""
        self.headers = headers or {}

    def connect(self):
        raw = socket.create_connection((self.host, self.port), timeout=self.timeout)
        if self.secure:
            raw = ssl.create_default_context().wrap_socket(raw, server_hostname=self.host)
        self.sock = raw
        key = base64.b64encode(os.urandom(16)).decode()
        request = (
            f"GET {self.path} HTTP/1.1\r\n"
            f"Host: {self.host}:{self.port}\r\n"
            "Upgrade: websocket\r\n"
            "Connection: Upgrade\r\n"
            f"Sec-WebSocket-Key: {key}\r\n"
            "Sec-WebSocket-Version: 13\r\n"
        )
        for name, value in self.headers.items():
            request += f"{name}: {value}\r\n"
        request += "\r\n"
        self.sock.sendall(request.encode())
        response = b""
        while b"\r\n\r\n" not in response:
            chunk = self.sock.recv(4096)
            if not chunk:
                raise SyncwellError("HANDSHAKE", "connection closed during websocket handshake")
            response += chunk
        head, _, rest = response.partition(b"\r\n\r\n")
        status = head.split(b"\r\n", 1)[0]
        if b" 101 " not in status:
            raise SyncwellError("HANDSHAKE", f"server rejected websocket upgrade: {status.decode(errors='replace')}")
        accept = None
        for line in head.split(b"\r\n")[1:]:
            name, _, value = line.partition(b":")
            if name.strip().lower() == b"sec-websocket-accept":
                accept = value.strip().decode()
        expected = base64.b64encode(
            hashlib.sha1((key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode()).digest()
        ).decode()
        if accept != expected:
            raise SyncwellError("HANDSHAKE", "invalid Sec-WebSocket-Accept")
        self._buffer = rest

    def _send_frame(self, opcode: int, payload: bytes):
        mask = os.urandom(4)
        header = bytearray([0x80 | opcode])
        n = len(payload)
        if n < 126:
            header.append(0x80 | n)
        elif n < 1 << 16:
            header.append(0x80 | 126)
            header += struct.pack("!H", n)
        else:
            header.append(0x80 | 127)
            header += struct.pack("!Q", n)
        header += mask
        masked = bytes(b ^ mask[i % 4] for i, b in enumerate(payload))
        self.sock.sendall(bytes(header) + masked)

    def send(self, text: str):
        self._send_frame(0x1, text.encode())

    def _read_exact(self, n: int) -> bytes:
        while len(self._buffer) < n:
            chunk = self.sock.recv(65536)
            if not chunk:
                raise ConnectionError("websocket closed")
            self._buffer += chunk
        out, self._buffer = self._buffer[:n], self._buffer[n:]
        return out

    def recv(self) -> str | None:
        message = b""
        while True:
            first, second = self._read_exact(2)
            fin = bool(first & 0x80)
            opcode = first & 0x0F
            masked = bool(second & 0x80)
            length = second & 0x7F
            if length == 126:
                length = struct.unpack("!H", self._read_exact(2))[0]
            elif length == 127:
                length = struct.unpack("!Q", self._read_exact(8))[0]
            mask = self._read_exact(4) if masked else None
            payload = self._read_exact(length)
            if mask:
                payload = bytes(b ^ mask[i % 4] for i, b in enumerate(payload))
            if opcode == 0x8:  # close
                try:
                    self._send_frame(0x8, b"")
                except OSError:
                    pass
                return None
            if opcode == 0x9:  # ping
                self._send_frame(0xA, payload)
                continue
            if opcode == 0xA:  # pong
                continue
            if opcode in (0x1, 0x2, 0x0):
                message += payload
                if fin:
                    return message.decode(errors="replace")

    def close(self):
        try:
            if self.sock:
                self._send_frame(0x8, b"")
        except OSError:
            pass
        try:
            if self.sock:
                self.sock.close()
        finally:
            self.sock = None


class Syncwell:
    def __init__(
        self,
        base_url: str,
        room: str = "default",
        *,
        token: str | None = None,
        client_id: str | None = None,
        WebSocket=None,
        allow_query_token: bool = False,
        follow_owner: bool = False,
        route_timeout: float = 5.0,
        route_opener=None,
    ):
        self.base_url = base_url
        self.room = room
        self.token = token
        self.allow_query_token = allow_query_token
        self.follow_owner = follow_owner
        self.route_timeout = route_timeout
        self._route_opener = route_opener or urllib.request.urlopen
        self.client_id = client_id or f"py-{os.urandom(4).hex()}"
        self._ws_class = WebSocket or _WebSocket
        self._ws = None
        self._reader = None
        self._lock = threading.RLock()
        self._ready = threading.Event()
        self._closed = False
        self._handlers: dict[str, list] = {}
        self.clock = 0
        self.epoch = 1
        self.entries: dict[str, dict] = {}
        self.dirty: set[str] = set()

    # -- events ---------------------------------------------------------
    def on(self, event: str, callback):
        self._handlers.setdefault(event, []).append(callback)
        return self

    def off(self, event: str, callback=None):
        if callback is None:
            self._handlers.pop(event, None)
        else:
            self._handlers[event] = [cb for cb in self._handlers.get(event, []) if cb is not callback]
        return self

    def _emit(self, event: str, payload=None):
        for callback in list(self._handlers.get(event, [])):
            callback(payload)

    # -- connection -----------------------------------------------------
    def _url(self, base_url: str | None = None) -> str:
        params = {"room": self.room}
        if self.token and self.allow_query_token:
            params["token"] = self.token
        base = base_url or self.base_url
        sep = "&" if "?" in base else "?"
        return f"{base}{sep}{urlencode(params)}"

    def _resolve_owner_url(self) -> str:
        parsed = urlparse(self.base_url)
        if parsed.scheme not in ("ws", "wss") or not parsed.netloc:
            raise ValueError("follow_owner requires an absolute ws:// or wss:// base URL")
        scheme = "https" if parsed.scheme == "wss" else "http"
        route_url = f"{scheme}://{parsed.netloc}/route?{urlencode({'room': self.room})}"
        with self._route_opener(route_url, timeout=self.route_timeout) as response:
            payload = json.loads(response.read().decode("utf-8"))
        owner_url = payload.get("url") if isinstance(payload, dict) else None
        owner = urlparse(owner_url or "")
        if owner.scheme not in ("ws", "wss") or not owner.netloc:
            raise ValueError("route response must contain an absolute ws:// or wss:// URL")
        return owner_url

    def connect(self):
        if self._ws is not None:
            return self
        self._closed = False
        self._ready.clear()
        headers = {}
        if self.token and not self.allow_query_token:
            headers["Authorization"] = f"Bearer {self.token}"
        base_url = self._resolve_owner_url() if self.follow_owner else self.base_url
        ws = self._ws_class(self._url(base_url), headers=headers)
        ws.connect()
        with self._lock:
            self._ws = ws
        self._reader = threading.Thread(target=self._read_loop, daemon=True)
        self._reader.start()
        return self

    def close(self):
        self._closed = True
        self._ready.clear()
        with self._lock:
            ws, self._ws = self._ws, None
        if ws:
            try:
                ws.close()
            except OSError:
                pass

    def wait_ready(self, timeout: float = 5.0) -> bool:
        return self._ready.wait(timeout)

    def _read_loop(self):
        while not self._closed:
            ws = self._ws
            if ws is None:
                return
            try:
                data = ws.recv()
            except (OSError, ConnectionError):
                data = None
            if data is None:
                with self._lock:
                    if self._ws is ws:
                        self._ws = None
                self._ready.clear()
                self._emit("close")
                return
            try:
                frame = json.loads(data)
            except json.JSONDecodeError as exc:
                self._emit("error", SyncwellError("BAD_FRAME", "bad server frame", exc))
                continue
            self._receive(frame)

    def _send_raw(self, frame: dict):
        ws = self._ws
        if ws is None:
            return
        payload = {**frame, "protocol": PROTOCOL_VERSION, "epoch": self.epoch}
        ws.send(json.dumps(payload, separators=(",", ":")))

    def _send(self, frame: dict):
        if self._ready.is_set():
            self._send_raw(frame)

    def _receive(self, frame: dict):
        if frame.get("protocol") not in (None, PROTOCOL_VERSION):
            self._emit("error", SyncwellError("UNSUPPORTED_PROTOCOL", f"unsupported protocol {frame.get('protocol')}"))
            return
        kind = frame.get("type")
        if kind == "snapshot":
            self.epoch = max(1, int(frame.get("epoch") or self.epoch))
            self._merge_state(frame.get("state") or {})
            if not self._ready.is_set():
                # Ship offline edits, then acknowledge so GC can advance.
                if self.dirty:
                    self._send_raw({"type": "merge", "state": {k: self.entries[k] for k in self.dirty if k in self.entries}})
                    self.dirty.clear()
                self._ready.set()
                self._send_raw({"type": "ack", "clock": self.clock})
                self._emit("open")
            self._emit("change", self.live())
        elif kind == "op":
            reg = frame.get("register") or {}
            self.clock = max(self.clock, int(reg.get("t", {}).get("c", 0)))
            self._merge_register(frame.get("key"), reg)
            self._emit("change", self.live())
        elif kind == "presence":
            self._emit("presence", frame)
        elif kind == "leave":
            self._emit("leave", frame)
        elif kind == "error":
            self._emit("error", SyncwellError("SERVER_ERROR", str(frame.get("data") or "server error")))

    # -- CRDT -----------------------------------------------------------
    def _merge_register(self, key: str, reg: dict):
        if not key:
            return
        cur = self.entries.get(key)
        if cur is None or _register_after(reg, cur):
            self.entries[key] = reg

    def _merge_state(self, state: dict):
        for key, reg in state.items():
            self._merge_register(key, reg)

    def _tick(self) -> dict:
        self.clock += 1
        return {"c": self.clock, "n": self.client_id}

    def _write(self, key: str, reg: dict):
        self._merge_register(key, reg)
        self.dirty.add(key)
        self._send({"type": "op", "key": key, "register": reg})
        self._emit("change", self.live())

    def set(self, key: str, value):
        self._write(key, {"v": value, "t": self._tick()})

    def delete(self, key: str):
        self._write(key, {"t": self._tick(), "d": True})

    def presence(self, data):
        self._send({"type": "presence", "data": data})

    def get(self, key: str):
        reg = self.entries.get(key)
        if not reg or reg.get("d"):
            return None
        return reg.get("v")

    def live(self) -> dict:
        return {k: r.get("v") for k, r in self.entries.items() if not r.get("d")}

    def snapshot(self) -> dict:
        return dict(self.entries)

    def __enter__(self):
        return self.connect()

    def __exit__(self, *_exc):
        self.close()
