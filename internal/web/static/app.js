"use strict";
// NeoTunnel panel. No framework: views are template strings with escaping,
// events are delegated through data-act / data-bind attributes, and each
// render is patched into the page in place (morph), so only what changed is
// touched: hover, focus, scroll and running transitions survive the
// 3-second refresh.
(() => {
  // ---------- templating ----------
  const RAW = Symbol("raw");
  const raw = (s) => ({ [RAW]: true, s: String(s) });
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  const part = (v) => (v == null || v === false ? "" : Array.isArray(v) ? v.map(part).join("") : v[RAW] ? v.s : esc(v));
  const html = (strs, ...vals) => raw(strs.reduce((out, s, i) => out + s + (i < vals.length ? part(vals[i]) : ""), ""));
  const $ = (s, el = document) => el.querySelector(s);
  const $$ = (s, el = document) => [...el.querySelectorAll(s)];
  const reduceMotion = window.matchMedia && matchMedia("(prefers-reduced-motion: reduce)").matches;

  // ---------- morph: patch the DOM toward new markup ----------
  // Nodes are matched by position, or by id / data-key when they have one;
  // a node whose tag changed is replaced. Form fields keep what the user
  // typed unless the markup itself changed their value.
  const tpl = document.createElement("template");
  function morph(target, markup) {
    tpl.innerHTML = markup;
    morphChildren(target, tpl.content);
  }
  const keyOf = (n) => (n.nodeType === 1 ? n.id || n.getAttribute("data-key") || "" : "");
  function morphChildren(from, to) {
    const want = [...to.childNodes];
    let cur = from.firstChild;
    for (const next of want) {
      const k = keyOf(next);
      if (k && cur && keyOf(cur) !== k) {
        // Look ahead for the keyed node and move it here.
        let m = cur.nextSibling;
        while (m && keyOf(m) !== k) m = m.nextSibling;
        if (m) from.insertBefore(m, cur), (cur = m);
      }
      if (cur && same(cur, next)) {
        morphNode(cur, next);
        cur = cur.nextSibling;
      } else {
        from.insertBefore(next.cloneNode(true), cur);
      }
    }
    while (cur) { const n = cur.nextSibling; from.removeChild(cur); cur = n; }
  }
  const same = (a, b) => a.nodeType === b.nodeType && a.nodeName === b.nodeName && keyOf(a) === keyOf(b);
  function morphNode(a, b) {
    if (a.nodeType !== 1) { if (a.nodeValue !== b.nodeValue) a.nodeValue = b.nodeValue; return; }
    for (const { name, value } of [...b.attributes]) {
      if (a.getAttribute(name) === value) continue;
      a.setAttribute(name, value);
      if (name === "value" && "value" in a && a !== document.activeElement) a.value = value;
      if (name === "checked") a.checked = true;
      if (name === "open" && a.tagName === "DETAILS") a.open = true;
    }
    for (const { name } of [...a.attributes]) {
      if (b.hasAttribute(name)) continue;
      a.removeAttribute(name);
      if (name === "checked") a.checked = false;
      if (name === "selected") a.selected = false;
    }
    if (a.tagName === "SELECT") {
      morphChildren(a, b);
      const sel = [...b.options].find((o) => o.hasAttribute("selected"));
      if (sel && a !== document.activeElement) a.value = sel.value;
      return;
    }
    if (a.tagName === "TEXTAREA") { if (a !== document.activeElement && a.value !== b.value) a.value = b.value; return; }
    morphChildren(a, b);
  }

  // ---------- icons (stroke, 24px grid) ----------
  const svg = (d, size = 18) => raw(`<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${d}</svg>`);
  const I = {
    home: (s) => svg('<circle cx="12" cy="12" r="3"/><circle cx="12" cy="12" r="8" stroke-dasharray="2 3"/><path d="M4 12H2M22 12h-2"/>', s),
    tunnel: (s) => svg('<circle cx="5" cy="12" r="2.5"/><circle cx="19" cy="12" r="2.5"/><path d="M7.5 12c2-4 7-4 9 0"/><path d="M7.5 12c2 4 7 4 9 0"/>', s),
    server: (s) => svg('<rect x="3.5" y="4" width="17" height="6.5" rx="2"/><rect x="3.5" y="13.5" width="17" height="6.5" rx="2"/><path d="M7 7.25h.01M7 16.75h.01M11 7.25h6M11 16.75h6"/>', s),
    ports: (s) => svg('<path d="M4 7h11"/><path d="m12 4 3 3-3 3"/><path d="M20 17H9"/><path d="m12 14-3 3 3 3"/>', s),
    gauge: (s) => svg('<path d="M4.5 17a8.5 8.5 0 1 1 15 0"/><path d="m12 13 4-5"/><circle cx="12" cy="13" r="1.4"/>', s),
    term: (s) => svg('<rect x="3" y="4" width="18" height="16" rx="2.5"/><path d="m7 9 3 3-3 3M12.5 15H17"/>', s),
    gear: (s) => svg('<path d="M4 6h10M18 6h2M4 12h4M12 12h8M4 18h12"/><circle cx="16" cy="6" r="2"/><circle cx="10" cy="12" r="2"/><circle cx="18" cy="18" r="2"/>', s),
    plus: (s) => svg('<path d="M12 5v14M5 12h14"/>', s),
    trash: (s) => svg('<path d="M4 7h16M10 11v6M14 11v6M6 7l1 12a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2l1-12M9 7V4h6v3"/>', s),
    copy: (s) => svg('<rect x="8" y="8" width="12" height="12" rx="2.5"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2"/>', s),
    play: (s) => svg('<path d="M7 5v14l11-7z"/>', s),
    sync: (s) => svg('<path d="M20 11a8 8 0 0 0-14.3-4.9L4 8"/><path d="M4 4v4h4"/><path d="M4 13a8 8 0 0 0 14.3 4.9L20 16"/><path d="M20 20v-4h-4"/>', s),
    moon: (s) => svg('<path d="M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z"/>', s),
    sun: (s) => svg('<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M2 12h2M20 12h2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>', s),
    out: (s) => svg('<path d="M14 4h4a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-4"/><path d="M9 8l-4 4 4 4M5 12h11"/>', s),
    check: (s) => svg('<path d="m5 12.5 4.5 4.5L19 7.5"/>', s),
    x: (s) => svg('<path d="M6 6l12 12M18 6 6 18"/>', s),
    alert: (s) => svg('<path d="M12 3 2 20h20z"/><path d="M12 10v4M12 17h.01"/>', s),
    lock: (s) => svg('<rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/>', s),
    mask: (s) => svg('<path d="M3 8c3-2 15-2 18 0-1 7-4 10-9 10S4 15 3 8z"/><path d="M8 11h2M14 11h2"/>', s),
    chev: (s) => svg('<path d="m6 9 6 6 6-6"/>', s),
    ruler: (s) => svg('<path d="M3 17 17 3l4 4L7 21z"/><path d="m7 13 2 2M10 10l2 2M13 7l2 2"/>', s),
    upgrade: (s) => svg('<path d="M12 16V4M7 9l5-5 5 5"/><path d="M4 16v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3"/>', s),
    power: (s) => svg('<path d="M12 3v8"/><path d="M6.3 6.6a8 8 0 1 0 11.4 0"/>', s),
    download: (s) => svg('<path d="M12 4v12M7 11l5 5 5-5"/><path d="M4 20h16"/>', s),
    globe: (s) => svg('<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c3 3.5 3 14.5 0 18M12 3c-3 3.5-3 14.5 0 18"/>', s),
  };

  // ---------- formatting ----------
  const fmtRate = (bps) => {
    if (!bps || bps < 1) return "0 bps";
    for (const [n, d] of [["Gbps", 1e9], ["Mbps", 1e6], ["Kbps", 1e3]]) if (bps >= d) return `${(bps / d).toFixed(bps / d >= 100 ? 0 : 1)} ${n}`;
    return `${Math.round(bps)} bps`;
  };
  const splitRate = (bps) => { const [v, u] = fmtRate(bps).split(" "); return [v, u]; };
  const fmtBytes = (n) => {
    if (!n) return "0 B";
    const u = ["B", "KB", "MB", "GB", "TB", "PB"];
    const i = Math.min(u.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
    return `${(n / 1024 ** i).toFixed(i ? (n / 1024 ** i >= 100 ? 0 : 1) : 0)} ${u[i]}`;
  };
  const splitBytes = (n) => fmtBytes(n).split(" ");
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
  const ms = (v) => (v || v === 0 ? `${v.toFixed(v < 10 ? 1 : 0)} ms` : "-");
  const greeting = () => {
    const h = new Date().getHours();
    return h < 5 ? "شب بخیر" : h < 12 ? "صبح بخیر" : h < 16 ? "ظهر بخیر" : h < 20 ? "عصر بخیر" : "شب بخیر";
  };

  // ---------- protocols: what each tunnel kind is good for ----------
  const PROTO = {
    "6to4": { t: "6to4", d: "دو لایه‌ی سبک در کرنل: SIT یک شبکه‌ی IPv6 خصوصی بین دو سرور می‌سازد و IPIP6 ترافیک IPv4 را روی آن می‌برد. بهترین ترکیب سرعت و پایداری در بیشتر مسیرها.", layer: 3, carry: "IPv4", over: 60, crypt: "ندارد (IPsec اختیاری)", mt: "بله، 6to4 + IPIPv6", best: "پیشنهاد پیش‌فرض ایران ⇄ خارج", rec: true },
    "6to4-gre": { t: "6to4 + GRE6", d: "مثل 6to4، ولی لایه‌ی دوم GRE روی IPv6 است و هم IPv4 و هم IPv6 را می‌برد.", layer: 3, carry: "IPv4 و IPv6", over: 68, crypt: "ندارد (IPsec اختیاری)", mt: "بله، 6to4 + GREv6", best: "وقتی IPv6 هم باید از تانل رد شود" },
    ipip: { t: "IPIP", d: "ساده‌ترین تانل IPv4: فقط یک هدر ۲۰ بایتی. کمترین سربار و پردازش.", layer: 3, carry: "IPv4", over: 20, crypt: "ندارد (IPsec اختیاری)", mt: "بله، IPIP", best: "بیشترین سرعت روی مسیر تمیز" },
    gre: { t: "GRE", d: "تانل استاندارد GRE با کلید؛ با میکروتیک و بیشتر روترها سازگار است و هر دو نسخه‌ی IP را می‌برد.", layer: 3, carry: "IPv4 و IPv6", over: 28, crypt: "ندارد (IPsec اختیاری)", mt: "بله، GRE", best: "اتصال به روتر یا میکروتیک" },
    sit: { t: "SIT", d: "فقط IPv6 روی IPv4. پایه‌ی 6to4؛ تنها وقتی مفید است که فقط IPv6 لازم باشد.", layer: 3, carry: "IPv6", over: 20, crypt: "ندارد (IPsec اختیاری)", mt: "بله، 6to4", best: "شبکه‌ی IPv6 بین دو سرور" },
    wireguard: { t: "WireGuard", d: "تانل رمزنگاری‌شده در خود کرنل (ChaCha20-Poly1305). کلیدها از رمز مشترک تانل ساخته می‌شوند؛ پشت NAT هم کار می‌کند.", layer: 3, carry: "IPv4 و IPv6", over: 60, crypt: "دارد، در کرنل", mt: "بله، RouterOS 7", best: "رمزنگاری سریع، اتصال به میکروتیک", udp: true },
    obfs: { t: "NeoObfs", d: "لایه‌ی رمزنگاری و مبهم‌سازی NeoTunnel: هر بسته داخل UDP با XChaCha20-Poly1305 رمز می‌شود و هیچ بایت ثابتی ندارد، پس شبیه نویز تصادفی است. اندازه‌ی بسته‌ها هم با padding تغییر می‌کند.", layer: 3, carry: "IPv4 و IPv6", over: 82, crypt: "دارد، XChaCha20", mt: "خیر؛ دو طرف NeoTunnel", best: "مسیرهایی که پروتکل‌ها را شناسایی و فیلتر می‌کنند", udp: true },
    tcp: { t: "TCP", d: "بسته‌های IP پشت سر هم داخل یک اتصال TCP پایدار بین دو سرور می‌روند (در فضای کاربر، با TCP_NODELAY و اتصال دوباره‌ی خودکار). هر دو سر روی پورت گوش می‌دهند، پس اگر TCP فقط در یک جهت باز باشد هم وصل می‌شود. رمزنگاری اختیاری است و کلیدها از رمز مشترک تانل ساخته می‌شوند. هشدار: TCP داخل TCP در مسیرِ با افت بسته خیلی کند می‌شود، چون هر بسته‌ی گم‌شده کل جریان را نگه می‌دارد؛ فقط وقتی سراغش بروید که نه UDP رد می‌شود و نه پروتکل 41 و 47.", layer: 3, carry: "IPv4 و IPv6", over: 54, crypt: "اختیاری، AES-GCM یا ChaCha20 روی جریان", mt: "خیر؛ دو طرف NeoTunnel", best: "آخرین راه: وقتی فقط TCP بین دو سرور رد می‌شود", tcp: true },
    gretap: { t: "GRETAP", d: "لایه ۲ (Ethernet) روی GRE؛ دو سر مثل یک سوییچ مشترک رفتار می‌کنند.", layer: 2, carry: "Ethernet", over: 42, crypt: "ندارد (IPsec اختیاری)", mt: "شبیه EoIP، ولی با آن سازگار نیست", best: "بریج کردن دو شبکه" },
    vxlan: { t: "VXLAN", d: "لایه ۲ روی UDP؛ استاندارد شبکه‌های مجازی دیتاسنتر.", layer: 2, carry: "Ethernet", over: 50, crypt: "ندارد (IPsec اختیاری)", mt: "بله، RouterOS 7", best: "لایه ۲ روی مسیری که فقط UDP رد می‌شود", udp: true },
    geneve: { t: "GENEVE", d: "نسل جدیدتر VXLAN با هدر انعطاف‌پذیر.", layer: 2, carry: "Ethernet", over: 50, crypt: "ندارد (IPsec اختیاری)", mt: "خیر", best: "لایه ۲ بین سرورهای لینوکسی", udp: true },
    l2tpv3: { t: "L2TPv3", d: "شبه‌سیم Ethernet استاندارد با شناسه‌های ایستا، روی IP (پروتکل 115) یا روی UDP اگر پورت بدهید.", layer: 2, carry: "Ethernet", over: 38, crypt: "ندارد (IPsec اختیاری)", mt: "روترهایی که L2TPv3 ایستا دارند", best: "لایه ۲ سازگار با تجهیزات شبکه" },
    eoip: { t: "EoIP", d: "تانل لایه ۲ میکروتیک (Ethernet over IP) در لینوکس: فریم‌های Ethernet داخل GRE با شناسه‌ی تانل می‌روند؛ روی IPv6 همان EoIPv6 است. اگر مسیر پروتکل 47 را ببندد، با یک پورت داخل UDP اجرا می‌شود. در فضای کاربری NeoTunnel اجرا می‌شود و ماژول کرنلی نمی‌خواهد.", layer: 2, carry: "Ethernet", over: 42, crypt: "ندارد (IPsec اختیاری)", mt: "بله، EoIP و EoIPv6 (با شناسه‌ی تانل یکسان)", best: "بریج لایه ۲ بین دو سرور، یا اتصال به میکروتیک" },
  };
  const MODES_FWD = {
    nat: ["NAT", "سرویس مقصد آدرس داخلی تانل را می‌بیند. ساده و سازگار با همه‌چیز."],
    transparent: ["IP واقعی کاربر", "سرویس مقصد IP واقعی کاربر را می‌بیند، برای TCP و UDP و بدون هیچ تنظیمی در برنامه. پاسخ‌ها خودکار از همان تانل برمی‌گردند؛ سرور مقصد باید NeoTunnel داشته باشد."],
    proxy: ["PROXY protocol", "یک رله‌ی سبک، هدر PROXY (نسخه‌ی ۱ یا ۲) با IP و پورت کاربر را اول اتصال می‌فرستد. سرویس مقصد (nginx، HAProxy، Xray) باید proxy_protocol را بپذیرد. UDP فقط با نسخه‌ی ۲."],
  };
  const BENCH = [
    ["direct", "مسیر مستقیم"], ["6to4", "6to4"], ["6to4-udp", "6to4 روی UDP"], ["6to4-gre", "6to4 + GRE6"],
    ["ipip", "IPIP"], ["gre", "GRE"], ["gretap", "GRETAP"], ["vxlan", "VXLAN"], ["geneve", "GENEVE"],
    ["wireguard", "WireGuard"], ["l2tpv3", "L2TPv3"], ["l2tpv3-udp", "L2TPv3 روی UDP"], ["tcp", "TCP"],
  ];
  const kindTitle = (t) => { const p = PROTO[t.mode] || PROTO[t.kind]; return p ? p.t : (t.mode || t.kind).toUpperCase(); };
  const cipherTitle = (c) => ({ "aes-gcm-128": "AES-128-GCM", "aes-gcm-256": "AES-256-GCM", "chacha20-poly1305": "ChaCha20", wireguard: "WireGuard", "xchacha20-poly1305": "XChaCha20" }[c] || c);

  // ---------- state ----------
  const store = (k, v) => { try { if (v !== undefined) localStorage.setItem(k, v); return localStorage.getItem(k); } catch { return null; } };
  const S = {
    authed: true, ov: null, rates: {}, live: true, traffic: {}, system: null,
    route: location.hash.slice(2) || "dashboard",
    theme: store("nt-theme") || "dark",
    modal: null, wiz: null, form: {}, drawer: null,
    open: null, tset: {}, edit: false, fwdEdit: {},
    sec: store("nt-settings") || "node", defs: null,
    jobs: [], job: null, pv: {}, upgrading: {}, prates: {}, dtab: {}, speed: { tunnel: "", peer: "", only: BENCH.map((b) => b[0]) }, mtu: {},
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
    el.setAttribute("role", kind === "ok" ? "status" : "alert");
    el.innerHTML = part(html`${kind === "ok" ? I.check() : I.alert()}<div>${msg}</div>`);
    $("#toasts").appendChild(el);
    setTimeout(() => { el.classList.add("out"); setTimeout(() => el.remove(), 180); }, kind === "ok" ? 3500 : 8000);
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
        const pf = Object.fromEntries((S.ov.forwards || []).map((f) => [fwdKey(f), f]));
        for (const f of ov.forwards || []) {
          const p = pf[fwdKey(f)];
          if (p && dt > 0 && f.rx >= p.rx && f.tx >= p.tx) S.prates[fwdKey(f)] = { rx: ((f.rx - p.rx) * 8) / dt, tx: ((f.tx - p.tx) * 8) / dt };
        }
      }
      S.ov = ov; S.live = true; S.authed = true;
    } catch {
      S.live = false;
    }
    if (S.modal && !S.modal.passive) return;
    if (S.edit) patchLive(); else draw();
  }

  async function refreshTraffic() {
    try { S.traffic = (await api("GET", "traffic")) || {}; } catch { /* keep */ }
  }

  const visible = () => (S.ov ? S.ov.tunnels.filter((t) => !t.carrier) : []);
  const tunnel = (name) => S.ov && S.ov.tunnels.find((t) => t.name === name);
  const rate = (name) => S.rates[name] || { rx: 0, tx: 0 };
  const fwdKey = (f) => f.tunnel + "|" + f.ports;
  const prate = (f) => S.prates[fwdKey(f)] || { rx: 0, tx: 0 };
  const total = () => visible().reduce((a, t) => ({ rx: a.rx + rate(t.name).rx, tx: a.tx + rate(t.name).tx }), { rx: 0, tx: 0 });
  const peer = (name) => S.ov && S.ov.peers.find((p) => p.name === name);
  const status = (t) => {
    if (t.limits && t.limits.disabled) return ["off", "خاموش"];
    if (t.blocked) return ["warn", "حجم ماهانه تمام شد"];
    if (t.error) return ["bad", "خطا"];
    const p = t.peer && peer(t.peer);
    if (p && !p.paired) return ["warn", "منتظر سرور مقصد"];
    return t.up ? ["ok", "فعال"] : ["bad", "قطع"];
  };
  const stateEl = (t) => { const [c, l] = status(t); return html`<span class="state"><i class="dot ${c}"></i>${l}</span>`; };
  // A chip that says whether traffic really passes, from the daemon's probes.
  function healthTag(t) {
    const h = t.health;
    if (!h || h.probes < 3) return "";
    if (!h.passes) return html`<span class="tag warn">${I.alert(11)} ترافیک رد نمی‌شود</span>`;
    const cls = h.loss >= 20 ? "warn" : "";
    return html`<span class="tag ${cls}">${ms(h.ping_ms)}${h.loss >= 1 ? ` · افت ${h.loss.toFixed(0)}٪` : ""}</span>`;
  }
  const sumToday = () => visible().reduce((a, t) => a + t.today.rx + t.today.tx, 0);
  const sumMonth = () => visible().reduce((a, t) => a + t.month.rx + t.month.tx, 0);

  // Live values, patched in place while a form is open.
  function liveText(key) {
    const [kind, name] = key.split(":");
    const r = rate(name), t = tunnel(name);
    switch (kind) {
      case "rx": return fmtRate(r.rx);
      case "tx": return fmtRate(r.tx);
      case "today": return t ? fmtBytes(t.today.rx + t.today.tx) : "-";
      case "total-rate": { const x = total(); return fmtRate(x.rx + x.tx); }
      case "total-num": { const x = total(); return splitRate(x.rx + x.tx)[0]; }
      case "total-unit": { const x = total(); return splitRate(x.rx + x.tx)[1]; }
      case "total-rx": return fmtRate(total().rx);
      case "total-tx": return fmtRate(total().tx);
      case "sum": { const x = rate(name); return x.rx + x.tx > 800 ? fmtRate(x.rx + x.tx) : ""; }
    }
    return null;
  }
  function patchLive() {
    for (const el of $$("[data-live]")) {
      const v = liveText(el.dataset.live);
      if (v != null && el.textContent !== v) el.textContent = v;
    }
    const lv = $(".live"); if (lv) lv.classList.toggle("off", !S.live);
  }

  // ---------- views ----------
  const NAV = [
    ["dashboard", "داشبورد", I.home], ["tunnels", "تانل‌ها", I.tunnel], ["servers", "سرورها", I.server],
    ["ports", "پورت‌ها", I.ports], ["speed", "تست سرعت", I.gauge], ["commands", "دستورها", I.term], ["settings", "تنظیمات", I.gear],
  ];

  function draw() {
    const app = $("#app");
    if (!S.authed) { const first = !$("#login-user"); morph(app, part(viewLogin())); const u = $("#login-user"); if (u && first) u.focus(); return; }
    if (!S.ov) { morph(app, part(html`<div class="login-wrap"><div class="muted">در حال بارگذاری…</div></div>`)); return; }
    morph(app, part(viewShell()));
    drawModal();
    wireCharts();
  }

  function viewLogin() {
    return html`<div class="login-wrap"><div class="panel login">
      <div class="brand"><img src="assets/logo.svg" alt=""><div><b>NeoTunnel</b><small>مدیریت تانل‌های سرور</small></div></div>
      <h1>ورود به پنل</h1>
      <form data-form="login">
        <div class="field"><label for="login-user">نام کاربری</label><input class="input ltr" id="login-user" name="user" autocomplete="username" required></div>
        <div class="field"><label for="login-pass">رمز عبور</label><input class="input ltr" id="login-pass" name="password" type="password" autocomplete="current-password" required></div>
        <button class="btn primary" type="submit">ورود</button>
      </form>
      <p class="help" style="text-align:center;margin:18px 0 0">رمز را فراموش کرده‌اید؟ روی سرور اجرا کنید: <code class="ltr">sudo neotunnel panel password</code></p>
    </div></div>`;
  }

  function viewShell() {
    const ov = S.ov;
    const counts = { tunnels: visible().length, servers: ov.peers.length, ports: ov.forwards.length };
    const page = { dashboard: viewDashboard, tunnels: viewTunnels, servers: viewServers, ports: viewPorts, speed: viewSpeed, commands: viewCommands, settings: viewSettings }[S.route] || viewDashboard;
    const [title, sub, tools] = pageHead();
    return html`<div class="shell">
      <aside class="rail" aria-label="منو">
        <div class="brand"><img src="assets/logo.svg" alt=""><div><b>NeoTunnel</b><small class="ltr">${ov.node.version}</small></div></div>
        <nav class="nav">${NAV.map(([id, label, icon]) => html`<a href="#/${id}" class="${S.route === id ? "on" : ""}" ${S.route === id ? raw('aria-current="page"') : ""}>${icon()}<span>${label}</span>${counts[id] != null ? html`<span class="n">${counts[id]}</span>` : ""}</a>`)}</nav>
        <div class="rail-foot">
          <div class="me"><b>${ov.node.name}</b><span class="ltr">${ov.node.address || "-"}</span></div>
          <div class="rail-actions">
            <button class="btn sm ghost icon" data-act="theme" title="${S.theme === "dark" ? "تم روشن" : "تم تیره"}" aria-label="تغییر تم">${S.theme === "dark" ? I.sun(16) : I.moon(16)}</button>
            <span class="grow"></span>
            <button class="btn sm ghost" data-act="logout">${I.out(16)} خروج</button>
          </div>
        </div>
      </aside>
      <main class="main" data-key="page-${S.route}">
        ${S.route === "dashboard" ? "" : html`<div class="head"><div><h1>${title}</h1><div class="sub">${sub}</div></div>
          <div class="tools"><span class="live ${S.live ? "" : "off"}"><i></i>${S.live ? "زنده" : "قطع ارتباط با سرور"}</span>${tools}</div></div>`}
        ${ov.errors && ov.errors.length ? html`<div class="note bad" style="margin-bottom:16px"><b>${I.alert(16)} مشکل در اعمال تنظیمات</b>${ov.errors.map((e) => html`<div class="mono">${e}</div>`)}</div>` : ""}
        ${page()}
      </main>
    </div>${S.drawer ? viewDrawer() : ""}`;
  }

  function pageHead() {
    const add = html`<button class="btn primary" data-act="wizard">${I.plus(16)} تانل جدید</button>`;
    switch (S.route) {
      case "tunnels": return ["تانل‌ها", "روی هر تانل بزنید تا جزئیات و تنظیمات پیشرفته باز شود", add];
      case "servers": return ["سرورها", "سرورهای مقصد که از همین پنل مدیریت می‌شوند", html`<button class="btn" data-act="sync-all">${I.sync(16)} همگام‌سازی همه</button><button class="btn primary" data-act="add-server">${I.plus(16)} افزودن سرور</button>`];
      case "ports": return ["پورت‌ها", "پورت‌های این سرور و اینکه کاربر از کدام تانل به کجا می‌رسد", html`<button class="btn primary" data-act="add-forward">${I.plus(16)} فوروارد جدید</button>`];
      case "speed": return ["تست سرعت", "پینگ، جیتر، افت بسته و پهنای باند واقعی بین دو سرور", ""];
      case "commands": return ["دستورها", "معادل خط فرمان کارهای پنل، با نام‌ها و آدرس‌های همین سرور", ""];
      case "settings": return ["تنظیمات", "سرور، دسترسی پنل، پیش‌فرض تانل‌ها، شبکه و نگهداری", ""];
    }
    return ["", "", add];
  }

  // ---------- dashboard: the instrument ----------
  // A schematic of what is real: this server on one side, each destination
  // on the other, every tunnel a straight bus between them carrying its live
  // rate and ping, and each forwarded port a tick on this server's edge.
  // Monochrome; amber marks only what is moving right now.
  function viewDashboard() {
    const ov = S.ov, ts = visible(), tr = total();
    const load = ov.node.cpus ? (ov.node.load[0] / ov.node.cpus) * 100 : 0;
    const mem = ov.node.mem_total ? (ov.node.mem_used / ov.node.mem_total) * 100 : 0;
    const [tv, tu] = splitBytes(sumToday()), [mv, mu] = splitBytes(sumMonth());
    const nic = ov.node.nic ? ov.node.nic_month.rx + ov.node.nic_month.tx : 0;
    const ok = healthy(), bad = ts.length - ok;
    const [rv, ru] = splitRate(tr.rx + tr.tx);
    return html`<div class="dash">
      <header class="dash-head">
        <div class="id"><span class="mono up">${ov.node.name}</span><span class="mono faint">${ov.node.address || ""}</span>
          <span class="faint">روشن ${fmtUptime(ov.node.uptime_s)}</span>${ov.master ? html`<span class="faint">مدیریت از ${ov.master.name}</span>` : ""}</div>
        <div class="tools"><span class="live ${S.live ? "" : "off"}"><i></i>${S.live ? "زنده" : "قطع ارتباط"}</span><button class="btn primary" data-act="wizard">${I.plus(16)} تانل جدید</button></div>
      </header>
      <section class="readouts">
        <div class="ro main"><span class="k">ترافیک لحظه‌ای</span><span class="v mono"><b data-live="total-num">${rv}</b><small data-live="total-unit">${ru}</small></span>
          <span class="f mono ltr"><i class="rx">↓</i> <span data-live="total-rx">${fmtRate(tr.rx)}</span>  <i class="tx">↑</i> <span data-live="total-tx">${fmtRate(tr.tx)}</span></span></div>
        <div class="ro"><span class="k">امروز</span><span class="v mono"><b>${tv}</b><small>${tu}</small></span><span class="f">ترافیک کاربران، بدون تست‌ها</span></div>
        <div class="ro"><span class="k">این ماه</span><span class="v mono"><b>${mv}</b><small>${mu}</small></span><span class="f">${nic ? html`کارت شبکه <span class="mono">${fmtBytes(nic)}</span>` : "مجموع همه‌ی تانل‌ها"}</span></div>
        <div class="ro"><span class="k">تانل‌ها</span><span class="v mono"><b class="${bad ? "warn" : ""}">${ok}</b><small>/ ${ts.length}</small></span><span class="f">${!ts.length ? "هنوز تانلی نیست" : bad ? `${bad} تانل بررسی لازم دارد` : "ترافیک همه رد می‌شود"}</span></div>
        <div class="ro"><span class="k">سرور</span><span class="v mono"><b class="${load > 90 ? "bad" : load > 70 ? "warn" : ""}">${load.toFixed(0)}</b><small>% CPU</small></span>
          <span class="bar"><i style="transform:scaleX(${(Math.min(100, load) / 100).toFixed(3)})"></i></span><span class="f">حافظه <span class="mono">${mem.toFixed(0)}%</span> از ${fmtBytes(ov.node.mem_total)}</span></div>
      </section>
      <section class="scheme">${scheme()}</section>
      <section class="dash-grid">
        <div class="panel"><header><h2>۲۴ ساعت گذشته</h2><span class="aside">همه‌ی تانل‌ها، هر نقطه ۵ دقیقه</span></header><div class="in">${chart("dash", totalPairs())}</div></div>
        <div class="panel"><header><h2>تانل‌ها</h2><a class="aside" href="#/tunnels">همه</a></header>${tunnelTable(ts)}</div>
      </section>
      ${!ov.node.bbr ? html`<div class="note warn" style="margin-top:16px">BBR روی این سرور فعال نیست. از <a href="#/settings" data-act="goto-sec" data-sec="net">تنظیمات شبکه</a> یکجا فعالش کنید.</div>` : ""}
    </div>`;
  }

  const healthy = () => visible().filter((t) => { const h = t.health; return t.up && (!h || h.passes) && !(t.limits && t.limits.disabled); }).length;

  // The health of a tunnel, from real probes when available.
  function tunHealth(t) {
    const [c] = status(t);
    if (c !== "ok") return c;
    if (t.health && t.health.probes >= 3 && !t.health.passes) return "bad";
    if (t.health && t.health.loss >= 20) return "warn";
    return "ok";
  }

  // Destinations: one per peer or far address, with their tunnels.
  function destinations() {
    const ov = S.ov, groups = new Map();
    for (const t of visible()) {
      const key = t.peer || t.far_ip;
      if (!groups.has(key)) groups.set(key, { key, name: t.peer || "دستگاه دیگر", ip: t.far_ip, tunnels: [], paired: true });
      groups.get(key).tunnels.push(t);
    }
    for (const p of ov.peers) {
      if (!groups.has(p.name)) groups.set(p.name, { key: p.name, name: p.name, ip: p.address, tunnels: [], paired: p.paired });
      const g = groups.get(p.name); g.paired = p.paired; g.ip = p.address; g.version = p.version;
    }
    if (ov.master && !groups.size) groups.set(ov.master.name, { key: ov.master.name, name: ov.master.name, ip: ov.master.address.replace(/:\d+$/, ""), tunnels: [], paired: ov.master.paired });
    return [...groups.values()];
  }

  const trim = (s, n) => (s && s.length > n ? s.slice(0, n - 1) + "…" : s);

  function scheme() {
    const ov = S.ov, groups = destinations();
    if (!groups.length) return html`<div class="empty">${I.globe(34)}<b>هنوز مقصدی ندارید</b>یک سرور خارج اضافه کنید یا مستقیم یک تانل بسازید.<br><button class="btn primary" data-act="wizard">${I.plus(16)} تانل جدید</button></div>`;
    const rows = [];
    for (const g of groups) {
      const first = rows.length;
      if (g.tunnels.length) g.tunnels.forEach((t) => rows.push({ g, t }));
      else rows.push({ g, t: null });
      g.first = first; g.last = rows.length - 1;
    }
    const W = 1000, top = 46, rowH = 62, H = Math.max(280, top * 2 + rows.length * rowH);
    const y = (i) => top + rowH / 2 + i * rowH;
    const nodeX = 744, nodeW = 196, peerX = 60, peerW = 196, busL = peerX + peerW, busR = nodeX;
    const now = performance.now() / 1000, phase = (p) => `style="animation-delay:-${(now % p).toFixed(2)}s"`;
    const o = [];
    o.push(`<defs><pattern id="grid" width="20" height="20" patternUnits="userSpaceOnUse"><circle cx="1" cy="1" r=".9" class="gd"/></pattern></defs>`);
    o.push(`<rect width="${W}" height="${H}" fill="url(#grid)"/>`);
    const corners = (x, y0, w, h) => {
      const c = 10;
      return `<path class="brk" d="M${x} ${y0 + c}V${y0}H${x + c}M${x + w - c} ${y0}H${x + w}V${y0 + c}M${x + w} ${y0 + h - c}V${y0 + h}H${x + w - c}M${x + c} ${y0 + h}H${x}V${y0 + h - c}"/>`;
    };

    // Buses: one per tunnel, straight across.
    rows.forEach((r, i) => {
      const yy = y(i);
      if (!r.t) {
        o.push(`<line class="bus none" x1="${busL}" y1="${yy}" x2="${busL + 120}" y2="${yy}"/>`);
        o.push(`<text class="lbl faint" x="${busL + 132}" y="${yy + 4}">${r.g.paired ? "بدون تانل" : "منتظر اتصال"}</text>`);
        return;
      }
      const t = r.t, rt = rate(t.name), bps = rt.rx + rt.tx, st = tunHealth(t), off = t.limits && t.limits.disabled;
      const cls = off ? "off" : st === "bad" ? "down" : st === "warn" ? "wait" : bps > 1500 ? "hot" : "idle";
      const sel = S.drawer && S.drawer.name === t.name;
      const mid = (busL + busR) / 2, h = t.health;
      const ping = off ? "خاموش" : h && h.probes >= 3 ? (h.passes ? ms(h.ping_ms) + (h.loss >= 1 ? ` · ${h.loss.toFixed(0)}%` : "") : "بدون پاسخ") : "";
      o.push(`<g class="hit row ${cls} ${sel ? "sel" : ""}" role="button" tabindex="0" data-act="arc" data-key="${esc(t.name)}" data-type="tunnel" aria-label="${esc(t.name)}">
        <rect x="${busL}" y="${yy - rowH / 2}" width="${busR - busL}" height="${rowH}" fill="transparent"/>
        <line class="bus" x1="${busL}" y1="${yy}" x2="${busR}" y2="${yy}"/>
        ${cls === "hot" && !reduceMotion ? `<line class="flow" x1="${busR}" y1="${yy}" x2="${busL}" y2="${yy}" ${phase(1.2)}/>` : ""}
        <circle class="term" cx="${busL}" cy="${yy}" r="3.5"/><circle class="term" cx="${busR}" cy="${yy}" r="3.5"/>
        <rect class="chip" x="${mid - 74}" y="${yy - 13}" width="148" height="26" rx="4"/>
        <text class="chip-t" x="${mid}" y="${yy + 4.5}" text-anchor="middle">${esc(trim(t.name, 10))} · ${esc(trim(kindTitle(t), 12))}${t.encrypt || t.obfs ? " ◆" : ""}</text>
        <text class="rate" x="${busR - 12}" y="${yy - 9}" text-anchor="end" data-live="sum:${esc(t.name)}">${bps > 800 ? fmtRate(bps) : ""}</text>
        <text class="ping" x="${busL + 12}" y="${yy - 9}">${esc(ping)}</text></g>`);
    });

    // Destinations, each spanning its tunnels' rows.
    for (const g of groups) {
      const y0 = y(g.first) - rowH / 2 + 8, h = (g.last - g.first + 1) * rowH - 16;
      const st = !g.paired ? "warn" : g.tunnels.some((t) => tunHealth(t) === "ok") ? "ok" : g.tunnels.length ? "bad" : "idle";
      const cy = y0 + h / 2;
      o.push(`<g class="hit dest" role="button" tabindex="0" data-act="arc" data-key="${esc(g.key)}" data-type="dest" aria-label="${esc(g.name)}">
        <rect class="blk" x="${peerX}" y="${y0}" width="${peerW}" height="${h}" rx="3"/>${corners(peerX, y0, peerW, h)}
        <circle class="led ${st}" cx="${peerX + 18}" cy="${cy - (h > 40 ? 8 : 0)}" r="3.5"/>
        <text class="nm" x="${peerX + 30}" y="${cy - (h > 40 ? 3 : -4)}">${esc(trim(g.name, 18))}</text>
        ${h > 40 ? `<text class="ip" x="${peerX + 30}" y="${cy + 15}">${esc(g.ip || "")}</text>` : ""}</g>`);
    }

    // This server, spanning every row, with its ports as ticks on the edge.
    const ny = top - 6, nh = H - 2 * top + 12, tr = total(), [v, u] = splitRate(tr.rx + tr.tx), ncy = ny + nh / 2;
    o.push(`<rect class="blk me" x="${nodeX}" y="${ny}" width="${nodeW}" height="${nh}" rx="3"/>${corners(nodeX, ny, nodeW, nh)}`);
    o.push(`<text class="nm" x="${nodeX + 18}" y="${ncy - 24}">${esc(trim(ov.node.name, 18))}</text>`);
    o.push(`<text class="big" x="${nodeX + 18}" y="${ncy + 12}"><tspan data-live="total-num">${esc(v)}</tspan><tspan class="unit" dx="6" data-live="total-unit">${esc(u)}</tspan></text>`);
    o.push(`<text class="ip" x="${nodeX + 18}" y="${ncy + 32}">${esc(ov.node.address || "")}</text>`);
    const fs = ov.forwards || [];
    fs.slice(0, 12).forEach((f, i) => {
      const py = ny + 18 + (fs.length === 1 ? nh / 2 - 18 : ((nh - 36) * i) / Math.max(1, Math.min(fs.length, 12) - 1));
      const pr = prate(f), on = pr.rx + pr.tx > 800;
      o.push(`<g class="port ${on ? "hot" : ""}"><line x1="${nodeX + nodeW}" y1="${py}" x2="${nodeX + nodeW + 14}" y2="${py}"/>
        <text x="${nodeX + nodeW + 20}" y="${py + 4}">${esc(trim(f.ports, 11))}</text></g>`);
    });
    return raw(`<svg class="schematic" viewBox="0 0 ${W} ${H}" role="img" aria-label="نمای زنده‌ی تانل‌ها">${o.join("")}</svg>`);
  }

  // Every visible tunnel's 5-minute buckets, summed.
  function totalPairs() {
    const out = Array.from({ length: 288 }, () => ({ rx: 0, tx: 0 }));
    for (const t of visible()) {
      const s = S.traffic[t.name];
      if (!s || !s.series) continue;
      s.series.forEach((p, i) => { if (out[i]) { out[i].rx += p.rx; out[i].tx += p.tx; } });
    }
    return out;
  }

  function tunnelTable(ts) {
    if (!ts.length) return html`<div class="empty" style="padding:28px">هنوز تانلی ساخته نشده</div>`;
    return html`<table class="t inst"><thead><tr><th>تانل</th><th class="hide-sm">مقصد</th><th>پینگ</th><th class="num">↓</th><th class="num">↑</th><th class="num hide-sm">امروز</th></tr></thead><tbody>
      ${ts.map((t) => { const r = rate(t.name), h = t.health; return html`<tr class="hit" data-act="arc" data-key="${t.name}" data-type="tunnel">
        <td><span class="state"><i class="dot ${tunHealth(t)}"></i><b>${t.name}</b></span> <span class="faint small">${kindTitle(t)}</span></td>
        <td class="hide-sm">${t.peer || html`<span class="mono small">${t.far_ip}</span>`}</td>
        <td class="mono small">${t.limits && t.limits.disabled ? "خاموش" : h && h.probes >= 3 ? (h.passes ? ms(h.ping_ms) : "—") : ""}</td>
        <td class="num mono small" data-live="rx:${t.name}">${fmtRate(r.rx)}</td><td class="num mono small faint" data-live="tx:${t.name}">${fmtRate(r.tx)}</td>
        <td class="num mono small hide-sm" data-live="today:${t.name}">${fmtBytes(t.today.rx + t.today.tx)}</td></tr>`; })}
    </tbody></table>`;
  }

  // ---------- drawer ----------
  function viewDrawer() {
    const d = S.drawer;
    let body = "";
    if (d.type === "dest") {
      const g = destinations().find((x) => x.key === d.key);
      if (!g) { S.drawer = null; return ""; }
      const p = peer(g.key);
      body = html`<header><div><h3>${g.name}</h3><div class="sub ltr">${g.ip || ""}</div></div><button class="btn sm ghost icon" data-act="drawer-close" aria-label="بستن">${I.x(16)}</button></header>
        <div class="body">
          ${p ? html`<dl class="kv"><dt>وضعیت</dt><dd>${!p.paired ? "منتظر اجرای دستور اتصال" : p.in_sync ? "متصل و همگام" : "عقب از آخرین تغییرات"}</dd>
            <dt>نسخه</dt><dd class="ltr">${p.version || "-"} ${p.arch || ""}</dd><dt>آخرین ارتباط</dt><dd>${ago(p.last_seen)}</dd></dl>` : ""}
          <div><h4>تانل‌ها</h4>${g.tunnels.length ? html`<div class="layers">${g.tunnels.map((t) => html`<button class="layer" style="cursor:pointer;text-align:start;font:inherit;color:inherit" data-act="drawer" data-type="tunnel" data-name="${t.name}">
              <i class="dot ${status(t)[0]}"></i><span class="k">${t.name}</span><span class="faint">${kindTitle(t)}</span><span class="m ltr" data-live="rx:${t.name}">${fmtRate(rate(t.name).rx)}</span></button>`)}</div>`
            : html`<div class="muted small">هنوز تانلی به این مقصد نیست.</div>`}</div>
          <div class="row">${p ? html`<button class="btn sm" data-act="setup" data-name="${p.name}">${I.term(15)} دستور اتصال</button>` : ""}
            <button class="btn sm primary" data-act="wizard" data-peer="${p ? p.name : ""}">${I.plus(15)} تانل به این مقصد</button></div>
        </div>`;
    } else {
      const t = tunnel(d.name);
      if (!t) { S.drawer = null; return ""; }
      const r = rate(t.name), fwd = S.ov.forwards.filter((f) => f.tunnel === t.name), tr = S.traffic[t.name];
      body = html`<header><div><h3>${t.name}</h3><div class="sub">${kindTitle(t)} به ${t.peer || "دستگاه دیگر"} <span class="ltr">${t.far_ip}</span></div></div>
          <button class="btn sm ghost icon" data-act="drawer-close" aria-label="بستن">${I.x(16)}</button></header>
        <div class="body">
          <div class="row">${stateEl(t)}${healthTag(t)}${t.encrypt ? html`<span class="tag lock">${I.lock(12)} ${cipherTitle(t.encrypt)}</span>` : ""}${t.obfs ? html`<span class="tag lock">${I.mask(12)} مبهم</span>` : ""}${t.managed ? html`<span class="tag warn">از سرور اصلی</span>` : ""}</div>
          <div class="figs">
            <div><div class="k">دانلود</div><div class="v ltr" data-live="rx:${t.name}">${fmtRate(r.rx)}</div></div>
            <div><div class="k">آپلود</div><div class="v ltr" data-live="tx:${t.name}">${fmtRate(r.tx)}</div></div>
            <div><div class="k">امروز</div><div class="v ltr" data-live="today:${t.name}">${fmtBytes(t.today.rx + t.today.tx)}</div></div>
            <div><div class="k">این ماه</div><div class="v ltr">${fmtBytes(t.month.rx + t.month.tx)}</div></div>
            <div><div class="k">از ابتدا</div><div class="v ltr">${fmtBytes(t.total.rx + t.total.tx)}</div></div>
            <div><div class="k">MTU</div><div class="v ltr">${t.mtu || "-"}</div></div>
          </div>
          <div><h4>۲۴ ساعت گذشته</h4>${tr ? chart(t.name, tr.series) : html`<div class="muted small">آمار بعد از چند دقیقه کار سرویس جمع می‌شود.</div>`}</div>
          <div><h4>لایه‌ها</h4><div class="layers">${(t.stack || []).map((l) => html`<div class="layer"><i class="dot ${l.up ? "ok" : "bad"}"></i><span class="k">${l.name}</span><span class="faint">${l.kind}</span><span class="m">MTU ${l.mtu || "-"}</span></div>`)}</div></div>
          <dl class="kv">
            ${t.here4 ? html`<dt>IPv4 این طرف / آن طرف</dt><dd class="ltr">${t.here4.split("/")[0]} ⇄ ${t.there4}</dd>` : ""}
            ${t.here6 ? html`<dt>IPv6</dt><dd class="ltr small">${t.here6.split("/")[0]} ⇄ ${t.there6}</dd>` : ""}
            ${t.base_port ? html`<dt>پورت UDP</dt><dd class="ltr">${t.base_port}</dd>` : ""}
            ${t.path_mtu ? html`<dt>MTU مسیر (اندازه‌گیری‌شده)</dt><dd class="ltr">${t.path_mtu}</dd>` : ""}
            ${t.wg_key ? html`<dt>کلید عمومی WireGuard</dt><dd class="ltr small">${t.wg_key}</dd>` : ""}
            ${fwd.length ? html`<dt>پورت‌ها</dt><dd>${fwd.map((f) => html`<span class="tag ltr">${f.ports}${f.to ? " → " + f.to : ""}</span> `)}</dd>` : ""}
          </dl>
          ${t.error ? html`<div class="note bad"><div class="mono">${t.error}</div></div>` : ""}
          <div class="row">
            <button class="btn sm" data-act="test" data-name="${t.name}">${I.gauge(15)} تست سرعت</button>
            ${t.managed ? "" : html`<button class="btn sm" data-act="mtu" data-name="${t.name}">${I.ruler(15)} بهترین MTU</button>
              <button class="btn sm" data-act="open-tunnel" data-name="${t.name}">${I.gear(15)} تنظیمات</button>`}
          </div>
        </div>`;
    }
    const enter = S.drawerEnter ? " enter" : "";
    S.drawerEnter = false;
    return html`<div class="drawer-scrim${enter}" data-act="drawer-close"></div><aside class="drawer${enter}" role="dialog" aria-modal="true" aria-label="جزئیات">${body}</aside>`;
  }

  // Received / sent over the last 24 hours (two series, one axis, bits/s).
  function chart(id, series) {
    const W = 400, H = 150, P = { l: 4, r: 44, t: 8, b: 20 };
    const rx = series.map((p) => (p.rx * 8) / 300), tx = series.map((p) => (p.tx * 8) / 300);
    const max = Math.max(1000, ...rx, ...tx), n = series.length;
    const nice = niceMax(max);
    const x = (i) => P.l + (i / (n - 1)) * (W - P.l - P.r), y = (v) => P.t + (1 - v / nice) * (H - P.t - P.b);
    const line = (vals) => vals.map((v, i) => `${i ? "L" : "M"}${x(i).toFixed(1)} ${y(v).toFixed(1)}`).join("");
    const area = (vals) => `${line(vals)}L${x(n - 1)} ${y(0)}L${x(0)} ${y(0)}Z`;
    let grid = "";
    // Lines in the SVG; labels in HTML so they never stretch with it.
    let labels = "";
    for (const f of [0, 0.5, 1]) {
      grid += `<line class="grid" x1="${P.l}" x2="${W - P.r}" y1="${y(nice * f)}" y2="${y(nice * f)}" vector-effect="non-scaling-stroke"/>`;
      labels += `<span class="ya" style="top:${((y(nice * f) / H) * 100).toFixed(2)}%">${fmtRate(nice * f)}</span>`;
    }
    labels += `<span class="xa s">۲۴ ساعت پیش</span><span class="xa e">اکنون</span>`;
    CHARTS[id] = { rx, tx, n, x, y, W, H };
    return html`<div class="legend"><span><i class="rx"></i>دانلود</span><span><i class="tx"></i>آپلود</span></div>
      <div class="chart" data-chart="${id}">${raw(`<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="none" role="img" aria-label="نمودار ترافیک ۲۴ ساعت" style="direction:ltr">${grid}
        <path class="area rx" d="${area(rx)}"/><path class="area tx" d="${area(tx)}"/><path class="line rx" d="${line(rx)}" vector-effect="non-scaling-stroke"/><path class="line tx" d="${line(tx)}" vector-effect="non-scaling-stroke"/>
        <g class="hover" style="display:none"><line class="cross" y1="${P.t}" y2="${H - P.b}" vector-effect="non-scaling-stroke"/><circle class="mark rx" r="4"/><circle class="mark tx" r="4"/></g></svg>`)}${raw(labels)}<div class="tip" hidden></div></div>`;
  }
  const CHARTS = {};
  const niceMax = (v) => { const e = 10 ** Math.floor(Math.log10(v)), m = v / e; return (m <= 1 ? 1 : m <= 2 ? 2 : m <= 5 ? 5 : 10) * e; };

  function wireCharts() {
    for (const el of $$("[data-chart]")) {
      if (el._wired || !CHARTS[el.dataset.chart]) continue;
      el._wired = true;
      const svgEl = $("svg", el), g = $(".hover", el), tip = $(".tip", el);
      const move = (e) => {
        const c = CHARTS[el.dataset.chart]; if (!c) return;
        const box = svgEl.getBoundingClientRect();
        const px = ((e.clientX - box.left) / box.width) * c.W;
        let i = Math.round(((px - c.x(0)) / (c.x(c.n - 1) - c.x(0))) * (c.n - 1));
        i = Math.max(0, Math.min(c.n - 1, i));
        g.style.display = "";
        $("line", g).setAttribute("x1", c.x(i)); $("line", g).setAttribute("x2", c.x(i));
        const [m1, m2] = $$("circle", g);
        m1.setAttribute("cx", c.x(i)); m1.setAttribute("cy", c.y(c.rx[i]));
        m2.setAttribute("cx", c.x(i)); m2.setAttribute("cy", c.y(c.tx[i]));
        const mins = (c.n - 1 - i) * 5;
        tip.hidden = false;
        tip.innerHTML = part(html`<div class="faint">${mins ? (mins >= 60 ? `${Math.floor(mins / 60)} ساعت و ${mins % 60} دقیقه پیش` : `${mins} دقیقه پیش`) : "اکنون"}</div>
          <div class="s"><i style="background:var(--rx)"></i>دانلود <b class="ltr">${fmtRate(c.rx[i])}</b></div><div class="s"><i style="background:var(--tx)"></i>آپلود <b class="ltr">${fmtRate(c.tx[i])}</b></div>`);
        const left = (c.x(i) / c.W) * box.width;
        tip.style.left = Math.min(box.width - tip.offsetWidth, Math.max(0, left - tip.offsetWidth / 2)) + "px";
        tip.style.top = "-8px";
      };
      svgEl.addEventListener("pointermove", move);
      svgEl.addEventListener("pointerleave", () => { g.style.display = "none"; tip.hidden = true; });
    }
  }

  // ---------- tunnels ----------
  function viewTunnels() {
    const ts = visible();
    if (!ts.length) return html`<div class="panel"><div class="empty">${I.tunnel(32)}<b>هیچ تانلی وجود ندارد</b>با چند کلیک یک تانل 6to4 بسازید؛ اگر مسیر پروتکل‌ها را می‌بندد، روی پورت UDP یا با NeoObfs اجرایش کنید.<br><button class="btn primary" data-act="wizard">${I.plus(16)} تانل جدید</button></div></div>`;
    return html`<div class="panel"><div class="list">${ts.map(tunnelRow)}</div></div>`;
  }

  function tunnelRow(t) {
    const r = rate(t.name), open = S.open === t.name;
    return html`<div class="item ${open ? "open" : ""}">
      <button class="sum" data-act="toggle-tunnel" data-name="${t.name}" aria-expanded="${open}">
        <i class="dot ${status(t)[0]}"></i>
        <div class="name">${t.name}<small>${kindTitle(t)}${t.base_port ? ` روی UDP ${t.base_port}` : ""}</small></div>
        <div class="muted small hide-sm">${t.peer || "دستگاه دیگر"} <span class="ltr faint">${t.far_ip}</span></div>
        <div class="cell-tags hide-sm">${t.encrypt ? html`<span class="tag lock">${I.lock(12)} ${cipherTitle(t.encrypt)}</span>` : html`<span class="tag">بدون رمز</span>`}${t.obfs ? html`<span class="tag lock">${I.mask(12)} مبهم</span>` : ""}</div>
        <div class="rate"><b data-live="rx:${t.name}">${fmtRate(r.rx)}</b> ↓<br><span data-live="tx:${t.name}">${fmtRate(r.tx)}</span> ↑</div>
        <span class="chev">${I.chev(16)}</span>
      </button>
      ${open ? tunnelDetail(t) : ""}
    </div>`;
  }

  function tunnelDetail(t) {
    const st = S.tset[t.name] || (S.tset[t.name] = Object.assign({ port: t.base_port || "" }, t.settings, { noDF: !!t.settings.no_df, mtuMode: t.settings.mtu ? "manual" : t.settings.mtu_probe ? "probe" : "auto", rate: t.limits.rate_mbit || "", quota: t.limits.quota_gb || "" }));
    const base = (t.stack && t.stack[0]) || { kind: t.kind };
    const crypto = ["wireguard", "obfs"].includes(base.kind);
    const m = S.mtu[t.name];
    const tab = t.managed ? "overview" : (S.dtab[t.name] || "overview");
    const tabs = [["overview", "نمای کلی"], ["limits", "محدودیت‌ها"], ["settings", "تنظیمات"]];
    return html`<div class="detail">
        <div class="row dbar">
          <button class="btn sm" data-act="test" data-name="${t.name}">${I.gauge(15)} تست سرعت</button>
          ${t.managed ? "" : html`<button class="btn sm" data-act="mtu" data-name="${t.name}">${I.ruler(15)} بهترین MTU</button>`}
          <button class="btn sm" data-act="drawer" data-type="tunnel" data-name="${t.name}">نمودار</button>
          ${t.peer && peer(t.peer) ? html`<button class="btn sm ghost" data-act="setup" data-name="${t.peer}">${I.term(15)} دستور اتصال</button>` : ""}
          <span class="grow"></span>
          ${t.managed ? "" : html`<button class="btn sm ${t.limits.disabled ? "primary" : "ghost"}" data-act="tunnel-power" data-name="${t.name}">${I.power(15)} ${t.limits.disabled ? "روشن کردن" : "خاموش کردن"}</button>`}
          ${t.managed ? "" : html`<button class="btn sm danger" data-act="del-tunnel" data-name="${t.name}">${I.trash(15)} حذف</button>`}
        </div>
      ${t.managed ? "" : html`<div class="seg dtabs" role="tablist">${tabs.map(([id, l]) => html`<button type="button" role="tab" aria-selected="${tab === id}" class="${tab === id ? "on" : ""}" data-act="dtab" data-name="${t.name}" data-tab="${id}">${l}</button>`)}</div>`}
      ${tab === "overview" ? html`<div class="dpane stack" data-key="ov-${t.name}">
        <div class="figs">
          <div><div class="k">امروز</div><div class="v ltr" data-live="today:${t.name}">${fmtBytes(t.today.rx + t.today.tx)}</div></div>
          <div><div class="k">این ماه</div><div class="v ltr">${fmtBytes(t.month.rx + t.month.tx)}</div></div>
          <div><div class="k">از ابتدا</div><div class="v ltr">${fmtBytes(t.total.rx + t.total.tx)}</div></div>
        </div>
        <dl class="kv">
          ${t.here4 ? html`<dt>IPv4 این طرف ⇄ آن طرف</dt><dd class="ltr">${t.here4.split("/")[0]} ⇄ ${t.there4}</dd>` : ""}
          ${t.here6 ? html`<dt>IPv6</dt><dd class="ltr small">${t.here6.split("/")[0]} ⇄ ${t.there6}</dd>` : ""}
          <dt>لایه‌ها</dt><dd>${(t.stack || []).map((l) => html`<span class="tag ltr">${l.kind} · ${l.mtu || "-"}</span> `)}</dd>
          <dt>MTU فعلی</dt><dd class="ltr">${t.mtu || "-"}${t.path_mtu ? ` (path ${t.path_mtu})` : ""}</dd>
          ${t.wg_key ? html`<dt>کلید عمومی WireGuard</dt><dd class="ltr small">${t.wg_key} <button class="btn sm ghost icon" data-act="copy" data-text="${t.wg_key}" aria-label="کپی کلید">${I.copy(14)}</button></dd>` : ""}
        </dl>
        ${m ? html`<div class="note ok">مسیر تا <span class="ltr">${m.remote}</span> بسته‌های <b>${m.path_mtu}</b> بایتی را بدون شکستن رد می‌کند. با ${m.overhead} بایت سربار لایه‌ها، بهترین MTU این تانل <b>${m.best}</b> است${m.probing ? " و تانل خودکار با آن تنظیم می‌شود." : "."}</div>` : ""}
        ${t.error ? html`<div class="note bad"><div class="mono">${t.error}</div></div>` : ""}
      </div>` : ""}
      ${tab === "limits" ? html`<div class="dpane" data-key="lim-${t.name}">${limitsBlock(t, st)}</div>` : ""}
      ${tab === "settings" || t.managed ? html`<div class="dpane" data-key="set-${t.name}">
      ${t.managed ? html`<div class="note">این تانل را سرور اصلی (${S.ov.master ? S.ov.master.name : ""}) تعریف کرده؛ تنظیماتش را از همان‌جا تغییر دهید.</div>` : html`<form class="stack" data-form="tunnel-settings" data-name="${t.name}">
        <div class="field"><span class="lbl">رمزنگاری</span>
          ${crypto ? html`<div class="help">${PROTO[base.kind === "obfs" ? "obfs" : "wireguard"].t} خودش رمزنگاری می‌کند؛ لایه‌ی دیگری لازم نیست.</div>`
            : base.kind === "tcp" ? html`<select class="input" data-bind="tset.${t.name}.encrypt"><option value="">بدون رمزنگاری (سریع‌ترین)</option>${S.ov.ciphers.map((c) => html`<option value="${c.name}" ${st.encrypt === c.name ? "selected" : ""}>${c.title}</option>`)}</select>
            <span class="help">هر قاب روی جریان TCP با کلیدهای تازه‌ی همان اتصال مهر می‌شود؛ کلیدها از رمز مشترک تانل ساخته می‌شوند. هزینه: ۱۶ بایت در هر بسته.</span>`
            : html`<select class="input" data-bind="tset.${t.name}.encrypt"><option value="">بدون رمزنگاری (سریع‌ترین)</option>${S.ov.ciphers.map((c) => html`<option value="${c.name}" ${st.encrypt === c.name ? "selected" : ""}>IPsec ${c.title}</option>`)}</select>
            <span class="help">IPsec در کرنل روی بسته‌های بیرونی همین تانل؛ کلیدها از رمز مشترک تانل ساخته می‌شوند. هزینه: ${38} بایت از MTU.</span>`}</div>
        <details class="adv" ${S.tset[t.name].adv ? "open" : ""} data-act="adv-toggle" data-name="${t.name}">
          <summary>تنظیمات پیشرفته <span class="faint">MTU، TTL، پورت${base.kind === "obfs" ? "، padding" : ""}${["obfs", "wireguard", "tcp", "eoip"].includes(base.kind) ? "، keepalive" : ""}</span>${I.chev(16)}</summary>
          <div class="in">${advancedFields(`tset.${t.name}`, st, base.kind, true, false, t.kind)}</div>
        </details>
        <div class="form-foot"><button class="btn primary" type="submit">ذخیره‌ی تنظیمات</button><button class="btn ghost" type="button" data-act="reset-tset" data-name="${t.name}">بازگردانی</button></div>
      </form>`}
      </div>` : ""}
    </div>`;
  }

  // Speed limit and monthly quota, with how much of the quota is used.
  function limitsBlock(t, st) {
    const used = t.month.rx + t.month.tx, q = t.limits.quota_gb * 2 ** 30, f = q ? Math.min(1, used / q) : 0;
    return html`<div class="limits">
      <div class="grid2">
        <div class="field"><label>سقف سرعت (Mbps)</label><input class="input ltr" type="number" min="0" max="100000" data-bind="tset.${t.name}.rate" value="${st.rate}" placeholder="بدون محدودیت">
          <span class="help">هر دو سر همین سقف را روی ارسال خود می‌گذارند، پس دانلود و آپلود هر دو محدود می‌شوند.</span></div>
        <div class="field"><label>حجم ماهانه (GB)</label><input class="input ltr" type="number" min="0" step="any" data-bind="tset.${t.name}.quota" value="${st.quota}" placeholder="نامحدود">
          <span class="help">دانلود + آپلود این ماه. وقتی پر شد تانل تا اول ماه بعد خاموش می‌شود.</span></div>
      </div>
      ${q ? html`<div class="quota ${t.blocked ? "full" : f > 0.85 ? "near" : ""}"><div class="pl"><span>${fmtBytes(used)} از ${fmtBytes(q)}</span><span class="faint">${(f * 100).toFixed(0)}٪</span></div><div class="pbar"><i style="transform:scaleX(${f.toFixed(3)})"></i></div></div>` : ""}
      <div class="row"><button class="btn sm primary" type="button" data-act="save-limits" data-name="${t.name}">ذخیره‌ی محدودیت‌ها</button></div>
    </div>`;
  }

  // The advanced options, shared by the wizard and the tunnel editor.
  // kind is the bottom layer's (it owns the outer packets), top the tunnel's own.
  function advancedFields(path, st, kind, editing, noPort, top) {
    const mm = st.mtuMode || "auto";
    return html`<div class="field"><span class="lbl">MTU</span>
        <div class="seg" role="radiogroup">${[["auto", "خودکار"], ["probe", "اندازه‌گیری مسیر"], ["manual", "دستی"]].map(([v, l]) => html`<button type="button" role="radio" aria-checked="${mm === v}" class="${mm === v ? "on" : ""}" data-act="set" data-path="${path}.mtuMode" data-value="${v}">${l}</button>`)}</div>
        ${mm === "manual" ? html`<input class="input ltr" type="number" min="576" max="9000" data-bind="${path}.mtu" value="${st.mtu || ""}" placeholder="1400" aria-label="MTU">` : ""}
        <span class="help">${mm === "auto" ? "از ۱۵۰۰ و سربار همه‌ی لایه‌ها حساب می‌شود." : mm === "probe" ? "NeoTunnel با بسته‌های بدون شکستن، MTU واقعی مسیر تا مقصد را پیدا می‌کند، هر ۳۰ دقیقه دوباره می‌سنجد و تانل را با آن تنظیم می‌کند." : "فقط وقتی مقدار درست را می‌دانید."}</span></div>
      <div class="grid2">
        ${noPort ? "" : html`<div class="field"><label>پورت ${kind === "tcp" ? "TCP" : "UDP"}${editing ? "" : " (اختیاری)"}</label><input class="input ltr" type="number" min="1" max="65535" data-bind="${path}.port" value="${st.port || ""}" placeholder="${["obfs", "wireguard", "tcp"].includes(kind) ? "خودکار" : "پروتکل خام"}">
          <span class="help">${PROTO[kind] && (PROTO[kind].udp || PROTO[kind].tcp) || kind === "obfs" || kind === "wireguard" ? "پورتی که دو سر روی آن گوش می‌دهند." : "خالی = پروتکل خام (سریع‌ترین). با پورت، تانل داخل UDP می‌رود؛ وقتی مسیر پروتکل 41 یا 47 را می‌بندد."}</span></div>`}
        <div class="field"><label>TTL</label><input class="input ltr" type="number" min="1" max="255" data-bind="${path}.ttl" value="${st.ttl || ""}" placeholder="255"><span class="help">TTL بسته‌های بیرونی.</span></div>
        ${kind === "obfs" ? html`<div class="field"><label>Padding (بایت)</label><input class="input ltr" type="number" min="0" max="255" data-bind="${path}.padding" value="${st.padding || ""}" placeholder="0"><span class="help">تا این تعداد بایت تصادفی به هر بسته اضافه می‌شود تا اندازه‌ها چیزی را لو ندهند. هزینه‌اش همین مقدار از MTU است.</span></div>` : ""}
        ${["obfs", "wireguard", "eoip"].includes(kind) ? html`<div class="field"><label>Keepalive (ثانیه)</label><input class="input ltr" type="number" min="-1" max="3600" data-bind="${path}.keepalive" value="${st.keepalive || ""}" placeholder="25"><span class="help">نگه‌داشتن مسیر NAT باز؛ ‎-1 یعنی خاموش.</span></div>` : ""}
        ${kind === "tcp" ? html`<div class="field"><label>Keepalive (ثانیه)</label><input class="input ltr" type="number" min="-1" max="3600" data-bind="${path}.keepalive" value="${st.keepalive || ""}" placeholder="15"><span class="help">اتصالی که سه برابر این مدت ساکت بماند قطع و دوباره برقرار می‌شود؛ ‎-1 یعنی فقط keepalive خود TCP.</span></div>` : ""}
        ${kind === "wireguard" ? html`<div class="field full"><label>کلید عمومی طرف مقابل</label><input class="input ltr" data-bind="${path}.peer_key" value="${st.peer_key || ""}" placeholder="فقط وقتی آن طرف NeoTunnel ندارد"><span class="help">برای میکروتیک یا هر WireGuard دیگر؛ بین دو سرور NeoTunnel کلیدها خودکار ساخته می‌شوند.</span></div>` : ""}
        ${top === "eoip" ? html`<div class="field"><label>شناسه‌ی تانل (Tunnel ID)</label><input class="input ltr" type="number" min="0" max="65535" data-bind="${path}.tunnel_id" value="${st.tunnel_id ?? ""}" placeholder="خودکار"><span class="help">همان tunnel-id میکروتیک؛ دو سر باید یکی باشند. ۰ تا ۶۵۵۳۵، و روی EoIPv6 بدون پورت ۰ تا ۴۰۹۵. خالی = از نام تانل ساخته می‌شود.</span></div>` : ""}
        <div class="field"><label>DSCP بسته‌های بیرونی</label><input class="input ltr" type="number" min="0" max="63" data-bind="${path}.dscp" value="${st.dscp || ""}" placeholder="ارث‌بری"><span class="help">کلاس کیفیت سرویس؛ خالی = همان کلاس بسته‌ی داخلی. فقط تانل‌های کرنلی.</span></div>
        <div class="field full"><label class="switch"><input type="checkbox" data-act="st-check" data-path="${path}.noDF" ${st.noDF ? "checked" : ""}>اجازه‌ی تکه‌تکه شدن بسته‌ها در مسیر</label><span class="help">پیش‌فرض خاموش است (بیت Don't-Fragment روشن): سریع‌تر، ولی به MTU درست نیاز دارد. روشنش کنید اگر مسیر MTU کوچکی دارد و تانل بسته‌های بزرگ را می‌اندازد.</span></div>
      </div>`;
  }

  function settingsBody(st) {
    const out = { encrypt: st.encrypt || "", ttl: +st.ttl || 0, dscp: +st.dscp || 0, no_df: !!st.noDF, padding: +st.padding || 0, keepalive: +st.keepalive || 0, peer_key: (st.peer_key || "").trim(), mtu: 0, mtu_probe: false };
    if (st.mtuMode === "manual") out.mtu = +st.mtu || 0;
    if (st.mtuMode === "probe") out.mtu_probe = true;
    const id = st.tunnel_id ?? "";
    if (String(id).trim() !== "") {
      const n = Number(id);
      if (!Number.isInteger(n) || n < 0 || n > 65535) throw new Error("شناسه‌ی تانل باید عددی از ۰ تا ۶۵۵۳۵ باشد.");
      out.tunnel_id = n;
    }
    return out;
  }

  // ---------- servers ----------
  function viewServers() {
    const ov = S.ov;
    const master = ov.master ? html`<div class="note ${ov.master.paired ? "ok" : "warn"}" style="margin-bottom:16px">این سرور توسط <b>${ov.master.name}</b> (<span class="ltr">${ov.master.address}</span>) مدیریت می‌شود ${ov.master.paired ? "و به آن متصل است." : "ولی هنوز به آن نرسیده؛ سرویس مدام تلاش می‌کند."} تانل‌های آن فقط از پنل سرور اصلی تغییر می‌کنند.</div>` : "";
    if (!ov.peers.length) return html`${master}<div class="panel"><div class="empty">${I.server(32)}<b>هنوز سروری اضافه نشده</b>سرور خارج را با نام و IP اضافه کنید؛ پنل دستور یک‌خطی نصب و اتصال را برای همان سرور می‌سازد.<br><button class="btn primary" data-act="add-server">${I.plus(16)} افزودن سرور</button></div></div>`;
    const old = ov.peers.filter((p) => p.paired && p.version && p.version !== ov.node.version);
    return html`${master}
      ${old.length ? html`<div class="note warn" style="margin-bottom:16px">${old.length} سرور نسخه‌ی قدیمی‌تری از این سرور (<span class="ltr">${ov.node.version}</span>) دارد. برای اینکه همه‌ی قابلیت‌ها کار کنند ارتقا دهید.</div>` : ""}
      <div class="peers">${ov.peers.map(peerCard)}</div>`;
  }

  // A peer: reachability, version and what to do next, at a glance.
  function peerCard(p) {
    const ov = S.ov, up = S.upgrading[p.name];
    const stale = p.paired && p.last_seen && Date.now() / 1000 - p.last_seen > 180;
    const outdated = p.paired && p.version && p.version !== ov.node.version;
    const [cls, label] = up ? ["busy", "در حال ارتقا…"]
      : !p.paired ? ["warn", "منتظر اتصال"]
      : stale ? ["bad", "در دسترس نیست"]
      : p.in_sync ? ["ok", "متصل و همگام"] : ["warn", "عقب از تغییرات"];
    return html`<article class="peer ${up ? "busy" : ""}">
      <header><div><b>${p.name}</b><span class="ltr faint small">${p.address}</span></div><span class="state"><i class="dot ${cls}"></i>${label}</span></header>
      <dl>
        <div><dt>نسخه</dt><dd class="ltr">${p.version || "-"} ${outdated && !up ? html`<span class="tag warn">قدیمی</span>` : p.paired && p.version ? html`<span class="tag">به‌روز</span>` : ""}</dd></div>
        <div><dt>تانل‌ها</dt><dd>${p.tunnels}</dd></div>
        <div><dt>آخرین ارتباط</dt><dd>${ago(p.last_seen)}</dd></div>
      </dl>
      <footer>
        ${!p.paired ? html`<button class="btn sm primary" data-act="setup" data-name="${p.name}">${I.term(15)} دستور اتصال</button>`
          : html`${outdated || up ? html`<button class="btn sm primary" data-act="upgrade" data-name="${p.name}" ${up ? "disabled" : ""}>${I.upgrade(15)} ${up ? "در حال ارتقا…" : `ارتقا به ${ov.node.version}`}</button>` : ""}
            <button class="btn sm" data-act="bench" data-name="${p.name}" title="مقایسه‌ی سرعت انواع تانل">${I.gauge(15)} مقایسه</button>
            <button class="btn sm icon" data-act="sync" data-name="${p.name}" title="همگام‌سازی" aria-label="همگام‌سازی">${I.sync(15)}</button>
            <button class="btn sm icon" data-act="setup" data-name="${p.name}" title="دستور اتصال" aria-label="دستور اتصال">${I.term(15)}</button>`}
        <span class="grow"></span>
        <button class="btn sm danger icon" data-act="del-server" data-name="${p.name}" title="حذف سرور" aria-label="حذف سرور">${I.trash(15)}</button>
      </footer>
    </article>`;
  }

  // ---------- ports ----------
  function viewPorts() {
    const fs = S.ov.forwards;
    const intro = html`<div class="note" style="margin-bottom:16px">هر پورت این سرور می‌تواند از تانلی جدا به مقصدی جدا برود. ستون «IP کاربر» تعیین می‌کند سرویس مقصد چه آدرسی را به‌عنوان کاربر ببیند.</div>`;
    if (!fs.length) return html`${intro}<div class="panel"><div class="empty">${I.ports(32)}<b>فورواردی تعریف نشده</b>ترافیک ورودی پورت‌های این سرور را از داخل تانل به سرور مقصد بفرستید.<br><button class="btn primary" data-act="add-forward">${I.plus(16)} فوروارد جدید</button></div></div>`;
    return html`${intro}<div class="panel"><table class="t"><thead><tr><th>ورودی</th><th class="hide-sm">پروتکل</th><th>تانل و مقصد</th><th>IP کاربر</th><th></th></tr></thead><tbody>
      ${fs.map((f) => {
        const k = f.tunnel + "|" + f.ports, ed = S.fwdEdit[k];
        return html`<tr>
          <td><b class="ltr">${f.ports}</b><div class="faint small ltr">${f.public}</div></td>
          <td class="hide-sm">${f.proto === "both" ? "TCP + UDP" : f.proto.toUpperCase()}</td>
          <td>${f.tunnel} <span class="faint">به</span> ${f.peer || ""}<div class="faint small ltr">${f.target}:${f.to || f.ports}</div></td>
          <td>${ed ? html`<div class="row" style="flex-wrap:nowrap"><select class="input" style="height:32px;width:auto" data-bind="fwdEdit.${k}.mode" aria-label="حالت">${Object.entries(MODES_FWD).map(([v, [l]]) => html`<option value="${v}" ${ed.mode === v ? "selected" : ""}>${l}</option>`)}</select>
              ${ed.mode === "proxy" ? html`<select class="input" style="height:32px;width:auto" data-bind="fwdEdit.${k}.proxy_version" aria-label="نسخه"><option value="2" ${+ed.proxy_version !== 1 ? "selected" : ""}>v2</option><option value="1" ${+ed.proxy_version === 1 ? "selected" : ""}>v1</option></select>` : ""}
              <button class="btn sm primary" data-act="save-fwd" data-key="${k}">ذخیره</button><button class="btn sm ghost" data-act="cancel-fwd" data-key="${k}">انصراف</button></div>`
            : html`<button class="btn sm ghost" data-act="edit-fwd" data-key="${k}" title="${MODES_FWD[f.mode][1]}">${MODES_FWD[f.mode][0]}${f.mode === "proxy" ? ` v${f.proxy_version || 2}` : ""}</button>`}</td>
          <td style="text-align:end"><button class="btn sm danger icon" data-act="del-forward" data-tunnel="${f.tunnel}" data-ports="${f.ports}" aria-label="حذف فوروارد">${I.trash(15)}</button></td></tr>`;
      })}
    </tbody></table></div>
    <div class="panel" style="margin-top:16px"><header><h2>سه راه برای رساندن IP کاربر</h2></header><div class="in"><dl class="kv" style="grid-template-columns:auto 1fr">
      ${Object.values(MODES_FWD).map(([l, d]) => html`<dt><b style="color:var(--ink)">${l}</b></dt><dd style="text-align:start">${d}</dd>`)}</dl></div></div>`;
  }

  // ---------- speed ----------
  // The page is rendered only when results change. Between renders a frame
  // loop eases the gauge toward the latest sample, so the needle and the
  // trace move continuously although samples arrive five times a second.
  const G = { v: 0, to: 0, phase: "", seq: -1, hist: [], raf: 0, at: 0, sig: "", seen: {} };
  const GR = { cx: 160, cy: 160, R: 120, start: 135, sweep: 270 };
  GR.len = (Math.PI * GR.R * GR.sweep) / 180;
  const gfrac = (bps) => Math.max(0, Math.min(1, Math.log10(1 + bps / 1e6) / Math.log10(1001)));
  const isBwPhase = (p) => p === "upload" || p === "download";

  function viewSpeed() {
    const ts = visible(), ps = S.ov.peers.filter((p) => p.paired), sp = S.speed;
    if (!sp.tunnel && ts.length) sp.tunnel = ts[0].name;
    if (!sp.peer && ps.length) sp.peer = ps[0].name;
    const j = S.job, running = j && j.status === "running";
    return html`<div class="speed">
      <div class="panel">${gauge(j)}</div>
      <div class="stack">
        <div class="panel"><header><h2>تست یک تانل</h2></header><div class="in">
          ${ts.length ? html`<div class="row" style="flex-wrap:nowrap"><select class="input" data-bind="speed.tunnel" aria-label="تانل">${ts.map((t) => html`<option value="${t.name}" ${sp.tunnel === t.name ? "selected" : ""}>${t.name}، ${kindTitle(t)} به ${t.peer || t.far_ip}</option>`)}</select>
            <button class="btn primary" data-act="run-test" ${running ? "disabled" : ""}>${I.play(15)} شروع</button></div>
            <p class="help">پینگ، جیتر و افت؛ بعد ۴ ثانیه آپلود و ۴ ثانیه دانلود با ۴ اتصال هم‌زمان.</p>` : html`<div class="muted">اول یک تانل بسازید.</div>`}
        </div></div>
        <div class="panel"><header><h2>مقایسه‌ی انواع تانل</h2><span class="aside">بهترین را برای این مسیر پیدا کنید</span></header><div class="in">
          ${ps.length ? html`<div class="field"><label for="bench-peer">سرور مقصد</label><select id="bench-peer" class="input" data-bind="speed.peer">${ps.map((p) => html`<option value="${p.name}" ${sp.peer === p.name ? "selected" : ""}>${p.name} (${p.address})</option>`)}</select></div>
            <div class="row" style="margin-top:12px">${BENCH.map(([id, label]) => html`<label class="switch"><input type="checkbox" data-act="bench-only" value="${id}" ${sp.only.includes(id) ? "checked" : ""}>${label}</label>`)}</div>
            <div class="row" style="margin-top:14px"><button class="btn primary" data-act="run-bench" ${running ? "disabled" : ""}>${I.play(15)} شروع مقایسه</button><span class="help">اول اتصال همه با هم چند ثانیه بررسی می‌شود، بعد سرعت فقط برای آن‌هایی که وصل شدند. تانل‌های فعلی دست نمی‌خورند.</span></div>`
            : html`<div class="muted">برای مقایسه، سرور مقصد باید متصل باشد.</div>`}
        </div></div>
        ${j ? jobResults(j) : ""}
      </div>
    </div>`;
  }

  // The gauge: a 270° arc on a log scale up to 1 Gbps, a live trace of the
  // current measurement under it, and the three phases.
  function gauge(j) {
    const { cx, cy, R, start, sweep, len } = GR;
    const pt = (deg, r = R) => { const a = (deg * Math.PI) / 180; return [cx + r * Math.cos(a), cy + r * Math.sin(a)]; };
    const arc = (a0, a1, r = R) => { const [x0, y0] = pt(a0, r), [x1, y1] = pt(a1, r); return `M${x0.toFixed(1)} ${y0.toFixed(1)} A${r} ${r} 0 ${a1 - a0 > 180 ? 1 : 0} 1 ${x1.toFixed(1)} ${y1.toFixed(1)}`; };
    let ticks = "";
    for (const mbps of [0, 1, 10, 50, 100, 250, 500, 1000]) {
      const a = start + sweep * gfrac(mbps * 1e6), [x0, y0] = pt(a, R + 12), [x1, y1] = pt(a, R + 18), [tx, ty] = pt(a, R + 32);
      ticks += `<line class="tick" x1="${x0}" y1="${y0}" x2="${x1}" y2="${y1}"/><text x="${tx}" y="${ty + 4}" text-anchor="middle">${mbps >= 1000 ? "1G" : mbps}</text>`;
    }
    const running = j && j.status === "running", res = j && j.kind === "test" && j.results && j.results[0];
    if (!running) { G.to = res && !res.error ? res.down_bps : 0; if (!G.raf) G.v = G.to; }
    const [big, unit, what] = gaugeText(j);
    const ph = (k, label) => html`<div class="phase ${running && G.phase === k ? "now" : ""}" data-ph="${k}"><b>${phaseValue(j, k)}</b>${label}</div>`;
    return html`<div class="gauge-box ${running ? "running" : ""}">
      ${raw(`<svg class="gauge" viewBox="0 0 320 290" role="img" aria-label="سرعت لحظه‌ای">${ticks}
        <path class="track" d="${arc(start, start + sweep)}"/>
        <path id="g-fill" class="fill ${G.phase === "upload" ? "tx" : ""}" d="${arc(start, start + sweep)}" stroke-dasharray="${len.toFixed(1)}" stroke-dashoffset="${(len * (1 - gfrac(G.v))).toFixed(1)}"/>
        <text id="g-big" class="big" x="${cx}" y="${cy + 12}" text-anchor="middle" direction="ltr">${esc(big)}</text>
        <text id="g-unit" class="unit" x="${cx}" y="${cy + 40}" text-anchor="middle" direction="ltr">${esc(unit)}</text>
        <text id="g-what" class="what" x="${cx}" y="${cy + 92}" text-anchor="middle">${esc(what)}</text></svg>`)}
      ${raw(`<svg class="gtrace" viewBox="0 0 300 48" preserveAspectRatio="none" aria-hidden="true"><path id="g-area" class="area ${G.phase === "upload" ? "tx" : ""}" d="${sparkPath(true)}"/><path id="g-line" class="line ${G.phase === "upload" ? "tx" : ""}" d="${sparkPath(false)}"/></svg>`)}
      <div class="phases">${ph("ping", "پینگ")}${ph("upload", "آپلود")}${ph("download", "دانلود")}</div>
      ${running ? html`<button class="btn sm danger" style="margin-top:16px" data-act="cancel-job">توقف تست</button>` : ""}
    </div>`;
  }

  function gaugeText(j) {
    const running = j && j.status === "running", live = (j && j.live) || {};
    const res = j && j.kind === "test" && j.results && j.results[0];
    const clip = (s) => (s.length > 40 ? s.slice(0, 40) + "…" : s);
    if (running) {
      if (live.phase === "ping" && live.value) return [live.value.toFixed(1), "ms", "پینگ"];
      if (isBwPhase(G.phase)) { const [b, u] = splitRate(G.v); return [b, u, G.phase === "upload" ? "آپلود" : "دانلود"]; }
      return ["…", "", clip(live.step || "در حال آماده‌سازی")];
    }
    if (res && !res.error) { const [b, u] = splitRate(res.down_bps); return [b, u, "دانلود، آخرین تست"]; }
    return ["-", "", "آماده"];
  }

  function phaseValue(j, k) {
    const running = j && j.status === "running", res = j && j.kind === "test" && j.results && j.results[0];
    if (running) return S.pv[k] ? (k === "ping" ? ms(S.pv[k]) : fmtRate(S.pv[k])) : "…";
    if (res && !res.error) return k === "ping" ? ms(res.ping_ms) : fmtRate(k === "upload" ? res.up_bps : res.down_bps);
    return "-";
  }

  // The trace: recent samples of this measurement, scaled to their peak.
  function sparkPath(area) {
    const h = G.hist;
    if (h.length < 2) return area ? "" : "M0 47 L300 47";
    const n = 60, max = Math.max(1, ...h), step = 300 / (n - 1), off = (n - h.length) * step;
    const y = (v) => (46 - (v / max) * 40).toFixed(1);
    let d = h.map((v, i) => `${i ? "L" : "M"}${(off + i * step).toFixed(1)} ${y(v)}`).join(" ");
    if (area) d += ` L300 48 L${off.toFixed(1)} 48 Z`;
    return d;
  }

  // One frame: ease the shown value toward the latest sample.
  function gaugeFrame(now) {
    const dt = Math.min(100, now - (G.at || now));
    G.at = now;
    const k = reduceMotion ? 1 : 1 - Math.exp(-dt / 260);
    G.v += (G.to - G.v) * k;
    if (Math.abs(G.to - G.v) < Math.max(1, G.to * 0.001)) G.v = G.to;
    const fill = $("#g-fill");
    if (fill) fill.setAttribute("stroke-dashoffset", (GR.len * (1 - (isBwPhase(G.phase) ? gfrac(G.v) : 0))).toFixed(1));
    const running = S.job && S.job.status === "running";
    if (running && isBwPhase(G.phase)) {
      const [b, u] = splitRate(G.v), bg = $("#g-big"), un = $("#g-unit");
      if (bg && bg.textContent !== b) bg.textContent = b;
      if (un && un.textContent !== u) un.textContent = u;
    }
    if (G.v !== G.to || running) G.raf = requestAnimationFrame(gaugeFrame);
    else { G.raf = 0; G.at = 0; }
  }
  const kickGauge = () => { if (!G.raf) G.raf = requestAnimationFrame(gaugeFrame); };

  // Patch what changes between renders: the phase marks, texts and trace.
  function patchGauge() {
    const j = S.job, box = $(".gauge-box");
    if (!box) return;
    const [big, unit, what] = gaugeText(j);
    if (!isBwPhase(G.phase)) { $("#g-big").textContent = big; $("#g-unit").textContent = unit; }
    $("#g-what").textContent = what;
    for (const el of $$(".phase[data-ph]", box)) {
      el.classList.toggle("now", G.phase === el.dataset.ph);
      el.querySelector("b").textContent = phaseValue(j, el.dataset.ph);
    }
    for (const id of ["#g-fill", "#g-area", "#g-line"]) { const el = $(id); if (el) el.classList.toggle("tx", G.phase === "upload"); }
    $("#g-area").setAttribute("d", sparkPath(true));
    $("#g-line").setAttribute("d", sparkPath(false));
  }

  const ST = {
    pending: ["در صف", "wait"], checking: ["بررسی اتصال…", "busy"], passed: ["وصل شد، منتظر تست سرعت", "ok"],
    measuring: ["اندازه‌گیری سرعت…", "busy"], done: ["", "done"], failed: ["وصل نشد", "bad"], skipped: ["تست نشد", "wait"],
  };
  const rstatus = (r) => r.status || (r.error ? "failed" : "done");

  function jobResults(j) {
    const all = j.results || [], running = j.status === "running";
    const done = all.filter((r) => rstatus(r) === "done");
    const best = j.kind === "bench" && !running ? done.find((r) => r.name !== "direct") : null;
    const max = Math.max(1, ...done.map((r) => Math.max(r.down_bps, r.up_bps)));
    const st = { running: "در حال اجرا", done: "تمام شد", failed: "ناموفق", cancelled: "متوقف شد" }[j.status];
    const n = (s) => all.filter((r) => rstatus(r) === s).length;
    const finished = n("done") + n("failed") + n("skipped"), reached = all.length - n("pending") - n("checking") - n("failed") - n("skipped");
    const fresh = (r) => { const s = rstatus(r), was = G.seen[r.name]; G.seen[r.name] = s; return was !== undefined && was !== s; };
    const row = (r) => {
      const s = rstatus(r), [label, cls] = ST[s] || ST.done;
      const meta = s === "failed" || s === "skipped" ? html`<span class="ltr err" title="${r.error || ""}">${r.error || ""}</span>`
        : r.ping_ms ? html`پینگ ${ms(r.ping_ms)}، جیتر ${ms(r.jitter_ms)}، افت ${r.loss.toFixed(0)}٪${r.mtu ? `، MTU ${r.mtu}` : ""}` : "";
      const bars = s === "done"
        ? html`<div class="bars"><div class="bar" style="width:${((r.down_bps / max) * 100).toFixed(1)}%" title="دانلود ${fmtRate(r.down_bps)}"></div><div class="bar tx" style="width:${((r.up_bps / max) * 100).toFixed(1)}%" title="آپلود ${fmtRate(r.up_bps)}"></div></div>`
        : html`<div class="bars"><div class="lane ${cls}"><i></i></div></div>`;
      const right = s === "done" ? html`${fmtRate(r.down_bps)}<br><span class="faint">${fmtRate(r.up_bps)}</span>` : html`<span class="st ${cls}">${label}</span>`;
      return html`<div class="r ${best && r.name === best.name ? "best" : ""} ${fresh(r) ? "fresh" : ""}" data-st="${s}">
        <div><b class="nm"><i class="sd ${cls}"></i>${resultTitle(r)}</b><div class="faint small">${meta}</div></div>
        ${bars}<div class="small ltr" style="text-align:end">${right}</div></div>`;
    };
    return html`<div class="panel"><header><h2>${j.kind === "bench" ? "مقایسه با " : "تست تانل "}${j.target}</h2><span class="aside">${st}</span></header><div class="in stack">
      ${j.error ? html`<div class="note bad">${j.error}</div>` : ""}
      ${j.kind === "bench" && all.length ? html`<div class="progress"><div class="pl"><span>${running ? (n("checking") ? "بررسی اتصال همه با هم" : n("measuring") ? "اندازه‌گیری سرعت" : "در حال آماده‌سازی") : "نتیجه"}</span>
          <span class="faint">${reached} از ${all.length} وصل شد · ${n("done")} اندازه‌گیری شد</span></div>
        <div class="pbar"><i style="transform:scaleX(${(finished / all.length).toFixed(3)})"></i></div></div>` : ""}
      ${best ? html`<div class="note ok">سریع‌ترین گزینه تا <b>${j.target}</b>: <b>${resultTitle(best)}</b>، دانلود ${fmtRate(best.down_bps)} و پینگ ${ms(best.ping_ms)}.
        ${best.mode ? html` <button class="btn sm primary" data-act="wizard" data-peer="${j.target}" data-mode="${best.mode}" data-port="${best.port || ""}">ساخت همین تانل</button>` : ""}</div>` : ""}
      ${all.length ? html`<div><div class="legend"><span><i class="rx"></i>دانلود</span><span><i class="tx"></i>آپلود</span></div><div class="rank">${all.map(row)}</div></div>` : ""}
      <details class="adv" ${S.logOpen ? "open" : ""} data-act="log-toggle"><summary>گزارش کامل ${I.chev(16)}</summary><div class="in"><div class="console" id="console">${(j.log || []).map((l) => html`<div>${l}</div>`)}</div></div></details>
    </div></div>`;
  }

  const resultTitle = (r) => {
    const b = BENCH.find(([id]) => id === r.name);
    if (!b) return r.title;
    return /-udp$/.test(r.name) && r.port ? `${b[1].replace(" روی UDP", "")} روی UDP ${r.port}` : b[1];
  };

  async function startJob(kind, target, only) {
    try {
      S.job = await api("POST", "jobs", { kind, target, only });
      S.pv = {}; G.v = G.to = 0; G.phase = ""; G.seq = -1; G.hist = []; G.sig = ""; G.seen = {};
      if (S.drawer) S.drawer = null;
      location.hash = "#/speed";
      draw(); pollJob();
    } catch (e) { toast(e.message, "bad"); }
  }

  // What needs a full render: the status and the rows, not the live sample.
  const jobSig = (j) => j.status + "|" + (j.log || []).length + "|" + (j.results || []).map((r) => r.name + rstatus(r) + (r.ping_ms || 0) + (r.down_bps || 0)).join(",");

  async function pollJob() {
    if (!S.job || S.job.status !== "running") return;
    try { S.job = await api("GET", "jobs/" + S.job.id); } catch { /* retry */ }
    const lv = S.job.live || {};
    if (lv.seq !== G.seq) { G.seq = lv.seq; G.hist = []; S.pv = {}; G.to = 0; }
    if (lv.phase) {
      if (lv.phase !== G.phase) { G.phase = lv.phase; G.hist = []; }
      if (lv.value) { S.pv[lv.phase] = lv.value; if (isBwPhase(lv.phase)) { G.to = lv.value; G.hist.push(lv.value); if (G.hist.length > 60) G.hist.shift(); } }
    }
    if (S.route === "speed" && !S.modal) {
      const sig = jobSig(S.job);
      if (sig !== G.sig && !S.edit) {
        G.sig = sig; draw();
        const c = $("#console"); if (c) c.scrollTop = c.scrollHeight;
      } else patchGauge();
      kickGauge();
    }
    if (S.job.status === "running") setTimeout(pollJob, 200);
    else toast(S.job.status === "done" ? "تست تمام شد" : S.job.error || "تست متوقف شد", S.job.status === "done" ? "ok" : "bad");
  }

  // ---------- commands ----------
  function codeBlock(label, cmd, where) {
    return html`<div class="cmd"><div class="lbl">${label}${where ? html`<span class="faint">روی ${where}</span>` : ""}</div>
      <div class="code">${cmd}<button class="btn sm ghost icon" data-act="copy" data-text="${cmd}" aria-label="کپی">${I.copy(15)}</button></div></div>`;
  }

  function viewCommands() {
    const ov = S.ov;
    return html`<div class="stack">
      <div class="panel"><header><h2>نصب روی سرور جدید</h2></header><div class="in">
        <div class="help">برای سرور مقصد بهتر است دکمه‌ی «دستور اتصال» در صفحه‌ی سرورها را بزنید؛ آن دستور تنظیمات را هم اعمال می‌کند.</div>
        ${codeBlock("نصب با پنل", ov.install, "")}${codeBlock("نصب بدون پنل (سرور مقصد)", ov.install + " -s -- --no-panel", "")}</div></div>
      ${ov.commands.map((g) => html`<div class="panel"><header><h2>${g.title}</h2></header><div class="in">${g.commands.map((c) => codeBlock(c.label, c.run, c.where))}</div></div>`)}
    </div>`;
  }

  // ---------- settings ----------
  const SECS = [["node", "این سرور"], ["access", "دسترسی پنل"], ["defaults", "پیش‌فرض تانل‌ها"], ["net", "شبکه و سرعت"], ["traffic", "ترافیک"], ["maint", "نگهداری"], ["about", "درباره"]];

  function viewSettings() {
    const pane = { node: setNode, access: setAccess, defaults: setDefaults, net: setNet, traffic: setTraffic, maint: setMaint, about: setAbout }[S.sec] || setNode;
    return html`<div class="settings">
      <nav class="snav" aria-label="بخش‌های تنظیمات">${SECS.map(([k, l]) => html`<button class="${S.sec === k ? "on" : ""}" data-act="sec" data-sec="${k}">${l}</button>`)}</nav>
      <div class="spane">${pane()}</div></div>`;
  }

  function setNode() {
    const ov = S.ov;
    return html`<div class="panel"><header><h2>این سرور</h2></header><div class="in"><form data-form="node" class="stack">
      <div class="grid2"><div class="field"><label for="s-name">نام سرور</label><input id="s-name" class="input ltr" name="name" value="${ov.node.name}"><span class="help">در نقشه و دستورهای اتصال دیده می‌شود. حروف کوچک انگلیسی، عدد و خط تیره.</span></div>
      <div class="field"><label for="s-ip">IP عمومی</label><input id="s-ip" class="input ltr" name="address" value="${ov.node.address}"><span class="help">سرورهای مقصد با این IP به این سرور وصل می‌شوند.</span></div></div>
      <div class="form-foot"><button class="btn primary" type="submit">ذخیره</button></div></form></div></div>`;
  }

  function setAccess() {
    const ov = S.ov;
    return html`<div class="panel"><header><h2>آدرس پنل</h2></header><div class="in"><form data-form="port" class="stack">
        <div class="field"><label for="s-port">پورت پنل</label><input id="s-port" class="input ltr" name="port" type="number" min="1024" max="65535" value="${ov.panel.port}" style="max-width:200px">
        <span class="help">آدرس فعلی: <span class="ltr">${ov.panel.url}</span>. بعد از تغییر، پنل روی پورت جدید بالا می‌آید و به آن منتقل می‌شوید.</span></div>
        <div class="form-foot"><button class="btn primary" type="submit">تغییر پورت</button></div></form></div></div>
      <div class="panel"><header><h2>نام کاربری</h2></header><div class="in"><form data-form="user" class="stack">
        <div class="field"><label for="s-user">نام کاربری ورود</label><input id="s-user" class="input ltr" name="user" value="${ov.panel.user}" autocomplete="username" style="max-width:280px"></div>
        <div class="form-foot"><button class="btn primary" type="submit">ذخیره</button></div></form></div></div>
      <div class="panel"><header><h2>رمز عبور</h2></header><div class="in"><form data-form="password" class="stack">
        <div class="grid2"><div class="field full"><label for="s-cur">رمز فعلی</label><input id="s-cur" class="input ltr" name="current" type="password" autocomplete="current-password" required></div>
        <div class="field"><label for="s-new">رمز جدید</label><input id="s-new" class="input ltr" name="next" type="password" autocomplete="new-password" minlength="8" required><span class="help">حداقل ۸ کاراکتر.</span></div>
        <div class="field"><label for="s-again">تکرار رمز جدید</label><input id="s-again" class="input ltr" name="again" type="password" autocomplete="new-password" required></div></div>
        <div class="form-foot"><button class="btn primary" type="submit">تغییر رمز</button></div></form></div></div>`;
  }

  function setDefaults() {
    const d = S.defs || (S.defs = Object.assign({ encrypt: "", mtu_probe: false, obfs: false, padding: 0, forward_mode: "nat" }, S.ov.defaults));
    if (!d.forward_mode) d.forward_mode = "nat";
    return html`<div class="panel"><header><h2>پیش‌فرض تانل‌های جدید</h2></header><div class="in stack">
      <p class="help" style="margin:0">این مقادیر در ساخت تانل (پنل، منو و دستور quick) از قبل انتخاب می‌شوند و هر بار قابل تغییرند.</p>
      <div class="field"><label for="d-enc">رمزنگاری</label><select id="d-enc" class="input" data-bind="defs.encrypt" style="max-width:320px"><option value="">بدون رمزنگاری</option>${S.ov.ciphers.map((c) => html`<option value="${c.name}" ${d.encrypt === c.name ? "selected" : ""}>IPsec ${c.title}</option>`)}</select></div>
      <label class="switch"><input type="checkbox" data-act="def-check" data-key="mtu_probe" ${d.mtu_probe ? "checked" : ""}>اندازه‌گیری خودکار MTU مسیر</label>
      <label class="switch"><input type="checkbox" data-act="def-check" data-key="obfs" ${d.obfs ? "checked" : ""}>اجرای تانل‌ها روی لایه‌ی NeoObfs</label>
      <div class="field"><label for="d-pad">Padding پیش‌فرض NeoObfs (بایت)</label><input id="d-pad" class="input ltr" type="number" min="0" max="255" data-bind="defs.padding" value="${d.padding || ""}" placeholder="0" style="max-width:160px"></div>
      <div class="field"><span class="lbl">IP کاربر در فورواردها</span><div class="seg">${Object.entries(MODES_FWD).map(([v, [l]]) => html`<button type="button" class="${d.forward_mode === v ? "on" : ""}" data-act="set" data-path="defs.forward_mode" data-value="${v}">${l}</button>`)}</div><span class="help">${MODES_FWD[d.forward_mode][1]}</span></div>
      <div class="form-foot"><button class="btn primary" data-act="save-defaults">ذخیره‌ی پیش‌فرض‌ها</button></div>
    </div></div>`;
  }

  function setNet() {
    const si = S.system;
    if (!si) { loadSystem(); return html`<div class="panel"><div class="in muted">در حال خواندن تنظیمات کرنل…</div></div>`; }
    return html`<div class="panel"><header><h2>بهینه‌سازی شبکه</h2><span class="aside">${si.boosted ? html`<span class="state"><i class="dot ok"></i>فعال</span>` : html`<span class="state"><i class="dot warn"></i>کامل نیست</span>`}</span></header><div class="in stack">
      <p class="help" style="margin:0">BBR و صف fq، بافرهای بزرگ‌تر و یافتن MTU در TCP؛ برای سرعت بیشتر روی مسیرهای دور و پرنوسان. تنظیم دائمی است و بعد از ریبوت می‌ماند.</p>
      <table class="t"><thead><tr><th>تنظیم</th><th>پیشنهادی</th><th>فعلی</th></tr></thead><tbody>${si.sysctl.map((s) => html`<tr><td class="ltr small" style="text-align:start">${s.key}</td><td class="ltr small">${s.want}</td><td class="ltr small">${s.have === s.want ? html`<span class="state"><i class="dot ok"></i>${s.have}</span>` : html`<span class="state"><i class="dot warn"></i>${s.have || "-"}</span>`}</td></tr>`)}</tbody></table>
      <div class="form-foot"><button class="btn primary" data-act="boost" ${si.boosted ? "disabled" : ""}>${si.boosted ? "اعمال شده" : "اعمال بهینه‌سازی"}</button></div>
    </div></div>
    <div class="panel"><header><h2>وضعیت سیستم</h2></header><div class="in"><dl class="kv">
      <dt>کرنل</dt><dd class="ltr">${si.kernel || "-"}</dd>
      <dt>nftables (فوروارد و IPsec)</dt><dd>${si.nft ? "نصب است" : html`<span class="state"><i class="dot bad"></i>نصب نیست: apt install nftables</span>`}</dd>
      <dt>فایروال ufw</dt><dd>${si.ufw ? "فعال؛ پورت‌های لازم خودکار باز می‌شوند" : "غیرفعال"}</dd></dl></div></div>`;
  }

  function setTraffic() {
    const ts = visible();
    return html`<div class="panel"><header><h2>آمار ترافیک</h2></header><div class="in stack">
      <p class="help" style="margin:0">سرویس هر ۱۰ ثانیه شمارنده‌ی هر تانل را می‌خواند و مجموع روزانه، ماهانه و کل را نگه می‌دارد؛ ساخت دوباره‌ی تانل یا ریبوت آن را صفر نمی‌کند. سابقه‌ی روزانه تا ۶۲ روز می‌ماند.</p>
      ${ts.length ? html`<table class="t"><thead><tr><th>تانل</th><th class="num">امروز</th><th class="num">این ماه</th><th class="num">از ابتدا</th><th></th></tr></thead><tbody>${ts.map((t) => html`<tr><td>${t.name}</td><td class="num">${fmtBytes(t.today.rx + t.today.tx)}</td><td class="num">${fmtBytes(t.month.rx + t.month.tx)}</td><td class="num">${fmtBytes(t.total.rx + t.total.tx)}</td>
        <td style="text-align:end"><button class="btn sm ghost" data-act="reset-traffic" data-name="${t.name}">صفر کردن</button></td></tr>`)}</tbody></table>` : html`<div class="muted">تانلی نیست.</div>`}
      <div class="form-foot"><button class="btn danger" data-act="reset-traffic" data-name="">صفر کردن آمار همه‌ی تانل‌ها</button></div>
    </div></div>`;
  }

  function setMaint() {
    return html`<div class="panel"><header><h2>پشتیبان</h2></header><div class="in stack">
        <p class="help" style="margin:0">همه‌ی تنظیمات، سرورها، کلیدها و رمزهای تانل‌ها در یک فایل JSON. آن را جای امن نگه دارید.</p>
        <div><a class="btn" href="api/backup" download>${I.download(16)} دانلود پشتیبان</a></div></div></div>
      <div class="panel"><header><h2>سرویس</h2></header><div class="in stack">
        <p class="help" style="margin:0">ری‌استارت سرویس، تانل‌های کرنلی را قطع نمی‌کند؛ لایه‌ی NeoObfs و رله‌ها چند ثانیه قطع می‌شوند.</p>
        <div class="row"><button class="btn" data-act="restart">${I.sync(16)} ری‌استارت سرویس</button><button class="btn" data-act="sync-all">همگام‌سازی همه‌ی سرورها</button></div></div></div>
      <div class="panel"><header><h2>حذف NeoTunnel</h2></header><div class="in stack">
        <p class="help" style="margin:0">این دستور سرویس، تانل‌ها، IPsec، قوانین فوروارد و مسیریابی، تنظیمات، کلیدها و خود برنامه را از سرور پاک می‌کند. با <span class="ltr">--keep-config</span> تنظیمات برای نصب دوباره می‌ماند.</p>
        ${codeBlock("حذف کامل (روی همین سرور اجرا کنید)", "sudo neotunnel uninstall", "")}
        ${codeBlock("حذف و نگه‌داشتن تنظیمات", "sudo neotunnel uninstall --keep-config", "")}</div></div>`;
  }

  function setAbout() {
    const ov = S.ov;
    return html`<div class="panel"><header><h2>درباره</h2></header><div class="in"><dl class="kv">
      <dt>نسخه</dt><dd class="ltr">${ov.node.version} ${ov.node.arch}</dd><dt>اثر انگشت کلید سرور</dt><dd class="ltr">${ov.node.key}</dd>
      <dt>روشن بودن سرور</dt><dd>${fmtUptime(ov.node.uptime_s)}</dd><dt>کد منبع</dt><dd><a href="${ov.repo}" target="_blank" rel="noopener" class="ltr">${ov.repo.replace("https://", "")}</a></dd></dl></div></div>`;
  }

  async function loadSystem() {
    try { S.system = await api("GET", "system"); if (S.route === "settings" && S.sec === "net") draw(); } catch (e) { toast(e.message, "bad"); }
  }

  // ---------- modals ----------
  function drawModal() {
    const root = $("#modal-root");
    const open = $(".overlay", root);
    if (!S.modal) {
      // Leave the way it came, quickly; then drop it.
      if (open && !open.classList.contains("closing")) {
        open.classList.add("closing");
        setTimeout(() => { if (!S.modal) root.textContent = ""; }, reduceMotion ? 0 : 140);
      }
      return;
    }
    if (open && open.classList.contains("closing")) root.textContent = "";
    const first = !$(".overlay", root);
    morph(root, part(html`<div class="overlay" data-act="close-overlay"><div class="modal" role="dialog" aria-modal="true">${S.modal.view()}</div></div>`));
    if (first) { const f = $(".modal input, .modal select, .modal button.primary"); if (f) f.focus(); }
  }
  const openModal = (view, opts = {}) => { S.modal = Object.assign({ view }, opts); drawModal(); };
  const closeModal = () => { S.modal = null; S.wiz = null; drawModal(); draw(); };
  const modalHead = (title, extra = "") => html`<header><h3>${title}</h3>${extra}<button class="btn sm ghost icon" data-act="close" style="margin-inline-start:${extra ? "0" : "auto"}" aria-label="بستن">${I.x(16)}</button></header>`;

  function setupView(s, intro) {
    const where = `${s.peer} (${s.address})`;
    return html`${modalHead("دستور اتصال " + s.peer)}
      <div class="body">
        ${intro || (s.paired ? html`<div class="note ok">این سرور متصل است و تغییرات خودکار روی آن اعمال می‌شود. اگر روزی در دسترس نبود، دستور زیر را روی <b>${where}</b> اجرا کنید تا همه‌چیز یکجا اعمال شود.</div>`
          : html`<div class="note warn">این سرور هنوز وصل نشده. یکی از دستورهای زیر را روی <b>${where}</b> با دسترسی root اجرا کنید؛ بعد از آن هر تغییری خودکار روی آن هم اعمال می‌شود.</div>`)}
        ${s.install ? codeBlock("۱. نصب از همین سرور و اعمال تنظیمات (پیشنهادی)", s.install, s.peer) : ""}
        ${s.github ? codeBlock("۲. یا نصب از گیت‌هاب و اعمال تنظیمات", s.github, s.peer) : ""}
        ${codeBlock(s.install ? "۳. اگر NeoTunnel از قبل نصب است" : "اعمال همه‌ی تنظیمات (همگام‌سازی دستی)", s.apply, s.peer)}
        <div class="help">کلید اتصال (NT1.…) با کلید این سرور امضا شده و رمزهای تانل‌ها را هم دارد؛ آن را فقط روی همان سرور اجرا کنید.</div>
      </div>
      <footer><span class="grow"></span><button class="btn primary" data-act="close">بستن</button></footer>`;
  }

  async function showSetup(name) {
    try { const s = await api("GET", `peers/${encodeURIComponent(name)}/setup`); openModal(() => setupView(s)); }
    catch (e) { toast(e.message, "bad"); }
  }

  function reportChange(ch, okMsg) {
    const failed = (ch.peers || []).filter((p) => !p.ok);
    (ch.errors || []).forEach((e) => toast(e, "bad"));
    (ch.peers || []).filter((p) => p.ok).forEach((p) => toast(`${p.peer} به‌روز شد`));
    if (failed.length && failed[0].setup) {
      const p = failed[0], notPaired = /not paired/.test(p.error || "");
      openModal(() => setupView(p.setup, html`<div class="note ${notPaired ? "warn" : "bad"}">${okMsg ? html`<b>${okMsg}.</b> ` : ""}${notPaired
        ? html`سرور <b>${p.peer}</b> هنوز وصل نشده. دستور زیر را روی آن اجرا کنید تا آن طرف تانل هم ساخته شود.`
        : html`سرور <b>${p.peer}</b> در دسترس نبود (<span class="ltr small">${p.error}</span>). با اجرای دستور زیر روی آن، همه‌ی تغییرات یکجا اعمال می‌شود.`}</div>`));
      return;
    }
    if (okMsg) toast(okMsg);
  }

  // Tunnel wizard: destination → protocol → protection → ports and create.
  // With a connected server every choice is checked live: the same stack is
  // brought up on both ends for a few seconds to see that traffic passes.
  const WIZ_STEPS = ["مقصد", "پروتکل", "امنیت و پورت", "فوروارد و ساخت"];
  const SIT_MODES = ["6to4", "6to4-gre"];

  function wizardView() {
    const w = S.wiz;
    const anim = w.anim ? ` enter-${w.anim}` : "";
    const stepper = html`<ol class="stepper" aria-label="مرحله ${w.step} از ${WIZ_STEPS.length}">${WIZ_STEPS.map((l, i) => html`<li class="${w.step === i + 1 ? "now" : w.step > i + 1 ? "done" : ""}"><i>${w.step > i + 1 ? I.check(12) : i + 1}</i><span>${l}</span></li>`)}</ol>`;
    const body = [wizDest, wizProto, wizProtect, wizFinish][w.step - 1](w);
    const canNext = wizCanNext(w);
    return html`${modalHead("تانل جدید")}
      ${stepper}
      <div class="body wiz${anim}">${body}</div>
      <footer>${w.step > 1 ? html`<button class="btn ghost" data-act="wiz-back">قبلی</button>` : ""}<span class="grow"></span>
        ${w.step < WIZ_STEPS.length ? html`<button class="btn primary" data-act="wiz-next" ${canNext ? "" : "disabled"}>بعدی</button>`
          : html`<button class="btn primary" data-act="wiz-create" ${w.busy ? "disabled" : ""}>${w.busy ? "در حال ساخت…" : html`${I.check(16)} ساخت تانل`}</button>`}</footer>`;
  }

  // The step transition class stays until it has played, so redraws from
  // live checks in the meantime do not cut it off.
  function stepAnim(w, dir) {
    w.anim = dir;
    clearTimeout(w.animTimer);
    w.animTimer = setTimeout(() => { w.anim = ""; }, 260);
  }
  const wizPeer = (w) => (w.dest === "peer" ? peer(w.peer) : null);
  const canCheck = (w) => { const p = wizPeer(w); return !!(p && p.paired); };

  function wizDest(w) {
    const ov = S.ov;
    return html`<p class="lead">تانل بین این سرور و کجا ساخته شود؟</p>
      <div class="choices">
        ${ov.peers.map((p) => html`<button type="button" class="choice ${w.dest === "peer" && w.peer === p.name ? "on" : ""}" data-act="wiz-dest" data-dest="peer" data-name="${p.name}">
          <b>${I.server(16)} ${p.name}</b><span class="ltr">${p.address}</span><span class="state"><i class="dot ${p.paired ? "ok" : "warn"}"></i>${p.paired ? "متصل؛ هر انتخاب زنده بررسی می‌شود" : "منتظر اتصال"}</span></button>`)}
        <button type="button" class="choice ${w.dest === "new" ? "on" : ""}" data-act="wiz-dest" data-dest="new"><b>${I.plus(16)} سرور جدید</b><span>سرور خارج که NeoTunnel روی آن نصب می‌شود</span></button>
        <button type="button" class="choice ${w.dest === "remote" ? "on" : ""}" data-act="wiz-dest" data-dest="remote"><b>${I.globe(16)} دستگاه دیگر</b><span>مثلاً میکروتیک؛ فقط این طرف ساخته می‌شود</span></button>
      </div>
      ${w.dest === "new" ? html`<div class="grid2"><div class="field"><label for="w-nn">نام سرور</label><input id="w-nn" class="input ltr" data-bind="wiz.newName" value="${w.newName}" placeholder="germany"><span class="help">حروف کوچک انگلیسی، عدد و خط تیره</span></div>
        <div class="field"><label for="w-ip">IP عمومی سرور</label><input id="w-ip" class="input ltr" data-bind="wiz.newIP" value="${w.newIP}" placeholder="203.0.113.10"></div></div>
        <div class="note">بعد از ساخت، یک دستور یک‌خطی برای نصب روی آن سرور می‌گیرید. چون آن طرف هنوز نصب نیست، بررسی زنده‌ی اتصال در این حالت ممکن نیست.</div>` : ""}
      ${w.dest === "remote" ? html`<div class="field"><label for="w-rm">IP دستگاه مقابل</label><input id="w-rm" class="input ltr" data-bind="wiz.remote" value="${w.remote}" placeholder="203.0.113.10"><span class="help">آن طرف را خودتان (مثلاً روی میکروتیک) با همان نوع تانل تنظیم کنید.</span></div>` : ""}`;
  }

  function wizProto(w) {
    const ov = S.ov, v6 = w.underlay === "ipv6", p6 = wizPeer(w);
    const ok = (m) => !(w.dest === "remote" && m.peer) && !(v6 && SIT_MODES.includes(m.name));
    const groups = [["لایه ۳، ترافیک IP", (m) => m.layer === 3 && !m.crypt && !m.tcp], ["رمزدار", (m) => m.crypt], ["لایه ۲، Ethernet", (m) => m.layer === 2], ["آخرین راه، وقتی فقط TCP رد می‌شود", (m) => m.tcp]];
    const card = (m) => {
      const pi = PROTO[m.name] || {}, c = w.checks[checkKey(w, m.name, false)];
      return html`<button type="button" class="choice ${w.mode === m.name ? "on" : ""}" data-act="wiz-mode" data-mode="${m.name}">
        <b>${pi.t || m.title}${pi.rec && !v6 ? html` <span class="tag lock">پیشنهادی</span>` : ""}</b>
        <span>${pi.best || ""}</span>
        <span class="facts"><span class="tag">سربار ${pi.over || "?"} بایت</span>${m.udp ? html`<span class="tag">UDP</span>` : ""}${m.tcp ? html`<span class="tag warn">TCP · کند با افت بسته</span>` : ""}${m.crypt ? html`<span class="tag lock">${I.lock(11)} رمزدار</span>` : ""}</span>
        ${checkChip(c)}</button>`;
    };
    const sel = PROTO[w.mode];
    return html`<div class="field"><span class="lbl">بستر اتصال</span>
        <div class="seg" role="radiogroup">${[["ipv4", "IPv4"], ["ipv6", "IPv6"]].map(([v, l]) => html`<button type="button" role="radio" aria-checked="${w.underlay === v}" class="${w.underlay === v ? "on" : ""}" data-act="wiz-underlay" data-value="${v}">${l}</button>`)}</div>
        ${v6 ? html`<div class="grid2" style="margin-top:10px">
            <div class="field"><label for="w-l6">IPv6 این سرور</label><input id="w-l6" class="input ltr" data-bind="wiz.local6" value="${w.local6}" placeholder="2001:db8::1"><span class="help">${ov.node.address6 ? "خودکار پیدا شد." : "این سرور IPv6 عمومی ندارد یا پیدا نشد."}</span></div>
            <div class="field"><label for="w-r6">IPv6 مقصد</label><input id="w-r6" class="input ltr" data-bind="wiz.remote6" value="${w.remote6}" placeholder="2001:db8::2"><span class="help">${p6 && p6.address6 ? "از خود سرور مقصد گرفته شد." : "IPv6 عمومی سرور مقصد."}</span></div></div>
          <span class="help">وقتی مسیر IPv4 شلوغ یا محدود است. GRE و IPIP به نسخه‌ی IPv6 خود تبدیل می‌شوند؛ 6to4 که خودش روی IPv4 است اینجا نیست.</span>`
        : html`<span class="help">بیشتر سرورها فقط IPv4 دارند؛ اگر هر دو سرور IPv6 دارند، IPv6 هم امتحان کنید.</span>`}</div>
      <div class="guide">${I.alert(15)}<div><b>چه انتخاب کنم؟</b> اگر مطمئن نیستید <b>${v6 ? "GRE" : "6to4"}</b> را انتخاب کنید. اگر وصل نشد، در مرحله‌ی بعد آن را روی یک پورت UDP اجرا کنید یا WireGuard را امتحان کنید.
        ${canCheck(w) && !v6 ? html`<div style="margin-top:8px"><button type="button" class="btn sm" data-act="wiz-check-all" ${w.checkingAll ? "disabled" : ""}>${w.checkingAll ? html`<i class="spin"></i> در حال بررسی همه…` : html`${I.gauge(14)} بررسی سریع همه`}</button> <span class="faint small">همه با هم، حدود ۱۵ ثانیه</span></div>` : ""}</div></div>
      ${groups.map(([title, f]) => { const ms = ov.modes.filter(ok).filter(f); return ms.length ? html`<div class="group"><span class="lbl">${title}</span><div class="choices">${ms.map(card)}</div></div>` : ""; })}
      ${(() => { const c = w.checks[checkKey(w, w.mode, false)]; return c && c.state === "bad" ? html`<div class="note ${c.cfg ? "warn" : "bad"}"><b>${PROTO[w.mode] ? PROTO[w.mode].t : w.mode}:</b> ${c.cfg ? "" : "ترافیک از این مسیر رد نشد. "}<span class="ltr small">${c.err || ""}</span></div>` : ""; })()}
      ${sel ? html`<div class="proto-info"><div><span class="k">لایه و محتوا</span>لایه ${sel.layer}، ${sel.carry}</div><div><span class="k">سربار</span>${sel.over} بایت</div>
        <div><span class="k">رمزنگاری</span>${sel.crypt}</div><div><span class="k">میکروتیک</span>${sel.mt}</div></div>
        <p class="help" style="margin:0">${sel.d}</p>` : ""}
      ${sel && sel.tcp ? html`<div class="note warn"><b>TCP آخرین راه است.</b> ترافیک کاربران (که خودش بیشتر TCP است) داخل یک اتصال TCP دیگر می‌رود؛ با هر بسته‌ی گم‌شده کل تانل منتظر ارسال دوباره می‌ماند و سرعت در مسیرِ با افت بسته خیلی پایین می‌آید. اول 6to4، GRE یا یک پروتکل روی UDP را امتحان کنید؛ اگر هیچ‌کدام رد نشد، TCP را انتخاب کنید.</div>` : ""}
      <div class="field"><label for="w-name">نام تانل (اختیاری)</label><input id="w-name" class="input ltr" data-bind="wiz.name" value="${w.name}" placeholder="t1" maxlength="8" style="max-width:220px"><span class="help">خالی بگذارید تا خودکار انتخاب شود.</span></div>`;
  }

  function wizProtect(w) {
    const ov = S.ov, mode = ov.modes.find((m) => m.name === w.mode) || {};
    const canProtect = !mode.crypt && w.dest !== "remote";
    const pi = PROTO[w.mode] || {}, v6 = w.underlay === "ipv6";
    const fou = !mode.udp && !mode.tcp && !["l2tpv3"].includes(w.mode);
    if (mode.tcp) {
      // The TCP transport seals its own frames; NeoObfs (UDP) can't go under it.
      const p = wizProt(w, mode);
      return html`${checkBanner(w)}
      <div class="field"><span class="lbl">محافظت</span><div class="choices">
          <button type="button" class="choice ${p === "none" ? "on" : ""}" data-act="wiz-protect" data-value="none"><b>بدون رمزنگاری</b><span>سریع‌ترین. اتصال با رمز مشترک تانل احراز هویت می‌شود، ولی بسته‌ها خوانا می‌روند؛ برای ترافیکی که خودش رمز شده (TLS، Xray) کافی است.</span></button>
          <button type="button" class="choice ${p === "ipsec" ? "on" : ""}" data-act="wiz-protect" data-value="ipsec"><b>${I.lock(15)} رمزنگاری جریان</b><span>هر قاب با کلید تازه‌ی همان اتصال مهر می‌شود؛ ۱۶ بایت سربار.</span></button>
        </div>
        ${p === "ipsec" ? html`<select class="input" data-bind="wiz.encrypt" style="max-width:300px;margin-top:10px" aria-label="الگوریتم">${ov.ciphers.map((c) => html`<option value="${c.name}" ${w.encrypt === c.name ? "selected" : ""}>${c.title}</option>`)}</select>` : ""}</div>
      <div class="field"><label for="w-port">پورت TCP</label>
        <input id="w-port" class="input ltr" type="number" min="1" max="65535" data-bind="wiz.port" value="${w.port}" placeholder="خودکار" style="max-width:220px">
        <span class="help">هر دو سرور روی این پورت گوش می‌دهند و هر کدام که بتواند به دیگری وصل می‌شود. اگر فقط پورت‌های خاصی (مثلاً 443) بین دو سرور باز است، همان را بدهید؛ پورت نباید روی هیچ‌کدام از دو سرور در استفاده باشد.</span></div>`;
    }
    return html`${checkBanner(w)}
      ${canProtect ? html`<div class="field"><span class="lbl">محافظت</span><div class="choices">
          <button type="button" class="choice ${w.protect === "none" ? "on" : ""}" data-act="wiz-protect" data-value="none"><b>بدون رمزنگاری</b><span>سریع‌ترین. برای ترافیکی که خودش رمز شده (TLS، Xray) کافی است.</span></button>
          <button type="button" class="choice ${w.protect === "ipsec" ? "on" : ""}" data-act="wiz-protect" data-value="ipsec"><b>${I.lock(15)} IPsec در کرنل</b><span>AES-GCM با سرعت کرنل؛ ۳۸ بایت سربار. ترافیک بین دو سرور خوانا نیست.</span></button>
          <button type="button" class="choice ${w.protect === "obfs" ? "on" : ""}" data-act="wiz-protect" data-value="obfs"><b>${I.mask(15)} NeoObfs</b><span>رمز با XChaCha20 داخل UDP؛ کندتر از کرنل چون در فضای کاربر اجرا می‌شود.</span></button>
        </div>
        ${w.protect === "ipsec" ? html`<select class="input" data-bind="wiz.encrypt" style="max-width:300px;margin-top:10px" aria-label="الگوریتم">${ov.ciphers.map((c) => html`<option value="${c.name}" ${w.encrypt === c.name ? "selected" : ""}>${c.title}</option>`)}</select>` : ""}</div>`
        : html`<div class="note">${mode.crypt ? `${pi.t || w.mode} خودش رمزنگاری می‌کند؛ لایه‌ی دیگری لازم نیست.` : "برای دستگاه دیگر، رمزنگاری را با WireGuard انجام دهید."}</div>`}
      <div class="field"><label for="w-port">پورت UDP ${mode.udp || w.protect === "obfs" ? "" : "(اختیاری)"}</label>
        <input id="w-port" class="input ltr" type="number" min="1" max="65535" data-bind="wiz.port" value="${w.port}" placeholder="${mode.udp || w.protect === "obfs" ? "خودکار" : "خالی = پروتکل خام"}" style="max-width:220px" ${v6 && fou && w.protect !== "obfs" ? "disabled" : ""}>
        <span class="help">${v6 && fou && w.protect !== "obfs" ? "روی IPv6، GRE و IPIP بدون پورت اجرا می‌شوند." : mode.udp || w.protect === "obfs" ? "پورتی که دو سر روی آن گوش می‌دهند." : "خالی بهترین سرعت را دارد. اگر مسیر پروتکل خام را رد نمی‌کند، یک پورت UDP بدهید تا تانل داخل UDP برود."}</span></div>`;
  }

  // The protection that applies to the chosen mode: a TCP tunnel can't run
  // over NeoObfs and encrypts its own stream instead of using IPsec.
  function wizProt(w, mode) {
    return mode.tcp && w.protect === "obfs" ? "none" : w.protect;
  }

  function wizFinish(w) {
    const ov = S.ov, pi = PROTO[w.mode] || {};
    const mode = ov.modes.find((m) => m.name === w.mode) || {};
    const prot = { none: "بدون رمزنگاری", ipsec: mode.tcp ? cipherTitle(w.encrypt) : `IPsec ${cipherTitle(w.encrypt)}`, obfs: "NeoObfs" }[wizProt(w, mode)];
    return html`${checkBanner(w)}
      <dl class="summary">
        <div><dt>مقصد</dt><dd>${w.dest === "peer" ? w.peer : w.dest === "new" ? w.newName : html`<span class="ltr">${w.remote}</span>`}</dd></div>
        <div><dt>پروتکل</dt><dd>${pi.t || w.mode}${w.underlay === "ipv6" ? " روی IPv6" : ""}</dd></div>
        <div><dt>محافظت</dt><dd>${mode.crypt ? "رمزنگاری خودِ پروتکل" : prot}</dd></div>
        <div><dt>پورت</dt><dd class="ltr">${w.port ? `${mode.tcp ? "TCP" : "UDP"} ${w.port}` : mode.udp || mode.tcp ? "خودکار" : "پروتکل خام"}</dd></div>
      </dl>
      <div class="grid2">
        <div class="field"><label for="w-fwd">فوروارد پورت‌های این سرور (اختیاری)</label><input id="w-fwd" class="input ltr" data-bind="wiz.fwd" value="${w.fwd}" placeholder="443, 8443, 2053-2096"><span class="help">ترافیک این پورت‌ها از داخل تانل به مقصد می‌رود.</span></div>
        <div class="field"><label for="w-to">پورت مقصد (اختیاری)</label><input id="w-to" class="input ltr" type="number" data-bind="wiz.fwdTo" value="${w.fwdTo}" placeholder="همان پورت"><span class="help">مثلاً 2053 اینجا به 443 آن طرف.</span></div>
        <div class="field"><span class="lbl">پروتکل فوروارد</span><div class="seg">${["both", "tcp", "udp"].map((p) => html`<button type="button" class="${w.proto === p ? "on" : ""}" data-act="set" data-path="wiz.proto" data-value="${p}">${p === "both" ? "TCP + UDP" : p.toUpperCase()}</button>`)}</div></div>
        <div class="field"><span class="lbl">IP کاربر</span><div class="seg">${Object.entries(MODES_FWD).filter(([v]) => w.dest !== "remote" || v !== "transparent").map(([v, [l]]) => html`<button type="button" class="${w.fmode === v ? "on" : ""}" data-act="set" data-path="wiz.fmode" data-value="${v}">${l}</button>`)}</div></div>
        <div class="full help">${MODES_FWD[w.fmode][1]}</div>
        ${w.fmode === "proxy" ? html`<div class="field"><span class="lbl">نسخه‌ی PROXY protocol</span><div class="seg">${["2", "1"].map((v) => html`<button type="button" class="${String(w.pver) === v ? "on" : ""}" data-act="set" data-path="wiz.pver" data-value="${v}">v${v}</button>`)}</div></div>` : ""}
      </div>
      <details class="adv" ${w.adv ? "open" : ""} data-act="adv-toggle" data-name="">
        <summary>تنظیمات پیشرفته <span class="faint">MTU، TTL، DSCP${wizProt(w, mode) === "obfs" || w.mode === "obfs" ? "، padding" : ""}</span>${I.chev(16)}</summary>
        <div class="in">${advancedFields("wiz", w, wizProt(w, mode) === "obfs" ? "obfs" : w.mode, false, true, w.mode)}</div>
      </details>`;
  }

  // The live check of the current choices.
  function checkKey(w, mode, opts) {
    const k = { mode, u: w.underlay, l6: w.underlay === "ipv6" ? w.local6 : "", r6: w.underlay === "ipv6" ? w.remote6 : "" };
    if (opts) Object.assign(k, { p: w.protect, e: w.protect === "ipsec" ? w.encrypt : "", port: +w.port || 0 });
    return JSON.stringify(k);
  }
  function checkChip(c) {
    if (!c) return "";
    if (c.state === "run") return html`<span class="chk run"><i class="spin"></i>بررسی اتصال…</span>`;
    if (c.state === "skip") return html`<span class="chk run">بعد از ساخت تست می‌شود</span>`;
    if (c.state === "ok") return html`<span class="chk ok">${I.check(12)} وصل شد · ${ms(c.ping)}</span>`;
    return html`<span class="chk ${c.cfg ? "warn" : "bad"}" title="${c.err || ""}">${I.x(12)} ${c.cfg ? "با تنظیمات فعلی ساخته نمی‌شود" : "وصل نشد"}</span>`;
  }
  function checkBanner(w) {
    if (!canCheck(w)) return "";
    const c = w.checks[checkKey(w, w.mode, true)];
    if (!c || c.state === "run") return html`<div class="check run"><i class="spin"></i><div><b>بررسی اتصال با این تنظیمات…</b><span>تانل چند ثانیه روی هر دو سرور بالا می‌آید و پاک می‌شود.</span></div></div>`;
    if (c.state === "skip") return html`<div class="check">${I.mask(16)}<div><b>NeoObfs پیش از ساخت سنجیده نمی‌شود</b><span>در فضای کاربر اجرا می‌شود؛ بعد از ساخت، با «تست سرعت» همان تانل بررسی کنید.</span></div></div>`;
    if (c.state === "ok") return html`<div class="check ok">${I.check(16)}<div><b>با این تنظیمات وصل می‌شود</b><span>پینگ ${ms(c.ping)}${c.loss ? `، افت ${c.loss.toFixed(0)}٪` : ""}${c.mtu ? `، MTU ${c.mtu}` : ""}</span></div></div>`;
    if (c.cfg) return html`<div class="check bad">${I.alert(16)}<div><b>این ترکیب ساخته نمی‌شود</b><span class="ltr">${c.err || ""}</span></div></div>`;
    return html`<div class="check bad">${I.alert(16)}<div><b>با این تنظیمات ترافیک رد نمی‌شود</b><span class="ltr">${c.err || ""}</span><span>${w.mode === "tcp" ? "پورت TCP دیگری بدهید که بین دو سرور باز است." : +w.port ? "پروتکل دیگری امتحان کنید یا NeoObfs را بزنید." : "یک پورت UDP بدهید یا پروتکل دیگری امتحان کنید."}</span></div></div>`;
  }

  function wizRequest(w, peerName) {
    const set = settingsBody(w);
    const prot = wizProt(w, S.ov.modes.find((m) => m.name === w.mode) || {});
    const req = Object.assign(set, {
      mode: w.mode, name: w.name.trim(), port: +w.port || 0, forward: w.fwd.trim(), forward_to: +w.fwdTo || 0, proto: w.proto,
      forward_mode: w.fmode, proxy_version: w.fmode === "proxy" ? +w.pver : 0,
      obfs: prot === "obfs", encrypt: prot === "ipsec" ? w.encrypt : "",
    });
    if (w.underlay === "ipv6") Object.assign(req, { underlay: "ipv6", local6: w.local6.trim(), remote6: w.remote6.trim() });
    if (w.dest === "remote") req.remote = w.remote.trim(); else req.peer = peerName;
    return req;
  }

  // Run a check unless its answer is known; repaint only the open modal.
  async function runCheck(opts, tries = 0) {
    const w = S.wiz;
    if (!w || !canCheck(w) || !w.mode) return;
    if (w.underlay === "ipv6" && (!w.local6 || !w.remote6)) return;
    const key = checkKey(w, w.mode, opts);
    if (w.checks[key] && w.checks[key].state !== "run") return;
    w.checks[key] = { state: "run" }; drawModal();
    const req = wizRequest(w, w.peer);
    if (!opts) Object.assign(req, { obfs: false, encrypt: "", port: 0 });
    req.forward = "";
    try {
      const r = ((await api("POST", "check", req)) || [])[0] || {};
      w.checks[key] = r.status === "passed" ? { state: "ok", ping: r.ping_ms, loss: r.loss, mtu: r.mtu } : r.status === "skipped" ? (r.error && /NeoObfs/.test(r.error) ? { state: "skip" } : { state: "bad", err: r.error, cfg: true }) : { state: "bad", err: r.error };
    } catch (e) {
      if (/در حال اجراست/.test(e.message) && tries < 8) { setTimeout(() => runCheck(opts, tries + 1), 1500); return; }
      w.checks[key] = { state: "bad", err: e.message, cfg: true };
    }
    if (S.wiz === w) drawModal();
  }
  let checkTimer = 0;
  const checkSoon = (opts) => { clearTimeout(checkTimer); checkTimer = setTimeout(() => runCheck(opts), 500); };

  async function checkAll() {
    const w = S.wiz;
    if (!w || !canCheck(w) || w.checkingAll) return;
    w.checkingAll = true;
    for (const m of S.ov.modes) { const k = checkKey(w, m.name, false); if (!w.checks[k]) w.checks[k] = { state: "run" }; }
    drawModal();
    try {
      const rs = (await api("POST", "check", { peer: w.peer, mode: "6to4", all: true })) || [];
      for (const r of rs) {
        if (/-udp$/.test(r.name)) continue; // the UDP variants answer a different question
        const k = checkKey(w, r.mode || r.name, false);
        w.checks[k] = r.status === "passed" ? { state: "ok", ping: r.ping_ms, loss: r.loss, mtu: r.mtu } : { state: "bad", err: r.error, cfg: r.status === "skipped" };
      }
    } catch (e) { toast(e.message, "bad"); }
    for (const k of Object.keys(w.checks)) if (w.checks[k].state === "run") delete w.checks[k];
    w.checkingAll = false;
    if (S.wiz === w) drawModal();
  }

  function wizCanNext(w) {
    if (w.step === 1) return !!((w.dest === "peer" && w.peer) || (w.dest === "new" && w.newName && w.newIP) || (w.dest === "remote" && w.remote));
    if (w.step === 2) return !!w.mode && (w.underlay !== "ipv6" || !!(w.local6 && w.remote6));
    return true;
  }

  function openWizard(preset = {}) {
    const ov = S.ov, d = ov.defaults || {};
    S.wiz = Object.assign({
      step: 1, dest: ov.peers.length ? "peer" : "new", peer: ov.peers.length ? ov.peers[0].name : "", newName: "", newIP: "", remote: "",
      mode: "6to4", name: "", port: "", fwd: "", fwdTo: "", proto: "both", busy: false, anim: "",
      underlay: "ipv4", local6: ov.node.address6 || "", remote6: "", checks: {}, checkingAll: false,
      protect: d.obfs ? "obfs" : d.encrypt ? "ipsec" : "none", encrypt: d.encrypt || "aes-gcm-128",
      fmode: d.forward_mode || "nat", pver: 2, mtuMode: d.mtu_probe ? "probe" : "auto", mtu: "", ttl: "", dscp: "", noDF: false, padding: d.padding || "", keepalive: "", peer_key: "", adv: false,
    }, preset);
    if (preset.peer) { S.wiz.dest = "peer"; S.wiz.step = preset.mode ? 3 : 2; }
    const p = wizPeer(S.wiz); if (p && p.address6) S.wiz.remote6 = p.address6;
    openModal(wizardView);
    if (S.wiz.step === 2) runCheck(false);
    if (S.wiz.step >= 3) runCheck(true);
  }

  async function createTunnel() {
    const w = S.wiz;
    w.busy = true; drawModal();
    try {
      const set = settingsBody(w); // validates before anything is created
      wizRequest(w, w.peer); // validates before anything is created
      let peerName = w.peer;
      if (w.dest === "new") {
        await api("POST", "peers", { name: w.newName.trim(), address: w.newIP.trim() });
        peerName = w.newName.trim();
      }
      const ch = await api("POST", "tunnels", wizRequest(w, peerName));
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
      <div class="body"><p class="muted" style="margin:0">پنل یک دستور یک‌خطی می‌سازد که NeoTunnel را روی آن سرور نصب و به این سرور وصل می‌کند.</p>
        <div class="grid2"><div class="field"><label for="a-n">نام</label><input id="a-n" class="input ltr" data-bind="form.name" value="${f.name || ""}" placeholder="germany"></div>
          <div class="field"><label for="a-ip">IP عمومی</label><input id="a-ip" class="input ltr" data-bind="form.address" value="${f.address || ""}" placeholder="203.0.113.10"></div></div></div>
      <footer><span class="grow"></span><button class="btn ghost" data-act="close">انصراف</button><button class="btn primary" data-act="add-server-go">${I.plus(16)} افزودن سرور</button></footer>`;
  }

  function addForwardView() {
    const f = S.form, ts = visible().filter((t) => !t.managed);
    if (!f.tunnel && ts.length) f.tunnel = ts[0].name;
    const t = tunnel(f.tunnel), remote = t && !t.peer;
    return html`${modalHead("فوروارد پورت جدید")}
      <div class="body">${ts.length ? html`<div class="grid2">
          <div class="field full"><label for="f-t">از طریق تانل</label><select id="f-t" class="input" data-bind="form.tunnel" data-redraw="1">${ts.map((x) => html`<option value="${x.name}" ${f.tunnel === x.name ? "selected" : ""}>${x.name} به ${x.peer || x.far_ip} (${x.there4 || x.there6})</option>`)}</select></div>
          <div class="field"><label for="f-p">پورت(های) این سرور</label><input id="f-p" class="input ltr" data-bind="form.ports" value="${f.ports || ""}" placeholder="443, 2053-2096"></div>
          <div class="field"><label for="f-to">پورت مقصد (اختیاری)</label><input id="f-to" class="input ltr" type="number" data-bind="form.to" value="${f.to || ""}" placeholder="همان پورت"></div>
          <div class="field"><span class="lbl">پروتکل</span><div class="seg">${["both", "tcp", "udp"].map((p) => html`<button type="button" class="${(f.proto || "both") === p ? "on" : ""}" data-act="set" data-path="form.proto" data-value="${p}">${p === "both" ? "TCP + UDP" : p.toUpperCase()}</button>`)}</div></div>
          <div class="field"><span class="lbl">IP کاربر</span><div class="seg">${Object.entries(MODES_FWD).filter(([v]) => !remote || v !== "transparent").map(([v, [l]]) => html`<button type="button" class="${f.mode === v ? "on" : ""}" data-act="set" data-path="form.mode" data-value="${v}">${l}</button>`)}</div></div>
          <div class="full help">${MODES_FWD[f.mode][1]}</div>
          ${f.mode === "proxy" ? html`<div class="field"><span class="lbl">نسخه</span><div class="seg">${["2", "1"].map((v) => html`<button type="button" class="${String(f.pver) === v ? "on" : ""}" data-act="set" data-path="form.pver" data-value="${v}">v${v}</button>`)}</div></div>` : ""}
        </div>` : html`<div class="muted">اول یک تانل بسازید.</div>`}</div>
      <footer><span class="grow"></span><button class="btn ghost" data-act="close">انصراف</button><button class="btn primary" data-act="add-forward-go" ${ts.length ? "" : "disabled"}>${I.plus(16)} افزودن فوروارد</button></footer>`;
  }

  function confirmView(title, text, act, data, label) {
    return html`${modalHead(title)}<div class="body"><div>${text}</div></div>
      <footer><span class="grow"></span><button class="btn ghost" data-act="close">انصراف</button><button class="btn danger" data-act="${act}" ${raw(Object.entries(data).map(([k, v]) => `data-${k}="${esc(v)}"`).join(" "))}>${I.trash(16)} ${label}</button></footer>`;
  }

  // ---------- events ----------
  // data-bind paths: "wiz.port", "form.ports", "tset.<name>.mtu", "fwdEdit.<k>.mode", "defs.padding".
  function setPath(path, value) {
    const parts = path.split(".");
    const roots = { wiz: S.wiz, form: S.form, speed: S.speed, tset: S.tset, fwdEdit: S.fwdEdit, defs: S.defs };
    let obj = roots[parts[0]];
    if (!obj) return;
    if (parts[0] === "fwdEdit") { obj = obj[parts.slice(1, -1).join(".")]; if (!obj) return; obj[parts[parts.length - 1]] = value; return; }
    for (const p of parts.slice(1, -1)) { if (!obj[p]) obj[p] = {}; obj = obj[p]; }
    obj[parts[parts.length - 1]] = value;
  }

  document.addEventListener("input", (e) => {
    const b = e.target.dataset.bind;
    if (!b) { if (e.target.closest("#app form")) S.edit = true; return; }
    setPath(b, e.target.value);
    if (e.target.closest("form, .detail, table.t, .spane")) S.edit = true;
    const next = $('[data-act="wiz-next"]');
    if (next && S.wiz) next.disabled = !wizCanNext(S.wiz);
    if (S.wiz) {
      if (["wiz.port", "wiz.encrypt"].includes(b) && S.wiz.step >= 3) checkSoon(true);
      if (["wiz.local6", "wiz.remote6"].includes(b) && S.wiz.step === 2) checkSoon(false);
    }
  });
  document.addEventListener("change", (e) => {
    const t = e.target;
    if (t.dataset.bind) { setPath(t.dataset.bind, t.value); if (t.closest(".modal")) drawModal(); else if (!t.closest("form")) draw(); }
    if (t.dataset.act === "bench-only") {
      const only = S.speed.only.filter((x) => x !== t.value);
      S.speed.only = t.checked ? only.concat(t.value) : only;
    }
    if (t.dataset.act === "def-check") { S.defs[t.dataset.key] = t.checked; S.edit = true; }
    if (t.dataset.act === "st-check") { setPath(t.dataset.path, t.checked); if (!t.closest(".modal")) S.edit = true; }
  });
  document.addEventListener("toggle", (e) => {
    const d = e.target;
    if (!(d instanceof HTMLDetailsElement)) return;
    if (d.dataset.act === "log-toggle") { S.logOpen = d.open; return; }
    if (d.dataset.act !== "adv-toggle") return;
    if (d.dataset.name) { if (S.tset[d.dataset.name]) S.tset[d.dataset.name].adv = d.open; } else if (S.wiz) S.wiz.adv = d.open;
  }, true);

  document.addEventListener("submit", async (e) => {
    const form = e.target.dataset.form;
    if (!form) return;
    e.preventDefault();
    const d = Object.fromEntries(new FormData(e.target));
    try {
      if (form === "login") { await api("POST", "login", { user: d.user, password: d.password }); S.authed = true; await refresh(); refreshTraffic().then(draw); return; }
      if (form === "node") { await api("POST", "settings/node", { name: d.name.trim(), address: d.address.trim() }); toast("ذخیره شد"); }
      if (form === "user") { await api("POST", "settings/user", { user: d.user.trim() }); toast("نام کاربری عوض شد"); }
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
      if (form === "tunnel-settings") {
        const name = e.target.dataset.name, st = S.tset[name];
        const ch = await api("PATCH", "tunnels/" + encodeURIComponent(name), Object.assign(settingsBody(st), { port: +st.port || 0 }));
        delete S.tset[name];
        reportChange(ch, "تنظیمات تانل ذخیره شد");
      }
      S.edit = false; await refresh();
    } catch (err) { toast(err.message, "bad"); }
  });

  const actions = {
    theme() { S.theme = S.theme === "dark" ? "light" : "dark"; store("nt-theme", S.theme); document.documentElement.dataset.theme = S.theme; draw(); },
    async logout() { await api("POST", "logout").catch(() => {}); S.authed = false; S.ov = null; draw(); },
    copy(el) { copy(el.dataset.text); },
    close() { closeModal(); },
    "close-overlay"(el, e) { if (e.target === el) closeModal(); },
    set(el) {
      setPath(el.dataset.path, el.dataset.value);
      if (el.closest(".modal")) drawModal(); else { S.edit = el.dataset.path.startsWith("tset") || el.dataset.path.startsWith("defs") ? true : S.edit; draw(); }
    },
    drawer(el) {
      S.drawerEnter = !S.drawer;
      S.drawer = el.dataset.type === "dest" ? { type: "dest", key: el.dataset.key } : { type: "tunnel", name: el.dataset.name };
      draw(); const c = $(".drawer .btn"); if (c) c.focus();
      if (el.dataset.type === "tunnel") refreshTraffic().then(() => { if (S.drawer) draw(); });
    },
    // An item on the arc: ports open their forwards page, the rest the drawer.
    arc(el) {
      const type = el.dataset.type, key = el.dataset.key;
      if (type === "port") { location.hash = "#/ports"; return; }
      S.drawerEnter = !S.drawer;
      S.drawer = type === "dest" ? { type: "dest", key } : { type: "tunnel", name: key };
      draw(); const c = $(".drawer .btn"); if (c) c.focus();
      if (type === "tunnel") refreshTraffic().then(() => { if (S.drawer) draw(); });
    },
    "drawer-close"() { S.drawer = null; draw(); },
    "open-tunnel"(el) { S.drawer = null; S.open = el.dataset.name; location.hash = "#/tunnels"; draw(); },
    "toggle-tunnel"(el) { const n = el.dataset.name; S.open = S.open === n ? null : n; if (S.open !== n) delete S.tset[n]; S.edit = false; draw(); },
    dtab(el) { S.dtab[el.dataset.name] = el.dataset.tab; draw(); },
    "reset-tset"(el) { delete S.tset[el.dataset.name]; S.edit = false; draw(); },
    async mtu(el) {
      const n = el.dataset.name;
      toast("در حال اندازه‌گیری مسیر؛ تا یک دقیقه طول می‌کشد…");
      try { S.mtu[n] = await api("POST", `tunnels/${encodeURIComponent(n)}/mtu`); S.open = n; S.drawer = null; location.hash = "#/tunnels"; await refresh(); draw(); }
      catch (e) { toast(e.message, "bad"); }
    },
    wizard(el) { openWizard(el.dataset.peer ? { peer: el.dataset.peer, mode: el.dataset.mode || "", port: el.dataset.port || "" } : {}); if (el.dataset.peer && !el.dataset.mode) S.wiz.mode = "6to4"; S.drawer = null; drawModal(); },
    "wiz-dest"(el) {
      Object.assign(S.wiz, { dest: el.dataset.dest, peer: el.dataset.name || S.wiz.peer });
      const p = wizPeer(S.wiz); if (p && p.address6) S.wiz.remote6 = p.address6;
      drawModal();
    },
    "wiz-mode"(el) { S.wiz.mode = el.dataset.mode; drawModal(); runCheck(false); },
    "wiz-underlay"(el) {
      const w = S.wiz; w.underlay = el.dataset.value;
      if (w.underlay === "ipv6" && SIT_MODES.includes(w.mode)) w.mode = "gre";
      drawModal(); runCheck(false);
    },
    "wiz-protect"(el) { S.wiz.protect = el.dataset.value; drawModal(); runCheck(true); },
    "wiz-check-all"() { checkAll(); },
    "wiz-next"() { const w = S.wiz; w.step++; stepAnim(w, "next"); drawModal(); if (w.step === 2) runCheck(false); if (w.step === 3) runCheck(true); },
    "wiz-back"() { S.wiz.step--; stepAnim(S.wiz, "back"); drawModal(); },
    "wiz-create"() { createTunnel(); },
    setup(el) { showSetup(el.dataset.name); },
    "add-server"() { S.form = {}; openModal(addServerView); },
    async "add-server-go"() {
      try {
        const s = await api("POST", "peers", { name: (S.form.name || "").trim(), address: (S.form.address || "").trim() });
        await refresh(); openModal(() => setupView(s)); location.hash = "#/servers";
      } catch (e) { toast(e.message, "bad"); }
    },
    "add-forward"() { S.form = { proto: "both", mode: (S.ov.defaults && S.ov.defaults.forward_mode) || "nat", pver: 2 }; openModal(addForwardView); },
    async "add-forward-go"() {
      const f = S.form;
      try {
        const ch = await api("POST", "forwards", { tunnel: f.tunnel, ports: (f.ports || "").trim(), proto: f.proto || "both", to: +f.to || 0, mode: f.mode, proxy_version: f.mode === "proxy" ? +f.pver : 0 });
        closeModal(); await refresh(); reportChange(ch, "فوروارد اضافه شد");
      } catch (e) { toast(e.message, "bad"); }
    },
    "edit-fwd"(el) {
      const f = S.ov.forwards.find((x) => x.tunnel + "|" + x.ports === el.dataset.key);
      if (f) { S.fwdEdit[el.dataset.key] = { mode: f.mode, proxy_version: f.proxy_version || 2 }; S.edit = true; draw(); }
    },
    "cancel-fwd"(el) { delete S.fwdEdit[el.dataset.key]; S.edit = false; draw(); },
    async "save-fwd"(el) {
      const k = el.dataset.key, ed = S.fwdEdit[k], f = S.ov.forwards.find((x) => x.tunnel + "|" + x.ports === k);
      try {
        const ch = await api("POST", "forwards/update", { tunnel: f.tunnel, ports: f.ports, proto: f.proto, to: f.to || 0, mode: ed.mode, proxy_version: ed.mode === "proxy" ? +ed.proxy_version : 0 });
        delete S.fwdEdit[k]; S.edit = false; await refresh(); reportChange(ch, "حالت فوروارد عوض شد");
      } catch (e) { toast(e.message, "bad"); }
    },
    "del-forward"(el) { openModal(() => confirmView("حذف فوروارد", html`فوروارد پورت <b class="ltr">${el.dataset.ports}</b> روی تانل <b>${el.dataset.tunnel}</b> حذف شود؟`, "del-forward-go", { tunnel: el.dataset.tunnel, ports: el.dataset.ports }, "حذف فوروارد")); },
    async "del-forward-go"(el) {
      try { const ch = await api("POST", "forwards/remove", { tunnel: el.dataset.tunnel, ports: el.dataset.ports }); closeModal(); await refresh(); reportChange(ch, "فوروارد حذف شد"); }
      catch (e) { toast(e.message, "bad"); }
    },
    async "tunnel-power"(el) {
      const t = tunnel(el.dataset.name); if (!t) return;
      const l = Object.assign({}, t.limits, { disabled: !t.limits.disabled });
      try { const ch = await api("POST", `tunnels/${encodeURIComponent(t.name)}/limits`, l); await refresh(); reportChange(ch, l.disabled ? "تانل خاموش شد" : "تانل روشن شد"); }
      catch (e) { toast(e.message, "bad"); }
    },
    async "save-limits"(el) {
      const t = tunnel(el.dataset.name), st = S.tset[el.dataset.name]; if (!t || !st) return;
      const l = { disabled: t.limits.disabled, rate_mbit: Math.round(+st.rate || 0), quota_gb: +st.quota || 0 };
      try { const ch = await api("POST", `tunnels/${encodeURIComponent(t.name)}/limits`, l); S.edit = false; await refresh(); reportChange(ch, "محدودیت‌ها ذخیره شد"); }
      catch (e) { toast(e.message, "bad"); }
    },
    "del-tunnel"(el) { openModal(() => confirmView("حذف تانل", html`تانل <b>${el.dataset.name}</b> با لایه‌های زیرین و فورواردهایش، روی هر دو سرور حذف شود؟`, "del-tunnel-go", { name: el.dataset.name }, "حذف تانل")); },
    async "del-tunnel-go"(el) {
      try { const ch = await api("DELETE", "tunnels/" + encodeURIComponent(el.dataset.name)); closeModal(); S.open = null; S.drawer = null; await refresh(); reportChange(ch, "تانل حذف شد"); }
      catch (e) { toast(e.message, "bad"); }
    },
    "del-server"(el) { openModal(() => confirmView("حذف سرور", html`سرور <b>${el.dataset.name}</b> و تانل‌های این سرور به آن حذف شوند؟ روی خود آن سرور چیزی پاک نمی‌شود.`, "del-server-go", { name: el.dataset.name }, "حذف سرور")); },
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
      const n = el.dataset.name;
      if (S.upgrading[n]) return;
      S.upgrading[n] = true; draw();
      try { const r = await api("POST", `peers/${encodeURIComponent(n)}/upgrade`); toast(`${n} به ${r.version} ارتقا یافت و دوباره وصل شد`); }
      catch (e) { toast(e.message, "bad"); }
      delete S.upgrading[n]; await refresh();
    },
    test(el) { startJob("test", el.dataset.name); },
    bench(el) { S.speed.peer = el.dataset.name; startJob("bench", el.dataset.name, S.speed.only); },
    "run-test"() { startJob("test", S.speed.tunnel); },
    "run-bench"() { startJob("bench", S.speed.peer, S.speed.only); },
    async "cancel-job"() { if (S.job) await api("POST", `jobs/${S.job.id}/cancel`).catch(() => {}); },
    sec(el) { S.sec = el.dataset.sec; store("nt-settings", S.sec); S.edit = false; S.defs = null; draw(); },
    "goto-sec"(el, e) { e.preventDefault(); S.sec = el.dataset.sec; location.hash = "#/settings"; },
    async "save-defaults"() {
      const d = S.defs;
      try { await api("POST", "settings/defaults", { encrypt: d.encrypt, mtu_probe: !!d.mtu_probe, obfs: !!d.obfs, padding: +d.padding || 0, forward_mode: d.forward_mode === "nat" ? "" : d.forward_mode }); S.edit = false; S.defs = null; await refresh(); toast("پیش‌فرض‌ها ذخیره شد"); }
      catch (e) { toast(e.message, "bad"); }
    },
    async boost() {
      try { await api("POST", "system/boost"); S.system = null; toast("بهینه‌سازی اعمال شد"); loadSystem(); }
      catch (e) { toast(e.message, "bad"); }
    },
    restart() { openModal(() => confirmView("ری‌استارت سرویس", "سرویس NeoTunnel چند ثانیه دیگر دوباره بالا می‌آید و پنل خودکار وصل می‌شود.", "restart-go", {}, "ری‌استارت")); },
    async "restart-go"() {
      try { await api("POST", "system/restart"); closeModal(); toast("سرویس در حال ری‌استارت است…"); }
      catch (e) { toast(e.message, "bad"); }
    },
    "reset-traffic"(el) {
      const n = el.dataset.name;
      openModal(() => confirmView("صفر کردن آمار", n ? html`آمار ترافیک تانل <b>${n}</b> صفر شود؟` : "آمار ترافیک همه‌ی تانل‌ها صفر شود؟", "reset-traffic-go", { name: n }, "صفر کردن"));
    },
    async "reset-traffic-go"(el) {
      try { await api("POST", "traffic/reset", { tunnel: el.dataset.name }); closeModal(); await refreshTraffic(); await refresh(); toast("آمار صفر شد"); }
      catch (e) { toast(e.message, "bad"); }
    },
  };

  document.addEventListener("click", (e) => {
    const el = e.target.closest("[data-act]");
    if (!el || el.type === "checkbox" || el.tagName === "DETAILS") return;
    const fn = actions[el.dataset.act];
    if (fn) { if (el.tagName === "A" && el.dataset.act !== "goto-sec") e.preventDefault(); fn(el, e); }
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") { if (S.modal) closeModal(); else if (S.drawer) { S.drawer = null; draw(); } return; }
    // SVG nodes act as buttons.
    if ((e.key === "Enter" || e.key === " ") && e.target.matches && e.target.matches('g[role="button"]')) { e.preventDefault(); e.target.dispatchEvent(new MouseEvent("click", { bubbles: true })); }
  });

  window.addEventListener("hashchange", () => {
    S.route = location.hash.slice(2) || "dashboard";
    S.edit = false; S.drawer = null; S.defs = null;
    if (S.route === "settings") S.system = null;
    draw(); window.scrollTo(0, 0);
  });

  // ---------- boot ----------
  async function boot() {
    await refresh();
    if (S.authed) {
      await refreshTraffic();
      try { const jobs = await api("GET", "jobs"); if (jobs.length) { S.job = jobs[0]; pollJob(); } } catch { /* none */ }
      draw();
    }
    setInterval(() => { if (S.authed && !document.hidden) refresh(); }, 3000);
    setInterval(() => { if (S.authed && !document.hidden) refreshTraffic(); }, 30000);
  }
  boot();
})();
