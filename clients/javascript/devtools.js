// Visual CRDT DevTools: Lightweight browser inspector overlay for Syncwell.
//
// Shows live document state, Lamport clocks, tombstones, real-time op stream,
// and connected peers/awareness cursors.
//
// Usage:
//   import { mountDevTools } from "syncwell-client/devtools";
//   const devtools = mountDevTools(sw, { awareness });
//   // unmount when done:
//   devtools.unmount();

export function mountDevTools(syncwell, options = {}) {
  if (typeof document === "undefined") {
    return { unmount() {}, open() {}, close() {} };
  }

  const awareness = options.awareness || null;
  const position = options.position || "bottom-right";

  // Create host container
  const host = document.createElement("div");
  host.id = "syncwell-devtools";
  host.style.cssText = `
    position: fixed;
    z-index: 999999;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, monospace;
    font-size: 12px;
    line-height: 1.4;
    color: #e2e8f0;
    ${position.includes("bottom") ? "bottom: 16px;" : "top: 16px;"}
    ${position.includes("right") ? "right: 16px;" : "left: 16px;"}
  `;

  // Toggle button (Floating Pill)
  const pill = document.createElement("button");
  pill.textContent = "⚡ Syncwell CRDT";
  pill.style.cssText = `
    background: #0f172a;
    color: #38bdf8;
    border: 1px solid #334155;
    padding: 6px 12px;
    border-radius: 9999px;
    cursor: pointer;
    font-weight: 600;
    box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.3);
    display: flex;
    align-items: center;
    gap: 6px;
  `;

  // Main panel
  const panel = document.createElement("div");
  panel.style.cssText = `
    display: none;
    width: 440px;
    height: 520px;
    background: #0f172a;
    border: 1px solid #334155;
    border-radius: 10px;
    box-shadow: 0 20px 25px -5px rgba(0, 0, 0, 0.5);
    flex-direction: column;
    overflow: hidden;
    margin-bottom: 8px;
  `;

  // Header
  const header = document.createElement("div");
  header.style.cssText = `
    background: #1e293b;
    padding: 8px 12px;
    display: flex;
    justify-content: space-between;
    align-items: center;
    border-bottom: 1px solid #334155;
  `;
  header.innerHTML = `
    <div style="font-weight: bold; color: #38bdf8; display: flex; align-items: center; gap: 6px;">
      <span>⚡</span> Syncwell CRDT DevTools
    </div>
    <button id="sw-dt-close" style="background: transparent; border: none; color: #94a3b8; cursor: pointer; font-size: 16px;">&times;</button>
  `;

  // Navigation tabs
  const tabs = document.createElement("div");
  tabs.style.cssText = `
    display: flex;
    background: #1e293b;
    border-bottom: 1px solid #334155;
  `;
  const tabNames = ["State", "Clocks", "Op Stream", "Peers"];
  let activeTab = "State";

  const renderTabs = () => {
    tabs.innerHTML = "";
    for (const name of tabNames) {
      const btn = document.createElement("button");
      btn.textContent = name;
      const isActive = activeTab === name;
      btn.style.cssText = `
        flex: 1;
        padding: 6px 8px;
        background: ${isActive ? "#0f172a" : "transparent"};
        color: ${isActive ? "#38bdf8" : "#94a3b8"};
        border: none;
        border-bottom: 2px solid ${isActive ? "#38bdf8" : "transparent"};
        cursor: pointer;
        font-weight: 500;
        font-size: 11px;
      `;
      btn.onclick = () => {
        activeTab = name;
        renderTabs();
        updateView();
      };
      tabs.appendChild(btn);
    }
  };

  // Content body
  const body = document.createElement("div");
  body.style.cssText = `
    flex: 1;
    overflow-y: auto;
    padding: 12px;
    background: #0f172a;
  `;

  panel.appendChild(header);
  panel.appendChild(tabs);
  panel.appendChild(body);

  host.appendChild(panel);
  host.appendChild(pill);
  (options.container || document.body).appendChild(host);

  // Toggle visibility
  let isOpen = false;
  const toggle = (open = !isOpen) => {
    isOpen = open;
    panel.style.display = isOpen ? "flex" : "none";
    pill.style.display = isOpen ? "none" : "flex";
    if (isOpen) updateView();
  };

  pill.onclick = () => toggle(true);
  header.querySelector("#sw-dt-close").onclick = () => toggle(false);

  // Keyboard shortcut Ctrl+Shift+S / Cmd+Shift+S
  const keyHandler = (e) => {
    if ((e.ctrlKey || e.metaKey) && e.shiftKey && (e.key === "s" || e.key === "S")) {
      e.preventDefault();
      toggle();
    }
  };
  window.addEventListener("keydown", keyHandler);

  // Op history stream
  const opHistory = [];
  const recordOp = (direction, op) => {
    opHistory.unshift({
      time: new Date().toLocaleTimeString(),
      direction,
      op,
    });
    if (opHistory.length > 50) opHistory.pop();
    if (isOpen && activeTab === "Op Stream") updateView();
  };

  const updateView = () => {
    if (!isOpen) return;

    if (activeTab === "State") {
      const live = syncwell.live ? syncwell.live() : {};
      const snap = syncwell.doc?.snapshot ? syncwell.doc.snapshot() : {};
      body.innerHTML = `
        <div style="margin-bottom: 10px; color: #94a3b8; font-size: 11px;">
          Room: <b style="color: #f8fafc;">${escapeHtml(syncwell.room)}</b> | Client ID: <b style="color: #f8fafc;">${escapeHtml(syncwell.clientId)}</b>
        </div>
        <div style="font-weight: 600; color: #38bdf8; margin-bottom: 4px;">Live State (${Object.keys(live).length} keys)</div>
        <pre style="background: #1e293b; padding: 8px; border-radius: 4px; overflow: auto; color: #a5f3fc;">${escapeHtml(JSON.stringify(live, null, 2))}</pre>
        <div style="font-weight: 600; color: #94a3b8; margin: 8px 0 4px;">All Registers Snapshot (${Object.keys(snap).length} entries)</div>
        <pre style="background: #1e293b; padding: 8px; border-radius: 4px; overflow: auto; font-size: 10px; color: #cbd5e1;">${escapeHtml(JSON.stringify(snap, null, 2))}</pre>
      `;
    } else if (activeTab === "Clocks") {
      const snap = syncwell.doc?.snapshot ? syncwell.doc.snapshot() : {};
      let tombstones = 0;
      for (const reg of Object.values(snap)) {
        if (reg.d || reg.deleted) tombstones++;
      }
      body.innerHTML = `
        <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 8px; margin-bottom: 12px;">
          <div style="background: #1e293b; padding: 10px; border-radius: 6px;">
            <div style="color: #94a3b8; font-size: 10px;">Lamport Clock Counter</div>
            <div style="font-size: 20px; font-weight: bold; color: #38bdf8;">${syncwell.clock?.counter ?? "N/A"}</div>
          </div>
          <div style="background: #1e293b; padding: 10px; border-radius: 6px;">
            <div style="color: #94a3b8; font-size: 10px;">Epoch</div>
            <div style="font-size: 20px; font-weight: bold; color: #f59e0b;">${syncwell.epoch ?? 1}</div>
          </div>
          <div style="background: #1e293b; padding: 10px; border-radius: 6px;">
            <div style="color: #94a3b8; font-size: 10px;">Active Registers</div>
            <div style="font-size: 20px; font-weight: bold; color: #10b981;">${Object.keys(snap).length - tombstones}</div>
          </div>
          <div style="background: #1e293b; padding: 10px; border-radius: 6px;">
            <div style="color: #94a3b8; font-size: 10px;">Tombstones (GC pending)</div>
            <div style="font-size: 20px; font-weight: bold; color: #ef4444;">${tombstones}</div>
          </div>
        </div>
        <div style="background: #1e293b; padding: 8px; border-radius: 4px; color: #94a3b8; font-size: 11px;">
          <b>Causal Ordering Rules:</b><br/>
          1. Higher logical counter wins.<br/>
          2. Ties broken lexicographically by Node ID.<br/>
          3. Tombstone deletes deterministically win on identical timestamp.<br/>
          4. Content tie-break ensures byte-identical convergence.
        </div>
      `;
    } else if (activeTab === "Op Stream") {
      if (opHistory.length === 0) {
        body.innerHTML = `<div style="color: #64748b; text-align: center; padding: 20px;">No operations recorded yet.</div>`;
      } else {
        body.innerHTML = `
          <div style="display: flex; flex-direction: column; gap: 6px;">
            ${opHistory
              .map(
                (item) => `
                <div style="background: #1e293b; padding: 6px 8px; border-radius: 4px; font-size: 11px; border-left: 3px solid ${item.direction === "IN" ? "#10b981" : "#38bdf8"};">
                  <div style="display: flex; justify-content: space-between; color: #94a3b8; margin-bottom: 2px;">
                    <span style="font-weight: 600; color: ${item.direction === "IN" ? "#10b981" : "#38bdf8"};">${item.direction}</span>
                    <span>${item.time}</span>
                  </div>
                  <pre style="margin: 0; overflow: auto; color: #f1f5f9; font-size: 10px;">${escapeHtml(JSON.stringify(item.op))}</pre>
                </div>
              `
              )
              .join("")}
          </div>
        `;
      }
    } else if (activeTab === "Peers") {
      const states = awareness ? awareness.getStates() : [];
      body.innerHTML = `
        <div style="font-weight: 600; color: #38bdf8; margin-bottom: 8px;">Connected Peers (${states.length})</div>
        <div style="display: flex; flex-direction: column; gap: 6px;">
          ${states
            .map(
              (peer) => `
              <div style="background: #1e293b; padding: 8px; border-radius: 6px; display: flex; align-items: center; justify-content: space-between;">
                <div style="display: flex; align-items: center; gap: 8px;">
                  <span style="width: 12px; height: 12px; border-radius: 9999px; background: ${sanitizeColor(peer.color)};"></span>
                  <div>
                    <div style="font-weight: 600; color: #f8fafc;">${escapeHtml(peer.user?.name || peer.clientId || "")} ${peer.self ? '<span style="color: #38bdf8; font-size: 10px;">(You)</span>' : ""}</div>
                    <div style="color: #94a3b8; font-size: 10px;">ID: ${escapeHtml(peer.clientId)}</div>
                  </div>
                </div>
                <div style="color: #94a3b8; font-size: 11px;">
                  ${peer.cursor ? `Cursor: ${escapeHtml(peer.cursor.index)}` : "Idle"}
                </div>
              </div>
            `
            )
            .join("")}
        </div>
      `;
    }
  };

  renderTabs();

  // Attach Syncwell listeners to update reactive state
  const changeHandler = () => {
    recordOp("STATE", { type: "change" });
    updateView();
  };
  const presenceHandler = (e) => {
    recordOp("IN", { type: "presence", ...e });
    updateView();
  };

  syncwell.on("change", changeHandler);
  syncwell.on("presence", presenceHandler);
  if (awareness) {
    awareness.on("update", updateView);
  }

  return {
    open() {
      toggle(true);
    },
    close() {
      toggle(false);
    },
    unmount() {
      window.removeEventListener("keydown", keyHandler);
      syncwell.off("change", changeHandler);
      syncwell.off("presence", presenceHandler);
      if (awareness) awareness.off("update", updateView);
      if (host.parentNode) host.parentNode.removeChild(host);
    },
  };
}

export function escapeHtml(str) {
  if (typeof str !== "string") str = String(str ?? "");
  return str
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

export function sanitizeColor(color) {
  if (typeof color !== "string") return "#38bdf8";
  color = color.trim();
  if (/^#([0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$/.test(color)) {
    return color;
  }
  if (/^rgba?\(\s*\d{1,3}\s*,\s*\d{1,3}\s*,\s*\d{1,3}\s*(?:,\s*(?:0|1|0?\.\d+)\s*)?\)$/i.test(color)) {
    return color;
  }
  if (/^hsla?\(\s*\d{1,3}\s*,\s*\d{1,3}%\s*,\s*\d{1,3}%\s*(?:,\s*(?:0|1|0?\.\d+)\s*)?\)$/i.test(color)) {
    return color;
  }
  return "#38bdf8";
}
