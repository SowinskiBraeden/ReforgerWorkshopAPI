/* Internal admin panel for reforgermods.net.
   Self-contained vanilla JS: hash routing, fetch against /internal/api/*,
   SVG charts, tables with server-side pagination, and detail drawers.
   Server-side authorization is authoritative; role checks here only hide UI. */
"use strict";

/* ---------- utilities ---------- */

const $ = (sel, el) => (el || document).querySelector(sel);
const esc = (v) => String(v == null ? "" : v)
  .replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
  .replaceAll('"', "&quot;").replaceAll("'", "&#39;");

const state = {
  range: localStorage.getItem("rfmAdmin.range") || "30d",
  from: "", to: "",
  tz: localStorage.getItem("rfmAdmin.tz") || "utc",
  session: null,
  timer: null,
};

function rangeParams() {
  const p = { range: state.range };
  if (state.range === "custom") { p.from = state.from; p.to = state.to; }
  return p;
}

async function api(path, params) {
  const url = new URL(path, location.origin);
  for (const [k, v] of Object.entries(params || {})) {
    if (v !== "" && v != null) url.searchParams.set(k, v);
  }
  const res = await fetch(url, { credentials: "same-origin" });
  if (res.status === 401) { location.reload(); throw new Error("session expired"); }
  if (!res.ok) {
    let msg = res.status + " " + res.statusText;
    try { const body = await res.json(); if (body.error) msg = body.error.message || body.error.code; } catch {}
    throw new Error(msg);
  }
  return res.json();
}

async function mutate(path, method, body) {
  const res = await fetch(path, {
    method, credentials: "same-origin",
    headers: { "Content-Type": "application/json", "X-Admin-CSRF": "1" },
    body: body == null ? undefined : JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error((data.error && (data.error.message || data.error.code)) || res.statusText);
  return data;
}

const nf = new Intl.NumberFormat("en-US");
const countryNames = typeof Intl !== "undefined" && Intl.DisplayNames
  ? new Intl.DisplayNames(["en"], { type: "region" })
  : null;
const num = (v) => nf.format(Math.round(v || 0));
const pct = (a, b) => (b > 0 ? (100 * a / b).toFixed(1) + "%" : "–");
const ratio = (v) => (v == null ? "–" : (100 * v).toFixed(0) + "%");
const msFmt = (v) => (v == null || isNaN(v) ? "–" : (v >= 1000 ? (v / 1000).toFixed(2) + " s" : Number(v).toFixed(v < 10 ? 1 : 0) + " ms"));
const bytesFmt = (v) => {
  if (!v) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0; let n = v;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return n.toFixed(n >= 10 || i === 0 ? 0 : 1) + " " + units[i];
};
function dt(v) {
  if (!v) return "–";
  const d = new Date(v);
  if (isNaN(d) || d.getTime() <= 0) return "–";
  return state.tz === "utc"
    ? d.toISOString().replace("T", " ").slice(0, 19) + "Z"
    : d.toLocaleString();
}
function delta(cur, prev) {
  if (!prev) return `<span class="delta flat">·</span>`;
  const change = 100 * (cur - prev) / prev;
  const cls = change > 0.5 ? "up" : change < -0.5 ? "down" : "flat";
  const sign = change > 0 ? "+" : "";
  return `<span class="delta ${cls}" title="previous period: ${num(prev)}">${sign}${change.toFixed(1)}%</span>`;
}
function countryName(code) {
  const c = String(code || "").trim().toUpperCase();
  if (!c || c === "ZZ") return "Unknown";
  try { return countryNames ? countryNames.of(c) || c : c; } catch { return c; }
}
function countryLabel(code) {
  const c = String(code || "").trim().toUpperCase();
  const name = countryName(c);
  return c && name !== c ? name : name;
}
function countryItems(items) {
  return (items || []).map(item => ({ ...item, label: countryLabel(item.key || item.countryCode) }));
}
function planClass(plan) {
  return plan === "pro" || plan === "developer" ? "ok" : plan === "internal" ? "warn" : "info";
}
function subscriptionPill(status) {
  const s = String(status || "none");
  return `<span class="pill ${s === "active" || s === "trialing" ? "ok" : s === "past_due" ? "warn" : ""}">${esc(s)}</span>`;
}
function userContact(row) {
  const email = row.email || "";
  if (!email) return `<span class="note">No email</span>`;
  return `<a href="mailto:${esc(email)}">${esc(email)}</a>`;
}

/* ---------- drawer ---------- */

function openDrawer(html) {
  $("#drawerbody").innerHTML = html;
  $("#drawer").classList.remove("hidden");
}
function closeDrawer() { $("#drawer").classList.add("hidden"); }
function kv(pairs) {
  return `<dl class="kv">` + pairs
    .filter(([, v]) => v !== "" && v != null)
    .map(([k, v, copy]) => `<dt>${esc(k)}</dt><dd>${v}${copy ? copyBtn(copy) : ""}</dd>`).join("") + `</dl>`;
}
function copyBtn(text) {
  return ` <span class="copy" data-copy="${esc(text)}">copy</span>`;
}
document.addEventListener("click", (e) => {
  const copy = e.target.closest("[data-copy]");
  if (copy) navigator.clipboard.writeText(copy.dataset.copy).catch(() => {});
});

/* ---------- charts ---------- */

const seriesColors = ["#26c29a", "#4aa3ff", "#ffb86b", "#ff6b6b", "#b28dff", "#6be0ff", "#e0d36b", "#ff8dc7"];

function lineChart(points, opts = {}) {
  // points: [{bucket, ...metrics}] ; opts.metrics: [{key, color, label}]
  const metrics = opts.metrics || [{ key: "requests", color: seriesColors[0], label: "requests" }];
  if (!points || !points.length) return `<div class="note">No data in range.</div>`;
  const W = 900, H = 150, P = 28;
  const groups = opts.groups; // grouped mode: map group -> points
  const buckets = [...new Set(points.map(p => p.bucket))].sort();
  const x = (i) => P + i * (W - P - 6) / Math.max(1, buckets.length - 1);
  let max = 1;
  const lines = [];
  if (groups) {
    const byGroup = {};
    for (const p of points) (byGroup[p.group || ""] = byGroup[p.group || ""] || {})[p.bucket] = p;
    Object.entries(byGroup).forEach(([g, m], gi) => {
      const vals = buckets.map(b => (m[b] ? m[b][metrics[0].key] || 0 : 0));
      max = Math.max(max, ...vals);
      lines.push({ label: opts.groupLabel ? opts.groupLabel(g) : (g || "(none)"), color: seriesColors[gi % seriesColors.length], vals });
    });
  } else {
    for (const [mi, metric] of metrics.entries()) {
      const byBucket = Object.fromEntries(points.map(p => [p.bucket, p]));
      const vals = buckets.map(b => (byBucket[b] ? byBucket[b][metric.key] || 0 : 0));
      max = Math.max(max, ...vals);
      lines.push({ label: metric.label || metric.key, color: metric.color || seriesColors[mi % seriesColors.length], vals });
    }
  }
  const y = (v) => H - 18 - v * (H - 30) / max;
  let svg = `<svg class="chart" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none">`;
  svg += `<line class="axis" x1="${P}" y1="${H - 18}" x2="${W - 4}" y2="${H - 18}"/>`;
  svg += `<text x="2" y="12">${num(max)}</text><text x="2" y="${H - 20}">0</text>`;
  const step = Math.max(1, Math.floor(buckets.length / 8));
  buckets.forEach((b, i) => {
    if (i % step === 0) svg += `<text x="${x(i)}" y="${H - 5}">${esc(b.slice(5))}</text>`;
  });
  for (const line of lines) {
    const d = line.vals.map((v, i) => `${i ? "L" : "M"}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
    svg += `<path d="${d}" fill="none" stroke="${line.color}" stroke-width="1.6"/>`;
  }
  svg += `</svg>`;
  if (lines.length > 1 || opts.legend) {
    svg += `<div class="legend">` + lines.slice(0, 10).map(l =>
      `<span><i style="background:${l.color}"></i>${esc(l.label)}</span>`).join("") + `</div>`;
  }
  return svg;
}

function barList(items, opts = {}) {
  if (!items || !items.length) return `<div class="note">No data.</div>`;
  const max = Math.max(...items.map(i => i.count || 0), 1);
  return items.map(i => {
    const filterAttr = opts.filter ? ` class="bar click" data-filter="${esc(opts.filter)}" data-value="${esc(i.key)}"` : ` class="bar"`;
    return `<div${filterAttr}>
      <span class="label" title="${esc(i.label || i.key)}">${esc(i.label || i.key)}</span>
      <span class="track"><span class="fill" style="width:${(100 * (i.count || 0) / max).toFixed(1)}%"></span></span>
      <span class="val">${num(i.count)}${i.extraStr ? " · " + esc(i.extraStr) : ""}</span>
    </div>`;
  }).join("");
}

function heatmap(rows) {
  if (!rows || !rows.length) return `<div class="note">No cohort data yet.</div>`;
  const periods = rows[0].periods.length;
  let html = `<table class="heat"><tr><th>Cohort</th><th>Size</th>`;
  for (let i = 0; i < periods; i++) html += `<th>+${i}</th>`;
  html += `</tr>`;
  for (const row of rows) {
    html += `<tr><td style="width:auto;padding:0 8px">${esc(row.cohort)}</td><td>${num(row.size)}</td>`;
    row.periods.forEach((share, i) => {
      const alpha = share > 0 ? 0.15 + share * 0.85 : 0;
      html += `<td style="background:rgba(38,194,154,${alpha.toFixed(2)})" title="period +${i}: ${(share * 100).toFixed(0)}%">${share > 0 ? (share * 100).toFixed(0) : ""}</td>`;
    });
    html += `</tr>`;
  }
  return html + `</table>`;
}

/* ---------- table ---------- */

function table(columns, rows, onRowAttrs) {
  // columns: [{key,label,fmt}] rows: objects
  let html = `<div class="tablewrap"><table><thead><tr>` +
    columns.map(c => `<th>${esc(c.label)}</th>`).join("") + `</tr></thead><tbody>`;
  for (const row of rows || []) {
    const attrs = onRowAttrs ? onRowAttrs(row) : "";
    html += `<tr ${attrs ? `class="click" ${attrs}` : ""}>` + columns.map(c => {
      const value = c.fmt ? c.fmt(row[c.key], row) : esc(row[c.key]);
      return `<td data-label="${esc(c.label)}">${value == null ? "" : value}</td>`;
    }).join("") + `</tr>`;
  }
  if (!rows || !rows.length) html += `<tr><td colspan="${columns.length}" class="note">No rows.</td></tr>`;
  return html + `</tbody></table></div>`;
}

function pager(total, limit, offset, key) {
  const page = Math.floor(offset / limit) + 1;
  const pages = Math.max(1, Math.ceil(total / limit));
  return `<div class="pager">
    <button data-page="${key}" data-offset="${Math.max(0, offset - limit)}" ${offset <= 0 ? "disabled" : ""}>‹ Prev</button>
    <span>Page ${page} / ${pages} · ${num(total)} rows</span>
    <button data-page="${key}" data-offset="${offset + limit}" ${offset + limit >= total ? "disabled" : ""}>Next ›</button>
  </div>`;
}

const statusPill = (s) => {
  const cls = s >= 500 ? "bad" : s === 429 ? "warn" : s >= 400 ? "warn" : "ok";
  return `<span class="pill ${cls}">${s}</span>`;
};
const cachePill = (s) => s ? `<span class="pill ${s === "HIT" ? "ok" : s === "STALE" ? "warn" : "info"}">${esc(s)}</span>` : "";
const sourcePill = (s) => `<span class="pill info">${esc(s)}</span>`;

/* ---------- navigation ---------- */

const NAV = [
  ["Main"],
  ["overview", "Overview"], ["requests", "Requests"], ["audience", "Audience"],
  ["retention", "Retention"], ["users", "Users"],
  ["Ops"],
  ["realtime", "Real-time"], ["errors", "Errors"], ["health", "Health"],
  ["Deep Dive"],
  ["endpoints", "Endpoints"], ["performance", "Performance"],
  ["cache", "Cache"], ["ratelimits", "Rate Limits"], ["insights", "Insights & Export"],
  ["logs", "Logs"], ["jobs", "Background Jobs"],
  ["Admin"],
  ["audit", "Audit Log"], ["settings", "Settings"],
];

// Old bookmarks and cross-links keep working.
const PAGE_ALIASES = {
  traffic: "requests", clients: "audience", geography: "audience",
  networks: "audience", searches: "insights", marketing: "insights", keys: "users",
};

function renderNav() {
  $("#navlinks").innerHTML = NAV.map(item => item.length === 1
    ? `<div class="sep">${item[0]}</div>`
    : `<a href="#/${item[0]}" data-page="${item[0]}">${item[1]}</a>`).join("");
}

function currentPage() {
  const page = (location.hash.replace(/^#\//, "") || "overview").split("?")[0];
  return PAGE_ALIASES[page] || page;
}

async function navigate() {
  clearInterval(state.timer);
  const page = currentPage();
  document.querySelectorAll("#navlinks a").forEach(a =>
    a.classList.toggle("active", a.dataset.page === page));
  const def = PAGES[page] || PAGES.overview;
  $("#pagetitle").textContent = def.title;
  const el = $("#content");
  el.innerHTML = `<div class="loading">Loading…</div>`;
  try {
    await def.render(el);
  } catch (err) {
    el.innerHTML = `<div class="error-note">Failed to load: ${esc(err.message)}</div>`;
  }
}

/* ---------- pages ---------- */

const PAGES = {};

PAGES.overview = {
  title: "Overview",
  async render(el) {
    const d = await api("/internal/api/overview", rangeParams());
    const t = d.totals, p = d.previous || {};
    const cacheTotal = t.cacheHit + t.cacheStale + t.cacheMiss;
    // One card per question; detail lives on the linked page.
    const cards = [
      ["Requests", num(t.requests) + delta(t.requests, p.requests), "#/requests"],
      ["Active users", num(t.uniqueAccounts) + delta(t.uniqueAccounts, p.uniqueAccounts), "#/users"],
      ["Active integrations", num(t.uniqueClients) + delta(t.uniqueClients, p.uniqueClients), "#/audience"],
      ["New integrations", num(d.newClients), "#/retention"],
      ["Error rate", pct(t.errors, t.requests) + delta(t.errors, p.errors), "#/errors"],
      ["p95 latency", msFmt(t.latency.p95Ms), "#/performance"],
      ["Cache served", pct(t.cacheHit + t.cacheStale, cacheTotal), "#/cache"],
      ["Job queue", num(d.gauges.refresh_queue_depth), "#/jobs"],
    ];
    const sources = Object.entries(t.bySource || {}).sort((a, b) => b[1] - a[1])
      .map(([k, v]) => ({ key: k, label: k, count: v }));
    el.innerHTML = `
      <div class="grid cards">${cards.map(([label, value, href]) =>
        `<div class="card link" onclick="location.hash='${href.slice(1)}'">
          <h3>${label}</h3><div class="big">${value}</div></div>`).join("")}</div>
      <div class="card">${lineChart(d.series, { metrics: [
        { key: "requests", label: "requests" }, { key: "errors", label: "errors", color: "#ff6b6b" },
        { key: "rateLimited", label: "rate limited", color: "#ffb86b" }], legend: true })}</div>
      <div class="grid three" style="margin-top:12px">
        <div class="card"><h3>Traffic sources</h3>${barList(sources, { filter: "source" })}</div>
        <div class="card"><h3>Top endpoints</h3>${barList((d.topEndpoints || []).slice(0, 6), { filter: "route" })}</div>
        <div class="card"><h3>Countries</h3>${barList(countryItems((d.topCountries || []).slice(0, 6)), { filter: "country" })}</div>
      </div>
      <div class="card"><h3>Recent errors <a class="copy" href="#/errors">all →</a></h3>${table(
        [
          { key: "at", label: "Time", fmt: dt }, { key: "severity", label: "Sev" },
          { key: "message", label: "Message" }, { key: "routeTemplate", label: "Route" },
          { key: "status", label: "Status", fmt: (v) => v ? statusPill(v) : "" },
        ], (d.recentErrors || []).slice(0, 5), (row) => `data-error="${esc(row.errorId)}"`)}</div>
      <div class="note">Version ${esc(d.version)} · Aggregation: last run ${esc(d.aggregation.last_aggregation_run || "never")}${d.aggregation.last_aggregation_error ? ` · <span class="pill bad">error: ${esc(d.aggregation.last_aggregation_error)}</span>` : ""}</div>`;
    bindBarFilters(el);
    bindErrorRows(el);
  },
};

function bindBarFilters(el) {
  el.querySelectorAll(".bar.click").forEach(bar => bar.addEventListener("click", () => {
    location.hash = `#/requests?${bar.dataset.filter}=${encodeURIComponent(bar.dataset.value)}`;
  }));
}
function bindErrorRows(el) {
  el.querySelectorAll("tr[data-error]").forEach(tr => tr.addEventListener("click", () => showErrorDrawer(tr.dataset.error)));
}

PAGES.realtime = {
  title: "Real-time",
  async render(el) {
    const load = async () => {
      const d = await api("/internal/api/realtime");
      const cards = [
        ["Requests (5m)", num(d.last5m.requests)], ["Requests (60m)", num(d.last60m.requests)],
        ["Active clients (60m)", num(d.last60m.uniqueClients)],
        ["Error rate (60m)", pct(d.last60m.errors, d.last60m.requests)],
        ["Rate limited (60m)", pct(d.last60m.rateLimited, d.last60m.requests)],
        ["p95 (60m)", msFmt(d.last60m.latency.p95Ms)],
        ["Queue depth", num(d.gauges.refresh_queue_depth)],
        ["Workers", `${num(d.gauges.refresh_active_workers)} / ${num(d.gauges.refresh_workers)}`],
      ];
      el.innerHTML = `
        <div class="note">Auto-refreshes every 10 seconds. Generated ${dt(d.generatedAt)}. Failures land in <a href="#/errors">Errors</a>; slow requests in <a href="#/performance">Performance</a>.</div>
        <div class="grid cards">${cards.map(([l, v]) => `<div class="card"><h3>${l}</h3><div class="big">${v}</div></div>`).join("")}</div>
        <div class="card"><h3>Requests per minute (30m)</h3>${lineChart(d.perMinute)}</div>
        <div class="card" style="margin-top:12px"><h3>Live request feed (15m)</h3>${requestTable(d.recent)}</div>`;
      bindRequestRows(el);
    };
    await load();
    state.timer = setInterval(() => { if (currentPage() === "realtime") load().catch(() => {}); }, 10000);
  },
};

function requestTable(rows) {
  return table([
    { key: "at", label: "Time", fmt: dt },
    { key: "method", label: "M" },
    { key: "requestPath", label: "Path", fmt: (v, r) => esc(v) + (r.query ? `<span class="note">?${esc(r.query)}</span>` : "") },
    { key: "status", label: "Status", fmt: statusPill },
    { key: "durationMs", label: "Time", fmt: msFmt },
    { key: "source", label: "Source", fmt: sourcePill },
    { key: "cacheStatus", label: "Cache", fmt: cachePill },
    { key: "clientName", label: "Client" },
    { key: "countryCode", label: "Country", fmt: countryLabel },
  ], rows, (row) => `data-request="${esc(row.requestId || row.id)}"`);
}
function bindRequestRows(el) {
  el.querySelectorAll("tr[data-request]").forEach(tr =>
    tr.addEventListener("click", () => showRequestDrawer(tr.dataset.request)));
}

async function showRequestDrawer(id) {
  const d = await api("/internal/api/requests/" + encodeURIComponent(id));
  const r = d.request;
  openDrawer(`<h2>Request ${esc(r.requestId)}</h2>
    ${kv([
      ["Time", dt(r.at)], ["Method + route", esc(r.method + " " + r.routeTemplate)],
      ["Path", esc(r.requestPath), r.requestPath], ["Query", esc(r.query)],
      ["Status", statusPill(r.status)], ["Duration", msFmt(r.durationMs)],
      ["Response size", bytesFmt(r.responseBytes)], ["Source", sourcePill(r.source)],
      ["Client kind", esc(r.clientKind)], ["Auth", esc(r.authType)],
      ["User", r.accountID ? `<a href="#/users?open=${esc(r.accountID)}">${esc(r.accountID)}</a>` : ""],
      ["API key", esc(r.apiKeyID)], ["Client", esc(r.clientName) + (r.clientVerified ? ` <span class="pill ok">verified</span>` : r.clientName ? ` <span class="pill">self-reported</span>` : "")],
      ["Client version", esc(r.clientVersion)],
      ["User agent", esc(r.userAgent)],
      ["Country", esc(countryLabel(r.countryCode)) + " (approx.)"], ["Network ID", esc(r.networkID), r.networkID],
      ["ASN / network", esc([r.asn, r.networkName].filter(Boolean).join(" · ") || "unavailable")],
      ["Hosting", esc(r.isHosting)], ["Via trusted proxy", r.viaProxy ? "yes" : "no"],
      ["Cache", cachePill(r.cacheStatus) + " " + esc(r.refreshResult || "")],
      ["Rate limited", r.rateLimited ? `<span class="pill bad">yes</span> (${esc(r.rateBucket)})` : "no"],
      ["Error", esc([r.errorCategory, r.errorCode].filter(Boolean).join(" / "))],
      ["Search term", esc(r.searchTerm)], ["Mod ID", esc(r.modID)],
      ["App version", esc(r.appVersion)], ["Instance", esc(r.instanceID)],
      ["Request ID", esc(r.requestId), r.requestId],
    ])}
    <div class="section-title">Correlated logs (${(d.logs || []).length})</div>
    ${table([{ key: "at", label: "Time", fmt: dt }, { key: "level", label: "Level" }, { key: "message", label: "Message" }], d.logs)}
    ${(d.jobs || []).length ? `<div class="section-title">Background jobs triggered</div>` + table([
      { key: "jobId", label: "Job" }, { key: "status", label: "Status" }, { key: "durationMs", label: "Duration", fmt: msFmt }], d.jobs) : ""}
    ${(d.errors || []).length ? `<div class="section-title">Errors</div>` + table([
      { key: "at", label: "Time", fmt: dt }, { key: "message", label: "Message" }], d.errors) : ""}
    <div class="section-title">Raw event</div><pre>${esc(JSON.stringify(r, null, 2))}</pre>`);
}

// Requests combines the old Traffic (grouped chart) and Requests (explorer)
// pages: one chart on top, filterable row-level table below, sharing filters.
PAGES.requests = {
  title: "Requests",
  async render(el) {
    const query = new URLSearchParams(location.hash.split("?")[1] || "");
    const offset = Number(query.get("offset") || 0);
    const groupBy = query.get("group_by") || "source";
    const params = { ...rangeParams(), limit: 50, offset };
    for (const key of ["source", "route", "client", "country", "status", "status_family", "cache", "q", "method", "group", "user", "key", "network", "order", "min_ms", "rate_limited", "auth", "term", "error_category"]) {
      if (query.get(key)) params[key] = query.get(key);
    }
    const [d, series] = await Promise.all([
      api("/internal/api/requests", params),
      api("/internal/api/timeseries", { ...params, group_by: groupBy }),
    ]);
    el.innerHTML = `
      <div class="card">
        ${lineChart(series.series, { groups: true, legend: true, groupLabel: groupBy === "country" ? countryLabel : null })}
        <div class="legend">chart grouped by
          <select id="fgroupby">${["source", "endpoint_group", "route", "method", "status_family", "country", "cache", "auth", "client_kind", "client", "version"]
            .map(g => `<option value="${g}" ${g === groupBy ? "selected" : ""}>${g}</option>`).join("")}</select>
          · ${num(series.totals.requests)} requests, ${pct(series.totals.errors, series.totals.requests)} errors, ${pct(series.totals.rateLimited, series.totals.requests)} rate limited
        </div>
      </div>
      <div class="filters" style="margin-top:12px">
        <input id="fq" placeholder="free text (path, UA, client)" value="${esc(params.q || "")}">
        <input id="froute" placeholder="route template" value="${esc(params.route || "")}">
        <select id="fsource"><option value="">source: all</option>${["website", "internal-web", "api-key", "api-anon", "internal-service", "health", "monitoring", "crawler", "ai-crawler", "bot", "browser", "admin", "unknown"]
          .map(s => `<option ${params.source === s ? "selected" : ""}>${s}</option>`).join("")}</select>
        <select id="fstatus"><option value="">status: all</option>${[2, 3, 4, 5].map(f => `<option value="${f}" ${String(params.status_family) === String(f) ? "selected" : ""}>${f}xx</option>`).join("")}</select>
        <select id="fcache"><option value="">cache: all</option>${["HIT", "STALE", "MISS", "BYPASS"].map(c => `<option ${params.cache === c ? "selected" : ""}>${c}</option>`).join("")}</select>
        <input id="fminms" placeholder="min ms" size="6" value="${esc(params.min_ms || "")}">
        <button id="fapply" class="primary">Filter</button>
      </div>
      ${requestTable(d.requests)}
      ${pager(d.total, d.limit, d.offset, "requests")}`;
    bindRequestRows(el);
    const apply = () => {
      const parts = new URLSearchParams();
      if ($("#fq").value) parts.set("q", $("#fq").value);
      if ($("#froute").value) parts.set("route", $("#froute").value);
      if ($("#fsource").value) parts.set("source", $("#fsource").value);
      if ($("#fstatus").value) parts.set("status_family", $("#fstatus").value);
      if ($("#fcache").value) parts.set("cache", $("#fcache").value);
      if ($("#fminms").value) parts.set("min_ms", $("#fminms").value);
      if ($("#fgroupby").value !== "source") parts.set("group_by", $("#fgroupby").value);
      location.hash = "#/requests?" + parts.toString();
    };
    $("#fapply").addEventListener("click", apply);
    $("#fgroupby").addEventListener("change", apply);
    bindPager(el, "requests", query);
  },
};

function bindPager(el, page, query) {
  el.querySelectorAll(`button[data-page="${page}"]`).forEach(button => button.addEventListener("click", () => {
    query.set("offset", button.dataset.offset);
    location.hash = `#/${page}?` + query.toString();
  }));
}
function interactiveClick(e) {
  return !!e.target.closest("a,button,input,select,textarea");
}

PAGES.endpoints = {
  title: "Endpoints",
  async render(el) {
    const d = await api("/internal/api/endpoints", rangeParams());
    el.innerHTML = `<div class="note">Route templates for the selected range. Click a row for the raw requests.</div>` +
      table([
        { key: "method", label: "M" }, { key: "routeTemplate", label: "Route" },
        { key: "requests", label: "Requests", fmt: num },
        { key: "errors", label: "Errors", fmt: (v, r) => `${num(v)} <span class="note">${pct(v, r.requests)}</span>` },
        { key: "rateLimited", label: "429s", fmt: num },
        { key: "uniqueNetworks", label: "Networks", fmt: num },
        { key: "cacheHit", label: "Cache", fmt: (v, r) => pct(v + r.cacheStale, v + r.cacheStale + r.cacheMiss) },
        { key: "avgMs", label: "Avg", fmt: msFmt },
        { key: "p95Ms", label: "p95", fmt: msFmt }, { key: "p99Ms", label: "p99", fmt: msFmt },
        { key: "lastAt", label: "Last", fmt: dt },
      ], d.endpoints, (row) => `data-route="${esc(row.routeTemplate)}"`);
    el.querySelectorAll("tr[data-route]").forEach(tr => tr.addEventListener("click", () =>
      location.hash = "#/requests?route=" + encodeURIComponent(tr.dataset.route)));
  },
};

PAGES.performance = {
  title: "Performance",
  async render(el) {
    const query = new URLSearchParams(location.hash.split("?")[1] || "");
    const offset = Number(query.get("offset") || 0);
    const threshold = query.get("threshold_ms") || "";
    const params = { ...rangeParams(), limit: 25, offset };
    if (threshold) params.threshold_ms = threshold;
    const d = await api("/internal/api/performance", params);
    const l = d.latency;
    el.innerHTML = `
      <div class="grid cards">
        ${[["Requests", num(l.count)], ["Avg", msFmt(l.avgMs)], ["Median", msFmt(l.p50Ms)],
          ["p90", msFmt(l.p90Ms)], ["p95", msFmt(l.p95Ms)], ["p99", msFmt(l.p99Ms)],
          ["Max", msFmt(l.maxMs)], ["Refresh p95", msFmt(d.jobs.p95DurationMs)]]
          .map(([label, value]) => `<div class="card"><h3>${label}</h3><div class="big">${value}</div></div>`).join("")}
      </div>
      <div class="card">
        <h3>Slow request explorer</h3>
        <div class="filters">
          <label>Threshold (ms)</label>
          <input id="perfth" size="8" value="${esc(threshold || d.thresholdMs)}">
          <button id="perfapply" class="primary">Apply</button>
          <span class="note">${num(d.slowTotal)} requests above threshold in range · per-endpoint latency lives on <a href="#/endpoints">Endpoints</a></span>
        </div>
        ${requestTable(d.slow)}
        ${pager(d.slowTotal, 25, offset, "performance")}
      </div>`;
    bindRequestRows(el);
    bindPager(el, "performance", query);
    $("#perfapply").addEventListener("click", () => {
      location.hash = "#/performance?threshold_ms=" + encodeURIComponent($("#perfth").value);
    });
  },
};

PAGES.cache = {
  title: "Cache",
  async render(el) {
    const d = await api("/internal/api/cache", rangeParams());
    const t = d.totals;
    const total = t.cacheHit + t.cacheStale + t.cacheMiss + t.cacheBypass;
    const jobs = d.refreshes;
    el.innerHTML = `
      <div class="grid cards">
        ${[["Hit rate (hit+stale)", pct(t.cacheHit + t.cacheStale, total)],
          ["Fresh hits", pct(t.cacheHit, total)],
          ["Stale serves", pct(t.cacheStale, total)],
          ["Misses", pct(t.cacheMiss, total)],
          ["Refresh success", pct(jobs.succeeded, jobs.total)],
          ["Live entries", num(d.live ? d.live.entries : 0) + " / " + num(d.live ? d.live.maxEntries : 0)]]
          .map(([label, value]) => `<div class="card"><h3>${label}</h3><div class="big">${value}</div></div>`).join("")}
      </div>
      <div class="card"><h3>Cache result over time</h3>${lineChart(d.series, { groups: true, legend: true })}</div>
      <div class="card" style="margin-top:12px"><h3>Cache by endpoint</h3>${table([
        { key: "routeTemplate", label: "Route" },
        { key: "cacheHit", label: "Hit", fmt: num }, { key: "cacheStale", label: "Stale", fmt: num },
        { key: "cacheMiss", label: "Miss", fmt: num },
        { key: "requests", label: "Hit rate", fmt: (v, r) => pct(r.cacheHit + r.cacheStale, r.cacheHit + r.cacheStale + r.cacheMiss) },
      ], (d.byEndpoint || []).filter(e => e.cacheHit + e.cacheStale + e.cacheMiss > 0))}</div>
      <div class="note">One request = one event; cache results are attributes of the request and can never inflate traffic. Refresh job details live under <a href="#/jobs">Background Jobs</a>; popular mods under <a href="#/insights">Insights</a>.</div>`;
  },
};

PAGES.errors = {
  title: "Errors",
  async render(el) {
    const query = new URLSearchParams(location.hash.split("?")[1] || "");
    const offset = Number(query.get("offset") || 0);
    const params = { ...rangeParams(), limit: 50, offset };
    for (const key of ["severity", "category", "resolution", "q", "route", "status", "fingerprint"]) {
      if (query.get(key)) params[key] = query.get(key);
    }
    const d = await api("/internal/api/errors", params);
    el.innerHTML = `
      <div class="card"><h3>Error patterns in range (new vs recurring)</h3>${table([
        { key: "isNew", label: "", fmt: (v) => v ? `<span class="pill bad">NEW</span>` : `<span class="pill">recurring</span>` },
        { key: "count", label: "Count", fmt: num },
        { key: "category", label: "Category" }, { key: "message", label: "Message" },
        { key: "route", label: "Route" }, { key: "resolution", label: "Resolution", fmt: (v) => `<span class="pill ${v === "resolved" ? "ok" : v === "open" ? "bad" : "warn"}">${esc(v)}</span>` },
        { key: "firstAt", label: "First", fmt: dt }, { key: "lastAt", label: "Last", fmt: dt },
      ], d.patterns, (row) => `data-error="${esc(row.sampleId)}"`)}</div>
      <div class="filters" style="margin-top:14px">
        <input id="eq" placeholder="search message/path" value="${esc(params.q || "")}">
        <select id="esev"><option value="">severity: all</option>${["error", "fatal", "warn"].map(s => `<option ${params.severity === s ? "selected" : ""}>${s}</option>`).join("")}</select>
        <select id="eres"><option value="">resolution: all</option>${["open", "acknowledged", "investigating", "resolved", "ignored"].map(s => `<option ${params.resolution === s ? "selected" : ""}>${s}</option>`).join("")}</select>
        <button id="eapply" class="primary">Filter</button>
      </div>
      ${table([
        { key: "at", label: "Time", fmt: dt }, { key: "severity", label: "Sev" },
        { key: "category", label: "Category" }, { key: "code", label: "Code" },
        { key: "message", label: "Message" }, { key: "routeTemplate", label: "Route" },
        { key: "status", label: "Status", fmt: (v) => v ? statusPill(v) : "" },
        { key: "clientName", label: "Client" }, { key: "resolution", label: "Resolution" },
      ], d.errors, (row) => `data-error="${esc(row.errorId)}"`)}
      ${pager(d.total, 50, offset, "errors")}`;
    bindErrorRows(el);
    bindPager(el, "errors", query);
    $("#eapply").addEventListener("click", () => {
      const parts = new URLSearchParams();
      if ($("#eq").value) parts.set("q", $("#eq").value);
      if ($("#esev").value) parts.set("severity", $("#esev").value);
      if ($("#eres").value) parts.set("resolution", $("#eres").value);
      location.hash = "#/errors?" + parts.toString();
    });
  },
};

async function showErrorDrawer(errorId) {
  const d = await api("/internal/api/errors/" + encodeURIComponent(errorId));
  const e = d.error;
  openDrawer(`<h2>Error ${esc(e.errorId)}</h2>
    ${kv([
      ["Time", dt(e.at)], ["Severity", esc(e.severity)], ["Category", esc(e.category)],
      ["Code", esc(e.code)], ["Message", esc(e.message)],
      ["Route", esc(e.routeTemplate)], ["Path", esc(e.requestPath)],
      ["Status", e.status ? statusPill(e.status) : ""],
      ["Request ID", esc(e.requestId), e.requestId], ["Job ID", esc(e.jobId)],
      ["User", esc(e.accountId)], ["Client", esc(e.clientName)],
      ["Country", esc(countryLabel(e.countryCode))], ["Version", esc(e.appVersion)],
      ["Fingerprint", esc(e.fingerprint), e.fingerprint],
      ["Occurrences (pattern)", num((d.related || []).length) + " loaded"],
    ])}
    <div class="formrow">
      <label>Resolution</label>
      <select id="eresolution">${["open", "acknowledged", "investigating", "resolved", "ignored"]
        .map(s => `<option ${e.resolution === s ? "selected" : ""}>${s}</option>`).join("")}</select>
      <label><input type="checkbox" id="ewhole" checked> whole pattern</label>
    </div>
    <div class="formrow"><label>Notes</label><textarea id="enotes" rows="2" style="flex:1">${esc(e.notes)}</textarea></div>
    <button class="primary" id="esave">Save triage</button>
    ${e.stack ? `<div class="section-title">Stack</div><pre>${esc(e.stack)}</pre>` : ""}
    ${d.request ? `<div class="section-title">Request</div><pre>${esc(JSON.stringify(d.request, null, 2))}</pre>` : ""}`);
  $("#esave").addEventListener("click", async () => {
    await mutate("/internal/api/errors/" + encodeURIComponent(errorId), "PATCH", {
      resolution: $("#eresolution").value, notes: $("#enotes").value, wholePattern: $("#ewhole").checked,
    });
    closeDrawer(); navigate();
  });
}

PAGES.ratelimits = {
  title: "Rate Limits",
  async render(el) {
    const d = await api("/internal/api/rate-limits", rangeParams());
    const t = d.totals;
    el.innerHTML = `
      <div class="grid cards">
        ${[["Rejected (429)", num(t.rateLimited)], ["Rate-limit rate", pct(t.rateLimited, t.requests)],
          ["Anonymous rejections", num(d.anonymousRateLimited)]]
          .map(([label, value]) => `<div class="card"><h3>${label}</h3><div class="big">${value}</div></div>`).join("")}
      </div>
      <div class="card"><h3>Requests vs rate limited</h3>${lineChart(d.series, { metrics: [
        { key: "requests", label: "requests" }, { key: "rateLimited", label: "rate limited", color: "#ffb86b" }], legend: true })}</div>
      <div class="grid two" style="margin-top:12px">
        <div class="card"><h3>Most limited clients</h3>${barList(d.topClients)}</div>
        <div class="card"><h3>Most limited networks</h3>${barList(d.topNetworks)}</div>
      </div>
      <div class="note">High anonymous rejections are upgrade opportunities; sustained bursts from a single network suggest abuse — drill into the rows below or the network on <a href="#/audience">Clients & Geography</a>.</div>
      <div class="card"><h3>Recent rate-limited requests</h3>${requestTable(d.recent)}</div>`;
    bindRequestRows(el);
  },
};

// Audience combines the old Clients, Geography, and Networks pages.
PAGES.audience = {
  title: "Clients & Geography",
  async render(el) {
    const [d, geo, nets] = await Promise.all([
      api("/internal/api/clients", { ...rangeParams(), limit: 50 }),
      api("/internal/api/geography", rangeParams()),
      api("/internal/api/networks", { ...rangeParams(), limit: 30 }),
    ]);
    el.innerHTML = `
      <div class="note">"verified" traffic authenticated with an API key; <code>ua:</code> identities are self-reported. Country and network data are approximate; network IDs are anonymous and rotate — no raw IPs exist anywhere.</div>
      <div class="card"><h3>Client applications</h3>${table([
        { key: "clientName", label: "Client", fmt: (v, r) => esc(v || r.clientKey) + (r.verified ? ` <span class="pill ok">verified</span>` : "") },
        { key: "requests", label: "Requests", fmt: num },
        { key: "errors", label: "Errors", fmt: num },
        { key: "rateLimited", label: "429s", fmt: num },
        { key: "cacheHit", label: "Cache", fmt: (v, r) => pct(v + r.cacheStale, v + r.cacheStale + r.cacheMiss) },
        { key: "countries", label: "Countries" },
        { key: "daysActive", label: "Days active", fmt: num },
        { key: "lastSeenAt", label: "Last seen", fmt: dt },
      ], d.clients, (row) => `data-client="${esc(row.clientName || row.clientKey)}"`)}</div>
      <div class="grid two" style="margin-top:12px">
        <div class="card"><h3>Countries</h3>${table([
          { key: "countryCode", label: "Country", fmt: countryLabel },
          { key: "requests", label: "Requests", fmt: num },
          { key: "errors", label: "Errors", fmt: num },
          { key: "rateLimited", label: "429s", fmt: num },
          { key: "avgMs", label: "Avg", fmt: msFmt },
        ], (geo.countries || []).slice(0, 15), (row) => `data-country="${esc(row.countryCode)}"`)}</div>
        <div class="card"><h3>Top networks (anonymous)</h3>${table([
          { key: "networkId", label: "Network ID" },
          { key: "countryCode", label: "Country", fmt: countryLabel },
          { key: "asn", label: "ASN", fmt: (v) => esc(v || "–") },
          { key: "isHosting", label: "Type", fmt: (v) => `<span class="pill ${v === "hosting" ? "warn" : v === "residential" ? "ok" : ""}">${esc(v)}</span>` },
          { key: "requests", label: "Requests", fmt: num },
          { key: "rateLimited", label: "429s", fmt: num },
        ], (nets.networks || []).slice(0, 15), (row) => `data-network="${esc(row.networkId)}"`)}</div>
      </div>
      <div class="section-title">Registered API clients</div>
      <button id="newclient" class="primary" style="margin-bottom:10px">Register client</button>
      ${table([
        { key: "name", label: "Name" }, { key: "accountId", label: "Owner" },
        { key: "environment", label: "Env" }, { key: "status", label: "Status" },
        { key: "isInternal", label: "Internal", fmt: (v) => v ? "yes" : "" },
        { key: "publiclyNameable", label: "Public", fmt: (v) => v ? "yes" : "" },
        { key: "tags", label: "Tags" },
      ], d.registered, (row) => `data-regclient='${esc(JSON.stringify(row))}'`)}`;
    el.querySelectorAll("tr[data-client]").forEach(tr => tr.addEventListener("click", () =>
      location.hash = "#/requests?client=" + encodeURIComponent(tr.dataset.client)));
    el.querySelectorAll("tr[data-country]").forEach(tr => tr.addEventListener("click", () =>
      location.hash = "#/requests?country=" + encodeURIComponent(tr.dataset.country)));
    el.querySelectorAll("tr[data-network]").forEach(tr => tr.addEventListener("click", () =>
      location.hash = "#/requests?network=" + encodeURIComponent(tr.dataset.network)));
    el.querySelectorAll("tr[data-regclient]").forEach(tr => tr.addEventListener("click", () =>
      showRegisteredClientDrawer(JSON.parse(tr.dataset.regclient))));
    $("#newclient").addEventListener("click", () => showRegisteredClientDrawer(null));
  },
};

function showRegisteredClientDrawer(client) {
  const c = client || {};
  openDrawer(`<h2>${client ? "Edit client" : "Register API client"}</h2>
    ${["name", "accountId", "description", "websiteUrl", "tags", "notes"].map(f =>
      `<div class="formrow"><label>${f}</label><input id="cl_${f}" style="flex:1" value="${esc(c[f] || "")}" ${client && f === "accountId" ? "disabled" : ""}></div>`).join("")}
    <div class="formrow"><label>environment</label><select id="cl_environment">${["production", "staging", "development", "test"].map(v => `<option ${c.environment === v ? "selected" : ""}>${v}</option>`).join("")}</select></div>
    <div class="formrow"><label>clientType</label><input id="cl_clientType" value="${esc(c.clientType || "")}"></div>
    <div class="formrow"><label>status</label><select id="cl_status">${["active", "paused", "archived"].map(v => `<option ${c.status === v ? "selected" : ""}>${v}</option>`).join("")}</select></div>
    <div class="formrow"><label>monthlyQuota</label><input id="cl_monthlyQuota" type="number" value="${c.monthlyQuota || 0}"></div>
    <div class="formrow"><label>internal</label><input type="checkbox" id="cl_isInternal" ${c.isInternal ? "checked" : ""}></div>
    <div class="formrow"><label>publicly nameable</label><input type="checkbox" id="cl_publiclyNameable" ${c.publiclyNameable ? "checked" : ""}></div>
    <div class="formrow">
      <button class="primary" id="cl_save">${client ? "Save" : "Create"}</button>
      ${client ? `<button class="danger" id="cl_delete">Delete</button>` : ""}
    </div>`);
  $("#cl_save").addEventListener("click", async () => {
    const fields = {
      name: $("#cl_name").value, description: $("#cl_description").value,
      websiteUrl: $("#cl_websiteUrl").value, tags: $("#cl_tags").value, notes: $("#cl_notes").value,
      environment: $("#cl_environment").value, clientType: $("#cl_clientType").value,
      status: $("#cl_status").value, monthlyQuota: Number($("#cl_monthlyQuota").value || 0),
      isInternal: $("#cl_isInternal").checked, publiclyNameable: $("#cl_publiclyNameable").checked,
    };
    if (client) {
      await mutate("/internal/api/clients/" + encodeURIComponent(client.id), "PATCH", fields);
    } else {
      await mutate("/internal/api/clients", "POST", { ...fields, accountId: $("#cl_accountId").value });
    }
    closeDrawer(); navigate();
  });
  if (client) $("#cl_delete").addEventListener("click", async () => {
    if (!confirm(`Delete client "${client.name}"? Keys keep working but lose their client link.`)) return;
    await mutate("/internal/api/clients/" + encodeURIComponent(client.id), "DELETE");
    closeDrawer(); navigate();
  });
}

PAGES.retention = {
  title: "Retention",
  async render(el) {
    const entity = state.retEntity || "client";
    const d = await api("/internal/api/retention", { ...rangeParams(), entity });
    const s = d.summary;
    const entityName = {
      client: "API clients",
      user: "billed users",
      key: "API keys",
      network: "anonymous networks",
    }[entity] || entity;
    el.innerHTML = `
      <div class="filters">
        <label>Measure</label>
        <select id="rentity">${[["client", "API clients"], ["user", "Billed users"], ["key", "API keys"], ["network", "Anonymous networks (rough estimate)"]]
          .map(([v, l]) => `<option value="${v}" ${entity === v ? "selected" : ""}>${l}</option>`).join("")}</select>
        ${entity === "network" ? `<span class="pill warn">rough estimate</span>` : ""}
      </div>
      <div class="note">This answers: when ${esc(entityName)} first show up, how many come back later? Anonymous network retention is useful for directional traffic only because IDs rotate.</div>
      <div class="grid cards">
        ${[["First seen", num(s.cohortSize)],
          ["Came back next day", ratio(s.day1)], ["Came back within 7 days", ratio(s.day7)],
          ["Came back within 30 days", ratio(s.day30)],
          ["New this range", num(s.new)], ["Already active", num(s.returning)],
          ["Stopped showing up", num(s.churned)], ["Returned after gap", num(s.reactivated)]]
          .map(([label, value]) => `<div class="card"><h3>${label}</h3><div class="big">${value}</div></div>`).join("")}
      </div>
      <div class="card"><h3>Active trend</h3>${lineChart(d.series.map(p => ({ bucket: p.day, ...p })), { metrics: [
        { key: "daily", label: "DAU" }, { key: "weekly", label: "WAU", color: "#4aa3ff" },
        { key: "monthly", label: "MAU", color: "#ffb86b" }, { key: "new", label: "new", color: "#b28dff" }], legend: true })}</div>
      <div class="grid two" style="margin-top:12px">
        <div class="card"><h3>Weekly groups</h3><div class="note">Rows are first-seen weeks. Each column shows the share active again after that many weeks.</div>${heatmap(d.weeklyCohorts)}</div>
        <div class="card"><h3>Monthly groups</h3><div class="note">Rows are first-seen months. Each column shows the share active again after that many months.</div>${heatmap(d.monthlyCohorts)}</div>
      </div>
      <div class="note">Active = ${esc(d.definitions.active)}</div>`;
    $("#rentity").addEventListener("change", (e) => { state.retEntity = e.target.value; navigate(); });
  },
};

PAGES.insights = {
  title: "Insights & Export",
  async render(el) {
    const [d, search] = await Promise.all([
      api("/internal/api/marketing", rangeParams()),
      api("/internal/api/search-analytics", { ...rangeParams(), limit: 15 }),
    ]);
    const t = d.totals, p = d.previous || {};
    const cr = d.clientRetention;
    const summary = [
      { label: "API requests", value: num(t.requests), previous: delta(t.requests, p.requests) },
      { label: "Active integrations", value: num(t.uniqueClients), previous: delta(t.uniqueClients, p.uniqueClients) },
      { label: "New integrations", value: num(cr.new), previous: "" },
      { label: "Churned integrations", value: num(cr.churned), previous: "" },
      { label: "Active users", value: num(t.uniqueAccounts), previous: "" },
      { label: "Countries reached", value: num(d.countriesReached), previous: "" },
      { label: "Distinct mods served", value: num(t.distinctMods), previous: "" },
      { label: "Search terms", value: num(t.distinctSearchTerms), previous: "" },
      { label: "Requests / integration", value: t.uniqueClients ? num(t.requests / t.uniqueClients) : "–", previous: "" },
    ];
    const mods = (d.topMods || []).map(m => ({
      modId: m.modId,
      requests: m.requests,
      share: pct(m.requests, t.requests),
    }));
    el.innerHTML = `
      <div class="note">Aggregate, marketing-safe numbers. ${esc(d.estimateDisclaimer)} Use Export for CSV/JSON.</div>
      <div class="section-title">Summary</div>
      ${table([
        { key: "label", label: "Metric" },
        { key: "value", label: "Value" },
        { key: "previous", label: "Vs previous", fmt: (v) => v },
      ], summary)}
      <div class="grid two" style="margin-top:18px">
        <div>
          <div class="section-title">Most requested mods</div>
          ${table([
            { key: "modId", label: "Mod ID" },
            { key: "requests", label: "Requests", fmt: num },
            { key: "share", label: "Share" },
          ], mods)}
        </div>
        <div>
          <div class="section-title">Search demand</div>
          ${table([
          { key: "term", label: "Term" }, { key: "searches", label: "Searches", fmt: num },
          { key: "emptyResults", label: "Empty", fmt: num },
        ], search.terms, (row) => `data-term="${esc(row.term)}"`)}
        </div>
      </div>
      <div class="section-title">Publicly nameable integrations</div>
      ${table([
        { key: "name", label: "Name" }, { key: "websiteUrl", label: "URL" },
      ], d.publiclyNameable)}
      <div class="note">Only clients explicitly flagged appear here or in exports.</div>
      <div class="section-title">Export</div>
      <div class="formrow">
        <select id="expds">${["usage", "countries", "mods", "searches"].map(x => `<option>${x}</option>`).join("")}</select>
        <select id="expfmt"><option>csv</option><option>json</option></select>
        <button class="primary" id="expgo">Download</button>
      </div>`;
    $("#expgo").addEventListener("click", () => {
      const params = new URLSearchParams({ ...rangeParams(), dataset: $("#expds").value, format: $("#expfmt").value });
      window.open("/internal/api/export?" + params.toString(), "_blank");
    });
    el.querySelectorAll("tr[data-term]").forEach(tr => tr.addEventListener("click", () =>
      location.hash = "#/requests?term=" + encodeURIComponent(tr.dataset.term)));
  },
};

PAGES.logs = {
  title: "Logs",
  async render(el) {
    const query = new URLSearchParams(location.hash.split("?")[1] || "");
    const offset = Number(query.get("offset") || 0);
    const params = { ...rangeParams(), limit: 100, offset };
    for (const key of ["level", "q", "message", "request_id", "job_id", "status", "route", "client", "network", "country"]) {
      if (query.get(key)) params[key] = query.get(key);
    }
    const d = await api("/internal/api/logs", params);
    el.innerHTML = `
      <div class="filters">
        <input id="lq" placeholder="free text" value="${esc(params.q || "")}">
        <select id="llevel"><option value="">level: all</option>${["info", "warn", "error", "fatal"].map(l => `<option ${params.level === l ? "selected" : ""}>${l}</option>`).join("")}</select>
        <input id="lmsg" placeholder="message prefix" value="${esc(params.message || "")}">
        <input id="lreq" placeholder="request id" value="${esc(params.request_id || "")}">
        <input id="ljob" placeholder="job id" value="${esc(params.job_id || "")}">
        <button id="lapply" class="primary">Filter</button>
      </div>
      ${table([
        { key: "at", label: "Time", fmt: dt },
        { key: "level", label: "Level", fmt: (v) => `<span class="pill ${v === "error" || v === "fatal" ? "bad" : v === "warn" ? "warn" : ""}">${esc(v)}</span>` },
        { key: "message", label: "Message" },
        { key: "path", label: "Path" },
        { key: "status", label: "Status", fmt: (v) => v ? statusPill(v) : "" },
        { key: "requestId", label: "Request" },
        { key: "jobId", label: "Job" },
        { key: "caller", label: "Caller" },
      ], d.logs, (row) => `data-log="${row.id}"`)}
      ${pager(d.total, 100, offset, "logs")}`;
    bindPager(el, "logs", query);
    el.querySelectorAll("tr[data-log]").forEach(tr => tr.addEventListener("click", () => showLogDrawer(tr.dataset.log)));
    $("#lapply").addEventListener("click", () => {
      const parts = new URLSearchParams();
      for (const [id, key] of [["lq", "q"], ["llevel", "level"], ["lmsg", "message"], ["lreq", "request_id"], ["ljob", "job_id"]]) {
        if ($("#" + id).value) parts.set(key, $("#" + id).value);
      }
      location.hash = "#/logs?" + parts.toString();
    });
  },
};

async function showLogDrawer(id) {
  const d = await api("/internal/api/logs/" + encodeURIComponent(id));
  const e = d.event;
  const logRow = (l) => `<tr><td>${dt(l.at)}</td><td>${esc(l.level)}</td><td>${esc(l.message)}</td><td>${esc(l.requestId || l.jobId || "")}</td></tr>`;
  openDrawer(`<h2>Log #${e.id}</h2>
    ${kv([
      ["Time", dt(e.at)], ["Level", esc(e.level)], ["Message", esc(e.message)],
      ["Caller", esc(e.caller)], ["Request ID", esc(e.requestId), e.requestId],
      ["Trace ID", esc(e.traceId)], ["Job ID", esc(e.jobId), e.jobId],
      ["Route", esc(e.route)], ["Path", esc(e.path)],
      ["Status", e.status ? statusPill(e.status) : ""],
      ["Client", esc(e.clientName)], ["Country", esc(countryLabel(e.countryCode))],
      ["Network", esc(e.networkId)], ["Cache", esc(e.cacheStatus)],
      ["Instance", esc(e.instanceId)], ["Version", esc(e.appVersion)],
    ])}
    ${e.fields ? `<div class="section-title">Fields</div><pre>${esc(prettyJSON(e.fields))}</pre>` : ""}
    ${copyBtn(JSON.stringify(e))} copy safe JSON
    ${d.request ? `<div class="section-title">Related request</div><pre>${esc(JSON.stringify(d.request, null, 2))}</pre>` : ""}
    ${d.job ? `<div class="section-title">Related job</div><pre>${esc(JSON.stringify(d.job, null, 2))}</pre>` : ""}
    ${(d.correlated || []).length ? `<div class="section-title">Correlated events (same request/trace/job)</div>
      <div class="tablewrap"><table><thead><tr><th>Time</th><th>Level</th><th>Message</th><th>Ref</th></tr></thead><tbody>${d.correlated.map(logRow).join("")}</tbody></table></div>` : ""}
    ${(d.nearby || []).length ? `<div class="section-title">Nearby events</div>
      <div class="tablewrap"><table><thead><tr><th>Time</th><th>Level</th><th>Message</th><th>Ref</th></tr></thead><tbody>${d.nearby.map(logRow).join("")}</tbody></table></div>` : ""}`);
}
function prettyJSON(text) {
  try { return JSON.stringify(JSON.parse(text), null, 2); } catch { return text; }
}

PAGES.jobs = {
  title: "Background Jobs",
  async render(el) {
    const query = new URLSearchParams(location.hash.split("?")[1] || "");
    const offset = Number(query.get("offset") || 0);
    const params = { ...rangeParams(), limit: 50, offset };
    for (const key of ["kind", "status", "resource", "order", "min_ms"]) {
      if (query.get(key)) params[key] = query.get(key);
    }
    const d = await api("/internal/api/jobs", params);
    const s = d.stats;
    el.innerHTML = `
      <div class="grid cards">
        ${[["Jobs", num(s.total)], ["Succeeded", num(s.succeeded)], ["Failed", num(s.failed)],
          ["Running", num(s.running)], ["Queued rows", num(s.queued)], ["Deduplicated", num(s.deduplicated)],
          ["Retried", num(s.retried)], ["Panics recovered", num(s.panicRecovered)],
          ["Avg duration", msFmt(s.avgDurationMs)], ["p95 duration", msFmt(s.p95DurationMs)],
          ["Avg queue wait", msFmt(s.avgQueueWaitMs)],
          ["Live queue depth", num(d.gauges.refresh_queue_depth)]]
          .map(([label, value]) => `<div class="card"><h3>${label}</h3><div class="big">${value}</div></div>`).join("")}
      </div>
      <div class="filters">
        <select id="jkind"><option value="">kind: all</option>${["cache_refresh", "index_refresh"].map(k => `<option ${params.kind === k ? "selected" : ""}>${k}</option>`).join("")}</select>
        <select id="jstatus"><option value="">status: all</option>${["queued", "running", "succeeded", "failed", "expired"].map(k => `<option ${params.status === k ? "selected" : ""}>${k}</option>`).join("")}</select>
        <input id="jresource" placeholder="resource key contains" value="${esc(params.resource || "")}">
        <select id="jorder"><option value="">newest</option><option value="slowest" ${params.order === "slowest" ? "selected" : ""}>slowest</option></select>
        <button id="japply" class="primary">Filter</button>
      </div>
      ${table([
        { key: "enqueuedAt", label: "Enqueued", fmt: dt },
        { key: "kind", label: "Kind" },
        { key: "resourceKey", label: "Resource" },
        { key: "status", label: "Status", fmt: (v) => `<span class="pill ${v === "succeeded" ? "ok" : v === "failed" ? "bad" : ""}">${esc(v)}</span>` },
        { key: "statusCode", label: "Code" },
        { key: "queueWaitMs", label: "Wait", fmt: msFmt },
        { key: "durationMs", label: "Duration", fmt: msFmt },
        { key: "worker", label: "W" },
        { key: "deduplicated", label: "Dedup", fmt: (v) => v ? "yes" : "" },
        { key: "failureReason", label: "Failure" },
        { key: "requestId", label: "Trigger request" },
      ], d.jobs, (row) => `data-job="${esc(row.jobId)}"`)}
      ${pager(d.total, 50, offset, "jobs")}`;
    bindPager(el, "jobs", query);
    el.querySelectorAll("tr[data-job]").forEach(tr => tr.addEventListener("click", () => showJobDrawer(tr.dataset.job)));
    $("#japply").addEventListener("click", () => {
      const parts = new URLSearchParams();
      for (const [id, key] of [["jkind", "kind"], ["jstatus", "status"], ["jresource", "resource"], ["jorder", "order"]]) {
        if ($("#" + id).value) parts.set(key, $("#" + id).value);
      }
      location.hash = "#/jobs?" + parts.toString();
    });
  },
};

async function showJobDrawer(jobId) {
  const d = await api("/internal/api/jobs/" + encodeURIComponent(jobId));
  const j = d.job;
  openDrawer(`<h2>Job ${esc(j.jobId)}</h2>
    ${kv([
      ["Kind", esc(j.kind)], ["Resource", esc(j.resourceKey)], ["URL", esc(j.resourceUrl)],
      ["Status", esc(j.status)], ["HTTP code", j.statusCode || ""], ["Priority", esc(j.priority)],
      ["Enqueued", dt(j.enqueuedAt)], ["Started", dt(j.startedAt)], ["Finished", dt(j.finishedAt)],
      ["Queue wait", msFmt(j.queueWaitMs)], ["Duration", msFmt(j.durationMs)],
      ["Worker", j.worker || ""], ["Attempt", j.attempt], ["Deduplicated", j.deduplicated ? "yes" : "no"],
      ["Panic recovered", j.panicRecovered ? "yes" : "no"], ["Failure", esc(j.failureReason)],
      ["Triggered by request", j.requestId ? `<a href="#" onclick="showRequestDrawer('${esc(j.requestId)}');return false">${esc(j.requestId)}</a>` : "scheduler"],
    ])}
    ${(d.logs || []).length ? `<div class="section-title">Job logs</div>` + table([
      { key: "at", label: "Time", fmt: dt }, { key: "level", label: "Level" }, { key: "message", label: "Message" }], d.logs) : ""}`);
}

PAGES.health = {
  title: "System Health",
  async render(el) {
    const d = await api("/internal/api/health");
    const rec = d.recorder || {};
    const storage = d.storage || { rowCounts: {} };
    const warn = [];
    if (rec.dropped > 0) warn.push(`${num(rec.dropped)} telemetry events dropped (queue full)`);
    if (rec.writeErrors > 0) warn.push(`${num(rec.writeErrors)} telemetry write errors — last: ${rec.lastError}`);
    if ((d.aggregation || {}).last_aggregation_error) warn.push(`aggregation error: ${d.aggregation.last_aggregation_error}`);
    if ((d.recentRestarts || []).length > 3) warn.push(`${d.recentRestarts.length} restarts in the last 7 days`);
    el.innerHTML = `
      ${warn.length ? `<div class="warnbox">⚠ ${warn.map(esc).join("<br>⚠ ")}</div>` : `<div class="okbox">All health checks passing.</div>`}
      <div class="grid cards">
        ${[["Version", esc(d.version)], ["Instance", esc(d.instance)],
          ["Telemetry DB", d.telemetryDb ? (d.telemetryDb.ok ? `<span class="pill ok">ok</span> ${msFmt(d.telemetryDb.latencyMs)}` : `<span class="pill bad">down</span>`) : "disabled"],
          ["Billing DB", d.billingDb ? (d.billingDb.ok ? `<span class="pill ok">ok</span> ${num(d.billingDb.accounts)} accounts` : `<span class="pill bad">down</span>`) : "disabled"],
          ["DB size", bytesFmt(storage.dbSizeBytes)],
          ["Raw events", num(storage.rowCounts.request_events)],
          ["Structured logs", num(storage.rowCounts.structured_logs)],
          ["Errors stored", num(storage.rowCounts.request_errors)],
          ["Jobs stored", num(storage.rowCounts.background_jobs)],
          ["Events written", num(rec.written)], ["Events dropped", num(rec.dropped)],
          ["Requests (last hour)", num((d.lastHour || {}).requests)],
          ["Error rate (1h)", pct((d.lastHour || {}).errors, (d.lastHour || {}).requests)],
          ["p95 (1h)", msFmt(((d.lastHour || {}).latency || {}).p95Ms)],
          ["Queue depth", num(d.gauges.refresh_queue_depth)],
          ["Workers", `${num(d.gauges.refresh_active_workers)} / ${num(d.gauges.refresh_workers)}`]]
          .map(([label, value]) => `<div class="card"><h3>${label}</h3><div class="big">${value}</div></div>`).join("")}
      </div>
      <div class="grid two">
        <div class="card"><h3>Aggregation</h3>${kv(Object.entries(d.aggregation || {}).map(([k, v]) => [k, esc(v || "–")]))}
          <div class="note">Watermarks advance as buckets become final; the current bucket is re-aggregated each pass. <a href="#/settings">Rebuild from Settings</a>.</div></div>
        <div class="card"><h3>Recent restarts (7d)</h3>${(d.recentRestarts || []).map(t => `<div>${dt(t)}</div>`).join("") || `<div class="note">None recorded.</div>`}
          <div class="note">Derived from startup log lines. Frequent restarts previously wiped metrics; telemetry is now durable. <a href="#/logs?message=ReforgerWorkshopAPI">View startup logs</a></div></div>
        <div class="card"><h3>Table sizes</h3>${table([{ key: "0", label: "Table" }, { key: "1", label: "Rows", fmt: num }],
          Object.entries(storage.rowCounts).sort((a, b) => b[1] - a[1]))}</div>
        <div class="card"><h3>In-process counters (since restart)</h3>${table([{ key: "0", label: "Counter" }, { key: "1", label: "Value", fmt: num }],
          Object.entries(d.counters || {}).sort())}</div>
      </div>`;
  },
};

PAGES.users = {
  title: "Users",
  async render(el) {
    const query = new URLSearchParams(location.hash.split("?")[1] || "");
    const offset = Number(query.get("offset") || 0);
    const params = { limit: 50, offset };
    if (query.get("q")) params.q = query.get("q");
    if (query.get("status")) params.status = query.get("status");
    const d = await api("/internal/api/users", params);
    const paidOnPage = (d.users || []).filter(u => u.plan && u.plan !== "free").length;
    const activePaidOnPage = (d.users || []).filter(u => u.plan && u.plan !== "free" && u.subscriptionStatus === "active").length;
    el.innerHTML = `
      <div class="grid cards">
        ${[["Users on this page", num((d.users || []).length)], ["Paid plans on this page", num(paidOnPage)],
          ["Active paid on this page", num(activePaidOnPage)], ["Total accounts", num(d.total)]]
          .map(([label, value]) => `<div class="card"><h3>${label}</h3><div class="big">${value}</div></div>`).join("")}
      </div>
      <div class="filters">
        <input id="uq" placeholder="search email / id / stripe id" value="${esc(params.q || "")}">
        <select id="ustatus"><option value="">status: all</option>${["active", "suspended", "deleted"].map(s => `<option ${params.status === s ? "selected" : ""}>${s}</option>`).join("")}</select>
        <button id="uapply" class="primary">Search</button>
        <button id="unew">Create user</button>
      </div>
      ${table([
        { key: "email", label: "Support email", fmt: (v, r) => userContact(r) },
        { key: "plan", label: "Plan", fmt: (v) => `<span class="pill ${planClass(v)}">${esc(v)}</span>` },
        { key: "subscriptionStatus", label: "Billing", fmt: subscriptionPill },
        { key: "status", label: "Status", fmt: (v) => `<span class="pill ${v === "active" ? "ok" : "bad"}">${esc(v)}</span>` },
        { key: "isInternal", label: "Internal", fmt: (v) => v ? "yes" : "" },
        { key: "isTest", label: "Test", fmt: (v) => v ? "yes" : "" },
        { key: "tags", label: "Tags" },
        { key: "lastSeenAt", label: "Last activity", fmt: dt },
        { key: "daysActive", label: "Days active", fmt: num },
      ], d.users, (row) => `data-user="${esc(row.id)}"`)}
      ${pager(d.total, 50, offset, "users")}
      <div id="keysection"></div>`;
    bindPager(el, "users", query);
    renderKeySections($("#keysection"));
    el.querySelectorAll("tr[data-user]").forEach(tr => tr.addEventListener("click", (e) => {
      if (!interactiveClick(e)) showUserDrawer(tr.dataset.user);
    }));
    $("#uapply").addEventListener("click", () => {
      const parts = new URLSearchParams();
      if ($("#uq").value) parts.set("q", $("#uq").value);
      if ($("#ustatus").value) parts.set("status", $("#ustatus").value);
      location.hash = "#/users?" + parts.toString();
    });
    $("#unew").addEventListener("click", () => {
      openDrawer(`<h2>Create user</h2>
        <div class="formrow"><label>Email</label><input id="nu_email" style="flex:1"></div>
        <div class="formrow"><label>Plan</label><select id="nu_plan">${["free", "developer", "pro", "internal"].map(p => `<option>${p}</option>`).join("")}</select></div>
        <div class="formrow"><label>Notes</label><input id="nu_notes" style="flex:1"></div>
        <button class="primary" id="nu_create">Create</button>`);
      $("#nu_create").addEventListener("click", async () => {
        await mutate("/internal/api/users", "POST", { email: $("#nu_email").value, plan: $("#nu_plan").value, notes: $("#nu_notes").value });
        closeDrawer(); navigate();
      });
    });
    const open = query.get("open");
    if (open) showUserDrawer(open);
  },
};

async function showUserDrawer(accountID) {
  const d = await api("/internal/api/users/" + encodeURIComponent(accountID), rangeParams());
  const u = d.user, usage = d.usage || {}, profile = d.profile || {};
  openDrawer(`<h2>${esc(u.email || u.id)}</h2>
    ${kv([
      ["Support email", u.email ? `<a href="mailto:${esc(u.email)}">${esc(u.email)}</a>` : `<span class="note">No email on account</span>`, u.email],
      ["Account ID", esc(u.id), u.id],
      ["Status", `<span class="pill ${u.status === "active" ? "ok" : "bad"}">${esc(u.status)}</span>`],
      ["Plan", `<span class="pill ${planClass(u.plan)}">${esc(u.plan)}</span>`], ["Billing", subscriptionPill(u.subscriptionStatus)],
      ["Stripe customer", esc(u.stripeCustomerId || "–")],
      ["Created", dt(u.createdAt)],
      ["First API activity", dt(profile.firstSeenAt)], ["Last API activity", dt(profile.lastSeenAt)],
      ["Days active", num(profile.daysActive || 0)],
      ["Flags", [u.isInternal && "internal", u.isTest && "test"].filter(Boolean).join(", ") || "–"],
      ["Tags", esc(u.tags || "–")],
      ["Rate limit", `${num((d.limits || {}).limit_per_minute)} req/min (${esc((d.limits || {}).shared_by || "")})`],
      ["Requests in range", num(usage.requests || 0)],
      ["Errors in range", num(usage.errors || 0)],
      ["Rate limited", num(usage.rateLimited || 0)],
    ])}
    ${d.series ? lineChart(d.series) : ""}
    <div class="grid two" style="margin-top:8px">
      <div><div class="section-title">Countries</div>${barList(countryItems(d.countries))}</div>
      <div><div class="section-title">Top routes</div>${barList(d.routes || [])}</div>
    </div>
    <div class="section-title">API keys (${(d.keys || []).length})</div>
    ${table([
      { key: "prefix", label: "Prefix", fmt: (v, k) => esc(v) + "…" + esc(k.lastFour || "") },
      { key: "name", label: "Name" }, { key: "environment", label: "Env" },
      { key: "isActive", label: "State", fmt: (v, k) => k.revokedAt ? `<span class="pill bad">revoked</span>` : k.disabledAt ? `<span class="pill warn">disabled</span>` : `<span class="pill ok">active</span>` },
      { key: "lastUsedAt", label: "Last used", fmt: dt },
      { key: "id", label: "", fmt: (v, k) => k.revokedAt ? "" : `<button data-revoke="${esc(v)}" class="danger">Revoke</button> <button data-toggle="${esc(v)}" data-disabled="${k.disabledAt ? 1 : 0}">${k.disabledAt ? "Enable" : "Disable"}</button>` },
    ], d.keys)}
    <button class="primary" id="ud_newkey" style="margin-top:8px">Create API key</button>
    <div class="section-title">Registered clients (${(d.clients || []).length})</div>
    ${table([{ key: "name", label: "Name" }, { key: "environment", label: "Env" }, { key: "status", label: "Status" }], d.clients)}
    <div class="section-title">Recent errors</div>
    ${table([{ key: "at", label: "Time", fmt: dt }, { key: "message", label: "Message" }, { key: "status", label: "Status" }], d.recentErrors)}
    <div class="section-title">Administration</div>
    <div class="formrow"><label>Status</label>
      <select id="ud_status">${["active", "suspended", "deleted"].map(s => `<option ${u.status === s ? "selected" : ""}>${s}</option>`).join("")}</select>
      <label>Plan</label>
      <select id="ud_plan">${["free", "developer", "pro", "internal"].map(p => `<option ${u.plan === p ? "selected" : ""}>${p}</option>`).join("")}</select>
    </div>
    <div class="formrow"><label>Tags</label><input id="ud_tags" style="flex:1" value="${esc(u.tags || "")}"></div>
    <div class="formrow"><label>Internal / test</label>
      <label><input type="checkbox" id="ud_internal" ${u.isInternal ? "checked" : ""}> internal</label>
      <label><input type="checkbox" id="ud_test" ${u.isTest ? "checked" : ""}> test</label>
    </div>
    <div class="formrow"><label>Notes</label><textarea id="ud_notes" rows="3" style="flex:1">${esc(u.notes || "")}</textarea></div>
    <div class="formrow">
      <button class="primary" id="ud_save">Save</button>
      <button id="ud_loginlink">Send sign-in link</button>
      <button class="danger" id="ud_delete">Delete account</button>
    </div>`);
  $("#ud_save").addEventListener("click", async () => {
    await mutate("/internal/api/users/" + encodeURIComponent(accountID), "PATCH", {
      status: $("#ud_status").value, plan: $("#ud_plan").value, tags: $("#ud_tags").value,
      isInternal: $("#ud_internal").checked, isTest: $("#ud_test").checked, notes: $("#ud_notes").value,
    });
    closeDrawer(); navigate();
  });
  $("#ud_loginlink").addEventListener("click", async () => {
    await mutate("/internal/api/users/" + encodeURIComponent(accountID) + "/login-link", "POST", {});
    alert("Sign-in link sent (or logged when SMTP is not configured).");
  });
  $("#ud_delete").addEventListener("click", async () => {
    if (!confirm(`Permanently delete ${u.email || u.id} and all their keys?`)) return;
    await mutate("/internal/api/users/" + encodeURIComponent(accountID), "DELETE");
    closeDrawer(); navigate();
  });
  $("#ud_newkey").addEventListener("click", async () => {
    const name = prompt("Key name (optional):") || "";
    const created = await mutate("/internal/api/users/" + encodeURIComponent(accountID) + "/keys", "POST", { name });
    openDrawer(`<h2>API key created</h2>
      <div class="warnbox">Copy this key now — it is shown exactly once and only a hash is stored.</div>
      <pre>${esc(created.apiKey)}</pre>${copyBtn(created.apiKey)} copy`);
  });
  document.querySelectorAll("[data-revoke]").forEach(btn => btn.addEventListener("click", async (e) => {
    e.stopPropagation();
    const reason = prompt("Reason for revocation (mailed to the user):") || "";
    await mutate("/internal/api/keys/" + encodeURIComponent(btn.dataset.revoke) + "/revoke", "POST", { accountId: accountID, reason, notify: true });
    showUserDrawer(accountID);
  }));
  document.querySelectorAll("[data-toggle]").forEach(btn => btn.addEventListener("click", async (e) => {
    e.stopPropagation();
    await mutate("/internal/api/keys/" + encodeURIComponent(btn.dataset.toggle), "PATCH", { disabled: btn.dataset.disabled !== "1" });
    showUserDrawer(accountID);
  }));
}

// renderKeySections appends the cross-account key tables to the Users page.
async function renderKeySections(el) {
  try {
    const d = await api("/internal/api/keys", { limit: 200 });
    let internal = { api_keys: [] };
    try { internal = await api("/internal/api/internal-keys"); } catch {}
    el.innerHTML = `
      <div class="section-title">All API keys</div>
      <div class="note">Secrets are stored as HMAC hashes; only prefix and last four are visible. Create keys from a user's drawer; click a row to edit.</div>
      ${table([
        { key: "prefix", label: "Key", fmt: (v, k) => esc(v) + "…" + esc(k.lastFour || "") },
        { key: "name", label: "Name" },
        { key: "accountEmail", label: "Support email", fmt: (v, k) => v ? `<a href="#/users?open=${esc(k.accountId)}">${esc(v)}</a>` : `<a href="#/users?open=${esc(k.accountId)}">${esc(k.accountId)}</a>` },
        { key: "plan", label: "Plan" }, { key: "environment", label: "Env" },
        { key: "clientId", label: "Client" }, { key: "scopes", label: "Scopes" },
        { key: "isActive", label: "State", fmt: (v, k) => k.revokedAt ? `<span class="pill bad">revoked</span>` : k.disabledAt ? `<span class="pill warn">disabled</span>` : k.expiresAt && new Date(k.expiresAt) < new Date() ? `<span class="pill warn">expired</span>` : `<span class="pill ok">active</span>` },
        { key: "createdAt", label: "Created", fmt: dt },
        { key: "firstUsedAt", label: "First used", fmt: dt },
        { key: "lastUsedAt", label: "Last used", fmt: dt },
        { key: "expiresAt", label: "Expires", fmt: dt },
        { key: "adminNotes", label: "Notes" },
      ], d.keys, (row) => `data-key='${esc(JSON.stringify({ id: row.id, accountId: row.accountId, name: row.name, accountEmail: row.accountEmail }))}'`)}
      <div class="section-title">Internal (service) API keys</div>
      <button class="primary" id="ik_new" style="margin-bottom:8px">Create internal key</button>
      ${table([
        { key: "prefix", label: "Key", fmt: (v, k) => esc(v) + "…" + esc(k.lastFour || "") },
        { key: "name", label: "Name" },
        { key: "isActive", label: "State", fmt: (v, k) => k.revokedAt ? `<span class="pill bad">revoked</span>` : `<span class="pill ok">active</span>` },
        { key: "createdAt", label: "Created", fmt: dt }, { key: "lastUsedAt", label: "Last used", fmt: dt },
        { key: "id", label: "", fmt: (v, k) => k.revokedAt ? "" : `<button class="danger" data-ikrevoke="${esc(v)}">Revoke</button>` },
      ], internal.api_keys)}`;
    el.querySelectorAll("tr[data-key]").forEach(tr => tr.addEventListener("click", (e) => {
      if (interactiveClick(e)) return;
      const meta = JSON.parse(tr.dataset.key);
      showKeyDrawer(meta);
    }));
    $("#ik_new").addEventListener("click", async () => {
      const name = prompt("Internal key name:") || "Internal";
      const created = await mutate("/internal/api/internal-keys", "POST", { name });
      openDrawer(`<h2>Internal key created</h2><div class="warnbox">Shown once only.</div><pre>${esc(created.api_key)}</pre>${copyBtn(created.api_key)} copy`);
    });
    el.querySelectorAll("[data-ikrevoke]").forEach(btn => btn.addEventListener("click", async (e) => {
      e.stopPropagation();
      if (!confirm("Revoke this internal key?")) return;
      await mutate("/internal/api/internal-keys/" + encodeURIComponent(btn.dataset.ikrevoke), "DELETE");
      navigate();
    }));
  } catch (err) {
    el.innerHTML = `<div class="note">Keys unavailable: ${esc(err.message)}</div>`;
  }
}

function showKeyDrawer(meta) {
  openDrawer(`<h2>Key ${esc(meta.name || meta.id)}</h2>
    ${kv([
      ["Support email", meta.accountEmail ? `<a href="mailto:${esc(meta.accountEmail)}">${esc(meta.accountEmail)}</a>` : "–", meta.accountEmail],
      ["Account", `<a href="#/users?open=${esc(meta.accountId)}">${esc(meta.accountId)}</a>`, meta.accountId],
    ])}
    <div class="formrow"><label>Name</label><input id="k_name" value="${esc(meta.name || "")}"></div>
    <div class="formrow"><label>Environment</label><select id="k_env">${["production", "staging", "development", "test"].map(v => `<option>${v}</option>`).join("")}</select></div>
    <div class="formrow"><label>Scopes (csv)</label><input id="k_scopes"></div>
    <div class="formrow"><label>Monthly quota</label><input id="k_quota" type="number" value="0"></div>
    <div class="formrow"><label>Expires (YYYY-MM-DD)</label><input id="k_expires" placeholder="empty = never"></div>
    <div class="formrow"><label>Admin notes</label><input id="k_notes" style="flex:1"></div>
    <div class="formrow">
      <button class="primary" id="k_save">Save</button>
      <button class="danger" id="k_revoke">Revoke</button>
    </div>
    <div class="note">Usage for this key: <a href="#/requests?key=${esc(meta.id)}">request explorer</a>. Rotation = create a new key on the user page, migrate, then revoke this one.</div>`);
  $("#k_save").addEventListener("click", async () => {
    await mutate("/internal/api/keys/" + encodeURIComponent(meta.id), "PATCH", {
      name: $("#k_name").value, environment: $("#k_env").value, scopes: $("#k_scopes").value,
      monthlyQuota: Number($("#k_quota").value || 0), expiresAt: $("#k_expires").value, notes: $("#k_notes").value,
    });
    closeDrawer(); navigate();
  });
  $("#k_revoke").addEventListener("click", async () => {
    const reason = prompt("Reason (mailed to the user):") || "";
    await mutate("/internal/api/keys/" + encodeURIComponent(meta.id) + "/revoke", "POST", { accountId: meta.accountId, reason, notify: true });
    closeDrawer(); navigate();
  });
}

PAGES.audit = {
  title: "Audit Log",
  async render(el) {
    const query = new URLSearchParams(location.hash.split("?")[1] || "");
    const offset = Number(query.get("offset") || 0);
    const d = await api("/internal/api/audit", { ...rangeParams(), limit: 100, offset, actor: query.get("actor") || "", action: query.get("action") || "" });
    el.innerHTML = `
      <div class="filters">
        <input id="aactor" placeholder="actor" value="${esc(query.get("actor") || "")}">
        <input id="aaction" placeholder="action prefix (e.g. key.)" value="${esc(query.get("action") || "")}">
        <button id="aapply" class="primary">Filter</button>
      </div>
      ${table([
        { key: "at", label: "Time", fmt: dt },
        { key: "actor", label: "Actor" }, { key: "actorRole", label: "Role" },
        { key: "action", label: "Action" },
        { key: "targetType", label: "Target type" }, { key: "targetId", label: "Target" },
        { key: "details", label: "Details" },
        { key: "requestId", label: "Request" },
      ], d.events)}
      ${pager(d.total, 100, offset, "audit")}`;
    bindPager(el, "audit", query);
    $("#aapply").addEventListener("click", () => {
      const parts = new URLSearchParams();
      if ($("#aactor").value) parts.set("actor", $("#aactor").value);
      if ($("#aaction").value) parts.set("action", $("#aaction").value);
      location.hash = "#/audit?" + parts.toString();
    });
  },
};

PAGES.settings = {
  title: "Settings",
  async render(el) {
    const d = await api("/internal/api/settings");
    let adminUsers = { adminUsers: [] };
    try { adminUsers = await api("/internal/api/admin-users"); } catch {}
    const cfg = d.config;
    el.innerHTML = `
      <div class="grid two">
        <div class="card"><h3>Runtime settings</h3>
          <div class="formrow"><label>Slow request threshold (ms)</label>
            <input id="s_slow" value="${esc(d.settings.slow_request_ms || cfg.slowRequestMsDefault)}">
            <button class="primary" id="s_save">Save</button></div>
          <div class="note">Other limits are environment configuration (read-only here).</div>
          ${kv([
            ["Telemetry DB", esc(cfg.telemetryDbPath)],
            ["Raw event retention", cfg.rawRetentionDays + " days"],
            ["Hourly aggregate retention", cfg.hourlyRetentionDays + " days"],
            ["Log retention", cfg.logRetentionDays + " days"],
            ["Error retention", cfg.errorRetentionDays + " days"],
            ["Anonymous ID rotation", esc(cfg.anonIdRotation)],
            ["Display timezone default", esc(cfg.timezone)],
            ["Instance", esc(cfg.instance)],
          ])}
        </div>
        <div class="card"><h3>Operations</h3>
          <div class="formrow">
            <button id="op_import" class="primary">Import historical logs</button>
            <label><input type="checkbox" id="op_dry"> dry run</label>
            <label><input type="checkbox" id="op_fresh"> re-scan files</label>
          </div>
          <div class="formrow">
            <label>Range (optional)</label>
            <input type="date" id="op_from"> – <input type="date" id="op_to">
          </div>
          <div class="formrow"><button id="op_rebuild">Rebuild all aggregates</button></div>
          <pre id="op_out" class="hidden"></pre>
          <div class="section-title">Aggregation state</div>
          ${kv(Object.entries(d.aggregation || {}).map(([k, v]) => [k, esc(v || "–")]))}
        </div>
      </div>
      <div class="card"><h3>Admin panel accounts</h3>
        ${adminUsers.envAdminConfigured ? `<div class="note">The INTERNAL_ADMIN_USERNAME env account always works as administrator (bootstrap).</div>` : ""}
        ${table([
          { key: "username", label: "Username" }, { key: "role", label: "Role" },
          { key: "disabledAt", label: "State", fmt: (v) => v ? `<span class="pill bad">disabled</span>` : `<span class="pill ok">active</span>` },
          { key: "lastLoginAt", label: "Last login", fmt: dt },
          { key: "createdBy", label: "Created by" },
          { key: "id", label: "", fmt: (v, u) => `<button data-auedit='${esc(JSON.stringify(u))}'>Edit</button> <button class="danger" data-audel="${esc(v)}">Delete</button>` },
        ], adminUsers.adminUsers)}
        <div class="section-title">Create admin user</div>
        <div class="formrow">
          <input id="au_name" placeholder="username">
          <input id="au_pass" placeholder="password (min 12 chars)" type="password">
          <select id="au_role">${["viewer", "support", "operator", "administrator"].map(r => `<option>${r}</option>`).join("")}</select>
          <button class="primary" id="au_create">Create</button>
        </div>
        <div class="note">Roles: viewer (dashboards) → support (+user detail, notes, links) → operator (+job/error triage, imports) → administrator (everything).</div>
      </div>`;
    $("#s_save").addEventListener("click", async () => {
      await mutate("/internal/api/settings", "PATCH", { slow_request_ms: $("#s_slow").value });
      navigate();
    });
    $("#op_import").addEventListener("click", async () => {
      const out = $("#op_out");
      out.classList.remove("hidden");
      out.textContent = "Importing…";
      try {
        const summary = await mutate("/internal/api/import-logs", "POST", {
          dryRun: $("#op_dry").checked, fresh: $("#op_fresh").checked, from: $("#op_from").value, to: $("#op_to").value,
        });
        out.textContent = JSON.stringify(summary, null, 2);
      } catch (err) { out.textContent = "Import failed: " + err.message; }
    });
    $("#op_rebuild").addEventListener("click", async () => {
      if (!confirm("Rebuild all aggregate tables from raw events? This may take a moment.")) return;
      const out = $("#op_out");
      out.classList.remove("hidden");
      out.textContent = "Rebuilding…";
      try { out.textContent = JSON.stringify(await mutate("/internal/api/rebuild-aggregates", "POST", {}), null, 2); }
      catch (err) { out.textContent = "Rebuild failed: " + err.message; }
    });
    $("#au_create").addEventListener("click", async () => {
      await mutate("/internal/api/admin-users", "POST", {
        username: $("#au_name").value, password: $("#au_pass").value, role: $("#au_role").value,
      });
      navigate();
    });
    el.querySelectorAll("[data-audel]").forEach(btn => btn.addEventListener("click", async () => {
      if (!confirm("Delete this admin user?")) return;
      await mutate("/internal/api/admin-users/" + encodeURIComponent(btn.dataset.audel), "DELETE");
      navigate();
    }));
    el.querySelectorAll("[data-auedit]").forEach(btn => btn.addEventListener("click", () => {
      const u = JSON.parse(btn.dataset.auedit);
      openDrawer(`<h2>Edit ${esc(u.username)}</h2>
        <div class="formrow"><label>Role</label><select id="aue_role">${["viewer", "support", "operator", "administrator"].map(r => `<option ${u.role === r ? "selected" : ""}>${r}</option>`).join("")}</select></div>
        <div class="formrow"><label>Disabled</label><input type="checkbox" id="aue_disabled" ${u.disabledAt ? "checked" : ""}></div>
        <div class="formrow"><label>New password</label><input id="aue_pass" type="password" placeholder="leave empty to keep"></div>
        <button class="primary" id="aue_save">Save</button>`);
      $("#aue_save").addEventListener("click", async () => {
        await mutate("/internal/api/admin-users/" + encodeURIComponent(u.id), "PATCH", {
          role: $("#aue_role").value, disabled: $("#aue_disabled").checked, password: $("#aue_pass").value,
        });
        closeDrawer(); navigate();
      });
    }));
  },
};

/* ---------- boot ---------- */

async function boot() {
  renderNav();
  try {
    state.session = await api("/internal/api/session");
    $("#whoami").textContent = `${state.session.username} · ${state.session.role}`;
  } catch {
    location.reload();
    return;
  }
  $("#range").value = state.range;
  $("#customrange").classList.toggle("hidden", state.range !== "custom");
  $("#tz").value = state.tz;
  $("#range").addEventListener("change", (e) => {
    state.range = e.target.value;
    localStorage.setItem("rfmAdmin.range", state.range);
    $("#customrange").classList.toggle("hidden", state.range !== "custom");
    if (state.range !== "custom") navigate();
  });
  $("#applyrange").addEventListener("click", () => {
    state.from = $("#from").value; state.to = $("#to").value;
    navigate();
  });
  $("#tz").addEventListener("change", (e) => {
    state.tz = e.target.value;
    localStorage.setItem("rfmAdmin.tz", state.tz);
    navigate();
  });
  $("#refresh").addEventListener("click", navigate);
  $("#logout").addEventListener("click", async () => {
    await fetch("/internal/logout", { method: "POST", credentials: "same-origin" });
    location.reload();
  });
  $("#drawerclose").addEventListener("click", closeDrawer);
  $("#drawer").addEventListener("click", (e) => { if (e.target.id === "drawer") closeDrawer(); });
  window.addEventListener("hashchange", navigate);
  navigate();
}

boot();
