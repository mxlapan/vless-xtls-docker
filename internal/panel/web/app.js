"use strict";
const $ = (h) => { const t = document.createElement("template"); t.innerHTML = h.trim(); return t.content.firstChild; };
const app = document.getElementById("app");
let state = { me: null, version: "", tab: "dashboard", nodes: [], users: [] };

// ---------- inline SVG icons (crisp, theme-aware via currentColor) ----------
const ICONS = {
  dashboard: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
  nodes: '<rect x="3" y="4" width="18" height="7" rx="2"/><rect x="3" y="13" width="18" height="7" rx="2"/><line x1="7" y1="7.5" x2="7.01" y2="7.5"/><line x1="7" y1="16.5" x2="7.01" y2="16.5"/>',
  users: '<circle cx="12" cy="8" r="3.4"/><path d="M5.5 20a6.5 6.5 0 0 1 13 0"/>',
  shield: '<path d="M12 3l7 3v5c0 4.5-3 7.6-7 9-4-1.4-7-4.5-7-9V6l7-3z"/>',
  power: '<path d="M12 4v8"/><path d="M7.5 6.3a8 8 0 1 0 9 0"/>',
  download: '<path d="M12 3v12"/><path d="M8 11l4 4 4-4"/><path d="M4 20h16"/>',
  plus: '<path d="M12 5v14"/><path d="M5 12h14"/>',
  edit: '<path d="M4 20h4L18.5 9.5l-4-4L4 16v4z"/><path d="M13.5 6.5l4 4"/>',
  trash: '<path d="M4 7h16"/><path d="M9 7V5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/><path d="M6 7l1 12a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2l1-12"/>',
  refresh: '<path d="M4 12a8 8 0 0 1 13.7-5.7L20 8"/><path d="M20 4v4h-4"/><path d="M20 12a8 8 0 0 1-13.7 5.7L4 16"/><path d="M4 20v-4h4"/>',
  link: '<path d="M10 13a4 4 0 0 0 5.7 0l2.3-2.3a4 4 0 0 0-5.7-5.7L11 6"/><path d="M14 11a4 4 0 0 0-5.7 0L6 13.3a4 4 0 0 0 5.7 5.7L13 18"/>',
  chart: '<path d="M4 20V11"/><path d="M10 20V4"/><path d="M16 20v-6"/><line x1="2.5" y1="20" x2="21.5" y2="20"/>',
  terminal: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7.5 9.5l3 2.5-3 2.5"/><path d="M13 15h4"/>',
  copy: '<rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v1"/>',
  traffic: '<path d="M7 20V4l-3 3"/><path d="M17 4v16l3-3"/>',
  slash: '<circle cx="12" cy="12" r="8.5"/><line x1="6" y1="18" x2="18" y2="6"/>',
  inbox: '<path d="M4 13l2-8h12l2 8v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1v-5z"/><path d="M4 13h5a3 3 0 0 0 6 0h5"/>',
  devices: '<rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8"/><path d="M12 16v4"/>',
  settings: '<line x1="4" y1="8" x2="20" y2="8"/><line x1="4" y1="16" x2="20" y2="16"/><circle cx="9" cy="8" r="2.3"/><circle cx="15" cy="16" r="2.3"/>',
};
function svg(name, cls = "") { return `<svg class="ico ${cls}" viewBox="0 0 24 24">${ICONS[name] || ""}</svg>`; }

async function api(path, opts = {}) {
  const res = await fetch(path, { headers: { "Content-Type": "application/json" }, ...opts });
  if (res.status === 401) { state.me = null; renderLogin(); throw new Error("unauthorized"); }
  const txt = await res.text();
  const data = txt ? JSON.parse(txt) : null;
  if (!res.ok) throw new Error((data && data.error) || res.statusText);
  return data;
}

function toast(msg) {
  const t = $(`<div class="toast">${esc(msg)}</div>`);
  document.body.appendChild(t);
  setTimeout(() => t.remove(), 2600);
}
function strongPw(p){ return p.length>=8 && /[a-z]/.test(p) && /[A-Z]/.test(p) && /[0-9]/.test(p) && /[^A-Za-z0-9]/.test(p); }
function esc(s) { return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c])); }
async function copy(text) {
  try {
    if (navigator.clipboard) await navigator.clipboard.writeText(text);
    else { const t = document.createElement("textarea"); t.value = text; document.body.appendChild(t); t.select(); document.execCommand("copy"); t.remove(); }
    toast("Copied to clipboard");
  } catch { toast("Copy failed"); }
}
function fmtBytes(n) {
  n = Number(n || 0); if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB", "PB"]; let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return n.toFixed(i ? (n < 10 ? 2 : 1) : 0) + " " + u[i];
}
function fmtDate(sec) { return sec ? new Date(sec * 1000).toLocaleString() : "never"; }
function fmtDateShort(sec) { return sec ? new Date(sec * 1000).toLocaleDateString() : "never"; }
// fmtDur renders a span of seconds as a coarse duration ("26d 6h").
function fmtDur(sec) {
  sec = Number(sec || 0);
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60);
  if (d) return `${d}d ${h}h`;
  return h ? `${h}h ${m}m` : `${m}m`;
}
function relTime(sec) {
  if (!sec) return "never";
  const d = Math.floor(Date.now() / 1000) - sec;
  if (d < 60) return "just now";
  if (d < 3600) return Math.floor(d / 60) + "m ago";
  if (d < 86400) return Math.floor(d / 3600) + "h ago";
  return Math.floor(d / 86400) + "d ago";
}
function toEpoch(local) { return local ? Math.floor(new Date(local).getTime() / 1000) : 0; }
function toLocalInput(sec) {
  if (!sec) return "";
  const d = new Date(sec * 1000); const p = (x) => String(x).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

// ---------- login ----------
function renderLogin() {
  app.innerHTML = "";
  const box = $(`<div class="login-wrap"><div class="login">
    <div class="brand"><div class="logo">玄</div><div><b>Xuanwu</b><span>Control Panel</span></div></div>
    <div class="modal">
      <h3>Sign in</h3>
      <p class="hint">Administrator access</p>
      <label>Username</label><input id="u" autofocus autocomplete="username">
      <label>Password</label><input id="p" type="password" autocomplete="current-password">
      <div id="codewrap" style="display:none"><label>2FA code</label><input id="c" inputmode="numeric" autocomplete="one-time-code" placeholder="123456"></div>
      <div class="actions"><button class="primary" id="go" style="width:100%">Sign in</button></div>
    </div></div></div>`);
  const submit = async () => {
    const codeWrap = box.querySelector("#codewrap");
    const payload = { username: box.querySelector("#u").value, password: box.querySelector("#p").value };
    if (codeWrap.style.display !== "none") payload.code = box.querySelector("#c").value;
    const res = await fetch("/api/login", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) });
    const data = await res.json().catch(() => ({}));
    if (res.ok) { await boot(); return; }
    if (data.totp_required) {
      codeWrap.style.display = "block";
      box.querySelector("#c").focus();
      if (payload.code) toast(data.error || "invalid 2FA code");
      return;
    }
    toast(data.error || "login failed");
  };
  box.querySelector("#go").onclick = submit;
  box.querySelectorAll("input").forEach((i) => i.addEventListener("keydown", (e) => { if (e.key === "Enter") submit(); }));
  app.appendChild(box);
}

// ---------- shell ----------
const NAV = [
  { t: "dashboard", label: "Dashboard", icon: "dashboard" },
  { t: "nodes", label: "Nodes", icon: "nodes" },
  { t: "users", label: "Users", icon: "users" },
];
function renderShell() {
  app.innerHTML = "";
  const nav = NAV.map((n) => `<button class="navbtn ${n.t === state.tab ? "active" : ""}" data-t="${n.t}">${svg(n.icon)}${n.label}</button>`).join("");
  const layout = $(`<div class="layout">
    <aside>
      <div class="brand"><div class="logo">玄</div><div><b>Xuanwu</b><span title="Panel build (commit)">玄武 Panel${state.version ? " · " + esc(state.version) : ""}</span></div></div>
      <div class="navlabel">Manage</div>
      ${nav}
      <div class="grow"></div>
      <button class="navbtn" id="settings">${svg("settings")}Settings</button>
      <button class="navbtn" id="security">${svg("shield")}Security</button>
      <button class="navbtn" id="logout">${svg("power")}Logout</button>
      <div class="who">Signed in as<b>${esc(state.me || "")}</b></div>
    </aside>
    <main></main>
  </div>`);
  layout.querySelectorAll(".navbtn[data-t]").forEach((b) => b.onclick = () => { state.tab = b.dataset.t; render(); });
  layout.querySelector("#settings").onclick = () => showSettings();
  layout.querySelector("#security").onclick = () => show2FA();
  layout.querySelector("#logout").onclick = async () => { try { await api("/api/logout", { method: "POST" }); } catch {} state.me = null; renderLogin(); };
  app.appendChild(layout);
  return layout.querySelector("main");
}

function render() {
  const main = renderShell();
  if (state.tab === "dashboard") renderDashboard(main);
  else if (state.tab === "nodes") renderNodes(main);
  else renderUsers(main);
}

function modal(node) {
  const bg = $(`<div class="modal-bg"></div>`);
  bg.appendChild(node);
  bg.addEventListener("click", (e) => { if (e.target === bg) bg.remove(); });
  document.addEventListener("keydown", function esc(e) { if (e.key === "Escape") { bg.remove(); document.removeEventListener("keydown", esc); } });
  document.body.appendChild(bg);
  return bg;
}

function bar(title, sub, btnLabel, onBtn, btnIcon) {
  const el = $(`<div class="bar"><div><h2>${esc(title)}</h2><div class="sub">${esc(sub)}</div></div>
    ${btnLabel ? `<button class="primary" id="__act">${btnIcon ? svg(btnIcon) : ""}${esc(btnLabel)}</button>` : ""}</div>`);
  if (btnLabel) el.querySelector("#__act").onclick = onBtn;
  return el;
}

// ---------- dashboard ----------
async function renderDashboard(main) {
  const b = bar("Dashboard", "Fleet overview", "Backup DB", () => { window.location = "/api/backup"; toast("Downloading backup…"); }, "download");
  main.appendChild(b);
  const holder = $(`<div class="loading">Loading…</div>`);
  main.appendChild(holder);
  let stats;
  try {
    [state.users, state.nodes, stats] = await Promise.all([api("/api/users"), api("/api/nodes"), api("/api/stats")]);
  } catch (e) { holder.textContent = e.message; return; }
  stats = stats || { total: { up: 0, down: 0 }, today: { up: 0, down: 0 }, month: { up: 0, down: 0 }, trend: [], nodes: [], users: {} };
  const nodeAgg = {};
  (stats.nodes || []).forEach((n) => { nodeAgg[n.node_id] = n; });
  const userAgg = stats.users || {};
  const now = Math.floor(Date.now() / 1000);
  const online = state.nodes.filter((n) => n.online).length;
  const active = state.users.filter((u) => u.enabled && (!u.expire_at || u.expire_at > now) && (!u.data_limit || u.data_used < u.data_limit)).length;
  const up = Number(stats.total.up || 0), down = Number(stats.total.down || 0);
  const tup = Number(stats.today.up || 0), tdn = Number(stats.today.down || 0);
  const mup = Number(stats.month.up || 0), mdn = Number(stats.month.down || 0);
  const overQuota = state.users.filter((u) => u.data_limit && u.data_used >= u.data_limit).length;
  const split = (a, b) => `<span class="up">↑ ${fmtBytes(a)}</span> · <span class="dn">↓ ${fmtBytes(b)}</span>`;
  const stat = (k, v, foot, ic, cls) => `<div class="stat ${cls}">
    <div class="k"><span class="chip ${cls}">${svg(ic)}</span>${k}</div>
    <div class="v">${v}</div><div class="foot">${foot}</div></div>`;
  holder.outerHTML = `<div class="stats">
    ${stat("Nodes", `${online}<small> / ${state.nodes.length} online</small>`, state.nodes.length ? `${state.nodes.length - online} offline` : "no nodes yet", "nodes", "blue")}
    ${stat("Users", `${active}<small> / ${state.users.length} active</small>`, overQuota ? `${overQuota} over quota` : "all within quota", "users", "green")}
    ${stat("This month", fmtBytes(mup + mdn), `today ${fmtBytes(tup + tdn)}`, "traffic", "violet")}
    ${stat("Traffic used", fmtBytes(up + down), split(up, down), "traffic", "cyan")}
    ${stat("Disabled", state.users.filter((u) => !u.enabled).length, "manually switched off", "slash", "amber")}
  </div>`;

  // fleet-wide daily trend, stacked up/down
  const trend = stats.trend || [];
  const trendTotal = trend.reduce((s, d) => s + Number(d.up || 0) + Number(d.down || 0), 0);
  main.appendChild($(`<div class="sectlabel">Fleet traffic — last ${trend.length} days</div>`));
  const chartCard = $(`<div class="card" style="padding:16px 18px 12px">
    ${fleetChart(trend)}
    <div class="mut" style="font-size:12px;margin-top:6px">${trend.length}-day total ${fmtBytes(trendTotal)} · ${split(trend.reduce((s, d) => s + Number(d.up || 0), 0), trend.reduce((s, d) => s + Number(d.down || 0), 0))}</div>
  </div>`);
  main.appendChild(chartCard);

  // per-node (live + directional) + top-user traffic
  const nodeRows = [...state.nodes]
    .map((n) => ({ n, t: nodeAgg[n.id] || { up: 0, down: 0 } }))
    .sort((a, b) => (Number(b.t.up) + Number(b.t.down)) - (Number(a.t.up) + Number(a.t.down)))
    .slice(0, 8)
    .map(({ n, t }) => {
      const rate = Number(n.rate_bps || 0), clients = Number(n.clients || 0);
      const label = `<span class="dot ${n.online ? "on" : "off"}"></span>${esc(n.name)}${clients ? ` <span class="mut" title="active clients (5m)">· ${clients}</span>` : ""}`;
      const live = n.online && rate ? `<span class="live">${fmtRate(rate)}</span>` : `<span class="mut">—</span>`;
      return `<tr data-node="${n.id}" class="rowlink"><td>${label}</td>
        <td class="mono">${live}</td>
        <td class="mono up">↑ ${fmtBytes(t.up)}</td>
        <td class="mono dn">↓ ${fmtBytes(t.down)}</td>
        <td class="mono"><b>${fmtBytes(Number(t.up) + Number(t.down))}</b></td></tr>`;
    }).join("");
  const userRows = [...state.users]
    .map((u) => ({ u, t: userAgg[u.id] || { up: 0, down: 0 } }))
    .sort((a, b) => (Number(b.t.up) + Number(b.t.down)) - (Number(a.t.up) + Number(a.t.down)))
    .slice(0, 8)
    .map(({ u, t }) => trafficRow(`<b>${esc(u.username)}</b>`, t.up, t.down)).join("");
  const panels = $(`<div class="grid2">
    <div><div class="sectlabel">Traffic by node</div>
      <div class="card"><table><thead><tr><th>Node</th><th>Live</th><th>Up</th><th>Down</th><th>Total</th></tr></thead>
      <tbody>${nodeRows || `<tr><td colspan="5" class="mut" style="text-align:center;padding:26px">No nodes yet</td></tr>`}</tbody></table></div></div>
    <div><div class="sectlabel">Top users by traffic</div>
      <div class="card"><table><thead><tr><th>User</th><th>Up</th><th>Down</th><th>Total</th></tr></thead>
      <tbody>${userRows || `<tr><td colspan="4" class="mut" style="text-align:center;padding:26px">No users yet</td></tr>`}</tbody></table></div></div>
  </div>`);
  panels.querySelectorAll("tr[data-node]").forEach((tr) => tr.onclick = () => {
    const n = state.nodes.find((x) => x.id == tr.dataset.node);
    showNodeStats(tr.dataset.node, n);
  });
  main.appendChild(panels);
}

// trafficRow renders a label plus an up / down / total byte breakdown.
function trafficRow(label, up, down) {
  up = Number(up || 0); down = Number(down || 0);
  return `<tr><td>${label}</td>
    <td class="mono up">↑ ${fmtBytes(up)}</td>
    <td class="mono dn">↓ ${fmtBytes(down)}</td>
    <td class="mono"><b>${fmtBytes(up + down)}</b></td></tr>`;
}

// fmtRate formats a bytes/second throughput as bits/second (network convention).
function fmtRate(bytesPerSec) {
  let bits = Number(bytesPerSec || 0) * 8;
  const u = ["bps", "Kbps", "Mbps", "Gbps"]; let i = 0;
  while (bits >= 1000 && i < u.length - 1) { bits /= 1000; i++; }
  return bits.toFixed(bits < 10 && i ? 1 : 0) + " " + u[i];
}

// fleetChart renders a stacked (download over upload) daily bar chart.
function fleetChart(hist) {
  const W = 900, H = 150, pad = 24;
  if (!hist.length) return `<div class="mut" style="text-align:center;padding:30px">No traffic yet</div>`;
  const tot = hist.map((d) => Number(d.up || 0) + Number(d.down || 0));
  const max = Math.max(1, ...tot);
  const bw = (W - pad * 2) / hist.length;
  const bars = hist.map((d, i) => {
    const u = Number(d.up || 0), dn = Number(d.down || 0);
    const hU = Math.round((H - pad * 2) * (u / max));
    const hD = Math.round((H - pad * 2) * (dn / max));
    const x = pad + i * bw + 1, wd = Math.max(1, bw - 2);
    const yD = H - pad - hD, yU = yD - hU;
    const t = esc(`${d.day}: ↑ ${fmtBytes(u)} · ↓ ${fmtBytes(dn)}`);
    return `<g><title>${t}</title>
      <rect x="${x.toFixed(1)}" y="${yD}" width="${wd.toFixed(1)}" height="${hD}" fill="#b79bff"/>
      <rect x="${x.toFixed(1)}" y="${yU}" width="${wd.toFixed(1)}" height="${hU}" fill="#9db0ff"/></g>`;
  }).join("");
  const first = hist[0].day.slice(5), last = hist[hist.length - 1].day.slice(5);
  return `<svg viewBox="0 0 ${W} ${H}" width="100%" style="max-width:100%">
    <line x1="${pad}" y1="${H - pad}" x2="${W - pad}" y2="${H - pad}" stroke="var(--line2)"/>
    ${bars}
    <text x="${pad}" y="${H - 6}" fill="var(--mut2)" font-size="11">${first}</text>
    <text x="${W - pad}" y="${H - 6}" fill="var(--mut2)" font-size="11" text-anchor="end">${last}</text>
    <text x="${pad}" y="14" fill="var(--mut2)" font-size="11">peak ${fmtBytes(max)}/day · <tspan fill="#9db0ff">↑ up</tspan> <tspan fill="#b79bff">↓ down</tspan></text>
  </svg>`;
}

// showNodeStats opens a node's daily traffic trend.
async function showNodeStats(id, n) {
  n = n || {};
  const f = $(`<div class="modal" style="max-width:640px">
    <h3>Traffic — ${esc(n.name || "node")}</h3>
    <p class="hint">Daily usage (UTC), last 30 days. Per-node history begins when the node started reporting under this version.</p>
    <div id="chart" class="mut" style="padding:20px 0;text-align:center">Loading…</div>
    <div class="actions"><button class="primary" id="ok">Close</button></div>
  </div>`);
  const bg = modal(f);
  f.querySelector("#ok").onclick = () => bg.remove();
  try {
    const hist = await api("/api/nodes/" + id + "/traffic-history?days=30");
    const total = hist.reduce((s, d) => s + Number(d.up || 0) + Number(d.down || 0), 0);
    f.querySelector("#chart").outerHTML = `<div>${fleetChart(hist)}<div class="mut" style="font-size:12px;margin-top:8px">30-day total: ${fmtBytes(total)}</div></div>`;
  } catch (e) { f.querySelector("#chart").textContent = e.message; }
}

function usageCell(u) {
  const used = Number(u.data_used || 0);
  if (!u.data_limit) return `<span class="mono">${fmtBytes(used)}</span> <span class="mut">/ ∞</span>`;
  const pct = Math.min(100, Math.round(used / u.data_limit * 100));
  const cls = pct >= 100 ? "bad" : pct >= 80 ? "warn" : "";
  const eta = Number(u.eta_days || 0);
  const etaTag = eta && eta <= 14
    ? ` <span class="pill warn" title="projected from the last 7 days' average">~${eta}d to quota</span>`
    : "";
  return `<div><span class="mono">${fmtBytes(used)}</span> <span class="mut">/ ${fmtBytes(u.data_limit)} (${pct}%)</span>${etaTag}
    <div class="prog ${cls}"><i style="width:${pct}%"></i></div></div>`;
}

// ---------- nodes ----------
async function renderNodes(main) {
  main.appendChild(bar("Nodes", "Managed servers running the Xray stack", "Add node", () => nodeForm(null), "plus"));
  const holder = $(`<div class="loading">Loading…</div>`);
  main.appendChild(holder);
  try { state.nodes = await api("/api/nodes"); }
  catch (e) { holder.textContent = e.message; return; }
  if (!state.nodes.length) {
    holder.outerHTML = `<div class="card"><div class="empty"><div class="eico">${svg("nodes")}</div>No nodes yet<br><span class="mut" style="font-size:13px">Add your first node to generate an install command.</span></div></div>`;
    return;
  }
  const rows = state.nodes.map((n) => `<tr>
    <td><span class="dot ${n.online ? "on" : "off"}"></span><b>${esc(n.name)}</b>${n.remark ? `<div class="mut" style="font-size:12px">${esc(n.remark)}</div>` : ""}</td>
    <td class="mono">${esc(n.address || "-")}</td>
    <td class="mut">${n.reality_server_name ? esc(n.reality_server_name) : `<span class="mut">-</span>`}</td>
    <td>${nodeHealth(n)}</td>
    <td>${n.online ? `<span class="pill on"><span class="dot on" style="margin:0"></span>online</span>` : `<span class="mut" title="${esc(fmtDate(n.last_seen))}">offline · ${relTime(n.last_seen)}</span>`}</td>
    <td class="actions-cell"><div class="rowacts">
      <button data-i="${n.id}" class="iconbtn inst" title="Install command">${svg("terminal")}</button>
      <button data-i="${n.id}" class="iconbtn edit" title="Edit">${svg("edit")}</button>
      <button data-i="${n.id}" class="iconbtn danger del" title="Delete">${svg("trash")}</button>
    </div></td></tr>`).join("");
  holder.outerHTML = `<div class="card"><table>
    <thead><tr><th>Name</th><th>Address</th><th>REALITY SNI</th><th>Health</th><th>Status</th><th></th></tr></thead>
    <tbody>${rows}</tbody></table></div>`;
  main.querySelectorAll(".edit").forEach((b) => b.onclick = () => nodeForm(state.nodes.find((n) => n.id == b.dataset.i)));
  main.querySelectorAll(".inst").forEach((b) => b.onclick = () => showInstall(b.dataset.i));
  main.querySelectorAll(".del").forEach((b) => b.onclick = async () => {
    if (!confirm("Delete this node? Its users lose access to it.")) return;
    try { await api("/api/nodes/" + b.dataset.i, { method: "DELETE" }); toast("Node deleted"); render(); }
    catch (e) { toast(e.message); }
  });
}

function nodeHealth(n) {
  const m = n.metrics;
  if (!n.online || !m) return `<span class="mut">—</span>`;
  const parts = [];
  const up = Number(n.rate_up_bps || 0), dn = Number(n.rate_down_bps || 0);
  if (up || dn) parts.push(`<span class="live" title="current throughput (up / down)"><span class="up">↑ ${fmtRate(up)}</span> <span class="dn">↓ ${fmtRate(dn)}</span></span>`);
  if (n.clients) parts.push(`<span class="mut" title="active clients (last 5m)">${n.clients} client${n.clients > 1 ? "s" : ""}</span>`);
  const load = (+m.load_avg || 0).toFixed(2);
  // cpu_cores is only sent by agents that report the host snapshot; without it
  // there is no CPU reading to show, so fall back to the load average alone.
  if (m.cpu_cores) {
    const cores = `${m.cpu_cores} core${m.cpu_cores > 1 ? "s" : ""}`;
    parts.push(`<span class="mut" title="${esc(m.cpu_model ? m.cpu_model + " · " + cores : cores)} · load ${load}">cpu ${m.cpu_used_pct || 0}%</span>`);
  } else {
    parts.push(`<span class="mut" title="1-min load average">load ${load}</span>`);
  }
  parts.push(`<span class="mut" title="${m.mem_total ? `${fmtBytes(m.mem_used)} of ${fmtBytes(m.mem_total)} used` : "memory used"}">mem ${m.mem_used_pct || 0}%</span>`);
  if (m.disk_total) parts.push(`<span class="mut" title="${fmtBytes(m.disk_used)} of ${fmtBytes(m.disk_total)} used">disk ${Math.round(m.disk_used / m.disk_total * 100)}%</span>`);
  if (m.host_uptime) parts.push(`<span class="mut" title="host uptime">up ${fmtDur(m.host_uptime)}</span>`);
  if (m.xray_version) parts.push(`<span class="mut" title="Xray version">xray ${esc(m.xray_version)}</span>`);
  if (n.agent_version) parts.push(`<span class="mut" title="Agent build (commit)">agent ${esc(n.agent_version)}</span>`);
  if (m.cert_expiry) {
    const days = Math.ceil((m.cert_expiry - Date.now() / 1000) / 86400);
    const cls = days < 0 ? "off" : days < 14 ? "off" : "on";
    parts.push(`<span class="pill ${cls}" title="TLS cert expiry">cert ${days < 0 ? "expired" : days + "d"}</span>`);
  }
  return `<div style="font-size:12px;display:flex;gap:8px;flex-wrap:wrap;align-items:center">${parts.join("")}</div>`;
}

function nodeForm(n) {
  const isEdit = !!n; n = n || {};
  const sec = (title, note) => `<div style="margin:20px 0 3px;padding-top:14px;border-top:1px solid var(--line);font-size:13px;font-weight:650">${title} <span style="font-weight:400;color:var(--mut2);font-size:11.5px">— ${note}</span></div>`;
  const f = $(`<div class="modal">
    <h3>${isEdit ? "Edit" : "Add"} node</h3>
    <p class="hint">Enable <b>TLS-Vision</b>, <b>REALITY</b>, or both — fill a section to enable it, leave it blank to skip. ${isEdit ? "Changes are pushed to the node immediately." : "A node token is generated on save."}</p>

    <label>Name</label><input id="name" value="${esc(n.name || "")}" placeholder="node-frankfurt">
    <label>Public address (host/IP used in share links)</label><input id="address" value="${esc(n.address || "")}" placeholder="1.2.3.4 or host.example.com">

    ${sec("TLS-Vision", "real TLS certificate")}
    <p class="hint" style="margin:2px 0 6px">Clients connect using your real domain as SNI. Needs a cert on the node (<code>deploy/node/certs</code>, or the ACME profile). Leave blank for a REALITY-only node — no cert required.</p>
    <label>TLS domain (subscription SNI — enables TLS-Vision)</label>
    <input id="tls_domain" value="${esc(n.tls_domain || "")}" placeholder="node.example.com">

    ${sec("REALITY", "no certificate needed")}
    <p class="hint" style="margin:2px 0 6px">Borrows a real site's TLS handshake. Fill dest + serverName, click <b>Generate REALITY keypair</b>, then Save. Leave the whole section blank to skip.</p>
    <label>REALITY dest — real site to borrow (host:443)</label>
    <input id="reality_dest" value="${esc(n.reality_dest || "")}" placeholder="www.microsoft.com:443">
    <label>REALITY serverName — the SNI clients use</label>
    <input id="reality_server_name" value="${esc(n.reality_server_name || "")}" placeholder="www.microsoft.com">
    <div class="hint" style="margin:4px 0 0">Clients use this as their SNI. The node's nginx SNI routing is configured automatically from this value — no matching <code>.env</code> needed.</div>
    <div class="row" style="margin-top:6px">
      <div><label>REALITY private key</label><input id="reality_private_key" value="${esc(n.reality_private_key || "")}" placeholder="${isEdit ? "•••••• (unchanged)" : ""}"></div>
      <div><label>REALITY public key</label><input id="reality_public_key" value="${esc(n.reality_public_key || "")}"></div>
    </div>
    <label>REALITY shortId</label><input id="reality_short_id" value="${esc(n.reality_short_id || "")}">
    <div style="margin-top:10px"><button class="sm" id="genkeys">${svg("refresh")}Generate REALITY keypair</button></div>

    <div class="actions"><button class="ghost" id="cancel">Cancel</button><button class="primary" id="save">Save</button></div>
  </div>`);
  const bg = modal(f);
  f.querySelector("#genkeys").onclick = async () => {
    try {
      const k = await api("/api/reality-keys", { method: "POST" });
      f.querySelector("#reality_private_key").value = k.private_key;
      f.querySelector("#reality_public_key").value = k.public_key;
      f.querySelector("#reality_short_id").value = k.short_id;
      toast("Generated new REALITY keypair");
    } catch (e) { toast(e.message); }
  };
  f.querySelector("#cancel").onclick = () => bg.remove();
  f.querySelector("#save").onclick = async () => {
    const body = {};
    ["name", "address", "tls_domain", "reality_dest", "reality_server_name", "reality_private_key", "reality_public_key", "reality_short_id"]
      .forEach((k) => body[k] = f.querySelector("#" + k).value.trim());
    if (!body.name) { toast("Name is required"); return; }
    // REALITY is all-or-nothing: any field set requires a serverName + a keypair.
    const realityAny = body.reality_dest || body.reality_server_name || body.reality_public_key || body.reality_short_id;
    if (realityAny && !(body.reality_server_name && body.reality_public_key)) {
      toast("REALITY needs a serverName and a keypair (click Generate) — or clear the REALITY fields to skip it");
      return;
    }
    try {
      if (isEdit) await api("/api/nodes/" + n.id, { method: "PUT", body: JSON.stringify(body) });
      else await api("/api/nodes", { method: "POST", body: JSON.stringify(body) });
      bg.remove(); toast(isEdit ? "Node updated" : "Node created"); render();
    } catch (e) { toast(e.message); }
  };
}

function copyField(label, value) {
  return `<label>${esc(label)}</label><div class="field"><code>${esc(value)}</code>
    <button class="iconbtn" title="Copy" data-copy="${esc(value)}">${svg("copy")}</button></div>`;
}

async function showInstall(id) {
  let info;
  try { info = await api("/api/nodes/" + id + "/install"); } catch (e) { toast(e.message); return; }
  const f = $(`<div class="modal">
    <h3>Install node</h3>
    <p class="hint">On the target server, in <code>deploy/node</code>, set these in <code>.env</code> and run compose:</p>
    ${copyField("Panel URL", info.panel_url)}
    ${copyField("Node token", info.token)}
    ${copyField("One-liner", info.compose_cmd)}
    <div class="actions"><button class="primary" id="ok">Done</button></div>
  </div>`);
  const bg = modal(f);
  f.querySelectorAll("[data-copy]").forEach((b) => b.onclick = () => copy(b.dataset.copy));
  f.querySelector("#ok").onclick = () => bg.remove();
}

// ---------- users ----------
async function renderUsers(main) {
  main.appendChild(bar("Users", "Proxy accounts and their quotas", "Add user", () => userForm(null), "plus"));
  const holder = $(`<div class="loading">Loading…</div>`);
  main.appendChild(holder);
  try { [state.users, state.nodes] = await Promise.all([api("/api/users"), api("/api/nodes")]); }
  catch (e) { holder.textContent = e.message; return; }
  if (!state.users.length) {
    holder.outerHTML = `<div class="card"><div class="empty"><div class="eico">${svg("users")}</div>No users yet<br><span class="mut" style="font-size:13px">Create a user to hand out a subscription link.</span></div></div>`;
    return;
  }
  const now = Math.floor(Date.now() / 1000);
  const nodeName = (id) => { const n = state.nodes.find((x) => x.id == id); return n ? n.name : "#" + id; };
  const rows = state.users.map((u) => {
    const nodes = (u.node_ids || []).map((id) => `<span class="tag">${esc(nodeName(id))}</span>`).join("") || `<span class="mut">none</span>`;
    const expd = u.expire_at && u.expire_at <= now;
    const devs = Number(u.device_count || 0);
    return `<tr>
      <td><b>${esc(u.username)}</b>${u.must_change_pw ? ` <span class="pill warn" title="Must change portal password on next login">temp pw</span>` : ""}<div class="mono" style="font-size:11px">${esc(u.uuid)}</div>${u.note ? `<div class="mut" style="font-size:11.5px;margin-top:3px;max-width:240px;white-space:normal">📝 ${esc(u.note)}</div>` : ""}</td>
      <td style="min-width:180px">${usageCell(u)}</td>
      <td>${devs ? `<span class="pill" title="Distinct devices seen (30d)">${svg("devices")}${devs}</span>` : `<span class="mut">—</span>`}</td>
      <td class="mut">${u.expire_at ? `<span title="${esc(fmtDate(u.expire_at))}" style="${expd ? "color:var(--bad)" : ""}">${fmtDateShort(u.expire_at)}</span>` : "never"}</td>
      <td>${nodes}</td>
      <td>${u.enabled ? `<span class="pill on">enabled</span>` : `<span class="pill off">disabled</span>`}</td>
      <td class="actions-cell"><div class="rowacts">
        <button data-i="${u.id}" class="iconbtn sub" title="Subscription & access">${svg("link")}</button>
        <button data-i="${u.id}" class="iconbtn devs" title="Devices">${svg("devices")}</button>
        <button data-i="${u.id}" class="iconbtn ustats" title="Traffic stats">${svg("chart")}</button>
        <button data-i="${u.id}" class="iconbtn edit" title="Edit">${svg("edit")}</button>
        <button data-i="${u.id}" class="iconbtn reset" title="Reset traffic">${svg("refresh")}</button>
        <button data-i="${u.id}" class="iconbtn danger del" title="Delete">${svg("trash")}</button>
      </div></td></tr>`;
  }).join("");
  holder.outerHTML = `<div class="card"><table>
    <thead><tr><th>User</th><th>Traffic</th><th>Devices</th><th>Expires</th><th>Nodes</th><th>State</th><th></th></tr></thead>
    <tbody>${rows}</tbody></table></div>`;
  main.querySelectorAll(".edit").forEach((b) => b.onclick = () => userForm(state.users.find((u) => u.id == b.dataset.i)));
  main.querySelectorAll(".sub").forEach((b) => b.onclick = () => showSub(b.dataset.i));
  main.querySelectorAll(".ustats").forEach((b) => b.onclick = () => showStats(b.dataset.i, state.users.find((u) => u.id == b.dataset.i)));
  main.querySelectorAll(".devs").forEach((b) => b.onclick = () => showDevices(b.dataset.i, state.users.find((u) => u.id == b.dataset.i)));
  main.querySelectorAll(".reset").forEach((b) => b.onclick = async () => {
    if (!confirm("Reset traffic counter for this user?")) return;
    try { await api("/api/users/" + b.dataset.i + "/reset-traffic", { method: "POST" }); toast("Traffic reset"); render(); }
    catch (e) { toast(e.message); }
  });
  main.querySelectorAll(".del").forEach((b) => b.onclick = async () => {
    if (!confirm("Delete this user? Their subscription stops working.")) return;
    try { await api("/api/users/" + b.dataset.i, { method: "DELETE" }); toast("User deleted"); render(); }
    catch (e) { toast(e.message); }
  });
}

function nodeChecks(selected) {
  const set = new Set((selected || []).map(Number));
  return state.nodes.map((n) => `<label><input type="checkbox" value="${n.id}" ${set.has(n.id) ? "checked" : ""}>${esc(n.name)}</label>`).join("")
    || `<span class="mut">No nodes; add one first.</span>`;
}

function userForm(u) {
  const isEdit = !!u; u = u || {};
  const limitGB = u.data_limit ? (u.data_limit / (1024 ** 3)).toString() : "";
  const f = $(`<div class="modal">
    <h3>${isEdit ? "Edit" : "Add"} user</h3>
    <p class="hint">The username doubles as the Xray traffic identity and can't change later.</p>
    <label>Username (unique)</label>
    <input id="username" value="${esc(u.username || "")}" ${isEdit ? "disabled" : ""} placeholder="alice">
    <div class="row">
      <div><label>Data limit (GB, 0 = unlimited)</label><input id="limit" type="number" min="0" step="0.1" value="${limitGB}"></div>
      <div><label>Expires at</label><input id="expire" type="datetime-local" value="${toLocalInput(u.expire_at)}"></div>
    </div>
    <label>Monthly reset day (1–28, 0 = off)</label><input id="reset_day" type="number" min="0" max="28" step="1" value="${u.reset_day || 0}">
    <label style="display:flex;align-items:center;gap:8px;margin-top:14px"><input type="checkbox" id="enabled" ${u.enabled === false ? "" : "checked"} style="width:auto"> Enabled</label>
    <label>Admin note (private — the user never sees this)</label>
    <textarea id="note" rows="2" placeholder="e.g. paid until March, contact @tg…">${esc(u.note || "")}</textarea>
    <label>Nodes</label><div class="checks">${nodeChecks(u.node_ids)}</div>
    <div class="actions"><button class="ghost" id="cancel">Cancel</button><button class="primary" id="save">Save</button></div>
  </div>`);
  const bg = modal(f);
  f.querySelector("#cancel").onclick = () => bg.remove();
  f.querySelector("#save").onclick = async () => {
    const limit = Math.round(parseFloat(f.querySelector("#limit").value || "0") * (1024 ** 3));
    const expire = toEpoch(f.querySelector("#expire").value);
    const resetDay = Math.max(0, Math.min(28, parseInt(f.querySelector("#reset_day").value || "0", 10) || 0));
    const enabled = f.querySelector("#enabled").checked;
    const note = f.querySelector("#note").value;
    const nodeIds = [...f.querySelectorAll(".checks input:checked")].map((c) => Number(c.value));
    try {
      if (isEdit) {
        await api("/api/users/" + u.id, { method: "PUT", body: JSON.stringify({ data_limit: limit, expire_at: expire, reset_day: resetDay, note, enabled }) });
        await api("/api/users/" + u.id + "/nodes", { method: "POST", body: JSON.stringify({ node_ids: nodeIds }) });
      } else {
        const name = f.querySelector("#username").value.trim();
        if (!name) { toast("Username is required"); return; }
        await api("/api/users", { method: "POST", body: JSON.stringify({ username: name, data_limit: limit, expire_at: expire, reset_day: resetDay, note, enabled, node_ids: nodeIds }) });
      }
      bg.remove(); toast(isEdit ? "User updated" : "User created"); render();
    } catch (e) { toast(e.message); }
  };
}

async function showSub(id) {
  let info;
  try { info = await api("/api/users/" + id + "/sub"); } catch (e) { toast(e.message); return; }
  const u = state.users.find((x) => x.id == id) || {};
  const portalURL = location.origin + "/portal";
  const f = $(`<div class="modal">
    <h3>Access — ${esc(u.username || "")}</h3>
    <p class="hint">Share the subscription URL with the client app, or give the user the self-service portal.</p>
    ${copyField("Base64 (v2rayN)", info.sub_url)}
    ${copyField("Clash / Mihomo", info.sub_url + "/clash")}
    ${copyField("sing-box", info.sub_url + "/singbox")}
    <div class="imp" style="display:flex;gap:10px;margin-top:12px">
      <button class="sm" id="rot-sub">${svg("refresh")}Rotate sub link</button>
      <button class="sm" id="rot-uuid">${svg("refresh")}Rotate UUID</button>
    </div>
    <div style="border-top:1px solid var(--line);margin:18px 0 4px"></div>
    <h3 style="font-size:15px">Self-service portal</h3>
    ${copyField("Portal URL", portalURL)}
    <p class="hint" id="pwstate">${u.has_portal_password ? "Password is set — user can sign in with their username." : "No password set yet — set one so the user can sign in."}</p>
    <div class="row">
      <div style="flex:2"><input id="pw" type="text" placeholder="New portal password (8+ with Aa1!)"></div>
      <div style="flex:1"><button id="setpw" style="width:100%">Set</button></div>
    </div>
    ${u.has_portal_password ? `<div style="margin-top:8px"><button class="sm danger" id="clearpw">Clear password (disable portal login)</button></div>` : ""}
    <div class="actions"><button class="primary" id="ok">Done</button></div>
  </div>`);
  const bg = modal(f);
  f.querySelectorAll("[data-copy]").forEach((b) => b.onclick = () => copy(b.dataset.copy));
  f.querySelector("#rot-sub").onclick = async () => {
    if (!confirm("Rotate the subscription link? The current link stops working immediately.")) return;
    try { await api("/api/users/" + id + "/rotate-sub", { method: "POST" }); toast("Subscription link rotated"); bg.remove(); showSub(id); }
    catch (e) { toast(e.message); }
  };
  f.querySelector("#rot-uuid").onclick = async () => {
    if (!confirm("Rotate the UUID? The user must re-import; existing clients stop connecting.")) return;
    try { await api("/api/users/" + id + "/rotate-uuid", { method: "POST" }); toast("UUID rotated & pushed to nodes"); bg.remove(); render(); }
    catch (e) { toast(e.message); }
  };
  f.querySelector("#setpw").onclick = async () => {
    const pw = f.querySelector("#pw").value.trim();
    if (!strongPw(pw)) { toast("Password needs 8+ chars with upper, lower, digit & special"); return; }
    try {
      await api("/api/users/" + id + "/portal-password", { method: "POST", body: JSON.stringify({ password: pw }) });
      toast("Portal password set"); bg.remove(); render();
    } catch (e) { toast(e.message); }
  };
  const clr = f.querySelector("#clearpw");
  if (clr) clr.onclick = async () => {
    if (!confirm("Clear the portal password? The user will no longer be able to sign in to the portal.")) return;
    try {
      await api("/api/users/" + id + "/portal-password", { method: "POST", body: JSON.stringify({ password: "" }) });
      toast("Portal password cleared"); bg.remove(); render();
    } catch (e) { toast(e.message); }
  };
  f.querySelector("#ok").onclick = () => bg.remove();
}

// barChart renders a self-contained SVG bar chart of daily total bytes.
function barChart(hist) {
  const W = 420, H = 140, pad = 22;
  const totals = hist.map((d) => Number(d.up || 0) + Number(d.down || 0));
  const max = Math.max(1, ...totals);
  const bw = (W - pad * 2) / hist.length;
  const bars = hist.map((d, i) => {
    const t = totals[i];
    const h = Math.round((H - pad * 2) * (t / max));
    const x = pad + i * bw;
    const y = H - pad - h;
    const title = `${d.day}: ${fmtBytes(t)}`;
    return `<rect x="${(x + 1).toFixed(1)}" y="${y}" width="${Math.max(1, bw - 2).toFixed(1)}" height="${h}" rx="1.5" fill="#5b8dff"><title>${esc(title)}</title></rect>`;
  }).join("");
  const first = hist[0] ? hist[0].day.slice(5) : "";
  const last = hist.length ? hist[hist.length - 1].day.slice(5) : "";
  return `<svg viewBox="0 0 ${W} ${H}" width="100%" style="max-width:100%">
    <line x1="${pad}" y1="${H - pad}" x2="${W - pad}" y2="${H - pad}" stroke="#2c3862"/>
    ${bars}
    <text x="${pad}" y="${H - 6}" fill="#6b789a" font-size="10">${first}</text>
    <text x="${W - pad}" y="${H - 6}" fill="#6b789a" font-size="10" text-anchor="end">${last}</text>
    <text x="${pad}" y="14" fill="#6b789a" font-size="10">peak ${fmtBytes(max)}/day</text>
  </svg>`;
}

async function showStats(id, u) {
  u = u || {};
  const nodeName = (nid) => { const n = state.nodes.find((x) => x.id == nid); return n ? n.name : "#" + nid; };
  const f = $(`<div class="modal" style="max-width:560px">
    <h3>Traffic — ${esc(u.username || "")}</h3>
    <p class="hint">Daily usage (UTC), last 30 days.</p>
    <div id="chart" class="mut" style="padding:20px 0;text-align:center">Loading…</div>
    <div class="sectlabel" style="margin-top:18px">By node</div>
    <div id="bynode" class="mut" style="padding:6px 0">Loading…</div>
    <div class="actions"><button class="primary" id="ok">Close</button></div>
  </div>`);
  const bg = modal(f);
  f.querySelector("#ok").onclick = () => bg.remove();
  try {
    const [hist, byNode] = await Promise.all([
      api("/api/users/" + id + "/traffic-history?days=30"),
      api("/api/users/" + id + "/traffic-nodes"),
    ]);
    const total = hist.reduce((s, d) => s + Number(d.up || 0) + Number(d.down || 0), 0);
    f.querySelector("#chart").outerHTML = `<div>${barChart(hist)}<div class="mut" style="font-size:12px;margin-top:8px">30-day total: ${fmtBytes(total)}</div></div>`;
    if (!byNode || !byNode.length) {
      f.querySelector("#bynode").textContent = "No per-node traffic yet.";
    } else {
      const rows = byNode.map((n) => trafficRow(esc(nodeName(n.node_id)), n.up, n.down)).join("");
      f.querySelector("#bynode").outerHTML = `<div class="card" style="box-shadow:none"><table>
        <thead><tr><th>Node</th><th>Up</th><th>Down</th><th>Total</th></tr></thead><tbody>${rows}</tbody></table></div>`;
    }
  } catch (e) { f.querySelector("#chart").textContent = e.message; }
}

async function showDevices(id, u) {
  u = u || {};
  const nodeName = (nid) => { const n = state.nodes.find((x) => x.id == nid); return n ? n.name : "#" + nid; };
  const f = $(`<div class="modal" style="max-width:600px">
    <h3>Devices — ${esc(u.username || "")}</h3>
    <p class="hint">Distinct client source IPs and how they connected. No browsing destinations are recorded.</p>
    <div id="body" class="mut" style="padding:14px 0">Loading…</div>
    <div class="actions"><button class="primary" id="ok">Close</button></div></div>`);
  const bg = modal(f);
  f.querySelector("#ok").onclick = () => bg.remove();
  try {
    const devs = await api("/api/users/" + id + "/devices");
    if (!devs.length) { f.querySelector("#body").textContent = "No devices observed yet."; return; }
    const rows = devs.map((d) => `<tr>
      <td class="mono">${esc(d.ip)}</td>
      <td><span class="tag">${esc(d.inbound || "-")}</span></td>
      <td class="mut">${esc(nodeName(d.node_id))}</td>
      <td class="mut">${d.conns}</td>
      <td class="mut" title="${esc(fmtDate(d.last_seen))}">${relTime(d.last_seen)}</td></tr>`).join("");
    f.querySelector("#body").outerHTML = `<div class="card" style="box-shadow:none;max-height:340px;overflow:auto"><table>
      <thead><tr><th>Source IP</th><th>Type</th><th>Node</th><th>Conns</th><th>Last seen</th></tr></thead>
      <tbody>${rows}</tbody></table></div>`;
  } catch (e) { f.querySelector("#body").textContent = e.message; }
}

// ---------- settings ----------
async function showSettings() {
  let s;
  try { s = await api("/api/settings"); } catch (e) { toast(e.message); return; }
  const f = $(`<div class="modal"><h3>Settings</h3>
    <p class="hint">Panel configuration — stored in the panel, not the environment.</p>
    <label>Public URL</label>
    <input id="purl" value="${esc(s.public_url || "")}" placeholder="https://panel.example.com">
    <div class="mut" style="font-size:11.5px;margin-top:4px">Used in subscription links and the node install command.</div>
    <label>Cookie security</label>
    <select id="cook">
      <option value="auto">Auto — Secure when the public URL is https</option>
      <option value="on">On — always mark cookies Secure (HTTPS only)</option>
      <option value="off">Off — allow plain HTTP (not for production)</option>
    </select>
    <label>Daily DB backups to keep (0 = disable)</label>
    <input id="bkeep" type="number" min="0" max="365" value="${Number(s.backup_keep) || 0}">
    <label>Clash template (optional)</label>
    <div class="mut" style="font-size:11.5px;margin:0 0 4px">Paste your full Clash config to keep your DNS / rules / routing. Put <code>{{PROXIES}}</code> on its own line under <code>proxies:</code> and <code>{{PROXY_NAMES}}</code> inside each proxy-group's <code>proxies: [ ]</code>. Leave blank for the built-in minimal config.</div>
    <textarea id="ctpl" rows="8" spellcheck="false" style="width:100%;box-sizing:border-box;font-family:monospace;font-size:12px" placeholder="proxies:\n{{PROXIES}}\nproxy-groups:\n  - {name: PROXY, type: select, proxies: [{{PROXY_NAMES}}]}">${esc(s.clash_template || "")}</textarea>
    <div class="actions"><button class="ghost" id="cancel">Close</button><button class="primary" id="save">Save</button></div>
  </div>`);
  const bg = modal(f);
  f.querySelector("#cook").value = s.cookie_secure || "auto";
  f.querySelector("#cancel").onclick = () => bg.remove();
  f.querySelector("#save").onclick = async () => {
    const body = {
      public_url: f.querySelector("#purl").value.trim(),
      cookie_secure: f.querySelector("#cook").value,
      backup_keep: parseInt(f.querySelector("#bkeep").value || "0", 10) || 0,
      clash_template: f.querySelector("#ctpl").value,
    };
    try { await api("/api/settings", { method: "PUT", body: JSON.stringify(body) }); toast("Settings saved"); bg.remove(); }
    catch (e) { toast(e.message); }
  };
}

// ---------- 2FA ----------
async function show2FA() {
  let st, notify = { enabled: false };
  try { st = await api("/api/2fa"); } catch (e) { toast(e.message); return; }
  try { notify = await api("/api/notify"); } catch (e) { /* non-fatal */ }
  const f = $(`<div class="modal"><h3>Security &amp; notifications</h3><div id="body"></div></div>`);
  const bg = modal(f);
  const body = f.querySelector("#body");
  const render2fa = (state) => {
    if (state.enabled) {
      body.innerHTML = `<p class="hint">2FA is <b style="color:var(--ok)">enabled</b> on this account.</p>
        <label>Enter a current code to disable</label><input id="dc" inputmode="numeric" placeholder="123456">
        <div class="actions"><button id="close">Close</button><button class="danger" id="disable">Disable 2FA</button></div>`;
      body.querySelector("#close").onclick = () => bg.remove();
      body.querySelector("#disable").onclick = async () => {
        try { await api("/api/2fa/disable", { method: "POST", body: JSON.stringify({ code: body.querySelector("#dc").value.trim() }) });
          toast("2FA disabled"); bg.remove(); }
        catch (e) { toast(e.message); }
      };
    } else {
      body.innerHTML = `<p class="hint">Scan the QR with an authenticator app, then enter a code to enable.</p>
        <div style="text-align:center;margin:10px 0"><img alt="2fa qr" style="width:180px;height:180px;background:#fff;padding:8px;border-radius:10px" src="/api/2fa/qr?ts=${Date.now()}"></div>
        <label>Code from your app</label><input id="vc" inputmode="numeric" placeholder="123456">
        <div class="actions"><button id="cancel">Cancel</button><button class="primary" id="enable">Enable</button></div>`;
      body.querySelector("#cancel").onclick = () => bg.remove();
      body.querySelector("#enable").onclick = async () => {
        try { await api("/api/2fa/verify", { method: "POST", body: JSON.stringify({ code: body.querySelector("#vc").value.trim() }) });
          toast("2FA enabled"); bg.remove(); }
        catch (e) { toast(e.message); }
      };
    }
    // Telegram — editable settings, saved to the panel (no restart needed)
    const nt = document.createElement("div");
    nt.style.cssText = "border-top:1px solid var(--line);margin-top:16px;padding-top:12px";
    const on = notify.enabled;
    const ids = (notify.admin_ids || []).join(", ");
    nt.innerHTML = `<div style="display:flex;align-items:center;gap:8px;margin-bottom:4px">
        <b style="font-size:13px">Telegram</b><span class="pill ${on ? "on" : "off"}">${on ? "enabled" : "off"}</span></div>
      <label>Bot token</label>
      <input id="tgtok" type="password" autocomplete="off" placeholder="${notify.has_token ? "•••••• (leave blank to keep)" : "123456:ABC… from @BotFather"}">
      <label>Admin chat IDs (comma-separated)</label>
      <input id="tgids" value="${esc(ids)}" placeholder="123456789, 987654321">
      <div style="display:flex;gap:8px;margin-top:12px;flex-wrap:wrap">
        <button class="primary sm" id="tgsave">Save</button>
        <button class="sm" id="tgtest">Send test</button>
        ${notify.has_token ? `<button class="sm danger" id="tgoff">Disable</button>` : ""}
      </div>
      <div class="mut" style="font-size:11.5px;margin-top:8px">Token from @BotFather; your numeric id from @userinfobot. Alerts on: ${(notify.events || []).join(", ")}.</div>`;
    body.appendChild(nt);
    nt.querySelector("#tgsave").onclick = async () => {
      const tok = nt.querySelector("#tgtok").value;
      const b = { admin_ids: nt.querySelector("#tgids").value };
      if (tok.trim() !== "") b.token = tok.trim();   // blank = keep current token
      try { await api("/api/notify", { method: "PUT", body: JSON.stringify(b) }); toast("Telegram settings saved"); bg.remove(); show2FA(); }
      catch (e) { toast(e.message); }
    };
    nt.querySelector("#tgtest").onclick = async () => {
      try { await api("/api/notify/test", { method: "POST" }); toast("Test message sent"); }
      catch (e) { toast(e.message); }
    };
    const offBtn = nt.querySelector("#tgoff");
    if (offBtn) offBtn.onclick = async () => {
      if (!confirm("Disable Telegram? This clears the bot token.")) return;
      try { await api("/api/notify", { method: "PUT", body: JSON.stringify({ token: "", admin_ids: nt.querySelector("#tgids").value }) }); toast("Telegram disabled"); bg.remove(); show2FA(); }
      catch (e) { toast(e.message); }
    };

    const so = document.createElement("div");
    so.style.cssText = "border-top:1px solid var(--line);margin-top:16px;padding-top:12px;display:flex;gap:8px;flex-wrap:wrap";
    so.innerHTML = `<button id="admins">Manage admins</button><button id="audit">Audit log</button><button class="danger" id="logoutall">Sign out everywhere</button>`;
    body.appendChild(so);
    so.querySelector("#admins").onclick = () => { bg.remove(); showAdmins(); };
    so.querySelector("#audit").onclick = () => { bg.remove(); showAudit(); };
    so.querySelector("#logoutall").onclick = async () => {
      if (!confirm("Sign out of all sessions? You will need to log in again.")) return;
      try { await api("/api/logout-all", { method: "POST" }); } catch {}
      state.me = null; bg.remove(); renderLogin();
    };
  };
  if (st.enabled) { render2fa(st); return; }
  // not enabled: (re)provision a secret, then show the QR
  try { await api("/api/2fa/setup", { method: "POST" }); } catch (e) { /* may already be pending */ }
  render2fa({ enabled: false });
}

async function showAdmins() {
  let admins;
  try { admins = await api("/api/admins"); } catch (e) { toast(e.message); return; }
  const rows = admins.map((u) => `<tr><td><b>${esc(u)}</b>${u === state.me ? ` <span class="mut">(you)</span>` : ""}</td>
    <td class="actions-cell"><button class="sm passwd" data-u="${esc(u)}">Password</button>
    <button class="sm danger del" data-u="${esc(u)}">Delete</button></td></tr>`).join("");
  const f = $(`<div class="modal"><h3>Admin accounts</h3>
    <div class="card" style="box-shadow:none;margin:0 0 14px"><table><tbody>${rows}</tbody></table></div>
    <h3 style="font-size:15px">Add admin</h3>
    <div class="row"><div><input id="au" placeholder="username"></div><div><input id="ap" type="text" placeholder="password (8+ with Aa1!)"></div></div>
    <div class="actions"><button id="close">Close</button><button class="primary" id="add">Add admin</button></div></div>`);
  const bg = modal(f);
  f.querySelector("#close").onclick = () => bg.remove();
  f.querySelector("#add").onclick = async () => {
    try { await api("/api/admins", { method: "POST", body: JSON.stringify({ username: f.querySelector("#au").value.trim(), password: f.querySelector("#ap").value }) });
      toast("Admin added"); bg.remove(); showAdmins(); }
    catch (e) { toast(e.message); }
  };
  f.querySelectorAll(".del").forEach((b) => b.onclick = async () => {
    if (!confirm("Delete admin " + b.dataset.u + "?")) return;
    try { await api("/api/admins/" + encodeURIComponent(b.dataset.u), { method: "DELETE" }); toast("Admin deleted"); bg.remove(); showAdmins(); }
    catch (e) { toast(e.message); }
  });
  f.querySelectorAll(".passwd").forEach((b) => b.onclick = async () => {
    const pw = prompt("New password for " + b.dataset.u + " (8+ with upper, lower, digit, special):");
    if (!pw) return;
    try { await api("/api/admins/" + encodeURIComponent(b.dataset.u) + "/password", { method: "POST", body: JSON.stringify({ password: pw }) });
      toast("Password changed"); }
    catch (e) { toast(e.message); }
  });
}

async function showAudit() {
  let entries;
  try { entries = await api("/api/audit"); } catch (e) { toast(e.message); return; }
  const rows = entries.map((e) => `<tr>
    <td class="mut" style="white-space:nowrap">${esc(fmtDate(e.ts))}</td>
    <td>${esc(e.actor)}</td><td><span class="tag">${esc(e.action)}</span></td>
    <td class="mut">${esc(e.detail || "")}</td></tr>`).join("");
  const f = $(`<div class="modal" style="max-width:640px"><h3>Audit log</h3>
    <div class="card" style="box-shadow:none;max-height:420px;overflow:auto"><table>
    <thead><tr><th>When</th><th>Actor</th><th>Action</th><th>Detail</th></tr></thead>
    <tbody>${rows || `<tr><td colspan="4" class="mut" style="text-align:center;padding:20px">No entries</td></tr>`}</tbody></table></div>
    <div class="actions"><button class="primary" id="ok">Close</button></div></div>`);
  const bg = modal(f);
  f.querySelector("#ok").onclick = () => bg.remove();
}

// ---------- boot ----------
async function boot() {
  try {
    const me = await api("/api/me");
    if (!me.username) { renderLogin(); return; }
    state.me = me.username; state.version = me.version; render();
  } catch { renderLogin(); }
}
boot();
