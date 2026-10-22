/* Shared demo helpers: room URL, welcome banner, share sheet, status colors.
   Classic script (not a module) so the kanban ?test=1 fake-SDK path still works. */
(function () {
  const WELCOMED_KEY = "syncwell-demo-welcomed";

  function isLocalHost() {
    const h = location.hostname;
    return h === "localhost" || h === "127.0.0.1" || h === "[::1]" || h === "::1";
  }

  function ensureStyles() {
    if (document.getElementById("demo-common-css")) return;
    if (document.querySelector('link[href="/demo-common.css"]')) return;
    const link = document.createElement("link");
    link.id = "demo-common-css";
    link.rel = "stylesheet";
    link.href = "/demo-common.css";
    document.head.appendChild(link);
  }

  // Stable per-client color (derived, not random) so a person keeps the same
  // color across reloads and pages.
  function hueOf(id) {
    let h = 0;
    for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) >>> 0;
    return h % 360;
  }

  // Human-readable peer label: raw client ids are noise to a visitor.
  function peerName(id, selfId) {
    if (id === selfId) return "You";
    return `Guest ${String(id).slice(-4)}`;
  }

  function room(prefix) {
    const params = new URLSearchParams(location.search);
    let id = params.get("room");
    if (!id) {
      id = prefix + "-" + Math.random().toString(36).slice(2, 10);
      params.set("room", id);
      history.replaceState(null, "", `${location.pathname}?${params.toString()}`);
    }
    return id;
  }

  function inviteURL(roomId, path) {
    const url = new URL(location.href);
    if (path) url.pathname = path;
    url.searchParams.set("room", roomId);
    return url.toString();
  }

  function nav() {
    const params = new URLSearchParams(location.search);
    const roomId = params.get("room");
    if (!roomId) return;
    for (const a of document.querySelectorAll(".demo-nav a[href]")) {
      try {
        const url = new URL(a.getAttribute("href"), location.origin);
        url.searchParams.set("room", roomId);
        a.setAttribute("href", url.pathname + url.search);
      } catch {
        /* leave external links alone */
      }
    }
  }

  function welcome() {
    ensureStyles();
    if (localStorage.getItem(WELCOMED_KEY)) return;
    const el = document.createElement("aside");
    el.className = "welcome";
    el.setAttribute("role", "note");
    el.innerHTML =
      '<div class="welcome-steps">' +
      "<h2>Try it live</h2>" +
      "<ol>" +
      "<li>Click <strong>Share</strong> and copy the invite link</li>" +
      "<li>Open it on a second device (same Wi-Fi)</li>" +
      "<li>Edit here — changes appear on the other screen instantly</li>" +
      "</ol>" +
      "</div>" +
      '<button type="button" class="btn dismiss">Got it</button>';
    el.querySelector(".dismiss").addEventListener("click", () => {
      localStorage.setItem(WELCOMED_KEY, "1");
      el.remove();
    });
    const main = document.querySelector("main");
    if (main) main.prepend(el);
  }

  function share(roomId) {
    ensureStyles();
    const btn = document.getElementById("share-btn");
    if (!btn) return;

    const sheet = document.createElement("div");
    sheet.className = "share-sheet";
    sheet.hidden = true;
    sheet.innerHTML =
      '<div class="share-panel" role="dialog" aria-modal="true" aria-labelledby="share-title">' +
      '<h2 id="share-title">Invite someone</h2>' +
      "<p>Open this link on another device to edit together in real time.</p>" +
      '<code class="share-url" id="share-url"></code>' +
      '<div class="share-actions">' +
      '<button type="button" class="btn primary" id="share-copy">Copy link</button>' +
      '<button type="button" class="btn" id="share-close">Close</button>' +
      "</div>" +
      '<div class="share-lan" id="share-lan" hidden>' +
      "<h3>On a phone or another computer?</h3>" +
      "<p>Use one of these addresses instead of localhost:</p>" +
      "<ul id=\"share-lan-list\"></ul>" +
      "</div>" +
      '<p class="share-room">Room: <code id="share-room"></code> — anyone with this link joins the session; the id is random and private.</p>' +
      "</div>";
    document.body.appendChild(sheet);

    const urlEl = sheet.querySelector("#share-url");
    const copyBtn = sheet.querySelector("#share-copy");
    const closeBtn = sheet.querySelector("#share-close");
    const lanBox = sheet.querySelector("#share-lan");
    const lanList = sheet.querySelector("#share-lan-list");
    const roomEl = sheet.querySelector("#share-room");

    const currentURL = () => inviteURL(roomId);
    let lastFocus = null;

    function open() {
      urlEl.textContent = currentURL();
      roomEl.textContent = roomId;
      if (isLocalHost()) {
        lanBox.hidden = false;
        loadLAN();
      }
      lastFocus = document.activeElement;
      sheet.hidden = false;
      const bg = document.querySelector("main");
      if (bg) bg.setAttribute("aria-hidden", "true");
      copyBtn.focus();
      document.addEventListener("keydown", onKey);
    }

    function close() {
      sheet.hidden = true;
      const bg = document.querySelector("main");
      if (bg) bg.removeAttribute("aria-hidden");
      document.removeEventListener("keydown", onKey);
      if (lastFocus && typeof lastFocus.focus === "function") lastFocus.focus();
    }

    function onKey(e) {
      if (e.key === "Escape") {
        close();
        return;
      }
      if (e.key !== "Tab") return;
      // Keep Tab inside the dialog while it is open.
      const items = Array.from(sheet.querySelectorAll("button")).filter(
        (b) => b.offsetParent !== null,
      );
      if (items.length === 0) return;
      const first = items[0];
      const last = items[items.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    }

    async function copyText(text, labelEl, original) {
      try {
        await navigator.clipboard.writeText(text);
        labelEl.textContent = "Copied!";
        labelEl.classList.add("copied");
      } catch {
        labelEl.textContent = "Copy failed";
      }
      setTimeout(() => {
        labelEl.textContent = original;
        labelEl.classList.remove("copied");
      }, 1500);
    }

    async function loadLAN() {
      if (lanList.childElementCount > 0) return;
      try {
        const res = await fetch("/demo/lan");
        if (!res.ok) return;
        const data = await res.json();
        const addrs = Array.isArray(data.addresses) ? data.addresses : [];
        for (const ip of addrs.slice(0, 4)) {
          const url = new URL(currentURL());
          url.hostname = ip;
          const li = document.createElement("li");
          const b = document.createElement("button");
          b.type = "button";
          b.textContent = url.toString();
          b.addEventListener("click", () => copyText(url.toString(), b, url.toString()));
          li.appendChild(b);
          lanList.appendChild(li);
        }
        if (addrs.length === 0) lanBox.hidden = true;
      } catch {
        lanBox.hidden = true;
      }
    }

    btn.addEventListener("click", open);
    closeBtn.addEventListener("click", close);
    sheet.addEventListener("click", (e) => {
      if (e.target === sheet) close();
    });
    copyBtn.addEventListener("click", () => copyText(currentURL(), copyBtn, "Copy link"));
  }

  function status(el) {
    let wasDown = false;
    return {
      connecting() {
        wasDown = false;
        el.textContent = "connecting…";
        el.className = "";
      },
      live() {
        el.className = "live";
        if (wasDown) {
          wasDown = false;
          el.textContent = "back live — synced";
          setTimeout(() => {
            if (el.className === "live" && el.textContent === "back live — synced") {
              el.textContent = "live";
            }
          }, 4000);
        } else {
          el.textContent = "live";
        }
      },
      offline() {
        wasDown = true;
        el.textContent = "offline — will resync";
        el.className = "offline";
      },
      error(codeOrErr, message) {
        wasDown = true;
        const code = typeof codeOrErr === "object" && codeOrErr ? codeOrErr.code : codeOrErr;
        const detail =
          message || (typeof codeOrErr === "object" && codeOrErr ? codeOrErr.message : "") || "";
        const friendly = {
          STORAGE_READ: "couldn't load saved changes — reloading may help",
          STORAGE_WRITE: "couldn't save locally — changes may be lost on refresh",
          BAD_FRAME: "server sent unreadable data — reconnecting…",
          UNSUPPORTED_PROTOCOL: "this demo is out of date — reload the page",
          STALE_EPOCH: "server restarted with newer data — resyncing…",
          SERVER_ERROR: detail || "the server said no — retrying…",
        };
        el.textContent = friendly[code] || detail || "connection problem — retrying…";
        el.className = "error";
        el.title = detail ? `error: ${code} — ${detail}` : `error: ${code}`;
      },
    };
  }

  window.SyncwellDemo = { room, inviteURL, nav, welcome, share, status, isLocalHost, hueOf, peerName };
})();
