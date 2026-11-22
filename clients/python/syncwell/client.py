"""Minimal, dependency-free Python client for the Syncwell realtime server.

Speaks the same JSON-over-WebSocket protocol and runs the same LWW merge
rule as the Go server and JavaScript SDK.
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
from typing import Any, Callable, Dict, List, Optional, Set
from urllib.parse import quote, urlencode, urlparse

from .crdt import Clock, LWWMap, canonical_json, register_after

PROTOCOL_VERSION = 1
MAX_WEBSOCKET_HANDSHAKE_BYTES = 64 * 1024


class SyncwellError(RuntimeError):
    def __init__(self, code: str, message: str, cause: Optional[Exception] = None):
        super().__init__(message)
        self.code = code
        self.cause = cause


def _canonical(value: Any) -> str:
    return canonical_json(value)


def _register_after(a: Dict[str, Any], b: Dict[str, Any]) -> bool:
    return register_after(a, b)


class _WebSocket:
    """Tiny RFC 6455 client: text frames, ping/pong, close, fragmentation."""

    def __init__(self, url: str, timeout: float = 10.0, headers: Optional[Dict[str, str]] = None):
        if not isinstance(url, str) or any(ord(char) <= 32 or ord(char) == 127 for char in url):
            raise SyncwellError("BAD_URL", "websocket URL contains invalid characters")
        try:
            parsed = urlparse(url)
            hostname = parsed.hostname
            explicit_port = parsed.port
        except ValueError as exc:
            raise SyncwellError("BAD_URL", "websocket URL has an invalid authority", exc) from exc
        if parsed.scheme not in ("ws", "wss") or not hostname:
            raise SyncwellError("BAD_URL", f"unsupported scheme {parsed.scheme!r}")
        if parsed.username is not None or parsed.password is not None or parsed.fragment:
            raise SyncwellError("BAD_URL", "websocket URL must not contain credentials or a fragment")
        self.secure = parsed.scheme == "wss"
        try:
            self.host = hostname if ":" in hostname else hostname.encode("idna").decode("ascii")
        except UnicodeError as exc:
            raise SyncwellError("BAD_URL", "websocket hostname is invalid", exc) from exc
        self.port = explicit_port if explicit_port is not None else (443 if self.secure else 80)
        self.host_header = f"[{self.host}]" if ":" in self.host else self.host
        if explicit_port is not None:
            self.host_header += f":{explicit_port}"
        self.path = quote(parsed.path or "/", safe="/%:@!$&'()*+,;=-._~")
        if parsed.query:
            self.path += "?" + quote(parsed.query, safe="/%?:@!$&'()*+,;=-._~")
        self.timeout = timeout
        self.sock: Optional[socket.socket] = None
        self._buffer = b""
        self.headers: Dict[str, str] = {}
        for name, value in (headers or {}).items():
            if not isinstance(name, str) or not name or any(
                char not in "!#$%&'*+-.^_`|~"
                and not ("0" <= char <= "9")
                and not ("A" <= char <= "Z")
                and not ("a" <= char <= "z")
                for char in name
            ):
                raise SyncwellError("BAD_HEADER", f"invalid header name {name!r}")
            if not isinstance(value, str) or any(ord(c) in (10, 13) or (ord(c) < 32 and ord(c) != 9) for c in value):
                raise SyncwellError("BAD_HEADER", f"invalid header value for {name!r}")
            self.headers[name] = value

    def connect(self) -> None:
        raw_sock = socket.create_connection((self.host, self.port), timeout=self.timeout)
        if self.secure:
            ctx = ssl.create_default_context()
            self.sock = ctx.wrap_socket(raw_sock, server_hostname=self.host)
        else:
            self.sock = raw_sock
        self.sock.settimeout(self.timeout)

        key = base64.b64encode(os.urandom(16)).decode("ascii")
        req_lines = [
            f"GET {self.path} HTTP/1.1",
            f"Host: {self.host_header}",
            "Upgrade: websocket",
            "Connection: Upgrade",
            f"Sec-WebSocket-Key: {key}",
            "Sec-WebSocket-Version: 13",
        ]
        for name, value in self.headers.items():
            req_lines.append(f"{name}: {value}")
        req_lines.extend(["", ""])
        self.sock.sendall("\r\n".join(req_lines).encode("latin-1"))

        resp = b""
        while b"\r\n\r\n" not in resp:
            chunk = self.sock.recv(4096)
            if not chunk:
                raise SyncwellError("HANDSHAKE_FAILED", "server closed connection during handshake")
            resp += chunk
            if len(resp) > MAX_WEBSOCKET_HANDSHAKE_BYTES:
                raise SyncwellError("HANDSHAKE_FAILED", "websocket handshake exceeded maximum size")
        header_bytes, self._buffer = resp.split(b"\r\n\r\n", 1)
        headers_text = header_bytes.decode("latin-1", errors="replace")
        status_line = headers_text.split("\r\n")[0]
        if " 101 " not in status_line:
            raise SyncwellError("HANDSHAKE_FAILED", f"handshake failed: {status_line}")

        accept_expected = base64.b64encode(
            hashlib.sha1((key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode("ascii")).digest()
        ).decode("ascii")
        headers = {}
        for line in headers_text.split("\r\n")[1:]:
            if ":" in line:
                k, v = line.split(":", 1)
                headers[k.strip().lower()] = v.strip()
        if headers.get("sec-websocket-accept") != accept_expected:
            raise SyncwellError("HANDSHAKE_FAILED", "invalid Sec-WebSocket-Accept")

    def _read_exact(self, n: int) -> bytes:
        while len(self._buffer) < n:
            if not self.sock:
                raise SyncwellError("SOCKET_CLOSED", "socket is not connected")
            chunk = self.sock.recv(max(4096, n - len(self._buffer)))
            if not chunk:
                raise SyncwellError("SOCKET_CLOSED", "socket closed by peer")
            self._buffer += chunk
        out, self._buffer = self._buffer[:n], self._buffer[n:]
        return out

    def recv(self) -> Optional[str]:
        fragments = []
        while True:
            head = self._read_exact(2)
            b1, b2 = head[0], head[1]
            fin = bool(b1 & 0x80)
            opcode = b1 & 0x0F
            masked = bool(b2 & 0x80)
            length = b2 & 0x7F
            if length == 126:
                length = struct.unpack("!H", self._read_exact(2))[0]
            elif length == 127:
                length = struct.unpack("!Q", self._read_exact(8))[0]
            mask_key = self._read_exact(4) if masked else None
            payload = self._read_exact(length)
            if masked and mask_key:
                payload = bytes(b ^ mask_key[i % 4] for i, b in enumerate(payload))

            if opcode == 0x8:  # close
                self.close()
                return None
            elif opcode == 0x9:  # ping -> pong
                self._send_frame(0xA, payload)
                continue
            elif opcode == 0xA:  # pong
                continue
            elif opcode == 0x1:  # text
                fragments.append(payload)
                if fin:
                    return b"".join(fragments).decode("utf-8")
            elif opcode == 0x0:  # continuation
                fragments.append(payload)
                if fin:
                    return b"".join(fragments).decode("utf-8")

    def _send_frame(self, opcode: int, payload: bytes) -> None:
        if not self.sock:
            return
        fin = 0x80
        mask_bit = 0x80
        b1 = fin | opcode
        length = len(payload)
        mask_key = os.urandom(4)
        if length < 126:
            header = bytes([b1, mask_bit | length]) + mask_key
        elif length < 65536:
            header = bytes([b1, mask_bit | 126]) + struct.pack("!H", length) + mask_key
        else:
            header = bytes([b1, mask_bit | 127]) + struct.pack("!Q", length) + mask_key
        masked_payload = bytes(b ^ mask_key[i % 4] for i, b in enumerate(payload))
        self.sock.sendall(header + masked_payload)

    def send(self, text: str) -> None:
        self._send_frame(0x1, text.encode("utf-8"))

    def close(self) -> None:
        sock, self.sock = self.sock, None
        if sock:
            try:
                sock.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            try:
                sock.close()
            except OSError:
                pass


class SyncwellClient:
    """Production Syncwell client for Python backends, bots, and agents."""

    def __init__(
        self,
        base_url: str,
        room: str = "default",
        *,
        token: Optional[str] = None,
        client_id: Optional[str] = None,
        WebSocket: Any = None,
        allow_query_token: bool = False,
        follow_owner: bool = False,
        route_timeout: float = 5.0,
        route_opener: Any = None,
    ):
        if not isinstance(allow_query_token, bool):
            raise TypeError("allow_query_token must be a bool")
        if token is not None and not isinstance(token, str):
            raise TypeError("token must be a string or None")
        self.base_url = base_url
        self.room = room
        self.token = token
        self.allow_query_token = allow_query_token
        self.follow_owner = follow_owner
        self.route_timeout = route_timeout
        self._route_opener = route_opener or urllib.request.urlopen
        self.client_id = client_id or f"py-{os.urandom(4).hex()}"
        self._ws_class = WebSocket or _WebSocket
        self._ws: Optional[_WebSocket] = None
        self._reader: Optional[threading.Thread] = None
        self._lock = threading.RLock()
        self._ready = threading.Event()
        self._closed = False
        self._handlers: Dict[str, List[Callable]] = {}

        self.crdt = LWWMap()
        self.entries = self.crdt.entries
        self.clock = 0
        self.epoch = 1
        self.dirty: Set[str] = set()

    # -- events ---------------------------------------------------------
    def on(self, event: str, callback: Callable) -> SyncwellClient:
        self._handlers.setdefault(event, []).append(callback)
        return self

    def off(self, event: str, callback: Optional[Callable] = None) -> SyncwellClient:
        if callback is None:
            self._handlers.pop(event, None)
        else:
            self._handlers[event] = [cb for cb in self._handlers.get(event, []) if cb is not callback]
        return self

    def _emit(self, event: str, payload: Any = None) -> None:
        for callback in list(self._handlers.get(event, [])):
            callback(payload)

    # -- connection -----------------------------------------------------
    def _url(self, base_url: Optional[str] = None) -> str:
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

    def connect(self) -> SyncwellClient:
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

    def close(self) -> None:
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

    def _read_loop(self) -> None:
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

    def _send_raw(self, frame: Dict[str, Any]) -> None:
        ws = self._ws
        if ws is None:
            return
        payload = {**frame, "protocol": PROTOCOL_VERSION, "epoch": self.epoch}
        ws.send(json.dumps(payload, separators=(",", ":")))

    def _send(self, frame: Dict[str, Any]) -> None:
        if self._ready.is_set():
            self._send_raw(frame)

    def _receive(self, frame: Dict[str, Any]) -> None:
        if frame.get("protocol") not in (None, PROTOCOL_VERSION):
            self._emit("error", SyncwellError("UNSUPPORTED_PROTOCOL", f"unsupported protocol {frame.get('protocol')}"))
            return
        kind = frame.get("type")
        if kind == "snapshot":
            self.epoch = max(1, int(frame.get("epoch") or self.epoch))
            self._merge_state(frame.get("state") or {})
            if not self._ready.is_set():
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
    def _merge_register(self, key: Optional[str], reg: Dict[str, Any]) -> None:
        if not key:
            return
        self.crdt.merge_register(key, reg)

    def _merge_state(self, state: Dict[str, Dict[str, Any]]) -> None:
        self.crdt.merge(state)

    def _tick(self) -> Dict[str, Any]:
        self.clock += 1
        return {"c": self.clock, "n": self.client_id}

    def _write(self, key: str, reg: Dict[str, Any]) -> None:
        self._merge_register(key, reg)
        self.dirty.add(key)
        self._send({"type": "op", "key": key, "register": reg})
        self._emit("change", self.live())

    def set(self, key: str, value: Any) -> None:
        self._write(key, {"v": value, "t": self._tick()})

    def delete(self, key: str) -> None:
        self._write(key, {"t": self._tick(), "d": True})

    def presence(self, data: Any) -> None:
        self._send({"type": "presence", "data": data})

    def get(self, key: str, fallback: Any = None) -> Any:
        return self.crdt.get(key, fallback)

    def live(self) -> Dict[str, Any]:
        return self.crdt.live()

    def snapshot(self) -> Dict[str, Dict[str, Any]]:
        return self.crdt.snapshot()

    def __enter__(self) -> SyncwellClient:
        return self.connect()

    def __exit__(self, *_exc: Any) -> None:
        self.close()


Syncwell = SyncwellClient
