"use strict";
// NeoTunnel panel. No framework: views are template strings with escaping,
// events are delegated through data-act / data-bind attributes.
(() => {
  // ---------- templating ----------
  const RAW = Symbol("raw");
  const raw = (s) => ({ [RAW]: true, s: String(s) });
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  const part = (v) => (v == null || v === false ? "" : Array.isArray(v) ? v.map(part).join("") : v[RAW] ? v.s : esc(v));
  const html = (strs, ...vals) => raw(strs.reduce((out, s, i) => out + s + (i < vals.length ? part(vals[i]) : ""), ""));
  const $ = (s, el = document) => el.querySelector(s);

  // ---------- icons (stroke, 24px grid) ----------
  const svg = (d, size = 18) => raw(`<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">${d}</svg>`);
  const I = {
    home: (s) => svg('<rect x="3" y="3" width="7.5" height="7.5" rx="2"/><rect x="13.5" y="3" width="7.5" height="7.5" rx="2"/><rect x="3" y="13.5" width="7.5" height="7.5" rx="2"/><rect x="13.5" y="13.5" width="7.5" height="7.5" rx="2"/>', s),
    tunnel: (s) => svg('<circle cx="5" cy="12" r="2.5"/><circle cx="19" cy="12" r="2.5"/><path d="M7.5 12c2-4 7-4 9 0"/><path d="M7.5 12c2 4 7 4 9 0"/>', s),
    server: (s) => svg('<rect x="3.5" y="4" width="17" height="6.5" rx="2"/><rect x="3.5" y="13.5" width="17" height="6.5" rx="2"/><path d="M7 7.25h.01M7 16.75h.01M11 7.25h6M11 16.75h6"/>', s),
    ports: (s) => svg('<path d="M4 7h11"/><path d="m12 4 3 3-3 3"/><path d="M20 17H9"/><path d="m12 14-3 3 3 3"/>', s),
    gauge: (s) => svg('<path d="M4.5 17a8.5 8.5 0 1 1 15 0"/><path d="m12 13 4-5"/><circle cx="12" cy="13" r="1.4"/>', s),
    term: (s) => svg('<rect x="3" y="4" width="18" height="16" rx="2.5"/><path d="m7 9 3 3-3 3M12.5 15H17"/>', s),
    gear: (s) => svg('<path d="M4 6h10M18 6h2M4 12h4M12 12h8M4 18h12M20 18h0"/><circle cx="16" cy="6" r="2"/><circle cx="10" cy="12" r="2"/><circle cx="18" cy="18" r="2"/>', s),
    plus: (s) => svg('<path d="M12 5v14M5 12h14"/>', s),
    trash: (s) => svg('<path d="M4 7h16M10 11v6M14 11v6M6 7l1 12a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2l1-12M9 7V4h6v3"/>', s),
    copy: (s) => svg('<rect x="8" y="8" width="12" height="12" rx="2.5"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2"/>', s),
    play: (s) => svg('<path d="M7 5v14l11-7z"/>', s),
    sync: (s) => svg('<path d="M20 11a8 8 0 0 0-14.3-4.9L4 8"/><path d="M4 4v4h4"/><path d="M4 13a8 8 0 0 0 14.3 4.9L20 16"/><path d="M20 20v-4h-4"/>', s),
    up: (s) => svg('<path d="M12 19V5M6 11l6-6 6 6"/>', s),
    down: (s) => svg('<path d="M12 5v14M6 13l6 6 6-6"/>', s),
    moon: (s) => svg('<path d="M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z"/>', s),
    sun: (s) => svg('<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M2 12h2M20 12h2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>', s),
    out: (s) => svg('<path d="M14 4h4a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-4"/><path d="M9 8l-4 4 4 4M5 12h11"/>', s),
    check: (s) => svg('<path d="m5 12.5 4.5 4.5L19 7.5"/>', s),
    x: (s) => svg('<path d="M6 6l12 12M18 6 6 18"/>', s),
    key: (s) => svg('<circle cx="8" cy="15" r="4"/><path d="m11 12 9-9M17 6l3 3M14.5 8.5l2 2"/>', s),
    cpu: (s) => svg('<rect x="6" y="6" width="12" height="12" rx="2"/><path d="M9 2v3M15 2v3M9 19v3M15 19v3M2 9h3M2 15h3M19 9h3M19 15h3"/>', s),
    bolt: (s) => svg('<path d="M13 3 5 14h6l-1 7 8-11h-6z"/>', s),
    globe: (s) => svg('<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c3 3.5 3 14.5 0 18M12 3c-3 3.5-3 14.5 0 18"/>', s),
    alert: (s) => svg('<path d="M12 3 2 20h20z"/><path d="M12 10v4M12 17h.01"/>', s),
    upgrade: (s) => svg('<path d="M12 16V4M7 9l5-5 5 5"/><path d="M4 16v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3"/>', s),
  };

  // ---------- formatting ----------
  const fmtRate = (bps) => {
    if (!bps || bps < 1) return "0 bps";
    const u = [["Gbps", 1e9], ["Mbps", 1e6], ["Kbps", 1e3]];
    for (const [n, d] of u) if (bps >= d) return `${(bps / d).toFixed(bps / d >= 100 ? 0 : 1)} ${n}`;
    return `${Math.round(bps)} bps`;
  };
  const fmtBytes = (n) => {
    if (!n) return "0 B";
    const u = ["B", "KB", "MB", "GB", "TB", "PB"];
    const i = Math.min(u.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
    return `${(n / 1024 ** i).toFixed(i ? 1 : 0)} ${u[i]}`;
  };
  const fmtUptime = (s) => {
    const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60);
    if (d) return `${d} روز و ${h} ساعت`;
    if (h) return `${h} ساعت و ${m} دقیقه`;
    return `${m} دقیقه`;
  };
  const ago = (unix) => {
    if (!unix) return "هرگز";
    const s = Math.max(0, Date.now() / 1000 - unix);
    if (s < 60) return "همین الان";
    if (s < 3600) return `${Math.floor(s / 60)} دقیقه پیش`;
    if (s < 86400) return `${Math.floor(s / 3600)} ساعت پیش`;
    return `${Math.floor(s / 86400)} روز پیش`;
  };
  const ms = (v) => (v || v === 0 ? `${v.toFixed(v < 10 ? 2 : 1)} ms` : "-");

  const MODE_INFO = {
    "6to4": ["SIT با IPv6 خصوصی و IPIP6 برای IPv4. سبک، سریع، بدون نرم‌افزار اضافه.", true],
    "6to4-gre": ["مثل 6to4، با لایه‌ی GRE6 که هم IPv4 و هم IPv6 را می‌برد.", false],
    ipip: ["ساده‌ترین و کم‌سربارترین تانل IPv4.", false],
    gre: ["GRE استاندارد؛ سازگار با میکروتیک و بیشتر روترها.", false],
    sit: ["فقط IPv6 روی IPv4.", false],
    gretap: ["لایه ۲ (Ethernet) روی GRE؛ مشابه EoIP میکروتیک.", false],
    vxlan: ["لایه ۲ روی UDP؛ مناسب شبکه‌های مجازی.", false],
    geneve: ["لایه ۲ روی UDP، نسل جدیدتر VXLAN.", false],
  };
  const BENCH = [
    ["direct", "مسیر مستقیم (بدون تانل)"], ["6to4", "6to4"], ["6to4-udp", "6to4 روی UDP"], ["6to4-gre", "6to4 + GRE6"],
    ["ipip", "IPIP"], ["gre", "GRE"], ["gretap", "GRETAP"], ["vxlan", "VXLAN"], ["geneve", "GENEVE"],
  ];

  // ---------- state ----------
  const store = (k, v) => { try { v === undefined ? null : localStorage.setItem(k, v); return localStorage.getItem(k); } catch { return null; } };
  const S = {
    authed: true, ov: null, prev: null, rates: {}, live: true,
    route: (location.hash.slice(2) || "dashboard"),
    theme: store("nt-theme") || "dark",
    modal: null, wiz: null, form: {},
    jobs: [], job: null, speed: { tunnel: "", peer: "", only: BENCH.map((b) => b[0]) },
    settingsDrawn: false,
  };
  document.documentElement.dataset.theme = S.theme;

  // ---------- api ----------
  async function api(method, path, body) {
    const res = await fetch("api/" + path, {
      method, credentials: "same-origin",
      headers: Object.assign({ "X-NeoTunnel": "1" }, body ? { "Content-Type": "application/json" } : {}),
      body: body ? JSON.stringify(body) : undefined,
    });
    let data = null;
    try { data = await res.json(); } catch { /* empty */ }
    if (res.status === 401 && path !== "login") { S.authed = false; draw(); throw new Error("لطفاً دوباره وارد شوید"); }
    if (!res.ok) throw new Error((data && data.error) || `خطا (${res.status})`);
    return data;
  }

  function toast(msg, kind = "ok") {
    const el = document.createElement("div");
    el.className = `toast ${kind}`;
    el.innerHTML = part(html`${kind === "ok" ? I.check() : I.alert()}<div>${msg}</div>`);
    $("#toasts").appendChild(el);
    setTimeout(() => el.remove(), kind === "ok" ? 3500 : 7000);
  }

  async function copy(text) {
    try { await navigator.clipboard.writeText(text); }
    catch {
      const t = document.createElement("textarea");
      t.value = text; document.body.appendChild(t); t.select(); document.execCommand("copy"); t.remove();
    }
    toast("کپی شد");
  }

  // ---------- data ----------
  async function refresh() {
    try {
      const ov = await api("GET", "overview");
      if (S.ov) {
        const dt = (ov.time - S.ov.time) / 1000;
        const prev = Object.fromEntries(S.ov.tunnels.map((t) => [t.name, t]));
        for (const t of ov.tunnels) {
          const p = prev[t.name];
          if (p && dt > 0 && t.rx >= p.rx && t.tx >= p.tx) S.rates[t.name] = { rx: ((t.rx - p.rx) * 8) / dt, tx: ((t.tx - p.tx) * 8) / dt };
        }
      }
      S.ov = ov; S.live = true; S.authed = true;
    } catch (e) {
      S.live = false;
    }
    if (!S.modal || S.modal.passive) draw();
  }

  // User-facing tunnels: hide carriers that only exist under another tunnel.
  const visibleTunnels = () => (S.ov ? S.ov.tunnels.filter((t) => !t.carrier) : []);
  const tunnel = (name) => S.ov && S.ov.tunnels.find((t) => t.name === name);
  const rate = (name) => S.rates[name] || { rx: 0, tx: 0 };
  const totalRate = () => visibleTunnels().reduce((a, t) => ({ rx: a.rx + rate(t.name).rx, tx: a.tx + rate(t.name).tx }), { rx: 0, tx: 0 });
  const peer = (name) => S.ov && S.ov.peers.find((p) => p.name === name);

  // ---------- views ----------
  const NAV = [
    ["dashboard", "داشبورد", I.home],
    ["tunnels", "تانل‌ها", I.tunnel],
    ["servers", "سرورها", I.server],
    ["ports", "پورت‌ها", I.ports],
    ["speed", "تست سرعت", I.gauge],
    ["commands", "دستورها", I.term],
    ["settings", "تنظیمات", I.gear],
  ];

  function draw() {
    const app = $("#app");
    if (!S.authed) { app.innerHTML = part(viewLogin()); $("#login-user") && $("#login-user").focus(); return; }
    if (!S.ov) { app.innerHTML = part(html`<div class="login-wrap"><div class="muted">در حال بارگذاری…</div></div>`); return; }
    // Forms on the settings page keep their values: draw them once per visit.
    if (S.route === "settings" && S.settingsDrawn && $(".main")) { drawChrome(); return; }
    app.innerHTML = part(viewShell());
    S.settingsDrawn = S.route === "settings";
    drawModal();
  }

  // Update only the sidebar/topbar status without touching page forms.
  function drawChrome() {
    const live = $(".live");
    if (live) live.classList.toggle("off", !S.live);
  }

  function viewLogin() {
    return html`
      <div class="login-wrap">
        <div class="card login">
          <div class="brand"><img src="assets/logo.svg" alt=""><div><b>NeoTunnel</b><small>مدیریت تانل‌های سرور</small></div></div>
          <h1>ورود به پنل</h1>
          <form data-form="login">
            <div class="field"><label for="login-user">نام کاربری</label><input class="input ltr" id="login-user" name="user" autocomplete="username" required></div>
            <div class="field"><label for="login-pass">رمز عبور</label><input class="input ltr" id="login-pass" name="password" type="password" autocomplete="current-password" required></div>
            <button class="btn primary" type="submit">ورود</button>
          </form>
          <p class="muted small" style="text-align:center;margin:18px 0 0">رمز را فراموش کرده‌اید؟ روی سرور اجرا کنید: <code>sudo neotunnel panel password</code></p>
        </div>
      </div>`;
  }

  function viewShell() {
    const ov = S.ov;
    const counts = { tunnels: visibleTunnels().length, servers: ov.peers.length, ports: ov.forwards.length };
    const page = { dashboard: viewDashboard, tunnels: viewTunnels, servers: viewServers, ports: viewPorts, speed: viewSpeed, commands: viewCommands, settings: viewSettings }[S.route] || viewDashboard;
    const [title, sub, actions] = pageHead();
    return html`
      <div class="shell">
        <aside class="sidebar">
          <div class="brand"><img src="assets/logo.svg" alt=""><div><b>NeoTunnel</b><small>${ov.node.version}</small></div></div>
          <nav class="nav">
            ${NAV.map(([id, label, icon]) => html`<a href="#/${id}" class="${S.route === id ? "active" : ""}">${icon()}<span>${label}</span>${counts[id] != null ? html`<span class="count">${counts[id]}</span>` : ""}</a>`)}
          </nav>
          <div class="sidebar-foot">
            <div class="node-card">
              <div class="name">${I.server(16)} ${ov.node.name}</div>
              <div class="meta"><span class="ltr">${ov.node.address || "-"}</span></div>
              <div class="meta">${ov.master ? `مدیریت‌شده توسط ${ov.master.name}` : ov.peers.length ? `سرور اصلی ${ov.peers.length} سرور` : "سرور اصلی"}</div>
            </div>
            <div class="foot-actions">
              <button class="btn sm ghost" data-act="theme" title="تغییر تم">${S.theme === "dark" ? I.sun(16) : I.moon(16)}</button>
              <span class="spacer"></span>
              <button class="btn sm ghost" data-act="logout">${I.out(16)} خروج</button>
            </div>
          </div>
        </aside>
        <main class="main">
          <div class="topbar">
            <div><h1>${title}</h1><div class="sub">${sub}</div></div>
            <div class="actions">
              <span class="live ${S.live ? "" : "off"}"><i></i>${S.live ? "زنده" : "قطع ارتباط"}</span>
              ${actions}
            </div>
          </div>
          ${ov.errors && ov.errors.length ? html`<div class="callout bad" style="margin-bottom:16px"><b>${I.alert(16)} مشکل در اعمال تنظیمات</b>${ov.errors.map((e) => html`<div class="mono small">${e}</div>`)}</div>` : ""}
          ${page()}
        </main>
      </div>`;
  }

  function pageHead() {
    const add = html`<button class="btn primary" data-act="wizard">${I.plus(16)} تانل جدید</button>`;
    switch (S.route) {
      case "tunnels": return ["تانل‌ها", "هر تانل می‌تواند روی پورت جداگانه و به سرور جداگانه برود", add];
      case "servers": return ["سرورها", "سرورهای مقصد (خارج) که از اینجا مدیریت می‌شوند", html`<button class="btn" data-act="sync-all">${I.sync(16)} همگام‌سازی همه</button><button class="btn primary" data-act="add-server">${I.plus(16)} افزودن سرور</button>`];
      case "ports": return ["پورت‌ها", "پورت‌های این سرور که به مقصدهای مختلف فوروارد می‌شوند", html`<button class="btn primary" data-act="add-forward">${I.plus(16)} فوروارد جدید</button>`];
      case "speed": return ["تست سرعت", "پینگ، جیتر، افت بسته و پهنای باند واقعی بین دو سرور", ""];
      case "commands": return ["دستورها", "معادل خط فرمان کارهای پنل، با مقادیر واقعی همین سرور", ""];
      case "settings": return ["تنظیمات", "نام سرور، آدرس، پورت پنل و رمز عبور", ""];
      default: return ["داشبورد", `${S.ov.node.name} · ${S.ov.node.address || ""}`, add];
    }
  }

  // ---------- dashboard ----------
  function viewDashboard() {
    const ov = S.ov, ts = visibleTunnels();
    const up = ts.filter((t) => t.up).length;
    const paired = ov.peers.filter((p) => p.paired).length;
    const tr = totalRate();
    const mem = ov.node.mem_total ? (ov.node.mem_used / ov.node.mem_total) * 100 : 0;
    return html`
      <div class="stack">
        <div class="grid stats">
          <div class="card stat accent"><div class="glyph">${I.tunnel(20)}</div><div class="label">تانل‌های فعال</div>
            <div class="value">${up}<small>/ ${ts.length}</small></div><div class="foot">${ts.length ? (up === ts.length ? "همه سالم" : `${ts.length - up} تانل قطع است`) : "هنوز تانلی ساخته نشده"}</div></div>
          <div class="card stat"><div class="glyph">${I.server(20)}</div><div class="label">سرورهای متصل</div>
            <div class="value">${paired}<small>/ ${ov.peers.length}</small></div><div class="foot">${ov.peers.length - paired ? `${ov.peers.length - paired} سرور منتظر اتصال` : "همه متصل"}</div></div>
          <div class="card stat"><div class="glyph">${I.bolt(20)}</div><div class="label">ترافیک لحظه‌ای</div>
            <div class="value ltr" style="font-size:21px">${I.down(15)} ${fmtRate(tr.rx)}</div><div class="foot ltr">${I.up(13)} ${fmtRate(tr.tx)}</div></div>
          <div class="card stat"><div class="glyph">${I.cpu(20)}</div><div class="label">منابع سرور</div>
            <div class="value">${ov.node.load[0].toFixed(2)}<small>بار / ${ov.node.cpus} هسته</small></div>
            <div class="foot">حافظه ${fmtBytes(ov.node.mem_used)} از ${fmtBytes(ov.node.mem_total)}</div><div class="meter"><b style="width:${mem.toFixed(0)}%"></b></div></div>
        </div>
        <div class="grid two">
          <div class="card"><h2>${I.globe(18)} نقشه‌ی اتصال‌ها <span class="hint">خط متحرک = ترافیک در جریان</span></h2>${topology()}</div>
          <div class="card"><h2>${I.tunnel(18)} تانل‌ها <span class="hint"><a href="#/tunnels">همه</a></span></h2>${miniTunnels()}</div>
        </div>
        ${!ov.node.bbr ? html`<div class="callout warn">${I.bolt(16)} بهینه‌سازی شبکه (BBR) روی این سرور فعال نیست. برای سرعت بیشتر روی سرور اجرا کنید: <code>sudo neotunnel optimize</code></div>` : ""}
      </div>`;
  }

  function miniTunnels() {
    const ts = visibleTunnels();
    if (!ts.length) return html`<div class="empty">${I.tunnel(28)}<b>اولین تانل را بسازید</b>6to4 پیشنهاد می‌شود: سریع و سبک<br><button class="btn primary" data-act="wizard">${I.plus(16)} تانل جدید</button></div>`;
    return html`<table class="table"><thead><tr><th>نام</th><th>مقصد</th><th>وضعیت</th><th style="text-align:end">ترافیک</th></tr></thead><tbody>
      ${ts.slice(0, 7).map((t) => html`<tr>
        <td><b>${t.name}</b> <span class="chip violet">${t.mode || t.kind}</span></td>
        <td class="muted">${t.peer || html`<span class="ltr">${t.far_ip}</span>`}</td>
        <td>${statusChip(t)}</td>
        <td class="num small">↓ ${fmtRate(rate(t.name).rx)}<br><span class="faint">↑ ${fmtRate(rate(t.name).tx)}</span></td></tr>`)}
    </tbody></table>`;
  }

  function statusChip(t) {
    if (t.error) return html`<span class="chip bad"><i></i>خطا</span>`;
    const p = t.peer && peer(t.peer);
    if (p && !p.paired) return html`<span class="chip warn"><i></i>منتظر سرور</span>`;
    return t.up ? html`<span class="chip ok"><i></i>فعال</span>` : html`<span class="chip bad"><i></i>قطع</span>`;
  }

  // Topology: this server on the right, destinations on the left.
  function topology() {
    const ov = S.ov;
    const groups = new Map();
    for (const t of visibleTunnels()) {
      const key = t.peer || t.far_ip;
      if (!groups.has(key)) groups.set(key, { name: t.peer || "دستگاه خارجی", ip: t.far_ip, tunnels: [], paired: true });
      groups.get(key).tunnels.push(t);
    }
    for (const p of ov.peers) {
      if (!groups.has(p.name)) groups.set(p.name, { name: p.name, ip: p.address, tunnels: [], paired: p.paired });
      const g = groups.get(p.name); g.paired = p.paired; g.ip = p.address;
    }
    if (ov.master && !groups.size) groups.set(ov.master.name, { name: ov.master.name, ip: ov.master.address.replace(/:\d+$/, ""), tunnels: [], paired: ov.master.paired });
    const list = [...groups.values()];
    if (!list.length) return html`<div class="empty">${I.globe(28)}<b>هنوز سروری وصل نیست</b>یک سرور مقصد اضافه کنید تا نقشه ساخته شود<br><button class="btn primary" data-act="add-server">${I.plus(16)} افزودن سرور</button></div>`;
    const W = 800, H = 360, n = list.length;
    const sx = 650, sy = H / 2;
    const gap = Math.min(112, (H - 70) / Math.max(1, n - 1));
    const y0 = sy - (gap * (n - 1)) / 2;
    const nodes = list.map((g, i) => {
      const y = n === 1 ? sy : y0 + i * gap, x = 150;
      const anyUp = g.tunnels.some((t) => t.up);
      const flowing = g.tunnels.some((t) => rate(t.name).rx + rate(t.name).tx > 1000);
      const st = !g.paired ? "wait" : anyUp ? "up" : g.tunnels.length ? "down" : "wait";
      const path = `M${sx - 105} ${sy} C ${sx - 230} ${sy}, ${x + 230} ${y}, ${x + 105} ${y}`;
      const label = g.tunnels.map((t) => t.name + (t.port ? `:${t.port}` : "")).join(" · ") || (g.paired ? "بدون تانل" : "منتظر اتصال");
      return { g, x, y, st, flowing, path, label };
    });
    return html`<svg class="topo" viewBox="0 0 ${W} ${H}" role="img" aria-label="نقشه‌ی اتصال سرورها">
      <defs><linearGradient id="auroraLine" gradientUnits="userSpaceOnUse" x1="0" y1="0" x2="${W}" y2="0"><stop offset="0" stop-color="#a78bfa"/><stop offset=".5" stop-color="#38bdf8"/><stop offset="1" stop-color="#2dd4bf"/></linearGradient></defs>
      ${nodes.map((d) => raw(`<path class="link ${d.st}" d="${d.path}"/>${d.st === "up" && d.flowing ? `<path class="flow" d="${d.path}"/>` : ""}`))}
      ${nodes.map((d) => raw(`<text class="tag" x="${d.x + 118}" y="${d.y - 12}" text-anchor="start" direction="ltr">${esc(d.label)}</text>`))}
      ${nodes.map((d) => raw(`<g><rect class="node-bg" x="${d.x - 105}" y="${d.y - 31}" width="210" height="62" rx="15"/>
        <circle class="dot ${d.st}" cx="${d.x + 84}" cy="${d.y - 8}" r="5.5"/>
        <text x="${d.x + 72}" y="${d.y - 3}" text-anchor="start" direction="rtl" font-weight="700">${esc(d.g.name)}</text>
        <text class="sub" x="${d.x - 90}" y="${d.y + 19}" text-anchor="start" direction="ltr">${esc(d.g.ip || "")}</text></g>`))}
      ${raw(`<g><rect class="node-self" x="${sx - 105}" y="${sy - 38}" width="210" height="76" rx="18"/>
        <text x="${sx + 88}" y="${sy - 7}" text-anchor="start" direction="rtl" font-weight="800" font-size="16">${esc(ov.node.name)}</text>
        <text class="sub" x="${sx - 90}" y="${sy + 22}" text-anchor="start" direction="ltr">${esc(ov.node.address || "")}</text>
        <text class="tag" x="${sx + 88}" y="${sy + 22}" text-anchor="start" direction="rtl">${ov.master ? "این سرور" : "سرور اصلی"}</text></g>`)}
    </svg>`;
  }

  // ---------- tunnels ----------
  function viewTunnels() {
    const ts = visibleTunnels();
    if (!ts.length) return html`<div class="empty">${I.tunnel(30)}<b>هیچ تانلی وجود ندارد</b>با چند کلیک یک تانل 6to4 بسازید؛ اگر مسیر پروتکل‌ها را می‌بندد، آن را روی یک پورت UDP اجرا کنید.<br><button class="btn primary" data-act="wizard">${I.plus(16)} تانل جدید</button></div>`;
    return html`<div class="grid cards">${ts.map(tunnelCard)}</div>`;
  }

  function tunnelCard(t) {
    const r = rate(t.name);
    const under = t.over ? tunnel(t.over) : null;
    const fwd = S.ov.forwards.filter((f) => f.tunnel === t.name);
    return html`<div class="card tunnel-card">
      <div class="tunnel-head">
        <div class="ico">${I.tunnel(22)}</div>
        <div><div class="title">${t.name}</div><div class="route">${S.ov.node.name} ← ${t.peer || "دستگاه خارجی"} · <span class="ltr">${t.far_ip}</span></div></div>
        <div class="chips">${statusChip(t)}</div>
      </div>
      <div class="row">
        <span class="chip violet">${t.mode || t.kind}</span>
        <span class="chip">لایه ${t.layer}</span>
        ${t.port || (under && under.port) ? html`<span class="chip info mono">UDP ${t.port || under.port}</span>` : html`<span class="chip">پروتکل خام</span>`}
        ${t.managed ? html`<span class="chip warn">از سرور اصلی</span>` : ""}
      </div>
      <dl class="kv">
        ${t.here4 ? html`<dt>IPv4 این طرف / آن طرف</dt><dd><span class="ltr">${t.here4.split("/")[0]}</span> ⇄ <span class="ltr">${t.there4}</span></dd>` : ""}
        ${t.here6 ? html`<dt>IPv6 این طرف / آن طرف</dt><dd><span class="ltr small">${t.here6.split("/")[0]} ⇄ ${t.there6}</span></dd>` : ""}
        ${under ? html`<dt>زیرساخت</dt><dd><span class="ltr small">${under.kind} ${under.name}</span> · <span class="ltr small">${under.here6 ? under.here6.split("/")[0] : ""}</span></dd>` : ""}
        <dt>MTU</dt><dd class="mono">${t.mtu || "-"}</dd>
        ${fwd.length ? html`<dt>پورت‌ها</dt><dd>${fwd.map((f) => html`<span class="chip mono">${f.ports}${f.to ? "→" + f.to : ""}</span> `)}</dd>` : ""}
      </dl>
      <div class="rates">
        <div class="rate"><small>${I.down(13)} دریافت</small><b>${fmtRate(r.rx)}</b><div class="total">${fmtBytes(t.rx)}</div></div>
        <div class="rate"><small>${I.up(13)} ارسال</small><b>${fmtRate(r.tx)}</b><div class="total">${fmtBytes(t.tx)}</div></div>
      </div>
      ${t.error ? html`<div class="err-line">${t.error}</div>` : ""}
      <div class="card-actions">
        <button class="btn sm" data-act="test" data-name="${t.name}">${I.gauge(15)} تست سرعت</button>
        <button class="btn sm icon" data-act="copy" data-text="${t.there4 || t.there6}" title="کپی IP داخلی مقصد">${I.copy(15)}</button>
        ${t.peer && peer(t.peer) ? html`<button class="btn sm icon" data-act="setup" data-name="${t.peer}" title="دستورهای سرور مقصد">${I.term(15)}</button>` : ""}
        <span class="spacer"></span>
        ${t.managed ? "" : html`<button class="btn sm danger icon" data-act="del-tunnel" data-name="${t.name}" title="حذف تانل">${I.trash(15)}</button>`}
      </div>
    </div>`;
  }

  // ---------- servers ----------
  function viewServers() {
    const ov = S.ov;
    const master = ov.master ? html`<div class="callout ${ov.master.paired ? "ok" : "warn"}">${I.server(16)} این سرور توسط <b>${ov.master.name}</b> (<span class="ltr">${ov.master.address}</span>) مدیریت می‌شود ${ov.master.paired ? "و متصل است." : "— هنوز به سرور اصلی نرسیده؛ سرویس مدام تلاش می‌کند."} تغییر تانل‌های آن فقط از پنل سرور اصلی ممکن است.</div>` : "";
    if (!ov.peers.length) return html`${master}<div class="empty">${I.server(30)}<b>هنوز سروری اضافه نشده</b>سرور خارج را با نام و IP اضافه کنید؛ پنل دستور یک‌خطی اتصال را برای همان سرور می‌سازد.<br><button class="btn primary" data-act="add-server">${I.plus(16)} افزودن سرور</button></div>`;
    return html`${master}<div class="grid cards" style="${master ? "margin-top:16px" : ""}">${ov.peers.map(serverCard)}</div>`;
  }

  function serverCard(p) {
    const state = !p.paired ? html`<span class="chip warn"><i></i>منتظر اتصال</span>` : p.in_sync ? html`<span class="chip ok"><i></i>متصل و همگام</span>` : html`<span class="chip warn"><i></i>عقب از تنظیمات</span>`;
    return html`<div class="card tunnel-card">
      <div class="tunnel-head">
        <div class="ico" style="color:var(--violet)">${I.server(22)}</div>
        <div><div class="title">${p.name}</div><div class="route ltr">${p.address}</div></div>
        <div class="chips">${state}</div>
      </div>
      <dl class="kv">
        <dt>تانل‌ها</dt><dd>${p.tunnels}</dd>
        <dt>نسخه</dt><dd class="mono">${p.version || "-"} ${p.arch ? "· " + p.arch : ""}</dd>
        <dt>آخرین ارتباط</dt><dd>${ago(p.last_seen)}</dd>
      </dl>
      ${!p.paired ? html`<div class="callout warn small">دستور اتصال را روی <b>${p.name}</b> اجرا کنید؛ بعد از آن هر تغییر خودکار روی آن اعمال می‌شود.</div>` : ""}
      <div class="card-actions">
        <button class="btn sm ${p.paired ? "" : "primary"}" data-act="setup" data-name="${p.name}">${I.term(15)} دستور اتصال</button>
        ${p.paired ? html`<button class="btn sm" data-act="bench" data-name="${p.name}">${I.gauge(15)} مقایسه‌ی سرعت</button>
        <button class="btn sm icon" data-act="sync" data-name="${p.name}" title="همگام‌سازی">${I.sync(15)}</button>
        <button class="btn sm icon" data-act="upgrade" data-name="${p.name}" title="ارتقا به نسخه‌ی همین سرور">${I.upgrade(15)}</button>` : ""}
        <button class="btn sm icon" data-act="wizard" data-peer="${p.name}" title="تانل جدید به این سرور">${I.plus(15)}</button>
        <span class="spacer"></span>
        <button class="btn sm danger icon" data-act="del-server" data-name="${p.name}" title="حذف سرور">${I.trash(15)}</button>
      </div>
    </div>`;
  }

  // ---------- ports ----------
  function viewPorts() {
    const fs = S.ov.forwards;
    const intro = html`<div class="callout" style="margin-bottom:16px">${I.ports(16)} هر پورت این سرور می‌تواند به یک تانل و یک سرور/لوکیشن متفاوت برود؛ مثلاً 443 به آلمان و 2053 به هلند (روی پورت 443 مقصد).</div>`;
    if (!fs.length) return html`${intro}<div class="empty">${I.ports(30)}<b>فورواردی تعریف نشده</b>ترافیک ورودی پورت‌های این سرور را از داخل تانل به سرور مقصد بفرستید.<br><button class="btn primary" data-act="add-forward">${I.plus(16)} فوروارد جدید</button></div>`;
    return html`${intro}<div class="card"><table class="table"><thead><tr><th>ورودی (این سرور)</th><th>پروتکل</th><th>تانل</th><th>مقصد</th><th></th></tr></thead><tbody>
      ${fs.map((f) => html`<tr>
        <td><span class="ltr"><b>${f.public}</b>:${f.ports}</span></td>
        <td><span class="chip">${f.proto === "both" ? "TCP + UDP" : f.proto.toUpperCase()}</span></td>
        <td><span class="chip violet">${f.tunnel}</span></td>
        <td>${f.peer || ""} <span class="ltr muted">${f.target}:${f.to || f.ports}</span></td>
        <td style="text-align:end"><button class="btn sm danger" data-act="del-forward" data-tunnel="${f.tunnel}" data-ports="${f.ports}">${I.trash(15)}</button></td></tr>`)}
    </tbody></table></div>`;
  }

  // ---------- speed ----------
  function viewSpeed() {
    const ts = visibleTunnels(), ps = S.ov.peers.filter((p) => p.paired);
    const sp = S.speed;
    if (!sp.tunnel && ts.length) sp.tunnel = ts[0].name;
    if (!sp.peer && ps.length) sp.peer = ps[0].name;
    const running = S.job && S.job.status === "running";
    return html`<div class="stack">
      <div class="grid two" style="grid-template-columns:1fr 1.35fr">
        <div class="card"><h2>${I.tunnel(18)} تست یک تانل</h2>
          ${ts.length ? html`<div class="field"><label>تانل</label><select class="input" data-bind="speed.tunnel">${ts.map((t) => html`<option value="${t.name}" ${sp.tunnel === t.name ? "selected" : ""}>${t.name} — ${t.mode || t.kind} → ${t.peer || t.far_ip}</option>`)}</select>
          <span class="help">پینگ، جیتر و افت بسته با ۱۰۰ بسته؛ سپس دانلود و آپلود هر کدام ۵ ثانیه.</span></div>
          <div class="row" style="margin-top:14px"><button class="btn primary" data-act="run-test" ${running ? "disabled" : ""}>${I.play(15)} شروع تست</button></div>`
          : html`<div class="muted">اول یک تانل بسازید.</div>`}
        </div>
        <div class="card"><h2>${I.gauge(18)} مقایسه‌ی همه‌ی انواع تانل <span class="hint">بهترین را پیدا کن</span></h2>
          ${ps.length ? html`<div class="field"><label>سرور مقصد</label><select class="input" data-bind="speed.peer">${ps.map((p) => html`<option value="${p.name}" ${sp.peer === p.name ? "selected" : ""}>${p.name} — ${p.address}</option>`)}</select></div>
          <div class="row" style="margin-top:12px">${BENCH.map(([id, label]) => html`<label class="check"><input type="checkbox" data-act="bench-only" value="${id}" ${sp.only.includes(id) ? "checked" : ""}>${label}</label>`)}</div>
          <div class="row" style="margin-top:14px"><button class="btn primary" data-act="run-bench" ${running ? "disabled" : ""}>${I.play(15)} شروع مقایسه</button><span class="muted small">تانل‌های موقت ساخته، اندازه‌گیری و پاک می‌شوند؛ تانل‌های فعلی دست نمی‌خورند.</span></div>`
          : html`<div class="muted">برای مقایسه، سرور مقصد باید متصل باشد.</div>`}
        </div>
      </div>
      ${S.job ? jobView(S.job) : ""}
    </div>`;
  }

  function jobView(j) {
    const results = j.results || [];
    const ok = results.filter((r) => !r.error);
    const maxBw = Math.max(1, ...ok.map((r) => Math.max(r.down_bps, r.up_bps)));
    const best = j.kind === "bench" ? ok.find((r) => r.name !== "direct") : null;
    const statusChipJ = { running: html`<span class="chip info"><i></i>در حال اجرا</span>`, done: html`<span class="chip ok"><i></i>تمام شد</span>`, failed: html`<span class="chip bad"><i></i>ناموفق</span>`, cancelled: html`<span class="chip"><i></i>لغو شد</span>` }[j.status];
    return html`<div class="card">
      <h2>${I.term(18)} ${j.kind === "bench" ? "مقایسه با " : "تست تانل "}${j.target} ${statusChipJ}
        <span class="hint">${j.status === "running" ? html`<button class="btn sm danger" data-act="cancel-job">توقف</button>` : ""}</span></h2>
      ${j.error ? html`<div class="callout bad" style="margin-bottom:12px">${j.error}</div>` : ""}
      ${results.length ? html`<table class="table" style="margin-bottom:14px"><thead><tr><th>تانل</th><th style="text-align:end">پینگ</th><th style="text-align:end">جیتر</th><th style="text-align:end">افت</th><th>دانلود</th><th>آپلود</th><th style="text-align:end">MTU</th><th></th></tr></thead><tbody>
        ${results.map((r) => r.error ? html`<tr><td><b>${resultTitle(r)}</b></td><td colspan="7" class="muted small ltr">${r.error}</td></tr>` : html`<tr class="${best && r.name === best.name ? "best" : ""}">
          <td><b>${best && r.name === best.name ? html`<span class="medal">★</span> ` : ""}${resultTitle(r)}</b></td>
          <td class="num">${ms(r.ping_ms)}</td><td class="num">${ms(r.jitter_ms)}</td><td class="num">${r.loss.toFixed(0)}%</td>
          <td><div class="row" style="flex-wrap:nowrap"><div class="bar down" style="flex:1"><b style="width:${((r.down_bps / maxBw) * 100).toFixed(1)}%"></b></div><span class="mono small">${r.down_bps ? fmtRate(r.down_bps) : "-"}</span></div></td>
          <td><div class="row" style="flex-wrap:nowrap"><div class="bar up" style="flex:1"><b style="width:${((r.up_bps / maxBw) * 100).toFixed(1)}%"></b></div><span class="mono small">${r.up_bps ? fmtRate(r.up_bps) : "-"}</span></div></td>
          <td class="num">${r.mtu || "-"}</td>
          <td>${j.kind === "bench" && r.mode ? html`<button class="btn sm" data-act="wizard" data-peer="${j.target}" data-mode="${r.mode}" data-port="${r.port || ""}">ساخت</button>` : ""}</td></tr>`)}
      </tbody></table>` : ""}
      ${best ? html`<div class="callout ok" style="margin-bottom:12px">★ سریع‌ترین گزینه تا <b>${j.target}</b>: <b>${resultTitle(best)}</b> — دانلود ${fmtRate(best.down_bps)}، پینگ ${ms(best.ping_ms)}</div>` : ""}
      <div class="console" id="console">${(j.log || []).map((l) => html`<div>${l}</div>`)}</div>
    </div>`;
  }

  // Persian titles for benchmark rows (the server sends English ones).
  const resultTitle = (r) => {
    const b = BENCH.find(([id]) => id === r.name);
    if (!b) return r.title;
    return r.name === "6to4-udp" && r.port ? `6to4 روی UDP ${r.port}` : b[1];
  };

  async function startJob(kind, target, only) {
    try {
      S.job = await api("POST", "jobs", { kind, target, only });
      location.hash = "#/speed";
      draw(); pollJob();
    } catch (e) { toast(e.message, "bad"); }
  }

  async function pollJob() {
    if (!S.job || S.job.status !== "running") return;
    try { S.job = await api("GET", "jobs/" + S.job.id); } catch { /* retry */ }
    if (S.route === "speed" && !S.modal) {
      draw();
      const c = $("#console"); if (c) c.scrollTop = c.scrollHeight;
    }
    if (S.job.status === "running") setTimeout(pollJob, 1000);
    else toast(S.job.status === "done" ? "تست تمام شد" : S.job.error || "تست متوقف شد", S.job.status === "done" ? "ok" : "bad");
  }

  // ---------- commands ----------
  function codeBlock(label, cmd, where) {
    return html`<div class="code-label">${label}${where ? html`<span class="chip where">روی ${where}</span>` : ""}</div>
      <div class="code"><button class="btn sm icon copy" data-act="copy" data-text="${cmd}" title="کپی">${I.copy(15)}</button>${cmd}</div>`;
  }

  function viewCommands() {
    const ov = S.ov;
    return html`<div class="stack">
      <div class="card"><h2>${I.upgrade(18)} نصب یک‌خطی روی سرور جدید (اوبونتو)</h2>
        <div class="muted small">نصب کامل با پنل (پورت تصادفی آزاد، رمز تصادفی). برای سرور مقصد بهتر است از دکمه‌ی «دستور اتصال» در صفحه‌ی سرورها استفاده کنید که همه‌ی تنظیمات را هم اعمال می‌کند.</div>
        ${codeBlock("نصب با پنل", ov.install, "")}
        ${codeBlock("نصب بدون پنل (سرور مقصد)", ov.install + " -s -- --no-panel", "")}
      </div>
      ${ov.commands.map((g) => html`<div class="card"><h2>${I.term(18)} ${g.title}</h2>${g.commands.map((c) => codeBlock(c.label, c.run, c.where))}</div>`)}
    </div>`;
  }

  // ---------- settings ----------
  function viewSettings() {
    const ov = S.ov;
    return html`<div class="grid two" style="grid-template-columns:1fr 1fr">
      <div class="card"><h2>${I.server(18)} این سرور</h2>
        <form data-form="node" class="stack">
          <div class="field"><label>نام سرور</label><input class="input ltr" name="name" value="${ov.node.name}"><span class="help">در نقشه و لینک‌های اتصال نمایش داده می‌شود.</span></div>
          <div class="field"><label>IP عمومی</label><input class="input ltr" name="address" value="${ov.node.address}"><span class="help">سرورهای مقصد با این IP به این سرور وصل می‌شوند.</span></div>
          <div><button class="btn primary" type="submit">ذخیره</button></div>
        </form>
      </div>
      <div class="card"><h2>${I.globe(18)} پورت پنل</h2>
        <form data-form="port" class="stack">
          <div class="field"><label>پورت</label><input class="input ltr" name="port" type="number" min="1024" max="65535" value="${ov.panel.port}"><span class="help">آدرس فعلی: <span class="ltr">${ov.panel.url}</span><br>بعد از تغییر، پنل روی پورت جدید بالا می‌آید و به آن منتقل می‌شوید.</span></div>
          <div><button class="btn primary" type="submit">تغییر پورت</button></div>
        </form>
      </div>
      <div class="card"><h2>${I.key(18)} رمز عبور</h2>
        <form data-form="password" class="stack">
          <div class="field"><label>رمز فعلی</label><input class="input ltr" name="current" type="password" autocomplete="current-password" required></div>
          <div class="field"><label>رمز جدید</label><input class="input ltr" name="next" type="password" autocomplete="new-password" minlength="8" required><span class="help">حداقل ۸ کاراکتر.</span></div>
          <div class="field"><label>تکرار رمز جدید</label><input class="input ltr" name="again" type="password" autocomplete="new-password" required></div>
          <div><button class="btn primary" type="submit">تغییر رمز</button></div>
        </form>
      </div>
      <div class="card"><h2>${I.cpu(18)} درباره</h2>
        <dl class="kv">
          <dt>نسخه</dt><dd class="mono">${ov.node.version} · ${ov.node.arch}</dd>
          <dt>کلید سرور</dt><dd class="mono">${ov.node.key}</dd>
          <dt>کاربر پنل</dt><dd class="mono">${ov.panel.user}</dd>
          <dt>روشن بودن سرور</dt><dd>${fmtUptime(ov.node.uptime_s)}</dd>
          <dt>BBR</dt><dd>${ov.node.bbr ? html`<span class="chip ok">فعال</span>` : html`<span class="chip warn">غیرفعال</span>`}</dd>
          <dt>کد منبع</dt><dd><a href="${ov.repo}" target="_blank" rel="noopener" class="ltr">${ov.repo.replace("https://", "")}</a></dd>
        </dl>
      </div>
    </div>`;
  }

  // ---------- modals ----------
  function drawModal() {
    const root = $("#modal-root");
    if (!S.modal) { root.innerHTML = ""; return; }
    root.innerHTML = part(html`<div class="overlay" data-act="close-overlay"><div class="modal" role="dialog" aria-modal="true">${S.modal.view()}</div></div>`);
  }
  const openModal = (view, opts = {}) => { S.modal = Object.assign({ view }, opts); drawModal(); };
  const closeModal = () => { S.modal = null; S.wiz = null; drawModal(); draw(); };
  const modalHead = (title, extra = "") => html`<header><h3>${title}</h3>${extra}<button class="btn sm ghost icon" data-act="close" style="margin-inline-start:${extra ? "0" : "auto"}">${I.x(16)}</button></header>`;

  function setupView(s, intro) {
    const where = `${s.peer} (${s.address})`;
    return html`${modalHead("دستور اتصال " + s.peer)}
      <div class="body">
        ${intro || (s.paired
          ? html`<div class="callout ok">این سرور متصل است و تغییرات خودکار روی آن اعمال می‌شود. اگر روزی در دسترس نبود، دستور زیر را روی <b>${where}</b> اجرا کنید تا همه‌ی تنظیمات یک‌جا اعمال شود.</div>`
          : html`<div class="callout warn">این سرور هنوز وصل نشده. یکی از دستورهای زیر را روی سرور <b>${where}</b> با دسترسی root اجرا کنید. بعد از آن، هر تغییری که اینجا بدهید خودکار روی آن هم اعمال می‌شود.</div>`)}
        ${s.install ? codeBlock("۱. نصب از همین سرور و اعمال تنظیمات (پیشنهادی)", s.install, s.peer) : ""}
        ${s.github ? codeBlock("۲. یا: نصب از گیت‌هاب و اعمال تنظیمات", s.github, s.peer) : ""}
        ${codeBlock(s.install ? "۳. اگر NeoTunnel از قبل روی آن نصب است" : "اعمال همه‌ی تنظیمات (همگام‌سازی دستی)", s.apply, s.peer)}
        <div class="muted small">لینک با کلید این سرور امضا شده است؛ سرور مقصد فقط لینک سرور اصلی خودش را قبول می‌کند.</div>
      </div>
      <footer><button class="btn primary" data-act="close">متوجه شدم</button></footer>`;
  }

  async function showSetup(name) {
    try { const s = await api("GET", `peers/${encodeURIComponent(name)}/setup`); openModal(() => setupView(s)); }
    catch (e) { toast(e.message, "bad"); }
  }

  // Report a change: problems here, then each peer (with its setup if unreachable).
  function reportChange(ch, okMsg) {
    const failed = (ch.peers || []).filter((p) => !p.ok);
    (ch.errors || []).forEach((e) => toast(e, "bad"));
    (ch.peers || []).filter((p) => p.ok).forEach((p) => toast(`${p.peer} به‌روز شد`));
    if (failed.length && failed[0].setup) {
      const p = failed[0];
      const notPaired = /not paired/.test(p.error || "");
      openModal(() => setupView(p.setup, html`<div class="callout ${notPaired ? "warn" : "bad"}">${okMsg ? html`<b>${okMsg}</b><br>` : ""}${notPaired
        ? html`سرور <b>${p.peer}</b> هنوز وصل نشده. دستور زیر را روی آن اجرا کنید تا تانل آن طرف هم ساخته شود.`
        : html`سرور <b>${p.peer}</b> در دسترس نبود (<span class="ltr small">${p.error}</span>). با اجرای دستور زیر روی آن، همه‌ی تغییرات یک‌جا اعمال می‌شود.`}</div>`));
      return;
    }
    if (okMsg) toast(okMsg);
  }

  // Tunnel wizard: destination → kind → port & forwarding.
  function wizardView() {
    const w = S.wiz, ov = S.ov;
    const steps = html`<div class="steps">${[1, 2, 3].map((i) => html`<i class="${w.step >= i ? "on" : ""}"></i>`)}</div>`;
    let body;
    if (w.step === 1) {
      body = html`<div class="field"><label>تانل به کجا برود؟</label>
        <div class="choices">
          ${ov.peers.map((p) => html`<button type="button" class="choice ${w.dest === "peer" && w.peer === p.name ? "on" : ""}" data-act="wiz-dest" data-dest="peer" data-name="${p.name}">
            <b>${I.server(16)} ${p.name}</b><span class="ltr">${p.address}</span><span>${p.paired ? "متصل" : "منتظر اتصال"}</span></button>`)}
          <button type="button" class="choice ${w.dest === "new" ? "on" : ""}" data-act="wiz-dest" data-dest="new"><b>${I.plus(16)} سرور جدید</b><span>سرور خارج که NeoTunnel روی آن نصب می‌شود</span></button>
          <button type="button" class="choice ${w.dest === "remote" ? "on" : ""}" data-act="wiz-dest" data-dest="remote"><b>${I.globe(16)} دستگاه دیگر</b><span>مثلاً میکروتیک؛ فقط این طرف ساخته می‌شود</span></button>
        </div></div>
        ${w.dest === "new" ? html`<div class="form-grid"><div class="field"><label>نام سرور</label><input class="input ltr" data-bind="wiz.newName" value="${w.newName}" placeholder="germany"><span class="help">حروف کوچک انگلیسی، عدد و خط تیره</span></div>
          <div class="field"><label>IP عمومی سرور</label><input class="input ltr" data-bind="wiz.newIP" value="${w.newIP}" placeholder="203.0.113.10"></div></div>` : ""}
        ${w.dest === "remote" ? html`<div class="field"><label>IP دستگاه مقابل</label><input class="input ltr" data-bind="wiz.remote" value="${w.remote}" placeholder="203.0.113.10"><span class="help">آن طرف را خودتان (مثلاً روی میکروتیک) با همان نوع تانل تنظیم کنید.</span></div>` : ""}`;
    } else if (w.step === 2) {
      body = html`<div class="field"><label>نوع تانل</label><div class="choices">
        ${ov.modes.map((m) => html`<button type="button" class="choice ${w.mode === m.name ? "on" : ""}" data-act="wiz-mode" data-mode="${m.name}">
          <b>${m.title} ${MODE_INFO[m.name] && MODE_INFO[m.name][1] ? html`<span class="chip ok">پیشنهادی</span>` : ""}</b>
          <span>${MODE_INFO[m.name] ? MODE_INFO[m.name][0] : ""}</span><span class="faint">لایه ${m.layer}</span></button>`)}
      </div></div>
      <div class="form-grid"><div class="field"><label>نام تانل (اختیاری)</label><input class="input ltr" data-bind="wiz.name" value="${w.name}" placeholder="t1" maxlength="8"><span class="help">خالی بگذارید تا خودکار انتخاب شود</span></div></div>`;
    } else {
      body = html`<div class="field"><label>پورت UDP تانل (اختیاری)</label><input class="input ltr" type="number" min="1" max="65535" data-bind="wiz.port" value="${w.port}" placeholder="مثلاً 3000">
          <span class="help">تانل داخل UDP روی این پورت اجرا می‌شود (FOU). وقتی مسیر پروتکل‌های 41/47 را می‌بندد مفید است، و هر تانل به هر سرور می‌تواند پورت خودش را داشته باشد. خالی = پروتکل خام (سریع‌ترین).</span></div>
        <div class="form-grid">
          <div class="field"><label>فوروارد پورت‌های این سرور (اختیاری)</label><input class="input ltr" data-bind="wiz.fwd" value="${w.fwd}" placeholder="443, 8443, 2053-2096"><span class="help">ترافیک این پورت‌ها از داخل تانل به مقصد می‌رود</span></div>
          <div class="field"><label>پورت مقصد (اختیاری)</label><input class="input ltr" type="number" data-bind="wiz.fwdTo" value="${w.fwdTo}" placeholder="همان پورت"><span class="help">مثلاً 2053 اینجا ← 443 آن طرف</span></div>
          <div class="field full"><label>پروتکل</label><div class="seg">${["both", "tcp", "udp"].map((p) => html`<button type="button" class="${w.proto === p ? "on" : ""}" data-act="wiz-proto" data-proto="${p}">${p === "both" ? "TCP + UDP" : p.toUpperCase()}</button>`)}</div></div>
        </div>`;
    }
    const canNext = wizCanNext(w);
    return html`${modalHead("تانل جدید", steps)}
      <div class="body">${body}</div>
      <footer>
        ${w.step > 1 ? html`<button class="btn ghost" data-act="wiz-back">قبلی</button>` : ""}
        <span class="spacer"></span>
        ${w.step < 3 ? html`<button class="btn primary" data-act="wiz-next" ${canNext ? "" : "disabled"}>بعدی</button>`
          : html`<button class="btn primary" data-act="wiz-create" ${w.busy ? "disabled" : ""}>${w.busy ? "در حال ساخت…" : html`${I.check(16)} ساخت تانل`}</button>`}
      </footer>`;
  }

  function wizCanNext(w) {
    if (w.step === 1) return !!((w.dest === "peer" && w.peer) || (w.dest === "new" && w.newName && w.newIP) || (w.dest === "remote" && w.remote));
    return w.step === 2 ? !!w.mode : true;
  }

  function openWizard(preset = {}) {
    const ov = S.ov;
    S.wiz = Object.assign({ step: 1, dest: ov.peers.length ? "peer" : "new", peer: ov.peers.length ? ov.peers[0].name : "", newName: "", newIP: "", remote: "", mode: "6to4", name: "", port: "", fwd: "", fwdTo: "", proto: "both", busy: false }, preset);
    if (preset.peer) { S.wiz.dest = "peer"; S.wiz.step = preset.mode ? 3 : 2; }
    openModal(wizardView);
  }

  async function createTunnel() {
    const w = S.wiz;
    w.busy = true; drawModal();
    try {
      let peerName = w.peer;
      if (w.dest === "new") {
        await api("POST", "peers", { name: w.newName.trim(), address: w.newIP.trim() });
        peerName = w.newName.trim();
      }
      const req = { mode: w.mode, name: w.name.trim(), port: +w.port || 0, forward: w.fwd.trim(), forward_to: +w.fwdTo || 0, proto: w.proto };
      if (w.dest === "remote") req.remote = w.remote.trim(); else req.peer = peerName;
      const ch = await api("POST", "tunnels", req);
      S.modal = null; S.wiz = null;
      await refresh();
      location.hash = "#/tunnels";
      reportChange(ch, "تانل ساخته شد");
    } catch (e) {
      w.busy = false; drawModal(); toast(e.message, "bad");
    }
  }

  function addServerView() {
    const f = S.form;
    return html`${modalHead("افزودن سرور مقصد")}
      <div class="body">
        <div class="muted">سرور خارج را اضافه کنید؛ پنل یک دستور یک‌خطی می‌سازد که NeoTunnel را روی آن نصب و به این سرور وصل می‌کند.</div>
        <div class="form-grid">
          <div class="field"><label>نام</label><input class="input ltr" data-bind="form.name" value="${f.name || ""}" placeholder="germany"></div>
          <div class="field"><label>IP عمومی</label><input class="input ltr" data-bind="form.address" value="${f.address || ""}" placeholder="203.0.113.10"></div>
        </div>
      </div>
      <footer><button class="btn ghost" data-act="close">انصراف</button><button class="btn primary" data-act="add-server-go">${I.plus(16)} افزودن</button></footer>`;
  }

  function addForwardView() {
    const f = S.form, ts = visibleTunnels().filter((t) => !t.managed);
    if (!f.tunnel && ts.length) f.tunnel = ts[0].name;
    return html`${modalHead("فوروارد پورت جدید")}
      <div class="body">
        ${ts.length ? html`<div class="form-grid">
          <div class="field full"><label>از طریق تانل</label><select class="input" data-bind="form.tunnel">${ts.map((t) => html`<option value="${t.name}" ${f.tunnel === t.name ? "selected" : ""}>${t.name} → ${t.peer || t.far_ip} (${t.there4 || t.there6})</option>`)}</select></div>
          <div class="field"><label>پورت(های) این سرور</label><input class="input ltr" data-bind="form.ports" value="${f.ports || ""}" placeholder="443, 2053-2096"></div>
          <div class="field"><label>پورت مقصد (اختیاری)</label><input class="input ltr" type="number" data-bind="form.to" value="${f.to || ""}" placeholder="همان پورت"></div>
          <div class="field full"><label>پروتکل</label><div class="seg">${["both", "tcp", "udp"].map((p) => html`<button type="button" class="${(f.proto || "both") === p ? "on" : ""}" data-act="form-proto" data-proto="${p}">${p === "both" ? "TCP + UDP" : p.toUpperCase()}</button>`)}</div></div>
        </div>` : html`<div class="muted">اول یک تانل بسازید.</div>`}
      </div>
      <footer><button class="btn ghost" data-act="close">انصراف</button><button class="btn primary" data-act="add-forward-go" ${ts.length ? "" : "disabled"}>${I.plus(16)} افزودن</button></footer>`;
  }

  function confirmView(title, text, act, data) {
    return html`${modalHead(title)}<div class="body"><div>${text}</div></div>
      <footer><button class="btn ghost" data-act="close">انصراف</button><button class="btn danger" data-act="${act}" ${raw(Object.entries(data).map(([k, v]) => `data-${k}="${esc(v)}"`).join(" "))}>${I.trash(16)} حذف</button></footer>`;
  }

  // ---------- events ----------
  const setPath = (path, value) => {
    const [root, key] = path.split(".");
    const obj = { wiz: S.wiz, form: S.form, speed: S.speed }[root];
    if (obj) obj[key] = value;
  };

  document.addEventListener("input", (e) => {
    const b = e.target.dataset.bind;
    if (!b) return;
    setPath(b, e.target.value);
    // Re-evaluate the Next button without redrawing the inputs.
    const next = $('[data-act="wiz-next"]');
    if (next && S.wiz) next.disabled = !wizCanNext(S.wiz);
  });
  document.addEventListener("change", (e) => {
    if (e.target.dataset.bind) setPath(e.target.dataset.bind, e.target.value);
    if (e.target.dataset.act === "bench-only") {
      const v = e.target.value, on = e.target.checked, only = S.speed.only.filter((x) => x !== v);
      S.speed.only = on ? only.concat(v) : only;
    }
  });

  document.addEventListener("submit", async (e) => {
    const form = e.target.dataset.form;
    if (!form) return;
    e.preventDefault();
    const d = Object.fromEntries(new FormData(e.target));
    try {
      if (form === "login") {
        await api("POST", "login", { user: d.user, password: d.password });
        S.authed = true; await refresh(); return;
      }
      if (form === "node") { await api("POST", "settings/node", { name: d.name.trim(), address: d.address.trim() }); toast("ذخیره شد"); }
      if (form === "password") {
        if (d.next !== d.again) throw new Error("تکرار رمز با رمز جدید یکی نیست");
        await api("POST", "settings/password", { current: d.current, next: d.next });
        e.target.reset(); toast("رمز عوض شد");
      }
      if (form === "port") {
        const r = await api("POST", "settings/port", { port: +d.port });
        toast("پنل در حال انتقال به پورت جدید…");
        setTimeout(() => { location.href = r.url; }, 3500);
        return;
      }
      S.settingsDrawn = false; await refresh();
    } catch (err) { toast(err.message, "bad"); }
  });

  const actions = {
    theme() { S.theme = S.theme === "dark" ? "light" : "dark"; store("nt-theme", S.theme); document.documentElement.dataset.theme = S.theme; draw(); },
    async logout() { await api("POST", "logout").catch(() => {}); S.authed = false; S.ov = null; draw(); },
    copy(el) { copy(el.dataset.text); },
    close() { closeModal(); },
    "close-overlay"(el, e) { if (e.target === el) closeModal(); },
    wizard(el) { openWizard(el.dataset.peer ? { peer: el.dataset.peer, mode: el.dataset.mode || "6to4", port: el.dataset.port || "" } : {}); },
    "wiz-dest"(el) { Object.assign(S.wiz, { dest: el.dataset.dest, peer: el.dataset.name || S.wiz.peer }); drawModal(); },
    "wiz-mode"(el) { S.wiz.mode = el.dataset.mode; drawModal(); },
    "wiz-proto"(el) { S.wiz.proto = el.dataset.proto; drawModal(); },
    "wiz-next"() { S.wiz.step++; drawModal(); },
    "wiz-back"() { S.wiz.step--; drawModal(); },
    "wiz-create"() { createTunnel(); },
    setup(el) { showSetup(el.dataset.name); },
    "add-server"() { S.form = {}; openModal(addServerView); },
    async "add-server-go"() {
      try {
        const s = await api("POST", "peers", { name: (S.form.name || "").trim(), address: (S.form.address || "").trim() });
        await refresh(); openModal(() => setupView(s)); location.hash = "#/servers";
      } catch (e) { toast(e.message, "bad"); }
    },
    "add-forward"() { S.form = { proto: "both" }; openModal(addForwardView); },
    "form-proto"(el) { S.form.proto = el.dataset.proto; drawModal(); },
    async "add-forward-go"() {
      const f = S.form;
      try {
        const ch = await api("POST", "forwards", { tunnel: f.tunnel, ports: (f.ports || "").trim(), proto: f.proto || "both", to: +f.to || 0 });
        closeModal(); await refresh(); reportChange(ch, "فوروارد اضافه شد");
      } catch (e) { toast(e.message, "bad"); }
    },
    "del-forward"(el) { openModal(() => confirmView("حذف فوروارد", html`فوروارد پورت <b class="ltr">${el.dataset.ports}</b> روی تانل <b>${el.dataset.tunnel}</b> حذف شود؟`, "del-forward-go", { tunnel: el.dataset.tunnel, ports: el.dataset.ports })); },
    async "del-forward-go"(el) {
      try { const ch = await api("POST", "forwards/remove", { tunnel: el.dataset.tunnel, ports: el.dataset.ports }); closeModal(); await refresh(); reportChange(ch, "حذف شد"); }
      catch (e) { toast(e.message, "bad"); }
    },
    "del-tunnel"(el) { openModal(() => confirmView("حذف تانل", html`تانل <b>${el.dataset.name}</b> (و تانل زیرساخت و فورواردهای آن) در هر دو سرور حذف شود؟`, "del-tunnel-go", { name: el.dataset.name })); },
    async "del-tunnel-go"(el) {
      try { const ch = await api("DELETE", "tunnels/" + encodeURIComponent(el.dataset.name)); closeModal(); await refresh(); reportChange(ch, "تانل حذف شد"); }
      catch (e) { toast(e.message, "bad"); }
    },
    "del-server"(el) { openModal(() => confirmView("حذف سرور", html`سرور <b>${el.dataset.name}</b> و تانل‌های این سرور به آن حذف شوند؟ (روی خود آن سرور دست نمی‌خورد)`, "del-server-go", { name: el.dataset.name })); },
    async "del-server-go"(el) {
      try { const ch = await api("DELETE", "peers/" + encodeURIComponent(el.dataset.name)); closeModal(); await refresh(); reportChange(ch, "سرور حذف شد"); }
      catch (e) { toast(e.message, "bad"); }
    },
    async sync(el) {
      try { const ch = await api("POST", `peers/${encodeURIComponent(el.dataset.name)}/sync`); await refresh(); reportChange(ch); }
      catch (e) { toast(e.message, "bad"); }
    },
    async "sync-all"() {
      try { const ch = await api("POST", "sync"); await refresh(); reportChange(ch, (ch.peers || []).length ? "" : "سروری برای همگام‌سازی نیست"); }
      catch (e) { toast(e.message, "bad"); }
    },
    async upgrade(el) {
      toast(`ارسال نسخه‌ی جدید به ${el.dataset.name}…`);
      try { const r = await api("POST", `peers/${encodeURIComponent(el.dataset.name)}/upgrade`); toast(`${el.dataset.name} به ${r.version} ارتقا یافت`); await refresh(); }
      catch (e) { toast(e.message, "bad"); }
    },
    test(el) { startJob("test", el.dataset.name); },
    bench(el) { S.speed.peer = el.dataset.name; startJob("bench", el.dataset.name, S.speed.only); },
    "run-test"() { startJob("test", S.speed.tunnel); },
    "run-bench"() { startJob("bench", S.speed.peer, S.speed.only); },
    async "cancel-job"() { if (S.job) await api("POST", `jobs/${S.job.id}/cancel`).catch(() => {}); },
  };

  document.addEventListener("click", (e) => {
    const el = e.target.closest("[data-act]");
    if (!el || el.type === "checkbox") return;
    const fn = actions[el.dataset.act];
    if (fn) { if (el.tagName === "A") e.preventDefault(); fn(el, e); }
  });
  document.addEventListener("keydown", (e) => { if (e.key === "Escape" && S.modal) closeModal(); });

  window.addEventListener("hashchange", () => {
    S.route = location.hash.slice(2) || "dashboard";
    S.settingsDrawn = false;
    draw(); window.scrollTo(0, 0);
  });

  // ---------- boot ----------
  async function boot() {
    await refresh();
    if (S.authed) {
      try {
        const jobs = await api("GET", "jobs");
        if (jobs.length) { S.job = jobs[0]; pollJob(); }
      } catch { /* none */ }
      draw();
    }
    setInterval(() => { if (S.authed && !document.hidden) refresh(); }, 3000);
  }
  boot();
})();
