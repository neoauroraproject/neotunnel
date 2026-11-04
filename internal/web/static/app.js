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
    eye: (s) => svg('<path d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7S2 12 2 12z"/><circle cx="12" cy="12" r="3"/>', s),
    eyeOff: (s) => svg('<path d="M3 3l18 18"/><path d="M10.6 5.1A10.4 10.4 0 0 1 12 5c6.4 0 10 7 10 7a17 17 0 0 1-3.2 4.1M6.6 6.6C3.8 8.4 2 12 2 12s3.6 7 10 7c1.7 0 3.2-.4 4.5-1.1"/><path d="M9.9 9.9a3 3 0 0 0 4.2 4.2"/>', s),
    home: (s) => svg('<circle cx="12" cy="12" r="3"/><circle cx="12" cy="12" r="8" stroke-dasharray="2 3"/><path d="M4 12H2M22 12h-2"/>', s),
    tunnel: (s) => svg('<circle cx="5" cy="12" r="2.5"/><circle cx="19" cy="12" r="2.5"/><path d="M7.5 12c2-4 7-4 9 0"/><path d="M7.5 12c2 4 7 4 9 0"/>', s),
    server: (s) => svg('<rect x="3.5" y="4" width="17" height="6.5" rx="2"/><rect x="3.5" y="13.5" width="17" height="6.5" rx="2"/><path d="M7 7.25h.01M7 16.75h.01M11 7.25h6M11 16.75h6"/>', s),
    ports: (s) => svg('<path d="M4 7h11"/><path d="m12 4 3 3-3 3"/><path d="M20 17H9"/><path d="m12 14-3 3 3 3"/>', s),
    gauge: (s) => svg('<path d="M4.5 17a8.5 8.5 0 1 1 15 0"/><path d="m12 13 4-5"/><circle cx="12" cy="13" r="1.4"/>', s),
    term: (s) => svg('<rect x="3" y="4" width="18" height="16" rx="2.5"/><path d="m7 9 3 3-3 3M12.5 15H17"/>', s),
    gear: (s) => svg('<path d="M4 6h10M18 6h2M4 12h4M12 12h8M4 18h12"/><circle cx="16" cy="6" r="2"/><circle cx="10" cy="12" r="2"/><circle cx="18" cy="18" r="2"/>', s),
    plus: (s) => svg('<path d="M12 5v14M5 12h14"/>', s),
    up: (s) => svg('<path d="m6 15 6-6 6 6"/>', s),
    down: (s) => svg('<path d="m6 9 6 6 6-6"/>', s),
    grip: (s) => svg('<circle cx="9" cy="6" r="1"/><circle cx="15" cy="6" r="1"/><circle cx="9" cy="12" r="1"/><circle cx="15" cy="12" r="1"/><circle cx="9" cy="18" r="1"/><circle cx="15" cy="18" r="1"/>', s),
    trash: (s) => svg('<path d="M4 7h16M10 11v6M14 11v6M6 7l1 12a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2l1-12M9 7V4h6v3"/>', s),
    copy: (s) => svg('<rect x="8" y="8" width="12" height="12" rx="2.5"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2"/>', s),
    play: (s) => svg('<path d="M7 5v14l11-7z"/>', s),
    sync: (s) => svg('<path d="M20 11a8 8 0 0 0-14.3-4.9L4 8"/><path d="M4 4v4h4"/><path d="M4 13a8 8 0 0 0 14.3 4.9L20 16"/><path d="M20 20v-4h-4"/>', s),
    moon: (s) => svg('<path d="M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z"/>', s),
    sun: (s) => svg('<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M2 12h2M20 12h2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>', s),
    out: (s) => svg('<path d="M14 4h4a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-4"/><path d="M9 8l-4 4 4 4M5 12h11"/>', s),
    check: (s) => svg('<path d="m5 12.5 4.5 4.5L19 7.5"/>', s),
    x: (s) => svg('<path d="M6 6l12 12M18 6 6 18"/>', s),
    arrow: (s) => svg('<path d="M19 12H5"/><path d="m11 6-6 6 6 6"/>', s),
    dice: (s) => svg('<rect x="4" y="4" width="16" height="16" rx="3"/><circle cx="9" cy="9" r="1.2" fill="currentColor"/><circle cx="15" cy="15" r="1.2" fill="currentColor"/><circle cx="15" cy="9" r="1.2" fill="currentColor"/><circle cx="9" cy="15" r="1.2" fill="currentColor"/>', s),
    edit: (s) => svg('<path d="M4 20h4L19 9l-4-4L4 16z"/><path d="m13.5 6.5 4 4"/>', s),
    alert: (s) => svg('<path d="M12 3 2 20h20z"/><path d="M12 10v4M12 17h.01"/>', s),
    lock: (s) => svg('<rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/>', s),
    mask: (s) => svg('<path d="M3 8c3-2 15-2 18 0-1 7-4 10-9 10S4 15 3 8z"/><path d="M8 11h2M14 11h2"/>', s),
    chev: (s) => svg('<path d="m6 9 6 6 6-6"/>', s),
    ruler: (s) => svg('<path d="M3 17 17 3l4 4L7 21z"/><path d="m7 13 2 2M10 10l2 2M13 7l2 2"/>', s),
    upgrade: (s) => svg('<path d="M12 16V4M7 9l5-5 5 5"/><path d="M4 16v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3"/>', s),
    power: (s) => svg('<path d="M12 3v8"/><path d="M6.3 6.6a8 8 0 1 0 11.4 0"/>', s),
    download: (s) => svg('<path d="M12 4v12M7 11l5 5 5-5"/><path d="M4 20h16"/>', s),
    globe: (s) => svg('<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c3 3.5 3 14.5 0 18M12 3c-3 3.5-3 14.5 0 18"/>', s),
    menu: (s) => svg('<path d="M4 7h16M4 12h16M10 17h10"/>', s),
    bolt: (s) => svg('<path d="M13 2 4.5 13.5H12L11 22l8.5-11.5H12z"/>', s),
    chart: (s) => svg('<path d="M4 20V10M10 20V4M16 20v-7M22 20H2"/>', s),
    clock: (s) => svg('<circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 2"/>', s),
    wave: (s) => svg('<path d="M2 12h3l2.5-6 4 12 3-9 2 3H22"/>', s),
    drop: (s) => svg('<path d="M12 3.5c3.5 4.5 6 7.7 6 10.5a6 6 0 0 1-12 0c0-2.8 2.5-6 6-10.5z"/>', s),
    cal: (s) => svg('<rect x="3.5" y="5" width="17" height="15" rx="2.5"/><path d="M3.5 10h17M8 3v4M16 3v4"/>', s),
    cpu: (s) => svg('<rect x="6" y="6" width="12" height="12" rx="2"/><path d="M9 2v4M15 2v4M9 18v4M15 18v4M2 9h4M2 15h4M18 9h4M18 15h4"/>', s),
    tag: (s) => svg('<path d="M3.5 12.2V4.5a1 1 0 0 1 1-1h7.7a1 1 0 0 1 .7.3l7.6 7.6a1 1 0 0 1 0 1.4l-7.7 7.7a1 1 0 0 1-1.4 0l-7.6-7.6a1 1 0 0 1-.3-.7z"/><circle cx="8.5" cy="8.5" r="1.5"/>', s),
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
  // "46.4 Mbps" as a figure with its unit set smaller.
  const unitSmall = (s) => { const [v, u] = s.split(" "); return html`${v}<small>${u || ""}</small>`; };
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
  Object.assign(PROTO, {
    tcpmux: { t: "TCP + mux", d: "تانل اتصال، مثل FRP: بسته‌ای جابه‌جا نمی‌شود؛ هر اتصال کاربر روی یک stream از چند جلسه‌ی TCP ماندگار بین دو سرور می‌رود و TCP کاربر در همین سرور تمام و آن طرف دوباره شروع می‌شود. آدرس داخلی و interface ندارد و فوروارد رویش «رله TCP» است. جهت را خودتان انتخاب می‌کنید: مستقیم (Direct)، این سرور به سرور مقابل وصل می‌شود؛ یا معکوس (Reverse)، سرور مقابل به این سرور وصل می‌شود. تانل روی سه پورت گوش می‌دهد و اگر اولی بسته بود، دومی و سومی امتحان می‌شوند.", layer: "۴ (اتصال)", carry: "اتصال‌های TCP کاربران", over: 8, crypt: "اختیاری، AES-GCM یا ChaCha20 روی جلسه", mt: "خیر؛ دو طرف NeoTunnel", best: "مثل FRP؛ وقتی فقط TCP رد می‌شود یا فقط اتصال از یک طرف باز است", stream: true },
    tlsmux: { t: "TLS + mux", d: "همان TCP + mux، داخل TLS 1.3. جلسه با رمز مشترک تانل احراز هویت می‌شود و کلیدهایش به همان اتصال TLS گره خورده است؛ کسی که رمز تانل را ندارد فقط یک وب‌سرور می‌بیند.", layer: "۴ (اتصال)", carry: "اتصال‌های TCP کاربران", over: 30, crypt: "TLS 1.3", mt: "خیر؛ دو طرف NeoTunnel", best: "مثل TCP + mux، وقتی ترافیک باید رمزدار و شبیه HTTPS باشد", stream: true },
  });
  PROTO["stream-tcp"] = PROTO.tcpmux; PROTO["stream-tls"] = PROTO.tlsmux;
  const STREAM_KINDS = ["stream-tcp", "stream-tls"];
  const isStreamMode = (mode) => !!(S.ov.modes.find((m) => m.name === mode) || {}).stream;
  const MODES_FWD = {
    nat: ["NAT", "سرویس مقصد آدرس داخلی تانل را می‌بیند. ساده و سازگار با همه‌چیز."],
    transparent: ["IP واقعی کاربر", "سرویس مقصد IP واقعی کاربر را می‌بیند، برای TCP و UDP و بدون هیچ تنظیمی در برنامه. پاسخ‌ها خودکار از همان تانل برمی‌گردند؛ سرور مقصد باید NeoTunnel داشته باشد."],
    proxy: ["PROXY protocol", "یک رله‌ی سبک، هدر PROXY (نسخه‌ی ۱ یا ۲) با IP و پورت کاربر را اول اتصال می‌فرستد. سرویس مقصد (nginx، HAProxy، Xray) باید proxy_protocol را بپذیرد. UDP فقط با نسخه‌ی ۲."],
    relay: ["رله TCP", "بدون بسته‌های تانل: هر اتصال کاربر با یک اتصال TCP جدا به سرور مقصد می‌رود و آنجا به همان پورت روی خود سرور (127.0.0.1) وصل می‌شود. هر کاربر کنترل ازدحام خودش را دارد، پس روی مسیرهای دور و پراُفت سریع‌ترین حالت است. فقط TCP؛ سرور مقصد باید NeoTunnel داشته باشد (روی پورت‌های رله‌ی همان تانل، یا پورت TCP 7712 روی نسخه‌های قبل از v0.8.16). اگر تانل رمزنگاری دارد، رله هم رمز می‌شود. هدر PROXY برای رساندن IP کاربر اختیاری است."],
  };
  // The PROXY header of a forward: required in proxy mode (pver), optional
  // in relay mode (rver, "0" = none). Relay mode carries TCP only.
  function pverField(path, o, mode) {
    const [key, vs] = mode === "proxy" ? ["pver", ["2", "1"]] : mode === "relay" ? ["rver", ["0", "2", "1"]] : [];
    if (!key) return "";
    const on = +(o[key] || 0) > 0;
    return html`<div class="field"><span class="lbl">هدر PROXY protocol</span><div class="seg">${vs.map((v) => html`<button type="button" class="${String(o[key] || 0) === v ? "on" : ""}" data-act="set" data-path="${path}.${key}" data-value="${v}">${v === "0" ? "بدون هدر" : "v" + v}</button>`)}</div>
      ${on ? html`<span class="help warn">سرویس مقصد باید هدر PROXY را بپذیرد (در Xray و 3x-ui گزینه‌ی acceptProxyProtocol / Proxy Protocol روی inbound)؛ وگرنه هیچ اتصالی برقرار نمی‌شود.</span>` : ""}</div>`;
  }
  // A connection tunnel carries relay forwards only: their mode is relay,
  // set for the user (a SOCKS/HTTP proxy is made in its own form).
  const fwdModeFor = (t, mode) => (t && t.stream ? "relay" : mode);
  // Where the far server hands a forward to its service: inside the tunnel
  // (127.0.0.1 for a relay) or at its public address, still through the
  // tunnel. A transparent forward and a proxy have no choice here.
  const toPublicOf = (mode, o) => mode !== "transparent" && mode !== "socks" && !!o.toPublic;
  function targetField(path, o, mode) {
    if (mode === "transparent" || mode === "socks") return "";
    const inside = mode === "relay" ? "127.0.0.1 روی سرور مقصد" : "آدرس داخل تانل";
    return html`<div class="field full"><span class="lbl">تحویل روی سرور مقصد</span><div class="seg">
        <button type="button" class="${o.toPublic ? "" : "on"}" data-act="set" data-path="${path}.toPublic" data-value="">${inside}</button>
        <button type="button" class="${o.toPublic ? "on" : ""}" data-act="set" data-path="${path}.toPublic" data-value="1">IP پابلیک سرور مقصد</button></div>
      <span class="help">${o.toPublic ? "ترافیک باز هم داخل تانل می‌رود، ولی سرور مقصد آن را روی IP پابلیک خودش تحویل می‌گیرد: برای سرویسی که فقط روی IP پابلیک گوش می‌دهد، یا فایروالی آنجا که ترافیک از رنج داخلی را می‌بندد." : mode === "relay" ? "سرور مقصد اتصال را به 127.0.0.1 و همین پورت روی خودش می‌رساند." : "سرور مقصد اتصال را روی آدرس خودش داخل تانل تحویل می‌گیرد."}</span></div>`;
  }
  const streamModeNote = html`<div class="field full"><span class="lbl">حالت فوروارد</span><div class="help">رله TCP، خودکار برای تانل اتصال: هر اتصال کاربر جدا روی جلسه‌های تانل می‌رود و سرور مقصد آن را به 127.0.0.1 و همین پورت روی خودش می‌رساند؛ سرویس آنجا باید روی 0.0.0.0 یا 127.0.0.1 گوش بدهد.</div></div>`;
  const fwdVersion = (mode, o) => mode === "proxy" ? +o.pver : mode === "relay" ? +(o.rver || 0) : 0;
  const fwdProtos = (mode) => mode === "relay" ? ["tcp"] : ["both", "tcp", "udp"];
  const fwdProto = (mode, p) => mode === "relay" ? "tcp" : p;
  // A small "i" beside a label that opens its explanation under it.
  const infoBtn = (id) => html`<button type="button" class="info-b ${S.tip === id ? "on" : ""}" data-act="tip" data-id="${id}" aria-expanded="${S.tip === id}" aria-label="راهنما" title="راهنما">i</button>`;
  const infoPop = (id, body) => S.tip === id ? html`<div class="info-pop" role="note">${body}</div>` : "";
  // On 6to4 a relay forward travels inside the SIT layer alone, several
  // times faster than the two layers a kernel forward goes through.
  const sitHint = (kind, mode) => ["6to4", "6to4-gre", "6to4-udp"].includes(kind) && mode !== "relay"
    ? html`<div class="help full">روی 6to4، حالت «رله TCP» برای TCP چند برابر سریع‌تر است: اتصال‌ها فقط از یک لایه‌ی تانل رد می‌شوند.</div>` : "";
  // The forward mode picker; to a server without NeoTunnel only the modes
  // that need nothing there.
  function modeField(path, cur, remote, key = "mode") {
    const id = path + "." + key, ms = Object.entries(MODES_FWD).filter(([v]) => !remote || (v !== "transparent" && v !== "relay"));
    return html`<div class="field full"><span class="lbl lbl-i">حالت فوروارد ${infoBtn(id)}</span>
      ${infoPop(id, html`<dl>${ms.map(([v, [l, d]]) => html`<dt class="${cur === v ? "on" : ""}">${l}</dt><dd>${d}</dd>`)}</dl>${remote ? html`<p>IP واقعی و رله TCP فقط وقتی سرور مقصد NeoTunnel دارد.</p>` : ""}`)}
      <div class="seg">${ms.map(([v, [l]]) => html`<button type="button" class="${cur === v ? "on" : ""}" data-act="set" data-path="${id}" data-value="${v}">${l}</button>`)}</div></div>`;
  }
  const BENCH = [
    ["direct", "مسیر مستقیم"], ["6to4", "6to4"], ["6to4-udp", "6to4 روی UDP"], ["6to4-gre", "6to4 + GRE6"],
    ["ipip", "IPIP"], ["gre", "GRE"], ["gretap", "GRETAP"], ["vxlan", "VXLAN"], ["geneve", "GENEVE"],
    ["wireguard", "WireGuard"], ["l2tpv3", "L2TPv3"], ["l2tpv3-udp", "L2TPv3 روی UDP"], ["tcp", "TCP"],
    ["tcpmux", "TCP + mux"], ["tlsmux", "TLS + mux"],
  ];
  // A connection tunnel's direction, by its own name: Direct (this server
  // connects out, the far one listens) or Reverse (the far one connects in,
  // like FRP).
  const dirName = (rev) => (rev ? "معکوس (Reverse)" : "مستقیم (Direct)");
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
    sec: store("nt-settings") || "node", defs: null, pe: null, portSort: store("nt-port-sort") || "manual",
    safe: store("nt-safe") === "1", safeSeen: store("nt-safe-seen") || "",
    jobs: [], job: null, pv: {}, night: {}, upgrading: {}, restarting: {}, prates: {}, dtab: {}, mtu: {}, ev: {}, evBusy: {}, tpr: store("nt-tpr") || "24h", priv: store("nt-priv") === "1", pm: null, fm: null,
    speed: Object.assign({ tunnel: "", peer: "", only: BENCH.map((b) => b[0]), secs: 10, streams: 8 }, (() => { try { return JSON.parse(store("nt-speed") || "{}"); } catch { return {}; } })()),
    // Graphs: the dashboard's range and source, the statistics page's range
    // and subject (the server or one tunnel), the drawer's range.
    hist: { range: store("nt-hist") || "24h", src: store("nt-hist-src") || "all" },
    st: { range: store("nt-st") || "24h", subject: "server" },
    dr: { range: "24h" },
    paused: false,
  };
  document.documentElement.dataset.theme = S.theme;
  document.documentElement.classList.toggle("private", S.priv);

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

  // apiUpload sends a file's text as the request body.
  async function apiUpload(path, text) {
    const res = await fetch("api/" + path, { method: "POST", credentials: "same-origin", headers: { "X-NeoTunnel": "1", "Content-Type": "application/json" }, body: text });
    let data = null;
    try { data = await res.json(); } catch { /* empty */ }
    if (!res.ok) throw new Error((data && data.error) || `خطا (${res.status})`);
    return data;
  }

  async function inspectBackup(file) {
    S.restore = { text: "", preview: null, error: "", busy: false };
    try {
      S.restore.text = await file.text();
      S.restore.preview = await apiUpload("backup/inspect", S.restore.text);
    } catch (e) { S.restore.error = e.message; }
    draw();
  }

  async function certSave(body, okMsg) {
    const e = S.ce; e.busy = true; draw();
    try {
      S.cert = await api("POST", "settings/cert", body);
      S.ce = null; toast(okMsg); await refresh();
      if (body.mode !== "self") toast("آدرس تازه‌ی پنل: " + S.ov.panel.url);
    } catch (err) { e.busy = false; toast(err.message, "bad"); }
    draw();
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
      }
      // Port counters move only when the daemon samples them (every 10 s),
      // so a rate is taken between two changes, and kept until the next
      // one, not dropped to zero by polls that land in between.
      const seen = S.pseen || (S.pseen = {});
      for (const f of ov.forwards || []) {
        const k = fwdKey(f), p = seen[k];
        // The first change after the page opens spans an unknown time.
        if (!p) { seen[k] = { rx: f.rx, tx: f.tx, t: ov.time, fresh: true }; continue; }
        if (f.rx !== p.rx || f.tx !== p.tx) {
          const dt = (ov.time - p.t) / 1000;
          if (!p.fresh && dt > 0 && f.rx >= p.rx && f.tx >= p.tx) S.prates[k] = { rx: ((f.rx - p.rx) * 8) / dt, tx: ((f.tx - p.tx) * 8) / dt };
          seen[k] = { rx: f.rx, tx: f.tx, t: ov.time };
        } else if (ov.time - p.t > 25e3) S.prates[k] = { rx: 0, tx: 0 };
      }
      S.ov = ov; S.live = true; S.authed = true;
      if (!S.updRun && Date.now() - (S.updAt || 0) > 3600e3) loadUpdate();
    } catch {
      S.live = false;
    }
    if ((S.modal && !S.modal.passive) || S.menuDrag) return;
    if (S.edit) patchLive(); else draw();
  }

  // ---------- updates of this server ----------
  // The newest release, as the server last looked it up (it caches GitHub's
  // answer); asked once an hour, or now from the settings.
  async function loadUpdate(force) {
    S.updAt = Date.now();
    try { S.upd = await api("GET", "update" + (force ? "?force=1" : "")); } catch { /* keep the last answer */ }
    if (S.modal && !S.modal.passive) drawModal(); else draw();
  }
  const updNew = () => (S.upd && S.upd.newer && !S.updRun ? S.upd.latest : "");

  // After the installer starts: wait for the new version to answer, then
  // load the page again (its code changed too). The service restarts in
  // between, so failed requests are expected for a while.
  async function pollUpdate() {
    const run = S.updRun;
    if (!run || run.failed || run.done) return;
    if (Date.now() - run.since > 5 * 60e3) { run.failed = true; drawModal(); return; }
    try {
      const ov = await api("GET", "overview");
      if (ov.node.version === run.to) {
        run.done = true; drawModal();
        setTimeout(() => location.reload(), 900);
        return;
      }
      const r = await api("GET", "update");
      if (!r.updating && r.log) { run.failed = true; run.log = r.log; drawModal(); return; }
    } catch { /* restarting */ }
    setTimeout(pollUpdate, 2000);
  }

  function updateView() {
    const u = S.upd || {}, run = S.updRun;
    if (run && run.done) {
      return html`${modalHead("به‌روزرسانی انجام شد")}<div class="body"><div class="upd-state">${I.check(22)}<div><b>نسخه‌ی <span class="ltr">${run.to}</span> بالا آمد</b><span>صفحه دوباره بارگذاری می‌شود…</span></div></div></div>`;
    }
    if (run && run.failed) {
      return html`${modalHead("به‌روزرسانی کامل نشد")}<div class="body stack">
        <div>${run.log ? "نصاب کار را تمام نکرد. آخرین خطوط خروجی آن:" : `نسخه‌ی جدید بعد از ۵ دقیقه جواب نداد.`}</div>
        ${run.log ? html`<pre class="upd-log ltr">${run.log}</pre>` : ""}
        <div class="help">روی سرور در ترمینال اجرا کنید: <code class="ltr">sudo neotunnel update</code>${run.log ? "" : html` · خروجی نصاب در <code class="ltr">/var/log/neotunnel-update.log</code> است.`}</div></div>
        <footer><span class="grow"></span><button class="btn ghost" data-act="update-done">بستن</button></footer>`;
    }
    if (run) {
      return html`${modalHead("در حال به‌روزرسانی")}<div class="body stack">
        <div class="upd-state"><i class="spin"></i><div><b>دانلود و نصب نسخه‌ی <span class="ltr">${run.to}</span></b><span>سرویس چند ثانیه دوباره راه می‌افتد و این صفحه بعد از آن خودش تازه می‌شود. تانل‌ها و تنظیمات می‌مانند.</span></div></div>
        <div class="help">اگر این پنجره را ببندید، به‌روزرسانی روی سرور ادامه پیدا می‌کند.</div></div>`;
    }
    return html`${modalHead("به‌روزرسانی NeoTunnel")}<div class="body stack">
      <dl class="kv"><dt>نسخه‌ی فعلی</dt><dd class="ltr">${u.current}</dd><dt>نسخه‌ی جدید</dt><dd class="ltr"><b>${u.latest}</b>${u.url ? html` · <a href="${u.url}" target="_blank" rel="noopener">تغییرات این نسخه</a>` : ""}</dd></dl>
      <div>برنامه‌ی جدید از گیت‌هاب دانلود و نصب می‌شود، مثل <code class="ltr">sudo neotunnel update</code>. تانل‌ها، فورواردها و تنظیمات می‌مانند و سرویس چند ثانیه دوباره راه می‌افتد.</div>
      ${S.ov.peers.length ? html`<div class="help">بعد از آن، سرورهای مقصد را از صفحه‌ی «سرورها» به همین نسخه ارتقا دهید.</div>` : ""}</div>
      <footer><span class="grow"></span><button class="btn ghost" data-act="close">انصراف</button><button class="btn primary" data-act="update-run">${I.upgrade(16)} به‌روزرسانی</button></footer>`;
  }

  async function refreshTraffic() {
    try { S.traffic = (await api("GET", "traffic")) || {}; } catch { /* keep */ }
  }

  // The servers that manage this one (older daemons report only one).
  const masters = () => (S.ov && (S.ov.masters || (S.ov.master ? [S.ov.master] : []))) || [];
  const visible = () => (S.ov ? S.ov.tunnels.filter((t) => !t.carrier) : []);
  const tunnel = (name) => S.ov && S.ov.tunnels.find((t) => t.name === name);
  const rate = (name) => S.rates[name] || { rx: 0, tx: 0 };
  const fwdKey = (f) => f.tunnel + "|" + f.ports;
  const prate = (f) => S.prates[fwdKey(f)] || { rx: 0, tx: 0 };
  const total = () => visible().reduce((a, t) => ({ rx: a.rx + rate(t.name).rx, tx: a.tx + rate(t.name).tx }), { rx: 0, tx: 0 });
  const peer = (name) => S.ov && S.ov.peers.find((p) => p.name === name);
  // Display names: a label when one is set, else the name, which is the ID
  // and never changes (renaming a tunnel or server only sets its label).
  const tname = (t) => t.label || t.name;
  const plabel = (p) => p.label || p.name;
  const nodeName = () => S.ov.node.label || S.ov.node.name;
  const pname = (name) => { if (!name) return ""; const p = peer(name); return p ? plabel(p) : S.ov && name === S.ov.node.name ? nodeName() : name; };
  const tlabel = (name) => { const t = tunnel(name); return t ? tname(t) : name; };
  const status = (t) => {
    if (t.limits && t.limits.disabled) return ["off", "خاموش"];
    if (t.blocked) return ["warn", "حجم ماهانه تمام شد"];
    if (t.error) return ["bad", "خطا"];
    const p = t.peer && peer(t.peer);
    if (p && !p.paired) return ["warn", "منتظر سرور مقصد"];
    return t.up ? ["ok", "فعال"] : ["bad", "قطع"];
  };
  const stateEl = (t) => { const [c, l] = status(t); return html`<span class="state"><i class="dot ${c}"></i>${l}</span>`; };
  // How well traffic passes, from the daemon's probe rounds: ok, degraded,
  // bad (most packets lost, though some still come back) or down. Daemons
  // older than the grades only say whether anything came back.
  const grade = (h) => (!h || h.probes < 3 ? "" : h.grade || (!h.passes ? "down" : h.loss >= 40 ? "bad" : h.loss >= 5 ? "degraded" : "ok"));
  // A chip that says whether traffic really passes, from the daemon's probes.
  function healthTag(t) {
    const h = t.health, g = grade(h);
    if (!g) return "";
    if (g === "down") return html`<span class="tag bad">${I.alert(11)} ترافیک رد نمی‌شود</span>`;
    if (g === "bad") return html`<span class="tag bad">${I.alert(11)} افت شدید ${h.loss.toFixed(0)}٪</span>`;
    return html`<span class="tag ${g === "degraded" ? "warn" : ""}">${ms(h.ping_ms)}${h.loss >= 1 ? ` · افت ${h.loss.toFixed(0)}٪` : ""}</span>`;
  }
  // The daemon's last automatic step for a tunnel, in the panel's words.
  function actionText(h) {
    if (!h || !h.action) return "";
    const m = /lost (\d+)% of its probes for (\d+) rounds/.exec(h.action), far = /far server: (.*)\)$/.exec(h.action);
    const moved = /^automatic encryption moved to (.*)$/.exec(h.action), stayed = /^automatic encryption stays on (.*?): /.exec(h.action);
    if (moved) return `رمزنگاری خودکار رفت روی ${rungTitle(moved[1] === "no encryption" ? "" : moved[1])}`;
    if (stayed) return `رمزنگاری خودکار روی ${rungTitle(stayed[1] === "no encryption" ? "" : stayed[1])} ماند: ${h.action.slice(stayed[0].length)}`;
    const what = h.action.startsWith("restarted") ? "ری‌استارت خودکار روی هر دو سرور" : h.action;
    return `${what}${m ? ` (افت ${m[1]}٪ در ${m[2]} دور بررسی)` : ""}${far ? `؛ سرور مقابل: ${far[1]}` : ""}`;
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
    ["ports", "پورت‌ها", I.ports], ["stats", "آمار", I.chart], ["speed", "تست سرعت", I.gauge], ["commands", "دستورها", I.term], ["settings", "تنظیمات", I.gear],
  ];

  function draw() {
    const app = $("#app");
    document.documentElement.classList.toggle("locked", !!(S.menu && S.authed && S.ov));
    if (!S.authed) { const first = !$("#login-user"); morph(app, part(viewLogin())); const u = $("#login-user"); if (u && first) u.focus(); return; }
    if (!S.ov) { morph(app, part(html`<div class="login-wrap"><div class="muted">در حال بارگذاری…</div></div>`)); return; }
    morph(app, part(viewShell()));
    markSensitive(app);
    drawModal();
    wireCharts();
  }

  function viewLogin() {
    return html`<div class="login-wrap"><div class="panel login">
      <div class="brand"><img src="assets/logo.png" alt=""><div><b>NeoTunnel</b><small>مدیریت تانل‌های سرور</small></div></div>
      <h1>ورود به پنل</h1>
      <form data-form="login">
        <div class="field"><label for="login-user">نام کاربری</label><input class="input ltr" id="login-user" name="user" autocomplete="username" required></div>
        <div class="field"><label for="login-pass">رمز عبور</label><input class="input ltr" id="login-pass" name="password" type="password" autocomplete="current-password" required></div>
        <button class="btn primary" type="submit">ورود</button>
      </form>
      <p class="help" style="text-align:center;margin:18px 0 0">رمز را فراموش کرده‌اید؟ روی سرور اجرا کنید: <code class="ltr">sudo neotunnel panel password</code></p>
    </div></div>`;
  }

  // ---------- sponsored ----------
  // One message from the NeoAurora ads service, through this server (see
  // web/ads.go): a card on the dashboard, a slim line on the other pages
  // that can be closed for a few hours.
  const AD_HIDE_H = 6;
  function ad(placement) {
    const a = S.ads || (S.ads = {});
    const c = a[placement];
    if (!c || (!c.busy && Date.now() - c.at > 15 * 60e3)) {
      a[placement] = Object.assign({}, c, { busy: true, at: Date.now() });
      api("GET", "spot?p=" + placement)
        .then((r) => { a[placement] = { ad: r.ad || null, at: Date.now() }; if (!S.edit && !(S.modal && !S.modal.passive)) draw(); })
        .catch(() => { a[placement] = Object.assign({}, a[placement], { busy: false }); });
    }
    return a[placement] && a[placement].ad;
  }
  const adHidden = () => +(store("nt-ad-hide") || 0) > Date.now();
  const adLink = (a, placement, cls, body) => a.link
    ? html`<a class="${cls}" href="${a.link}" target="_blank" rel="noopener sponsored" data-hl="${a.id}" data-place="${placement}">${body}</a>` : "";

  function adCard(a, closable) {
    const art = `<svg viewBox="0 0 80 80">${[1, 2, 3].map((i) => `<circle class="ring r${i}" cx="40" cy="40" r="${8 + i * 9}"/>`).join("")}<circle cx="40" cy="40" r="7" fill="url(#hl-orb)"/><defs><radialGradient id="hl-orb"><stop offset="0" stop-color="var(--accent-2)"/><stop offset="1" stop-color="var(--accent)"/></radialGradient></defs></svg>`;
    return html`<div class="hl-glow" aria-hidden="true"></div>
      <div class="hl-art" aria-hidden="true">${raw(art)}</div>
      <div class="hl-text"><div class="hl-top"><span class="hl-tag">تبلیغ</span>${a.badge ? html`<span class="hl-badge">${a.badge}</span>` : ""}<h2 class="hl-title" dir="auto">${a.title}</h2></div>
        ${a.body ? html`<p class="hl-body" dir="auto">${a.body}</p>` : ""}</div>
      ${adLink(a, closable ? "sidebar" : "home", "hl-cta", html`<span>${a.button || "مشاهده"}</span>${I.arrow(16)}`)}
      ${closable ? html`<button class="hl-x" data-act="spot-hide" aria-label="بستن تبلیغ" title="بستن تا ${AD_HIDE_H} ساعت">${I.x(14)}</button>` : ""}`;
  }

  function adStrip() {
    if (S.route === "dashboard" || adHidden()) return "";
    const a = ad("sidebar");
    if (!a) return "";
    return html`<section class="card b-hl hl-page" aria-label="تبلیغ" data-key="hl-page">${adCard(a, true)}</section>`;
  }

  // The frame: brand, the pages as one row of tabs, and the few global
  // controls (connection state, theme, a new tunnel, sign out).
  function viewShell() {
    const ov = S.ov;
    const counts = { tunnels: visible().length, servers: ov.peers.length, ports: ov.forwards.length };
    const page = { dashboard: viewDashboard, tunnels: viewTunnels, servers: viewServers, ports: viewPorts, stats: viewStats, speed: viewSpeed, commands: viewCommands, settings: viewSettings }[S.route] || viewDashboard;
    const [title, sub, tools] = pageHead();
    const liveCls = !S.live ? "off" : S.paused ? "paused" : "";
    return html`<div class="frame">
      <header class="topbar">
        <button class="iconbtn burger" data-act="menu" aria-label="منو" aria-haspopup="dialog" aria-expanded="${S.menu ? "true" : "false"}" aria-controls="nav-sheet">${I.menu(19)}</button>
        <a class="brand" href="#/dashboard" title="NeoTunnel ${ov.node.version}"><img src="assets/logo.png" alt=""><b>NeoTunnel</b><small class="ltr">${ov.node.version}</small></a>
        <nav class="tabs" aria-label="صفحه‌ها">${NAV.map(([id, label, icon]) => html`<a href="#/${id}" class="${S.route === id ? "on" : ""}" ${S.route === id ? raw('aria-current="page"') : ""}>${icon(18)}<span>${label}</span>${counts[id] ? html`<span class="n">${counts[id]}</span>` : ""}</a>`)}</nav>
        <div class="top-tools">
          ${updNew() ? html`<button class="btn sm upd-pill" data-act="update-ask" title="نسخه‌ی جدید NeoTunnel آمده؛ برای به‌روزرسانی بزنید">${I.upgrade(14)}<span class="ltr">${updNew()}</span></button>` : ""}
          <span class="live ${liveCls}" title="${S.live ? (S.paused ? "به‌روزرسانی زنده متوقف است" : "پنل هر ۳ ثانیه تازه می‌شود") : "ارتباط با سرور قطع است"}"><i></i><span>${!S.live ? "قطع ارتباط" : S.paused ? "متوقف" : "زنده"}</span></span>
          <button class="iconbtn" data-act="privacy" aria-pressed="${S.priv}" title="${S.priv ? "نمایش آدرس‌ها و اطلاعات حساس" : "پنهان کردن آدرس‌ها و اطلاعات حساس"}" aria-label="حالت حریم خصوصی">${S.priv ? I.eyeOff(17) : I.eye(17)}</button>
          <button class="iconbtn wide-only" data-act="theme" title="${S.theme === "dark" ? "تم روشن" : "تم تیره"}" aria-label="تغییر تم">${S.theme === "dark" ? I.sun(17) : I.moon(17)}</button>
          <button class="iconbtn hot" data-act="wizard" title="تانل جدید" aria-label="تانل جدید">${I.plus(18)}</button>
          <button class="iconbtn wide-only" data-act="logout" title="خروج" aria-label="خروج">${I.out(17)}</button>
        </div>
      </header>
      <main class="main" data-key="page-${S.route}">
        ${S.route === "dashboard" ? "" : html`<div class="head"><div><h1>${title}</h1><div class="sub">${sub}</div></div>
          <div class="tools">${tools}</div></div>`}
        ${behindNote()}
        ${safeBanner()}
        ${adStrip()}
        ${ov.errors && ov.errors.length ? html`<div class="note bad" style="margin-bottom:14px"><b>${I.alert(16)} مشکل در اعمال تنظیمات</b>${ov.errors.map((e) => { const t = errText(e); return t !== e ? html`<div>${t}<div class="mono faint small">${e}</div></div>` : html`<div class="mono">${e}</div>`; })}</div>` : ""}
        ${page()}
      </main>
    </div>${S.drawer ? viewDrawer() : ""}${S.menu ? viewMenu(counts) : ""}`;
  }

  // ---------- menu (narrow screens) ----------
  // The pages and the global controls in a sheet that slides in from the
  // side the menu button is on and leaves the same way. The button, the
  // scrim, Escape, a swipe toward the edge or choosing a page closes it.
  function viewMenu(counts) {
    const ov = S.ov, st = S.menuEnter ? "enter" : S.menuLeaving ? "leave" : "";
    S.menuEnter = false;
    const liveCls = !S.live ? "off" : S.paused ? "paused" : "";
    return html`<div class="sheet-scrim ${st}" data-act="menu-close"></div>
      <aside id="nav-sheet" class="sheet ${st}" role="dialog" aria-modal="true" aria-label="منو">
        <header class="sheet-head">
          <a class="brand" href="#/dashboard"><img src="assets/logo.png" alt=""><b>NeoTunnel</b><small class="ltr">${ov.node.version}</small></a>
          <button class="iconbtn" data-act="menu-close" aria-label="بستن منو">${I.x(18)}</button>
        </header>
        <div class="sheet-node">
          <div><b>${nodeName()}</b><span class="ltr">${ov.node.address || "-"}</span></div>
          <span class="live ${liveCls}"><i></i><span>${!S.live ? "قطع ارتباط" : S.paused ? "متوقف" : "زنده"}</span></span>
        </div>
        <nav class="sheet-nav" aria-label="صفحه‌ها">${NAV.map(([id, label, icon]) => html`<a href="#/${id}" class="${S.route === id ? "on" : ""}" ${S.route === id ? raw('aria-current="page"') : ""}>${icon(20)}<span>${label}</span>${counts[id] ? html`<span class="n">${counts[id]}</span>` : ""}</a>`)}</nav>
        <div class="sheet-foot">
          <button class="btn primary" data-act="wizard">${I.plus(16)} تانل جدید</button>
          ${updNew() ? html`<button class="btn" data-act="update-ask">${I.upgrade(16)} به‌روزرسانی به <span class="ltr">${updNew()}</span></button>` : ""}
          <div class="sheet-row">
            <button class="btn ghost" data-act="theme">${S.theme === "dark" ? I.sun(16) : I.moon(16)} ${S.theme === "dark" ? "تم روشن" : "تم تیره"}</button>
            <button class="btn ghost" data-act="live-toggle">${I.power(16)} ${S.paused ? "ادامه‌ی به‌روزرسانی" : "توقف به‌روزرسانی"}</button>
            <span class="grow"></span>
            <button class="btn ghost" data-act="logout">${I.out(16)} خروج</button>
          </div>
        </div>
      </aside>`;
  }

  function openMenu() {
    if (S.menu && !S.menuLeaving) return;
    clearTimeout(S.menuTimer);
    S.menu = true; S.menuLeaving = false; S.menuEnter = true;
    draw();
    const c = $('.sheet [data-act="menu-close"]'); if (c) c.focus();
  }
  // now: without the leaving animation, when another view takes over.
  function closeMenu(now) {
    if (!S.menu || (S.menuLeaving && !now)) return;
    clearTimeout(S.menuTimer);
    const done = () => {
      S.menu = false; S.menuLeaving = false; draw();
      const b = $(".burger"); if (b && b.offsetParent) b.focus({ preventScroll: true });
    };
    if (now || reduceMotion) { done(); return; }
    S.menuLeaving = true; draw();
    S.menuTimer = setTimeout(done, 240);
  }

  // Swipe the sheet toward its edge to close it: it follows the finger,
  // and on release a flick or a drag past 40% of its width closes it from
  // where it is; anything less springs back.
  (() => {
    let drag = null;
    const edge = () => (document.documentElement.dir === "ltr" ? -1 : 1);
    document.addEventListener("pointerdown", (e) => {
      const sh = e.target.closest && e.target.closest(".sheet");
      if (!sh || S.menuLeaving || e.button > 0) return;
      drag = { sh, x0: e.clientX, y0: e.clientY, id: e.pointerId, on: false, off: 0, hist: [[e.timeStamp, 0]], w: sh.offsetWidth };
    });
    document.addEventListener("pointermove", (e) => {
      if (!drag || e.pointerId !== drag.id) return;
      const dx = (e.clientX - drag.x0) * edge(), dy = e.clientY - drag.y0;
      if (!drag.on) {
        if (Math.abs(dy) > 10 && Math.abs(dy) > Math.abs(dx)) { drag = null; return; } // a scroll
        if (dx < 10) return;
        drag.on = true; S.menuDrag = true;
        drag.sh.setPointerCapture(e.pointerId);
        drag.sh.style.transition = "none";
      }
      drag.off = Math.max(0, dx - 10);
      drag.hist.push([e.timeStamp, drag.off]); if (drag.hist.length > 6) drag.hist.shift();
      drag.sh.style.transform = `translateX(${drag.off * edge()}px)`;
      const scrim = $(".sheet-scrim"); if (scrim) scrim.style.opacity = String(Math.max(0, 1 - drag.off / drag.w));
    });
    const end = (e) => {
      if (!drag || e.pointerId !== drag.id) return;
      const d = drag; drag = null;
      if (!d.on) return;
      // A drag ends with no click on what it started over.
      const swallow = (c) => { c.stopPropagation(); c.preventDefault(); };
      document.addEventListener("click", swallow, true);
      setTimeout(() => document.removeEventListener("click", swallow, true), 60);
      const [t0, o0] = d.hist[0], [t1, o1] = d.hist[d.hist.length - 1];
      const v = t1 > t0 ? (o1 - o0) / (t1 - t0) : 0; // px per ms
      const scrim = $(".sheet-scrim");
      const ease = "240ms cubic-bezier(0.32, 0.72, 0, 1)";
      d.sh.style.transition = `transform ${ease}`;
      if (scrim) scrim.style.transition = `opacity ${ease}`;
      if (d.off + v * 180 > d.w * 0.4) {
        d.sh.style.transform = `translateX(${d.w * edge()}px)`;
        if (scrim) scrim.style.opacity = "0";
        setTimeout(() => { S.menuDrag = false; closeMenu(true); }, 240);
      } else {
        d.sh.style.transform = "";
        if (scrim) scrim.style.opacity = "";
        setTimeout(() => { S.menuDrag = false; d.sh.style.transition = ""; if (scrim) scrim.style.transition = ""; }, 260);
      }
    };
    document.addEventListener("pointerup", end);
    document.addEventListener("pointercancel", end);
  })();

  // Safe changes: the preference, the query that asks for one, and the
  // banner of a change waiting for confirmation (or how the last one ended).
  const safeQ = () => (S.safe ? "?safe=1" : "");
  const safeField = (compact) => html`<label class="switch${compact ? " sm" : ""}" title="اگر تغییر مسیر را قطع کرد و ظرف ۶۰ ثانیه تأیید نشد، روی هر دو سرور خودکار برمی‌گردد"><input type="checkbox" data-act="safe-pref" ${S.safe ? "checked" : ""}>اعمال امن${compact ? "" : " (برگشت خودکار اگر ظرف ۶۰ ثانیه تأیید نشود)"}</label>`;
  function safeBanner() {
    const p = S.ov.safe, o = S.ov.safe_last;
    if (p) {
      const left = Math.max(0, Math.round((p.deadline - S.ov.time) / 1000));
      const waiting = p.kept ? (p.peers || []).filter((x) => !(p.acked || []).includes(x)) : [];
      return html`<div class="note warn behind safe" role="alert">${I.alert(16)}<div class="grow"><b>تغییر امن منتظر تأیید است</b> <span class="faint ltr small">${p.what}</span><br>${p.kept
        ? html`پنل تأیید کرد؛ منتظر تأیید ${waiting.join("، ")}… اگر تا <b>${left}</b> ثانیه جواب ندهد، روی هر دو سرور به حالت قبل برمی‌گردد.`
        : html`اگر تانل‌ها و پنل درست کار می‌کنند «نگه داشتن» را بزنید؛ وگرنه <b>${left}</b> ثانیه دیگر روی هر دو سرور به حالت قبل برمی‌گردد.`}</div>
        ${p.kept ? "" : html`<button class="btn sm primary" data-act="safe-keep" data-id="${p.id}">${I.check(14)} نگه داشتن</button>`}
        <button class="btn sm" data-act="safe-undo" data-id="${p.id}">برگرداندن</button></div>`;
    }
    if (!o || o.id === S.safeSeen || S.ov.time - Date.parse(o.at) > 10 * 60 * 1000) return "";
    return html`<div class="note ${o.kept ? "" : "bad"} behind safe">${o.kept ? I.check(16) : I.alert(16)}<div class="grow">${o.kept
      ? html`تغییر امن <span class="ltr small">${o.what}</span> تأیید و نگه داشته شد.`
      : html`<b>تغییر امن برگشت خورد.</b> <span class="ltr small">${o.what}</span> — تنظیمات قبلی روی هر دو سرور برگردانده شد. <span class="faint ltr small">${o.reason}</span>`}</div>
      <button class="btn sm ghost" data-act="safe-dismiss" data-id="${o.id}">باشه</button></div>`;
  }
  // Tells how a request made as a safe change went.
  const safeStarted = (ch) => { if (ch && ch.safe) { S.ov.safe = ch.safe; toast("تغییر اعمال شد؛ ظرف ۶۰ ثانیه تأییدش کنید"); } };

  // Servers that have not taken the latest changes, with the way to apply
  // them by hand.
  function behindNote() {
    const ps = (S.ov.peers || []).filter((p) => p.paired && !p.in_sync);
    const old = (S.ov.peers || []).filter((p) => p.paired && p.version && p.version !== S.ov.node.version && !S.upgrading[p.name]);
    const oldNote = old.length ? html`<div class="note warn behind">${I.alert(16)}<div class="grow">${old.map((p) => html`<b>${plabel(p)}</b> `)} نسخه‌ی قدیمی‌تری (${old.map((p) => p.version).join("، ")}) از این سرور (<span class="ltr">${S.ov.node.version}</span>) دارد؛ تنظیمات تازه را نمی‌شناسد و ممکن است تانل یا فوروارد کار نکند.</div>
      ${old.map((p) => html`<button class="btn sm primary" data-act="upgrade" data-name="${p.name}">${I.upgrade(14)} ارتقای ${plabel(p)}</button>`)}</div>` : "";
    if (!ps.length) return oldNote;
    return html`<div class="note warn behind">${I.alert(16)}<div class="grow">${ps.length === 1 ? html`سرور <b>${plabel(ps[0])}</b> آخرین تغییرات را نگرفته است.` : html`${ps.length} سرور آخرین تغییرات را نگرفته‌اند: ${ps.map((p) => p.name).join("، ")}.`}
      <span class="faint"> اگر دوباره ارسال نشد، لینک تغییرات را روی همان سرور اجرا کنید.</span></div>
      <button class="btn sm" data-act="sync-all">${I.sync(14)} ارسال دوباره</button>
      ${ps.map((p) => html`<button class="btn sm primary" data-act="setup" data-name="${p.name}">${I.term(14)} لینک تغییرات${ps.length > 1 ? " " + plabel(p) : ""}</button>`)}</div>${oldNote}`;
  }

  function pageHead() {
    const add = html`<button class="btn primary" data-act="wizard">${I.plus(16)} تانل جدید</button>`;
    switch (S.route) {
      case "tunnels": return ["تانل‌ها", "روی هر تانل بزنید تا جزئیات، رویدادها و تنظیمات پیشرفته باز شود", html`<div class="seg" role="radiogroup" aria-label="بازه‌ی نمودار پینگ">${PRANGES.map(([k, l]) => html`<button type="button" role="radio" aria-checked="${S.tpr === k}" class="${S.tpr === k ? "on" : ""}" data-act="tpr" data-range="${k}">${l}</button>`)}</div>${add}`];
      case "servers": return ["سرورها", "سرورهای مقصد که از همین پنل مدیریت می‌شوند", html`<button class="btn" data-act="sync-all">${I.sync(16)} همگام‌سازی همه</button><button class="btn primary" data-act="add-server">${I.plus(16)} افزودن سرور</button>`];
      case "ports": return ["پورت‌ها", "پورت‌های این سرور و اینکه کاربر از کدام تانل به کجا می‌رسد", html`<button class="btn primary" data-act="add-forward">${I.plus(16)} فوروارد جدید</button>`];
      case "stats": return ["آمار", "ترافیک، پینگ، پردازنده و حافظه در بازه‌های مختلف. هر نقطه میانگین همان بازه است و خط نازک بالای آن، اوج آن.", ""];
      case "speed": return ["تست سرعت", "پینگ، جیتر، افت بسته و پهنای باند واقعی بین دو سرور", ""];
      case "commands": return ["دستورها", "معادل خط فرمان کارهای پنل، با نام‌ها و آدرس‌های همین سرور", ""];
      case "settings": return ["تنظیمات", "سرور، دسترسی پنل، پیش‌فرض تانل‌ها، شبکه و نگهداری", ""];
    }
    return ["", "", add];
  }

  // ---------- dashboard: the bento ----------
  // One card per question an operator has: where is traffic going right now
  // (the flow), which machine is this and is it alive (the node), how hard
  // it works (load), how the path behaves (ping), what the network card
  // carries, and how all of it moved over time (history).
  function viewDashboard() {
    const ov = S.ov, sp = ad("home");
    return html`<div class="bento">
      <section class="card b-flow" aria-label="جریان ترافیک">${flowCard()}</section>
      <section class="card b-node" aria-label="این سرور">${nodeCard()}</section>
      ${sp ? html`<section class="card b-hl" aria-label="تبلیغ" data-key="hl-${sp.id}">${adCard(sp)}</section>` : ""}
      <section class="card b-det" aria-label="جزئیات">${detailsCard()}</section>
      <section class="card b-nic" aria-label="کارت شبکه">${nicCard()}</section>
      <section class="card b-ping" aria-label="پینگ">${pingCard()}</section>
      <section class="card b-load" aria-label="بار سرور">${loadCard()}</section>
      <section class="card b-hist" aria-label="تاریخچه‌ی ترافیک">${histCard()}</section>
    </div>
    ${!ov.node.bbr ? html`<div class="note warn" style="margin-top:14px">BBR روی این سرور فعال نیست. از <a href="#/settings" data-act="goto-sec" data-sec="net">تنظیمات شبکه</a> یکجا فعالش کنید.</div>` : ""}`;
  }

  const healthy = () => visible().filter((t) => { const g = grade(t.health); return t.up && g !== "bad" && g !== "down" && !(t.limits && t.limits.disabled); }).length;

  // The health of a tunnel, from real probes when available.
  function tunHealth(t) {
    const [c] = status(t);
    if (c !== "ok") return c;
    const g = grade(t.health);
    if (g === "down" || g === "bad") return "bad";
    if (g === "degraded") return "warn";
    return "ok";
  }

  // Destinations: one per peer or far address, with their tunnels.
  function destinations() {
    const ov = S.ov, groups = new Map();
    for (const t of visible()) {
      const key = t.peer || t.far_ip;
      if (!groups.has(key)) groups.set(key, { key, name: pname(t.peer) || "دستگاه دیگر", ip: t.far_ip, tunnels: [], paired: true });
      groups.get(key).tunnels.push(t);
    }
    for (const p of ov.peers) {
      if (!groups.has(p.name)) groups.set(p.name, { key: p.name, name: plabel(p), ip: p.address, tunnels: [], paired: p.paired });
      const g = groups.get(p.name); g.name = plabel(p); g.paired = p.paired; g.ip = p.address; g.version = p.version;
    }
    for (const m of masters()) if (!groups.has(m.name)) groups.set(m.name, { key: m.name, name: m.name, ip: m.address.replace(/:\d+$/, ""), tunnels: [], paired: m.paired });
    return [...groups.values()];
  }

  const lastVal = (vals) => { for (let i = (vals || []).length - 1; i >= 0; i--) if (vals[i] != null) return vals[i]; return null; };
  const trim = (s, n) => (s && s.length > n ? s.slice(0, n - 1) + "…" : s);
  const narrow = () => window.innerWidth < 760;

  // Flow: this server's total on the start side; a ribbon to every tunnel,
  // as thick as its share of the traffic, orange while it carries some.
  function flowCard() {
    const ts = visible(), tr = total(), sum = tr.rx + tr.tx;
    const head = html`<div class="flow-head"><div><h2 class="flow-title">Traffic<br>flow</h2><div class="sub">ترافیک لحظه‌ای تانل‌ها، دانلود و آپلود با هم</div><div class="flow-total"><b class="ltr">${fmtRate(sum)}</b> مجموع لحظه‌ای</div></div>
      <button class="btn sm" data-act="wizard">${I.plus(14)} تانل جدید</button></div>`;
    if (!ts.length) return html`${head}<div class="flow-empty"><div>${I.tunnel(34)}<p style="margin:10px 0 0">هنوز تانلی نیست. یک تانل بسازید تا مسیر ترافیک اینجا دیده شود.</p></div></div>${flowFoot()}`;
    const rowH = 58, H = Math.max(290, ts.length * rowH + 24), top = (H - ts.length * rowH) / 2;
    // Of the stage's 1000: the source box ends at src and the list starts
    // at out, or nearer its edge when CSS caps its width. The ribbons' own
    // box spans the gap between them (CSS), and they run across all of it.
    const src = narrow() ? 90 : 240, out = narrow() ? 400 : 560, sx = 0, ex = 1000;
    const rtl = document.documentElement.dir !== "ltr", X = (x) => (rtl ? 1000 - x : x);
    const rates = ts.map((t) => { const r = rate(t.name); return r.rx + r.tx; });
    // The ribbons fill the total's box edge to edge, at every rate: the box
    // is as tall as the bundle, and the widths are scaled to fit it.
    const n = ts.length, gap = 2;
    const box = Math.round(Math.max(54, Math.min(n * 28, Math.max(130, H * 0.3))));
    const room = box - gap * (n - 1);
    const raw0 = rates.map((r) => (sum > 1500 ? Math.max(5, (r / sum) * room) : room / n));
    const k = room / raw0.reduce((s, w) => s + w, 0), widths = raw0.map((w) => w * k);
    let a = H / 2 - box / 2;
    const defs = [], paths = [];
    ts.forEach((t, i) => {
      const w = widths[i], y = top + rowH * (i + 0.5), ew = Math.max(3, w * 0.6);
      const a0 = a, a1 = a + w; a += w + gap;
      const b0 = y - ew / 2, b1 = y + ew / 2, dx = (ex - sx) * 0.55;
      const hot = rates[i] > 1500 && !(t.limits && t.limits.disabled);
      const d = `M${X(sx)} ${a0.toFixed(1)} C${X(sx + dx)} ${a0.toFixed(1)} ${X(ex - dx)} ${b0.toFixed(1)} ${X(ex)} ${b0.toFixed(1)} L${X(ex)} ${b1.toFixed(1)} C${X(ex - dx)} ${b1.toFixed(1)} ${X(sx + dx)} ${a1.toFixed(1)} ${X(sx)} ${a1.toFixed(1)}Z`;
      const g = `rg${i}`;
      defs.push(`<linearGradient id="${g}" gradientUnits="userSpaceOnUse" x1="${X(sx)}" x2="${X(ex)}" y1="0" y2="0">${hot
        ? `<stop offset="0" stop-color="var(--ribbon)"/><stop offset=".45" stop-color="var(--ribbon)"/><stop offset="1" stop-color="var(--accent)"/>`
        : `<stop offset="0" stop-color="var(--ribbon)"/><stop offset="1" stop-color="var(--ribbon-2)"/>`}</linearGradient>`);
      paths.push(`<path class="ribbon" d="${d}" fill="url(#${g})"/>`);
      if (hot && !reduceMotion) {
        const m0 = (a0 + a1) / 2, m1 = y;
        paths.push(`<path class="ribbon-sheen" vector-effect="non-scaling-stroke" stroke-width="1.5" d="M${X(sx)} ${m0.toFixed(1)} C${X(sx + dx)} ${m0.toFixed(1)} ${X(ex - dx)} ${m1.toFixed(1)} ${X(ex)} ${m1.toFixed(1)}" style="animation-delay:-${((i * 0.37) % 2.4).toFixed(2)}s"/>`);
      }
    });
    const [v, u] = splitRate(sum);
    const outs = ts.map((t, i) => {
      const r = rate(t.name), off = t.limits && t.limits.disabled, hot = rates[i] > 1500 && !off, st = tunHealth(t);
      const [rv, ru] = splitRate(r.rx + r.tx);
      return html`<div class="out ${hot ? "hot" : "cold"}" style="top:${(top + rowH * (i + 0.5)).toFixed(1)}px" role="button" tabindex="0" data-act="arc" data-type="tunnel" data-key="${t.name}" aria-label="${tname(t)}">
        <div class="lbl"><small><i class="dot ${off ? "off" : st}"></i> <bdi>${tname(t)}</bdi> · ${kindTitle(t)}${t.peer ? html`<span class="pr"> · ${pname(t.peer)}</span>` : ""}</small><b>${off ? "خاموش" : html`${rv}<u>${ru}</u>`}</b></div>
        <button class="pill ${off ? "" : "on"}" data-act="power-ask" data-name="${t.name}" ${t.managed ? "disabled" : ""} aria-pressed="${!off}" title="${t.managed ? "این تانل را سرور اصلی مدیریت می‌کند" : off ? "روشن کردن تانل" : "خاموش کردن تانل"}" aria-label="${off ? "روشن کردن" : "خاموش کردن"} ${tname(t)}"></button>
      </div>`;
    });
    return html`${head}
      <div class="flow-stage" style="height:${H}px;--src:${src / 10}%;--out:${100 - out / 10}%">
        ${raw(`<svg class="ribbons" viewBox="0 0 1000 ${H}" preserveAspectRatio="none" aria-hidden="true"><defs>${defs.join("")}</defs>${paths.join("")}</svg>`)}
        <div class="source" style="top:${H / 2}px;height:${box}px"><div class="bar" style="flex:1"><small>مجموع لحظه‌ای</small><b data-live="total-rate">${fmtRate(sum)}</b></div><div class="tile ${sum > 1500 ? "" : "idle"}">${I.bolt(22)}</div></div>
        <div class="outs">${outs}</div>
      </div>
      ${flowFoot()}`;
  }

  // Under the flow: how long the server has run, and how busy it was over
  // the last hour.
  function flowFoot() {
    const ov = S.ov, load = ov.node.cpus ? (ov.node.load[0] / ov.node.cpus) * 100 : 0;
    const up = ov.node.uptime_s, d = Math.floor(up / 86400), h = Math.floor((up % 86400) / 3600);
    const [n, unit] = d ? [d, "روز"] : h ? [h, "ساعت"] : [Math.floor(up / 60), "دقیقه"];
    const level = load > 70 ? "زیاد" : load > 35 ? "متوسط" : "کم";
    const m = metrics("1h", ["cpu", "nic"]), s = m && m.series && m.series.cpu;
    return html`<div class="flow-foot">
      <div class="ff"><b>${n}<small>${unit}</small></b><span>روشن بودن سرور</span></div>
      <div class="ff split"><div class="row" style="gap:10px;flex-wrap:nowrap"><b style="font-size:15px">بار ${level}</b>${s ? sparkline(s.a) : ""}</div><span>پردازنده، یک ساعت گذشته</span></div>
    </div>`;
  }

  // fit: scale between the lowest and highest value instead of from zero,
  // for numbers that move a little around a large base (a ping of 90±5 ms).
  function sparkline(raw10s, { w = 120, h = 30, fit = false, points = 30 } = {}) {
    // Thirty averages: the trend of the hour, not every 10-second wobble.
    const per = Math.max(1, Math.ceil(raw10s.length / points)), vals = [];
    for (let i = 0; i < raw10s.length; i += per) {
      const g = raw10s.slice(i, i + per).filter((v) => v != null);
      vals.push(g.length ? g.reduce((a, b) => a + b, 0) / g.length : null);
    }
    const pts = vals.map((v, i) => [i, v]).filter(([, v]) => v != null);
    if (pts.length < 2) return "";
    const n = vals.length - 1, ys = pts.map(([, v]) => v);
    let lo = 0, hi = Math.max(5, ...ys);
    if (fit) { lo = Math.min(...ys); hi = Math.max(...ys); const pad = Math.max((hi - lo) * 0.15, 1); lo -= pad; hi += pad; }
    const x = (i) => (i / n) * w, y = (v) => h - 2 - ((v - lo) / (hi - lo)) * (h - 4);
    const d = pts.map(([i, v], k) => `${k ? "L" : "M"}${x(i).toFixed(1)} ${y(v).toFixed(1)}`).join("");
    const [li, lv] = pts[pts.length - 1];
    return raw(`<svg class="spark" viewBox="0 0 ${w} ${h}" aria-hidden="true"><defs><linearGradient id="spark-fill" x1="0" x2="0" y1="0" y2="1"><stop offset="0" stop-color="var(--accent)" stop-opacity=".28"/><stop offset="1" stop-color="var(--accent)" stop-opacity="0"/></linearGradient></defs>
      <path class="a" d="${d}L${x(li).toFixed(1)} ${h}L${x(pts[0][0]).toFixed(1)} ${h}Z"/><path class="l" d="${d}"/><circle cx="${x(li).toFixed(1)}" cy="${y(lv).toFixed(1)}" r="3"/></svg>`);
  }

  // The node: this server as an object under weather; its name, whether the
  // network is tuned, and the switch for live updates.
  function nodeCard() {
    const ov = S.ov, n = ov.node, hot = total().rx + total().tx > 1500, on = !S.paused;
    return html`${raw(nodeScene(hot))}
      <div class="node-top">
        <h2 class="node-name">${n.label || n.name}</h2>
        <div>${n.bbr ? html`<span class="node-pill"><i></i>BBR فعال</span>` : html`<a class="node-pill warn" href="#/settings" data-act="goto-sec" data-sec="net">BBR خاموش است</a>`}</div>
        <div class="node-meta"><span class="ltr">${n.address || "-"}</span> · ${n.cpus} هسته · ${fmtBytes(n.mem_total)}${masters().length ? ` · مدیریت از ${masters().map((m) => m.name).join("، ")}` : ""}</div>
      </div>
      <div class="node-foot">
        <span class="k">به‌روزرسانی زنده</span>
        <button class="knob ${on ? "on" : ""}" data-act="live-toggle" aria-pressed="${on}" aria-label="${on ? "توقف به‌روزرسانی زنده" : "ادامه‌ی به‌روزرسانی زنده"}" title="${on ? "پنل هر ۳ ثانیه تازه می‌شود؛ برای توقف بزنید" : "متوقف است؛ برای ادامه بزنید"}">${I.power(20)}</button>
        <span class="k ${on ? "on" : ""}">${on ? "روشن" : "خاموش"}</span>
      </div>`;
  }

  // Night, rain, a lightning bolt and the server, its edges lit while
  // traffic moves. Drawn, not photographed, so it follows the data.
  function nodeScene(hot) {
    const led = hot ? "var(--accent)" : "#5a5a60", glow = hot ? 0.75 : 0.18;
    let rain = "";
    for (let i = 0; i < 46; i++) {
      const x = (i * 53) % 420 - 10, y = (i * 97) % 300, l = 14 + (i % 4) * 6;
      rain += `<line x1="${x}" y1="${y}" x2="${x - 4}" y2="${y + l}"/>`;
    }
    return `<svg class="node-scene" viewBox="0 0 400 470" preserveAspectRatio="xMidYMid slice" aria-hidden="true">
      <defs>
        <linearGradient id="sky" x1="0" x2="0" y1="0" y2="1"><stop offset="0" stop-color="#2b2b31"/><stop offset=".55" stop-color="#1b1b1f"/><stop offset="1" stop-color="#101012"/></linearGradient>
        <radialGradient id="cloud" cx=".5" cy=".5" r=".5"><stop offset="0" stop-color="#4a4a52" stop-opacity=".9"/><stop offset="1" stop-color="#4a4a52" stop-opacity="0"/></radialGradient>
        <radialGradient id="floor" cx=".5" cy=".5" r=".5"><stop offset="0" stop-color="${led}" stop-opacity="${glow * 0.55}"/><stop offset="1" stop-color="${led}" stop-opacity="0"/></radialGradient>
        <linearGradient id="front" x1="0" x2="0" y1="0" y2="1"><stop offset="0" stop-color="#4a4a51"/><stop offset="1" stop-color="#242428"/></linearGradient>
        <linearGradient id="side" x1="0" x2="1"><stop offset="0" stop-color="#2a2a2e"/><stop offset="1" stop-color="#18181b"/></linearGradient>
        <linearGradient id="topf" x1="0" x2="0" y1="0" y2="1"><stop offset="0" stop-color="#5c5c63"/><stop offset="1" stop-color="#3c3c42"/></linearGradient>
        <filter id="blur8" x="-50%" y="-50%" width="200%" height="200%"><feGaussianBlur stdDeviation="8"/></filter>
        <filter id="glow" x="-50%" y="-50%" width="200%" height="200%"><feGaussianBlur stdDeviation="2.4" result="b"/><feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter>
      </defs>
      <rect width="400" height="470" fill="url(#sky)"/>
      <ellipse cx="80" cy="150" rx="150" ry="60" fill="url(#cloud)"/><ellipse cx="320" cy="120" rx="170" ry="70" fill="url(#cloud)"/><ellipse cx="220" cy="200" rx="140" ry="40" fill="url(#cloud)" opacity=".6"/>
      <polyline points="318,96 300,140 316,142 292,196 306,198 286,250" fill="none" stroke="#ffe3d4" stroke-width="1.6" stroke-linejoin="round" filter="url(#glow)" opacity=".55"/>
      <g stroke="#c8c8d0" stroke-opacity=".13" stroke-width="1">${rain}</g>
      <rect y="330" width="400" height="140" fill="#0d0d0f"/>
      <ellipse cx="200" cy="332" rx="260" ry="16" fill="#1d1d21"/>
      <ellipse cx="205" cy="372" rx="150" ry="26" fill="url(#floor)"/>
      <g transform="translate(118 228)">
        <path d="M14 0 H148 L176 -18 H42 Z" fill="url(#topf)"/>
        <path d="M148 0 L176 -18 V96 L148 118 Z" fill="url(#side)"/>
        <rect x="0" y="0" width="150" height="118" rx="10" fill="url(#front)"/>
        <rect x="12" y="14" width="126" height="22" rx="4" fill="#161619"/>
        <rect x="18" y="21" width="${hot ? 64 : 30}" height="8" rx="2" fill="${led}" opacity=".85" filter="url(#glow)"/>
        <g stroke="#1a1a1d" stroke-width="2" stroke-linecap="round"><line x1="16" y1="52" x2="96" y2="52"/><line x1="16" y1="62" x2="96" y2="62"/><line x1="16" y1="72" x2="96" y2="72"/><line x1="16" y1="82" x2="96" y2="82"/></g>
        <circle cx="122" cy="68" r="13" fill="#1a1a1d"/><circle cx="122" cy="68" r="5" fill="${led}" filter="url(#glow)"/>
        <path d="M3 108 V12 Q3 3 12 3" fill="none" stroke="${led}" stroke-width="2" filter="url(#glow)" stroke-linecap="round"/>
        <path d="M148 4 L174 -14" fill="none" stroke="${led}" stroke-width="1.6" filter="url(#glow)" stroke-linecap="round"/>
        <line x1="10" y1="116" x2="140" y2="116" stroke="${led}" stroke-width="1.6" filter="url(#glow)" stroke-linecap="round"/>
        <rect x="14" y="118" width="22" height="9" rx="3" fill="#0f0f11"/><rect x="114" y="118" width="22" height="9" rx="3" fill="#0f0f11"/>
      </g>
    </svg>`;
  }

  function detailsCard() {
    const ov = S.ov, ts = visible().filter((t) => t.health && t.health.probes >= 3 && t.health.passes);
    const avgPing = ts.length ? ts.reduce((a, t) => a + t.health.ping_ms, 0) / ts.length : null;
    const avgLoss = ts.length ? ts.reduce((a, t) => a + t.health.loss, 0) / ts.length : null;
    const ok = healthy(), all = visible().length;
    const nic = ov.node.nic ? ov.node.nic_month.rx + ov.node.nic_month.tx : sumMonth();
    const item = (k, v, icon) => html`<div class="det"><div><span>${k}</span><b>${v}</b></div>${icon(16)}</div>`;
    return html`<div class="det-t"><h2>Details</h2><span class="faint small">جزئیات این سرور</span></div>
      <div class="det-grid">
        ${item("تانل‌های سالم", `${ok} / ${all}`, I.tunnel)}
        ${item("روشن بودن", fmtUptimeShort(ov.node.uptime_s), I.clock)}
        ${item("میانگین پینگ", avgPing == null ? "—" : ms(avgPing), I.wave)}
        ${item("افت بسته", avgLoss == null ? "—" : `${avgLoss.toFixed(avgLoss < 1 ? 1 : 0)}%`, I.drop)}
        ${item("امروز", fmtBytes(sumToday()), I.cal)}
        ${item(ov.node.nic ? "این ماه، کارت شبکه" : "این ماه", fmtBytes(nic), I.globe)}
      </div>`;
  }
  const fmtUptimeShort = (s) => { const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60); return d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : `${m}m`; };

  // The network card: a plug in a lens, today's rates on callouts.
  function nicCard() {
    const ov = S.ov, nic = ov.node.nic || "nic", m = metrics("1h", ["cpu", "nic"]), s = m && m.series && m.series.nic;
    const r = { rx: s ? lastVal(s.a) || 0 : 0, tx: s ? lastVal(s.b) || 0 : 0 };
    const today = ov.node.nic ? ov.node.nic_today.rx + ov.node.nic_today.tx : sumToday();
    return html`${raw(`<svg class="nic-art" viewBox="0 0 300 190" aria-hidden="true">
        <defs><linearGradient id="plug" x1="0" x2="1"><stop offset="0" stop-color="#f1f1f4"/><stop offset=".6" stop-color="#bdbdc4"/><stop offset="1" stop-color="#8d8d95"/></linearGradient>
          <radialGradient id="lens" cx=".45" cy=".4" r=".6"><stop offset="0" stop-color="#fff" stop-opacity=".22"/><stop offset="1" stop-color="#fff" stop-opacity="0"/></radialGradient></defs>
        <path d="M-10 196 C 60 170, 70 120, 128 104" fill="none" stroke="#161618" stroke-width="16" stroke-linecap="round"/>
        <path d="M-10 190 C 58 166, 68 118, 126 100" fill="none" stroke="#4a4a50" stroke-width="3" stroke-linecap="round" opacity=".7"/>
        <g transform="translate(150 86) rotate(-28)">
          <rect x="-34" y="-20" width="44" height="40" rx="5" fill="#1d1d20"/>
          <rect x="6" y="-24" width="52" height="48" rx="6" fill="url(#plug)"/>
          <g stroke="var(--accent)" stroke-width="2.2" stroke-linecap="round"><line x1="50" y1="-16" x2="50" y2="-6"/><line x1="50" y1="-2" x2="50" y2="8"/><line x1="50" y1="12" x2="50" y2="18"/></g>
          <rect x="16" y="24" width="26" height="8" rx="2" fill="#a0a0a8"/>
        </g>
        <circle cx="152" cy="88" r="62" fill="url(#lens)" stroke="#fff" stroke-opacity=".5" stroke-width="1.4"/>
        <circle cx="152" cy="88" r="70" fill="none" stroke="#fff" stroke-opacity=".14" stroke-width="1"/>
        <circle cx="203" cy="52" r="3" fill="#fff"/><line x1="206" y1="50" x2="236" y2="34" stroke="#fff" stroke-opacity=".6"/>
        <text x="290" y="28" text-anchor="end" direction="ltr">↓ ${esc(fmtRate(r.rx))}</text><text class="cap" x="290" y="42" text-anchor="end" direction="ltr">دریافت</text>
        <circle cx="100" cy="128" r="3" fill="#fff"/><line x1="97" y1="130" x2="62" y2="150" stroke="#fff" stroke-opacity=".6"/>
        <text x="14" y="164" direction="ltr">↑ ${esc(fmtRate(r.tx))}</text><text class="cap" x="14" y="178" direction="ltr">ارسال</text>
      </svg>`)}
      <div class="nic-body"><div><span>کارت شبکه</span><b>${nic}</b></div><div class="today"><span>امروز</span><b>${fmtBytes(today)}</b></div></div>`;
  }

  // Ping: a die with traces; the average over working tunnels in its centre.
  function pingCard() {
    const ts = visible().filter((t) => t.health && t.health.probes >= 3 && t.health.passes);
    const vals = ts.map((t) => t.health.ping_ms), avg = vals.length ? vals.reduce((a, b) => a + b, 0) / vals.length : null;
    const lo = vals.length ? Math.min(...vals) : null, hi = vals.length ? Math.max(...vals) : null;
    const pins = [];
    for (let k = 0; k < 6; k++) {
      const y = 62 + k * 14, hot = k === 1 || k === 4;
      pins.push(`<path d="M104 ${y} H${70 - k * 4} V${y + (k < 3 ? -10 : 10)} H${20 + k * 3}" class="${hot ? "run" : ""}" stroke="${hot ? "var(--accent)" : "var(--card-3)"}"/>`);
      pins.push(`<path d="M196 ${y} H${230 + k * 4} V${y + (k < 3 ? -10 : 10)} H${282 - k * 3}" class="${k === 2 ? "run" : ""}" stroke="${k === 2 ? "var(--accent)" : "var(--card-3)"}"/>`);
    }
    // The hour's trend: the working tunnels' pings averaged per 10 seconds.
    const m = ts.length ? metrics("1h", ts.map((t) => "p:" + t.name)) : null, se = (m && m.series) || {};
    const lines = ts.map((t) => se["p:" + t.name] && se["p:" + t.name].a).filter(Boolean);
    const avgs = lines.length ? lines[0].map((_, i) => { const g = lines.map((l) => l[i]).filter((v) => v != null && v > 0); return g.length ? g.reduce((a, b) => a + b, 0) / g.length : null; }) : [];
    const trend = avgs.length ? sparkline(avgs, { w: 300, h: 54, fit: true, points: 60 }) : "";
    return html`${raw(`<svg class="ping-art" viewBox="0 0 300 196" aria-hidden="true">
        <defs><linearGradient id="die" x1="0" x2="0" y1="0" y2="1"><stop offset="0" stop-color="var(--card-3)"/><stop offset="1" stop-color="var(--card-2)"/></linearGradient></defs>
        <g fill="none" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${pins.join("")}</g>
        <g stroke="var(--ink-3)" stroke-width="2">${[0, 1, 2, 3, 4, 5].map((k) => `<line x1="96" y1="${62 + k * 14}" x2="104" y2="${62 + k * 14}"/><line x1="196" y1="${62 + k * 14}" x2="204" y2="${62 + k * 14}"/>`).join("")}</g>
        <rect x="104" y="46" width="92" height="100" rx="12" fill="url(#die)" stroke="var(--line-2)"/>
        <rect x="116" y="58" width="68" height="40" rx="8" fill="var(--card)" stroke="var(--line)"/>
        <text class="die-v" x="150" y="85" text-anchor="middle" direction="ltr">${avg == null ? "—" : esc(avg.toFixed(avg < 100 ? 1 : 0))}</text>
        <text class="die-k" x="150" y="120" text-anchor="middle">PING</text>
        <text class="die-l" x="150" y="134" text-anchor="middle">AVG · MS</text>
      </svg>`)}
      <div class="ping-body">${lo == null ? html`<span>پینگی ثبت نشده</span>` : html`<span>کمترین <b>${ms(lo)}</b></span><span>بیشترین <b>${ms(hi)}</b></span>`}
        <a class="btn sm" href="#/stats">${I.chart(14)} نمودار</a></div>
      ${trend ? html`<div class="ping-trend">${trend}<span>میانگین پینگ، یک ساعت گذشته</span></div>` : ""}`;
  }

  // Load: the processor's share in use, on a horseshoe.
  // Whether the CPU limits the speed, from the daemon's last reading: packets
  // dropped for lack of CPU, a core full of packet work, the packet budget
  // running out, or fragments (a too-large MTU costs CPU on every packet).
  function cpuVerdict(p, cpus) {
    if (!p) return null;
    const u = p.usage;
    if (p.conntrack_max && p.conntrack / p.conntrack_max >= 0.85) return ["bad", `جدول ردیابی اتصال‌ها تقریباً پر است (${p.conntrack.toLocaleString("fa-IR")} از ${p.conntrack_max.toLocaleString("fa-IR")})؛ وقتی پر شود، اتصال‌های تازه‌ی کاربران دور ریخته می‌شوند. مقدار net.netfilter.nf_conntrack_max را بالا ببرید.`];
    if (p.drops >= 1) return ["bad", `کرنل ${p.drops.toFixed(0)} بسته در ثانیه را برای کمبود CPU دور ریخت؛ سرعت این سرور به CPU محدود است.`];
    if (u.softirq_hot >= 80) return ["warn", `یک هسته ${u.softirq_hot.toFixed(0)}٪ وقتش را صرف پردازش بسته‌ها می‌کند؛ سرور نزدیک سقف سرعتش است.${cpus > 1 ? " حالت سرعت «حداکثر» این کار را بین هسته‌ها پخش می‌کند." : ""}`];
    if (p.squeezes >= 1) return ["warn", "بودجه‌ی کرنل برای پردازش بسته‌ها گاهی تمام می‌شود؛ سرور نزدیک سقف CPU است."];
    if (p.frags >= 50) return ["warn", `${p.frags.toFixed(0)} قطعه‌ی IP در ثانیه ساخته یا سرهم می‌شود؛ احتمالاً MTU یکی از تانل‌ها بزرگ است و بسته‌ها دو تکه می‌شوند.`];
    return ["ok", "CPU گلوگاه سرعت نیست."];
  }

  function loadCard() {
    const n = S.ov.node, cpu = n.cpus ? Math.min(100, (n.load[0] / n.cpus) * 100) : 0;
    const m = metrics("1h", ["cpu", "nic"]), s = m && m.series && m.series.cpu;
    const busy = s ? lastVal(s.a) : null, hot = s ? lastVal(s.b) : null;
    const v = busy == null ? cpu : busy, pr = n.pressure, vd = cpuVerdict(pr, n.cpus);
    const cx = 165, cy = 150, R = 108, a0 = 150, sweep = 240;
    const pt = (deg) => { const a = (deg * Math.PI) / 180; return [cx + R * Math.cos(a), cy + R * Math.sin(a)]; };
    const arc = (d0, d1) => { const [x0, y0] = pt(d0), [x1, y1] = pt(d1); return `M${x0.toFixed(1)} ${y0.toFixed(1)} A${R} ${R} 0 ${d1 - d0 > 180 ? 1 : 0} 1 ${x1.toFixed(1)} ${y1.toFixed(1)}`; };
    const len = (Math.PI * R * sweep) / 180, f = Math.max(0, Math.min(1, v / 100));
    const [ex0, ey0] = pt(a0), [ex1, ey1] = pt(a0 + sweep);
    return html`<div class="load-t"><h2>Server load</h2><span>پردازنده، الان${n.cpus ? ` · ${n.cpus} هسته` : ""}</span><button class="btn sm ghost cpu-open" data-act="cpu-open" title="CPU کجا مصرف می‌شود، با نمودار">${I.chart(14)} جزئیات</button></div>
      ${raw(`<svg class="horse" viewBox="0 0 330 250" role="img" aria-label="پردازنده ${v.toFixed(0)} درصد">
        <defs><linearGradient id="horse-fill" x1="0" x2="1"><stop offset="0" stop-color="var(--accent)"/><stop offset="1" stop-color="var(--accent-2)"/></linearGradient></defs>
        <path class="trk" d="${arc(a0, a0 + sweep)}"/>
        <path class="rest" d="${arc(a0, a0 + sweep)}" stroke-dasharray="${len.toFixed(1)}" stroke-dashoffset="${(-len * f).toFixed(1)}" opacity=".35"/>
        <path class="val" d="${arc(a0, a0 + sweep)}" stroke-dasharray="${len.toFixed(1)}" stroke-dashoffset="${(len * (1 - f)).toFixed(1)}"/>
        <path class="bolt" transform="translate(157 104) scale(.7)" d="M13 2 4.5 13.5H12L11 22l8.5-11.5H12z"/>
        <text class="big" x="${cx}" y="${cy + 22}" text-anchor="middle" direction="ltr">${v.toFixed(0)}%</text>
        <text class="end" x="${ex0.toFixed(0)}" y="${(ey0 + 34).toFixed(0)}" text-anchor="middle">0%</text>
        <text class="end" x="${ex1.toFixed(0)}" y="${(ey1 + 34).toFixed(0)}" text-anchor="middle">100%</text>
      </svg>`)}
      <div class="load-foot"><span>حافظه<b>${fmtBytes(n.mem_used)} / ${fmtBytes(n.mem_total)}</b></span><span title="یک تانل معمولاً روی یک هسته پردازش می‌شود؛ اگر این عدد نزدیک ۱۰۰ باشد، همان هسته گلوگاه سرعت است">پرکارترین هسته<b>${hot == null ? "—" : hot.toFixed(0) + "%"}</b></span>${pr ? html`<span class="${vd[0] === "ok" ? "" : vd[0]}" title="${vd[1]}">کار بسته‌ها<b>${pr.usage.softirq_hot.toFixed(0)}%</b></span>` : ""}</div>`;
  }

  // History on the dashboard: all tunnels, or the network card, over a range.
  function histCard() {
    const h = S.hist, key = h.src === "nic" ? "nic" : "all";
    const m = metrics(h.range, ["all", "nic"]), s = m && m.series && m.series[key];
    return html`<div class="hist-head"><h2>Traffic history</h2><span class="faint small">${h.src === "nic" ? "همه‌ی ترافیک کارت شبکه" : "ترافیک همه‌ی تانل‌ها"}</span><span class="grow"></span>
        <div class="seg sm" role="radiogroup" aria-label="منبع">${[["all", "تانل‌ها"], ["nic", "کارت شبکه"]].map(([v, l]) => html`<button type="button" role="radio" aria-checked="${h.src === v}" class="${h.src === v ? "on" : ""}" data-act="hist-src" data-value="${v}">${l}</button>`)}</div>
        ${rangeSeg("hist-range", h.range)}
        <a class="btn sm" href="#/stats">${I.chart(14)} آمار کامل</a></div>
      ${rateFigs(s)}
      <div class="chart-wrap ${m === undefined ? "loading" : ""}">${rateChart("dash-hist", m, s, "tall")}</div>`;
  }

  const rangeSeg = (act, cur, only) => html`<div class="seg sm" role="radiogroup" aria-label="بازه">${RANGES.filter(([v]) => !only || only.includes(v)).map(([v, l]) => html`<button type="button" role="radio" aria-checked="${cur === v}" class="${cur === v ? "on" : ""}" data-act="${act}" data-value="${v}">${l}</button>`)}</div>`;

  // Average, peak and volume of both directions of a rate series.
  function rateFigs(s) {
    const f = (k, v, cls) => html`<div class="fig"><span><i style="background:var(--${cls})"></i>${k}</span><b>${v}</b></div>`;
    if (!s) return html`<div class="figrow">${["میانگین دانلود", "اوج دانلود", "حجم دانلود", "میانگین آپلود", "اوج آپلود", "حجم آپلود"].map((k, i) => f(k, "—", i < 3 ? "rx" : "tx"))}</div>`;
    return html`<div class="figrow">
      ${f("میانگین دانلود", fmtRate(s.avg[0]), "rx")}${f("اوج دانلود", fmtRate(s.peak[0]), "rx")}${f("حجم دانلود", fmtBytes(s.sum[0] / 8), "rx")}
      ${f("میانگین آپلود", fmtRate(s.avg[1]), "tx")}${f("اوج آپلود", fmtRate(s.peak[1]), "tx")}${f("حجم آپلود", fmtBytes(s.sum[1] / 8), "tx")}</div>`;
  }

  function rateChart(id, m, s, size) {
    return tsChart(id, m, s ? [{ name: "دانلود", cls: "rx", vals: s.a, peaks: s.pa }, { name: "آپلود", cls: "tx", vals: s.b, peaks: s.pb }] : [], "bps", { size, legend: true });
  }

  // ---------- drawer ----------
  function drawerChart(t) {
    const k = "t:" + t.name, m = metrics(S.dr.range, [k]);
    return rateChart("dr-" + t.name, m, m && m.series && m.series[k], "short");
  }

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
              <i class="dot ${status(t)[0]}"></i><span class="k">${tname(t)}</span><span class="faint">${kindTitle(t)}</span><span class="m ltr" data-live="rx:${t.name}">${fmtRate(rate(t.name).rx)}</span></button>`)}</div>`
            : html`<div class="muted small">هنوز تانلی به این مقصد نیست.</div>`}</div>
          <div class="row">${p ? html`<button class="btn sm" data-act="setup" data-name="${p.name}">${I.term(15)} دستور اتصال</button>` : ""}
            <button class="btn sm primary" data-act="wizard" data-peer="${p ? p.name : ""}">${I.plus(15)} تانل به این مقصد</button></div>
        </div>`;
    } else {
      const t = tunnel(d.name);
      if (!t) { S.drawer = null; return ""; }
      const r = rate(t.name), fwd = S.ov.forwards.filter((f) => f.tunnel === t.name);
      body = html`<header><div><h3>${tname(t)}</h3><div class="sub">${kindTitle(t)} به ${pname(t.peer) || "دستگاه دیگر"} <span class="ltr">${t.far_ip}</span></div></div>
          <button class="btn sm ghost icon" data-act="drawer-close" aria-label="بستن">${I.x(16)}</button></header>
        <div class="body">
          <div class="row">${stateEl(t)}${healthTag(t)}${t.auto_encrypt ? encryptTag(t) : t.encrypt ? html`<span class="tag lock">${I.lock(12)} ${cipherTitle(t.encrypt)}</span>` : ""}${t.obfs ? html`<span class="tag lock">${I.mask(12)} مبهم</span>` : ""}${t.managed ? html`<span class="tag warn">از سرور اصلی</span>` : ""}</div>
          <div class="figs">
            <div><div class="k">دانلود</div><div class="v ltr" data-live="rx:${t.name}">${fmtRate(r.rx)}</div></div>
            <div><div class="k">آپلود</div><div class="v ltr" data-live="tx:${t.name}">${fmtRate(r.tx)}</div></div>
            <div><div class="k">امروز</div><div class="v ltr" data-live="today:${t.name}">${fmtBytes(t.today.rx + t.today.tx)}</div></div>
            <div><div class="k">این ماه</div><div class="v ltr">${fmtBytes(t.month.rx + t.month.tx)}</div></div>
            <div><div class="k">از ابتدا</div><div class="v ltr">${fmtBytes(t.total.rx + t.total.tx)}</div></div>
            <div><div class="k">MTU</div><div class="v ltr">${t.mtu || "-"}</div></div>
          </div>
          <div><h4>ترافیک ${rangeSeg("dr-range", S.dr.range, ["1h", "24h", "7d", "30d"])}</h4>${drawerChart(t)}</div>
          <div><h4>لایه‌ها</h4><div class="layers">${(t.stack || []).map((l) => html`<div class="layer"><i class="dot ${l.up ? "ok" : "bad"}"></i><span class="k">${l.name}</span><span class="faint">${l.kind}</span><span class="m">MTU ${l.mtu || "-"}</span></div>`)}</div></div>
          <dl class="kv">
            ${t.here4 ? html`<dt>IPv4 این طرف / آن طرف</dt><dd class="ltr">${t.here4.split("/")[0]} ⇄ ${t.there4}</dd>` : ""}
            ${t.here6 ? html`<dt>IPv6</dt><dd class="ltr small">${t.here6.split("/")[0]} ⇄ ${t.there6}</dd>` : ""}
            ${t.base_port ? html`<dt>پورت UDP</dt><dd class="ltr">${t.base_port}</dd>` : ""}
            ${t.path_mtu ? html`<dt>MTU مسیر (اندازه‌گیری‌شده)</dt><dd class="ltr">${t.path_mtu}</dd>` : ""}
            ${t.wg_key ? html`<dt>کلید عمومی WireGuard</dt><dd class="ltr small sens">${t.wg_key}</dd>` : ""}
            ${fwd.length ? html`<dt>پورت‌ها</dt><dd>${fwd.map((f) => html`<span class="tag ltr">${f.ports}${f.to ? " → " + f.to : ""}</span> `)}</dd>` : ""}
          </dl>
          ${t.error ? html`<div class="note bad"><div class="mono">${t.error}</div></div>` : ""}
          <div class="row">
            <button class="btn sm" data-act="test" data-name="${t.name}">${I.gauge(15)} تست سرعت</button>
            <button class="btn sm" data-act="st-pick" data-name="${t.name}">${I.chart(15)} آمار</button>
            ${t.managed ? "" : html`<button class="btn sm" data-act="mtu" data-name="${t.name}">${I.ruler(15)} بهترین MTU</button>
              <button class="btn sm" data-act="open-tunnel" data-name="${t.name}">${I.gear(15)} تنظیمات</button>`}
          </div>
        </div>`;
    }
    const enter = S.drawerEnter ? " enter" : "";
    S.drawerEnter = false;
    return html`<div class="drawer-scrim${enter}" data-act="drawer-close"></div><aside class="drawer${enter}" role="dialog" aria-modal="true" aria-label="جزئیات">${body}</aside>`;
  }

  // ---------- history: fetching and drawing ----------
  // The daemon keeps every series at five resolutions; a request names a
  // range and the series it wants. Answers are kept until a newer point
  // can exist (10 s for the last hour, longer for longer ranges).
  const RANGES = [["1h", "۱ ساعت"], ["6h", "۶ ساعت"], ["24h", "۲۴ ساعت"], ["7d", "۷ روز"], ["30d", "۳۰ روز"], ["90d", "۹۰ روز"], ["all", "از ابتدا"]];
  const M = { cache: {}, busy: {} };
  function metrics(range, keys) {
    const k = range + "|" + keys.join(","), c = M.cache[k];
    const ttl = range === "1h" ? 10e3 : range === "6h" || range === "24h" ? 60e3 : 300e3;
    if ((!c || Date.now() - c.at > ttl) && !M.busy[k]) {
      M.busy[k] = true;
      api("GET", `metrics?range=${range}${keys.length ? "&keys=" + encodeURIComponent(keys.join(",")) : ""}`)
        .then((v) => { M.cache[k] = { at: Date.now(), v }; })
        .catch(() => { M.cache[k] = { at: Date.now(), v: null }; })
        .finally(() => { delete M.busy[k]; if (S.modal && !S.modal.passive) return; if (S.edit) patchLive(); else draw(); });
    }
    return c ? c.v : undefined; // undefined: on its way; null: unavailable
  }

  const fmtU = (v, unit) => v == null || isNaN(v) ? "—" : unit === "bps" ? fmtRate(v) : unit === "%" ? `${v.toFixed(v < 10 ? 1 : 0)}%` : unit === "ms" ? ms(v) : v.toFixed(2);
  const dtf = (o) => new Intl.DateTimeFormat("fa-IR-u-nu-latn", o);
  const F = { hm: dtf({ hour: "2-digit", minute: "2-digit", hour12: false }), dm: dtf({ day: "numeric", month: "short" }), dmhm: dtf({ day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", hour12: false }), dmy: dtf({ day: "numeric", month: "long", year: "numeric" }) };
  // An axis label: the time of day within a day, the date beyond.
  const axisTime = (unix, span) => (span <= 2 * 86400 ? F.hm : F.dm).format(new Date(unix * 1000));
  // A tooltip's time: the interval a point covers.
  const pointTime = (unix, step) => step >= 86400 ? F.dmy.format(new Date(unix * 1000)) : `${F.dmhm.format(new Date(unix * 1000))}${step > 60 ? ` تا ${F.hm.format(new Date((unix + step) * 1000))}` : ""}`;

  // A time series: each line's averages over a 10% wash, its peaks as a
  // thin line of the same colour above, gaps where nothing was recorded.
  // One axis and one unit per chart; hovering shows every line at that time.
  function tsChart(id, v, lines, unit, opts = {}) {
    const H = opts.size === "tall" ? 240 : opts.size === "short" ? 130 : 190, W = 1000, P = { t: 10, b: 2 };
    const legend = opts.legend !== false && lines.length > 1 ? html`<div class="legend">${lines.map((l) => html`<span><i class="${l.cls}" style="background:var(--${l.cls})"></i>${l.name}</span>`)}${lines.some((l) => l.peaks) ? html`<span><i class="pk"></i>محدوده‌ی روشن: تا اوج همان بازه</span>` : ""}</div>` : "";
    const has = v && v.points && lines.some((l) => l.vals && l.vals.some((x) => x != null));
    if (!has) {
      const why = v === undefined ? "در حال خواندن…" : v === null ? "سرویس روی این سرور هنوز تاریخچه نگه نمی‌دارد؛ بعد از به‌روزرسانی جمع می‌شود." : "در این بازه داده‌ای ثبت نشده.";
      return html`${legend}<div class="chart ${opts.size || ""}"><div class="plot"><svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="none" aria-hidden="true"><line class="base" x1="0" x2="${W}" y1="${H - P.b}" y2="${H - P.b}" vector-effect="non-scaling-stroke"/></svg></div><div class="nodata">${why}</div></div><div class="chart-gap"></div>`;
    }
    const n = v.points;
    let max = Math.max(...lines.flatMap((l) => (l.peaks || l.vals).filter((x) => x != null)), unit === "bps" ? 1000 : unit === "%" ? 5 : 1);
    max = unit === "%" && max > 50 ? 100 : niceMax(max);
    // fit: from just under the lowest value to just over the highest, for a
    // value that moves a little around a large base (a ping of 80 to 82).
    let min = 0;
    if (opts.fit) {
      const vs = lines.flatMap((l) => l.vals.filter((v) => v != null)), ps = lines.flatMap((l) => (l.peaks || l.vals).filter((v) => v != null));
      const lo = Math.min(...vs), hi = Math.max(...ps), pad = Math.max((hi - lo) * 0.12, 1);
      min = Math.max(0, Math.floor(lo - pad)); max = Math.ceil(hi + pad);
    }
    const x = (i) => (n === 1 ? W / 2 : (i / (n - 1)) * W), y = (val) => P.t + (1 - (Math.max(min, Math.min(val, max)) - min) / (max - min)) * (H - P.t - P.b);
    const runs = (vals) => { const out = []; let cur = null; vals.forEach((val, i) => { if (val == null) { cur = null; return; } if (!cur) out.push((cur = [])); cur.push([i, val]); }); return out; };
    const line = (vals) => runs(vals).map((r) => r.map(([i, val], k) => `${k ? "L" : "M"}${x(i).toFixed(1)} ${y(val).toFixed(1)}`).join("") + (r.length === 1 ? `h2` : "")).join("");
    // The band between a line's averages and its peaks, where both exist.
    const band = (vals, peaks) => runs(vals.map((v, i) => (v == null || peaks[i] == null ? null : [v, peaks[i]]))).map((r) =>
      r.map(([i, [, p]], k) => `${k ? "L" : "M"}${x(i).toFixed(1)} ${y(p).toFixed(1)}`).join("") + r.slice().reverse().map(([i, [v]]) => `L${x(i).toFixed(1)} ${y(v).toFixed(1)}`).join("") + "Z").join("");
    const area = (vals) => runs(vals).map((r) => `M${x(r[0][0]).toFixed(1)} ${y(min)}` + r.map(([i, val]) => `L${x(i).toFixed(1)} ${y(val).toFixed(1)}`).join("") + `L${x(r[r.length - 1][0]).toFixed(1)} ${y(min)}Z`).join("");
    let grid = "", labels = "";
    for (const f of [0, 0.5, 1]) {
      const at = min + (max - min) * f;
      grid += `<line class="${f ? "grid" : "base"}" x1="0" x2="${W}" y1="${y(at).toFixed(1)}" y2="${y(at).toFixed(1)}" vector-effect="non-scaling-stroke"/>`;
      labels += `<span class="ya" style="top:${((y(at) / H) * 100).toFixed(2)}%">${esc(fmtU(at, unit))}</span>`;
    }
    const span = v.step * n, ticks = narrow() ? 3 : 5;
    let xl = "";
    for (let k = 0; k < ticks; k++) {
      const i = Math.round((k * (n - 1)) / (ticks - 1));
      const t = k === ticks - 1 ? "اکنون" : axisTime(v.start + i * v.step, span);
      xl += `<span class="xa ${k === 0 ? "first" : k === ticks - 1 ? "last" : ""}" style="left:${((x(i) / W) * 100).toFixed(2)}%">${esc(t)}</span>`;
    }
    const body = lines.map((l) => `<path class="area ${l.cls}" d="${area(l.vals)}"/>${l.peaks ? `<path class="band ${l.cls}" d="${band(l.vals, l.peaks)}"/>` : ""}<path class="line ${l.cls}" d="${line(l.vals)}" vector-effect="non-scaling-stroke"/>`).join("");
    CHARTS[id] = { n, x, y, W, H, lines, unit, start: v.start, step: v.step };
    return html`${legend}<div class="chart ${opts.size || ""}" data-chart="${id}">
      <div class="plot">${raw(`<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="none" role="img" aria-label="${esc(opts.label || "نمودار")}">${grid}${body}
        <g class="hover" style="display:none"><line class="cross" y1="${P.t}" y2="${H - P.b}" vector-effect="non-scaling-stroke"/>${lines.map((l) => `<circle class="mark ${l.cls}" r="4"/>`).join("")}</g></svg>`)}${raw(xl)}<div class="tip" hidden></div></div>${raw(labels)}</div><div class="chart-gap"></div>`;
  }
  const CHARTS = {};
  const niceMax = (v) => { if (!(v > 0)) return 1; const e = 10 ** Math.floor(Math.log10(v)), m = v / e; return (m <= 1 ? 1 : m <= 2 ? 2 : m <= 2.5 ? 2.5 : m <= 5 ? 5 : 10) * e; };

  function wireCharts() {
    for (const el of $$("[data-chart]")) {
      if (el._wired) continue;
      el._wired = true;
      const plot = $(".plot", el);
      const show = (clientX) => {
        const c = CHARTS[el.dataset.chart], svgEl = $("svg", el), g = $(".hover", el), tip = $(".tip", el);
        if (!c || !svgEl || !g) return;
        const box = svgEl.getBoundingClientRect();
        const i = Math.max(0, Math.min(c.n - 1, Math.round(((clientX - box.left) / box.width) * (c.n - 1))));
        g.style.display = "";
        const cross = $("line", g); cross.setAttribute("x1", c.x(i)); cross.setAttribute("x2", c.x(i));
        $$("circle", g).forEach((m, k) => { const val = c.lines[k].vals[i]; m.style.display = val == null ? "none" : ""; if (val != null) { m.setAttribute("cx", c.x(i)); m.setAttribute("cy", c.y(val)); } });
        tip.hidden = false;
        tip.innerHTML = part(html`<div class="t">${pointTime(c.start + i * c.step, c.step)}</div>${c.lines.map((l) => html`<div class="s"><i style="background:var(--${l.cls})"></i>${l.name}<b>${fmtU(l.vals[i], c.unit)}</b>${l.peaks && l.peaks[i] != null ? html`<em>اوج ${fmtU(l.peaks[i], c.unit)}</em>` : ""}</div>`)}`);
        const px = (c.x(i) / c.W) * box.width;
        tip.style.left = Math.min(box.width - tip.offsetWidth, Math.max(0, px - tip.offsetWidth / 2)) + "px";
        tip.style.top = "-12px"; tip.style.transform = "translateY(-100%)";
      };
      const hide = () => { const g = $(".hover", el), tip = $(".tip", el); if (g) g.style.display = "none"; if (tip) tip.hidden = true; };
      plot.addEventListener("pointermove", (e) => show(e.clientX));
      plot.addEventListener("pointerleave", hide);
      // Keyboard: arrows move along the time axis.
      el.tabIndex = 0;
      let at = -1;
      el.addEventListener("keydown", (e) => {
        const c = CHARTS[el.dataset.chart]; if (!c || !["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key)) return;
        e.preventDefault();
        at = e.key === "Home" ? 0 : e.key === "End" ? c.n - 1 : Math.max(0, Math.min(c.n - 1, (at < 0 ? c.n - 1 : at) + (e.key === "ArrowRight" ? 1 : -1)));
        const box = $("svg", el).getBoundingClientRect();
        show(box.left + (c.x(at) / c.W) * box.width);
      });
      el.addEventListener("blur", hide);
    }
  }

  // ---------- statistics ----------
  // One range for the whole page; the subject is the server (network card,
  // all tunnels, CPU, memory, load and every tunnel side by side) or one
  // tunnel (its traffic, ping and loss, and its totals since it was made).
  function viewStats() {
    const st = S.st, ts = visible();
    if (st.subject !== "server" && !tunnel(st.subject)) st.subject = "server";
    const filters = html`<div class="filters">${rangeSeg("st-range", st.range)}
      <select class="input" data-act="st-subject" aria-label="موضوع">
        <option value="server" ${st.subject === "server" ? "selected" : ""}>کل سرور</option>
        ${ts.map((t) => html`<option value="${t.name}" ${st.subject === t.name ? "selected" : ""}>تانل ${tname(t)} · ${kindTitle(t)}${t.peer ? " به " + pname(t.peer) : ""}</option>`)}
      </select></div>`;
    return html`${filters}${st.subject === "server" ? statsServer() : statsTunnel(tunnel(st.subject))}`;
  }

  const sinceText = (unix) => unix ? `از ${F.dmy.format(new Date(unix * 1000))}` : "";

  function statsServer() {
    const st = S.st, m = metrics(st.range, []), se = (m && m.series) || {}, ts = visible();
    const pct = (key, a, b, names) => tsChart("st-" + key, m, se[key] ? [{ name: names[0], cls: "rx", vals: se[key][a], peaks: se[key]["p" + a] }].concat(b ? [{ name: names[1], cls: "tx", vals: se[key][b], peaks: se[key]["p" + b] }] : []) : [], key === "load" ? "" : "%", { size: "short" });
    const loading = m === undefined ? "loading" : "";
    const rows = ts.map((t) => {
      const s = se["t:" + t.name], p = se["p:" + t.name];
      return html`<tr class="hit" data-act="st-pick" data-name="${t.name}"><td><span class="state"><i class="dot ${tunHealth(t)}"></i><b>${tname(t)}</b></span> <span class="faint small">${kindTitle(t)}</span></td>
        <td class="num">${s ? fmtRate(s.avg[0]) : "—"}</td><td class="num hide-sm">${s ? fmtRate(s.peak[0]) : "—"}</td><td class="num hide-sm">${s ? fmtRate(s.avg[1]) : "—"}</td>
        <td class="num">${s ? fmtBytes((s.sum[0] + s.sum[1]) / 8) : "—"}</td><td class="num hide-sm">${p && p.avg[0] ? ms(p.avg[0]) : "—"}</td><td class="num hide-sm">${p ? `${(p.avg[1] || 0).toFixed(1)}%` : "—"}</td>
        <td class="num hide-sm">${fmtBytes(t.total.rx + t.total.tx)}</td></tr>`;
    });
    return html`<div class="stats ${loading}">
      <div class="panel wide"><header><h2>کارت شبکه</h2><span class="aside">همه‌ی ترافیک سرور، همان چیزی که دیتاسنتر می‌شمارد${se.nic ? " · " + sinceText(se.nic.since) : ""}</span></header><div class="in">${rateFigs(se.nic)}${rateChart("st-nic", m, se.nic, "tall")}</div></div>
      <div class="panel wide"><header><h2>همه‌ی تانل‌ها</h2><span class="aside">ترافیک کاربران از داخل تانل‌ها</span></header><div class="in">${rateFigs(se.all)}${rateChart("st-all", m, se.all)}</div></div>
      <div class="panel"><header><h2>پردازنده</h2><span class="aside">${se.cpu ? `میانگین ${fmtU(se.cpu.avg[0], "%")} · پرکارترین هسته تا ${fmtU(se.cpu.peak[1], "%")}` : ""}</span></header><div class="in">${pct("cpu", "a", "b", ["همه‌ی هسته‌ها", "پرکارترین هسته"])}
        <p class="help" style="margin:0">بسته‌های یک تانل معمولاً روی یک هسته پردازش می‌شوند. اگر «پرکارترین هسته» به ۱۰۰٪ می‌رسد ولی میانگین پایین است، گلوگاه سرعت همان یک هسته است: حالت سرعت «حداکثر» یا مسیرهای موازی کمک می‌کند.</p></div></div>
      <div class="panel"><header><h2>حافظه</h2><span class="aside">${se.mem ? `میانگین ${fmtU(se.mem.avg[0], "%")}` : ""}</span></header><div class="in">${pct("mem", "a", "b", ["حافظه", "swap"])}</div></div>
      <div class="panel"><header><h2>Load</h2><span class="aside">میانگین یک‌دقیقه‌ای${S.ov.node.cpus ? ` · ${S.ov.node.cpus} هسته` : ""}</span></header><div class="in">${pct("load", "a", null, ["load"])}</div></div>
      <div class="panel"><header><h2>حجم روزانه</h2><span class="aside">همه‌ی تانل‌ها، ۳۰ روز گذشته</span></header><div class="in">${dailyBars(ts.map((t) => t.name))}</div></div>
      <div class="panel wide"><header><h2>تانل‌ها در این بازه</h2><span class="aside">روی هر تانل بزنید تا نمودارهای خودش باز شود</span></header>
        ${ts.length ? html`<div class="tbl-wrap"><table class="t"><thead><tr><th>تانل</th><th class="num">میانگین ↓</th><th class="num hide-sm">اوج ↓</th><th class="num hide-sm">میانگین ↑</th><th class="num">حجم</th><th class="num hide-sm">پینگ</th><th class="num hide-sm">افت</th><th class="num hide-sm">از ابتدا</th></tr></thead><tbody>${rows}</tbody></table></div>` : html`<div class="in muted">تانلی نیست.</div>`}</div>
    </div>`;
  }

  function statsTunnel(t) {
    const st = S.st, keys = ["t:" + t.name, "p:" + t.name], m = metrics(st.range, keys), se = (m && m.series) || {};
    const s = se[keys[0]], p = se[keys[1]], tr = S.traffic[t.name];
    const facts = html`<div class="figs" style="grid-template-columns:repeat(auto-fit,minmax(140px,1fr))">
      <div><div class="k">امروز</div><div class="v ltr">${fmtBytes(t.today.rx + t.today.tx)}</div></div>
      <div><div class="k">این ماه</div><div class="v ltr">${fmtBytes(t.month.rx + t.month.tx)}</div></div>
      <div><div class="k">از ساخت تانل${tr && tr.since ? " (" + F.dm.format(new Date(tr.since * 1000)) + ")" : ""}</div><div class="v ltr">${fmtBytes(t.total.rx + t.total.tx)}</div></div>
      <div><div class="k">تست‌های سرعت (جدا حساب می‌شود)</div><div class="v ltr">${fmtBytes((t.tests.rx || 0) + (t.tests.tx || 0))}</div></div></div>`;
    return html`<div class="stats ${m === undefined ? "loading" : ""}">
      <div class="panel wide"><header><h2>ترافیک ${tname(t)}</h2><span class="aside">${kindTitle(t)}${t.peer ? " به " + pname(t.peer) : ""} · <button class="btn sm ghost" data-act="drawer" data-type="tunnel" data-name="${t.name}">جزئیات تانل</button></span></header>
        <div class="in">${facts}${rateFigs(s)}${rateChart("st-t", m, s, "tall")}</div></div>
      <div class="panel"><header><h2>پینگ</h2><span class="aside">${p && p.avg[0] ? `میانگین ${ms(p.avg[0])} · بیشترین ${ms(p.peak[0])}` : ""}</span></header><div class="in">${tsChart("st-ping", m, p ? [{ name: "پینگ", cls: "rx", vals: p.a, peaks: p.pa }] : [], "ms", { size: "short" })}</div></div>
      <div class="panel"><header><h2>افت بسته</h2><span class="aside">${p ? `میانگین ${(p.avg[1] || 0).toFixed(1)}%` : ""}</span></header><div class="in">${tsChart("st-loss", m, p ? [{ name: "افت", cls: "tx", vals: p.b, peaks: p.pb }] : [], "%", { size: "short" })}</div></div>
      <div class="panel wide"><header><h2>حجم روزانه</h2><span class="aside">۳۰ روز گذشته</span></header><div class="in">${dailyBars([t.name])}</div></div>
    </div>`;
  }

  // Bytes per day for some tunnels, download on upload, from the traffic
  // accounting (kept 62 days, speed tests left out).
  function dailyBars(names) {
    const days = [];
    for (const n of names) {
      const s = S.traffic[n];
      if (!s || !s.days) continue;
      s.days.forEach((d, i) => { if (!days[i]) days[i] = { date: d.date, rx: 0, tx: 0 }; days[i].rx += d.rx; days[i].tx += d.tx; });
    }
    if (!days.length) return html`<div class="muted small">آمار روزانه بعد از چند دقیقه کار سرویس جمع می‌شود.</div>`;
    const max = Math.max(1, ...days.map((d) => d.rx + d.tx));
    const label = (d) => F.dm.format(new Date(d.date + "T12:00:00"));
    return html`<div class="days" role="img" aria-label="حجم روزانه">${days.map((d) => html`<div class="d" title="${label(d)}: دانلود ${fmtBytes(d.rx)} · آپلود ${fmtBytes(d.tx)}">
        <i class="rx" style="height:${((d.rx / max) * 100).toFixed(2)}%"></i><i class="tx" style="height:${((d.tx / max) * 100).toFixed(2)}%"></i></div>`)}</div>
      <div class="days-x"><span>${label(days[0])}</span><span>${label(days[days.length - 1])}</span></div>
      <div class="legend"><span><i class="rx"></i>دانلود</span><span><i class="tx"></i>آپلود</span><span class="faint">بیشترین روز ${fmtBytes(max)}</span></div>`;
  }

  // ---------- tunnels ----------
  // A step of automatic encryption, and a tunnel's encryption as a tag.
  const rungTitle = (r) => !r ? "بدون رمزنگاری" : r === "obfs" ? "NeoObfs" : cipherTitle(r);
  function encryptTag(t) {
    const now = t.encrypt ? cipherTitle(t.encrypt) : "";
    if (t.auto_encrypt) return html`<span class="tag lock" title="رمزنگاری خودکار؛ اگر ترافیک رد نشود، مرحله‌ی بعد امتحان می‌شود">${I.lock(12)} خودکار${now ? " · " + now : " · بدون رمز"}</span>`;
    return now ? html`<span class="tag lock">${I.lock(12)} ${now}</span>` : html`<span class="tag">بدون رمز</span>`;
  }

  function viewTunnels() {
    const ts = visible();
    if (!ts.length) return html`<div class="panel"><div class="empty">${I.tunnel(32)}<b>هیچ تانلی وجود ندارد</b>با چند کلیک یک تانل 6to4 بسازید؛ اگر مسیر پروتکل‌ها را می‌بندد، روی پورت UDP یا با NeoObfs اجرایش کنید.<br><button class="btn primary" data-act="wizard">${I.plus(16)} تانل جدید</button></div></div>`;
    const open = ts.find((t) => t.name === S.open);
    const pm = metrics(S.tpr, ts.map((t) => "p:" + t.name)), ps = (pm && pm.series) || {};
    const net = ts.filter((t) => !t.stream), conn = ts.filter((t) => t.stream);
    const cards = (list) => html`<div class="tcards">${list.map((t) => tunnelCard(t, ps["p:" + t.name]))}</div>`;
    return html`${conn.length && net.length ? html`<h3 class="tsec">تانل‌های شبکه</h3>` : ""}${net.length ? cards(net) : ""}
      ${conn.length ? html`<h3 class="tsec">تانل‌های اتصال (مثل FRP)</h3>${cards(conn)}` : ""}
      ${open ? html`<section class="panel tdetail" data-key="td-${open.name}"><header><h2>${tname(open)}</h2>${open.managed ? "" : renameBtn("tunnel", open.name)}<span class="aside">${kindTitle(open)} به ${pname(open.peer) || open.far_ip}${open.label ? html` · شناسه <span class="ltr">${open.name}</span>` : ""}</span>
        <button class="btn sm ghost icon" data-act="toggle-tunnel" data-name="${open.name}" style="margin-inline-start:auto" aria-label="بستن">${I.x(16)}</button></header>${tunnelDetail(open)}</section>` : ""}`;
  }

  // One tunnel at a glance: state, where it goes, live traffic, health, and
  // the actions used most; the card opens its details below the grid.
  function tunnelCard(t, pings) {
    const r = rate(t.name), open = S.open === t.name, [st, label] = status(t), h = t.health;
    const off = t.limits && t.limits.disabled, busy = S.restarting[t.name];
    const ping = off ? "" : h && h.probes >= 3 ? (h.passes ? ms(h.ping_ms) + (h.loss >= 1 ? ` · افت ${h.loss.toFixed(0)}٪` : "") : "بدون پاسخ") : "";
    return html`<article class="tcard ${open ? "open" : ""} ${off ? "off" : ""}" data-key="tc-${t.name}">
      <button class="tc-main" data-act="toggle-tunnel" data-name="${t.name}" aria-expanded="${open}">
        <div class="tc-top"><span class="state"><i class="dot ${busy ? "busy" : st}"></i>${busy ? "در حال ری‌استارت…" : label}</span>${ping ? html`<span class="faint small ltr">${ping}</span>` : ""}</div>
        <div class="tc-name"><b>${tname(t)}</b><span>${kindTitle(t)}${t.stream ? ` · TCP ${t.stream.ports.join("، ")}` : t.base_port ? ` · ${t.kind === "tcp" ? "TCP" : "UDP"} ${t.base_port}` : ""}</span></div>
        <div class="tc-dest">${I.server(14)} ${pname(t.peer) || "دستگاه دیگر"} <span class="ltr faint">${t.far_ip}</span></div>
        <div class="tc-rates"><div><span class="faint small">دانلود</span><b class="ltr" data-live="rx:${t.name}">${unitSmall(fmtRate(r.rx))}</b></div><div><span class="faint small">آپلود</span><b class="ltr" data-live="tx:${t.name}">${unitSmall(fmtRate(r.tx))}</b></div><div><span class="faint small">امروز</span><b class="ltr" data-live="today:${t.name}">${unitSmall(fmtBytes(t.today.rx + t.today.tx))}</b></div></div>
        ${off ? "" : pingTrend(pings && Object.assign({ name: t.name }, pings))}
        <div class="tc-tags">${t.stream ? html`<span class="tag">${t.settings && t.settings.reverse ? "معکوس" : "مستقیم"} · ${t.stream.sessions}/${t.stream.want} جلسه</span>` : ""}${profileTag(t)}${encryptTag(t)}${t.obfs ? html`<span class="tag lock">${I.mask(12)} مبهم</span>` : ""}${t.limits && t.limits.rate_mbit ? html`<span class="tag ltr">≤ ${t.limits.rate_mbit} Mbps</span>` : ""}${t.limits && t.limits.queue_mbit ? html`<span class="tag" title="صف هوشمند (CAKE) روی ${t.limits.queue_mbit} Mbps">صف هوشمند</span>` : ""}</div>
      </button>
      <footer class="tc-foot">
        <button class="btn sm" data-act="test" data-name="${t.name}">${I.gauge(14)} تست</button>
        ${t.managed ? "" : html`<button class="btn sm" data-act="tunnel-restart" data-name="${t.name}" ${busy || off ? "disabled" : ""}>${I.sync(14)} ری‌استارت</button>
        <button class="btn sm ${off ? "primary" : "ghost"}" data-act="tunnel-power" data-name="${t.name}">${I.power(14)} ${off ? "روشن" : "خاموش"}</button>`}
        <span class="grow"></span>
        <button class="btn sm ghost icon" data-act="toggle-tunnel" data-name="${t.name}" aria-label="جزئیات">${I.chev(16)}</button>
      </footer>
    </article>`;
  }

  // A tunnel's history (package events), newest first; loaded when the tab
  // opens and again every 15 seconds while it stays open.
  function eventsPane(t) {
    const e = S.ev[t.name];
    if (!e || Date.now() - e.at > 15e3) loadEvents(t.name);
    if (!e) return html`<div class="dpane muted" data-key="ev-${t.name}">در حال خواندن…</div>`;
    if (!e.list.length) return html`<div class="dpane muted" data-key="ev-${t.name}">هنوز رویدادی ثبت نشده. از این نسخه به بعد، تغییر وضعیت، ری‌استارت‌ها، رفتن به پشتیبان و تغییرات تنظیمات اینجا نوشته می‌شوند.</div>`;
    return html`<div class="dpane" data-key="ev-${t.name}"><ol class="evlog">${e.list.map((x) => { const [cls, text] = eventText(t, x); return html`<li class="${cls}"><i class="dot ${cls}"></i><div><span>${text}</span><small title="${F.dmhm.format(new Date(x.at * 1000))}">${F.dmhm.format(new Date(x.at * 1000))} · ${ago(x.at)}${x.auto ? " · خودکار" : ""}</small></div></li>`; })}</ol></div>`;
  }

  // The nightly speed tests, newest first, each against the best of them.
  function nightlyPane(t) {
    const n = S.night[t.name];
    if (!n) { loadNightly(t.name); return html`<div class="dpane muted" data-key="nt-${t.name}">در حال خواندن…</div>`; }
    const off = S.ov.defaults && S.ov.defaults.no_nightly;
    if (!n.length) return html`<div class="dpane muted" data-key="nt-${t.name}">${off ? "تست شبانه در تنظیمات ← پیش‌فرض تانل‌ها خاموش است." : "هنوز تستی نشده؛ هر شب ساعت ۴:۳۰ به وقت تهران یک تست سبک گرفته می‌شود."}</div>`;
    const list = n.slice().reverse(), top = Math.max(1, ...list.map((e) => e.down || 0));
    return html`<div class="dpane stack" data-key="nt-${t.name}"><div class="tbl-wrap pdays-t"><table class="t"><thead><tr><th>شب</th><th>دانلود</th><th>آپلود</th><th>پینگ</th><th>افت</th></tr></thead><tbody>
      ${list.map((e) => e.error ? html`<tr><td>${dayName(new Date(e.at * 1000))}</td><td colspan="4" class="bad small">${e.error}</td></tr>`
        : html`<tr><td>${dayName(new Date(e.at * 1000))}</td><td class="ltr"><b>${fmtRate(e.down)}</b><span class="daybar"><i style="transform:scaleX(${(e.down / top).toFixed(3)})"></i></span></td><td class="ltr">${fmtRate(e.up)}</td><td class="ltr">${ms(e.ping_ms)}</td><td class="ltr">${(e.loss || 0).toFixed(1)}٪</td></tr>`)}
      </tbody></table></div><p class="help" style="margin:0">۴ ثانیه هر جهت با ۴ اتصال، ساعت ۴:۳۰ صبح به وقت تهران؛ عددها کمی پایین‌تر از تست کامل‌اند ولی برای مقایسه‌ی شب‌ها با هم ساخته شده‌اند.</p></div>`;
  }
  async function loadNightly(name) {
    if (name in S.night) return; // loaded or loading
    S.night[name] = null;
    try { S.night[name] = await api("GET", `tunnels/${encodeURIComponent(name)}/nightly`); } catch { S.night[name] = []; }
    if (S.open === name && !S.edit) draw();
  }

  async function loadEvents(name) {
    if (S.evBusy[name]) return;
    S.evBusy[name] = true;
    try { S.ev[name] = { at: Date.now(), list: await api("GET", `tunnels/${encodeURIComponent(name)}/events?limit=200`) }; } catch { S.ev[name] = { at: Date.now(), list: (S.ev[name] && S.ev[name].list) || [] }; }
    S.evBusy[name] = false;
    if (S.open === name && !S.edit) draw();
  }

  // One event in the panel's words, with its colour.
  function eventText(t, x) {
    const loss = x.loss ? ` (افت ${x.loss.toFixed(0)}٪)` : "";
    const far = x.detail ? html`؛ سرور مقابل: <span class="ltr">${x.detail}</span>` : "";
    switch (x.kind) {
      case "health":
        if (x.to === "down") return ["bad", "قطع شد: ترافیک رد نمی‌شود"];
        if (x.to === "bad") return ["bad", `افت شدید${loss}`];
        return ["ok", x.from === "down" ? "دوباره وصل شد" : "دوباره سالم شد"];
      case "restart": return ["warn", html`${x.auto ? `ری‌استارت خودکار روی هر دو سرور${loss}` : "ری‌استارت دستی"}${far}`];
      case "cipher":
        if (x.detail) return ["bad", html`رمزنگاری خودکار عوض نشد و روی ${rungTitle(x.from)} ماند: <span class="ltr">${x.detail}</span>`];
        return ["warn", html`ترافیک رد نمی‌شد${loss}؛ رمزنگاری خودکار از ${rungTitle(x.from)} به ${rungTitle(x.to)} رفت و تانل ری‌استارت شد`];
      case "failover": {
        const [ports, why] = (x.detail || "").split("|");
        return x.to === t.name ? ["warn", html`پورت <span class="ltr">${ports}</span> از ${tlabel(x.from)} به این تانل آمد`] : ["warn", html`پورت <span class="ltr">${ports}</span> به ${tlabel(x.to)} رفت${why ? html` <span class="faint small ltr">(${why})</span>` : ""}`];
      }
      case "up": return ["ok", "تانل بالا آمد"];
      case "error": return ["bad", html`خطا: <span class="ltr">${x.detail}</span>`];
      case "created": return ["", `ساخته شد${x.detail ? ` (${x.detail})` : ""}`];
      case "changed": {
        const [what, ports] = (x.detail || "").split(/ (.+)/);
        if (what === "plan") return ["", html`فروش پورت <span class="ltr">${ports}</span> تغییر کرد`];
        if (what === "renew") return ["ok", html`پورت <span class="ltr">${ports}</span> تمدید شد`];
        return ["", { settings: "تنظیمات تغییر کرد", limits: "سقف سرعت، حجم ماهانه یا صف هوشمند تغییر کرد", label: "نام نمایشی عوض شد", aes: "سرور مقابل ارتقا یافت؛ رمز NeoObfs خودکار به AES-GCM (سریع‌تر) رفت" }[x.detail] || "تغییر کرد"];
      }
      case "on": return ["ok", "روشن شد"];
      case "off": return ["bad", "خاموش شد"];
      case "quota": return x.to === "blocked" ? ["bad", "حجم ماهانه تمام شد؛ تا ماه بعد خاموش است"] : ["ok", "حجم آزاد شد؛ دوباره روشن شد"];
      case "port": return x.to === "open" ? ["ok", html`پورت فروخته‌ی <span class="ltr">${x.detail}</span> دوباره باز شد`] : ["bad", html`پورت <span class="ltr">${x.detail}</span> بسته شد: ${CLOSED[x.to] || x.to}`];
    }
    return ["", x.kind];
  }

  // A tunnel's ping over the page's range, for its card.
  const PRANGES = [["1h", "۱ ساعت"], ["6h", "۶ ساعت"], ["24h", "۲۴ ساعت"]];
  function pingTrend(s) {
    if (!s || !s.a) return "";
    const vals = s.a.filter((v) => v != null && v > 0);
    if (vals.length < 2) return html`<div class="tc-ping faint small">هنوز پینگی در این بازه ثبت نشده</div>`;
    const peaks = (s.pa || []).filter((v) => v != null && v > 0);
    const avg = vals.reduce((a, b) => a + b, 0) / vals.length, lo = Math.min(...vals), hi = Math.max(...(peaks.length ? peaks : vals));
    const range = (PRANGES.find(([k]) => k === S.tpr) || PRANGES[0])[1];
    return html`<div class="tc-ping" data-act="ping-open" data-name="${s.name || ""}" title="جزئیات پینگ">${sparkline(s.a, { w: 300, h: 38, fit: true, points: 60 })}<span>پینگ ${range}: میانگین <b class="ltr">${ms(avg)}</b> · کمترین <b class="ltr">${ms(lo)}</b> · بیشترین <b class="ltr">${ms(hi)}</b></span></div>`;
  }

  // ---------- ping of one tunnel, in detail ----------
  // A window with the tunnel's ping over a chosen range: the chart answers
  // the pointer with each moment's average and peak, and the summary says
  // whether the ping held steady (most of the time between which two
  // values) or jumped, how often, and when the worst jump was.
  const PM_RANGES = [["1h", "۱ ساعت"], ["6h", "۶ ساعت"], ["24h", "۲۴ ساعت"], ["7d", "۷ روز"], ["30d", "۳۰ روز"]];
  const quantile = (sorted, q) => sorted.length ? sorted[Math.min(sorted.length - 1, Math.max(0, Math.round(q * (sorted.length - 1))))] : null;
  function pingStats(m, s) {
    if (!m || !s || !s.a) return null;
    const pts = s.a.map((v, i) => [i, v, (s.pa || [])[i]]).filter(([, v]) => v != null && v > 0);
    if (pts.length < 2) return null;
    const vals = pts.map(([, v]) => v), sorted = vals.slice().sort((a, b) => a - b);
    const avg = vals.reduce((a, b) => a + b, 0) / vals.length;
    const sd = Math.sqrt(vals.reduce((a, v) => a + (v - avg) ** 2, 0) / vals.length);
    const med = quantile(sorted, 0.5), p5 = quantile(sorted, 0.05), p95 = quantile(sorted, 0.95);
    const limit = med + Math.max(10, med * 0.3);
    let spikes = 0, worst = null, inSpike = false;
    for (const [i, v, pk] of pts) {
      const top = pk != null && pk > v ? pk : v;
      if (top > limit) { if (!inSpike) spikes++; inSpike = true; if (!worst || top > worst.v) worst = { v: top, at: m.start + i * m.step }; } else inSpike = false;
    }
    const lo = sorted[0], hi = Math.max(...pts.map(([, v, pk]) => (pk != null && pk > v ? pk : v)));
    return { avg, sd, lo, hi, p5, p95, spikes, worst };
  }
  function pingVerdict(st) {
    const band = html`بیشتر زمان بین <b class="ltr">${ms(st.p5)}</b> و <b class="ltr">${ms(st.p95)}</b>`;
    if (!st.spikes && st.p95 - st.p5 <= 6) return ["ok", html`پایدار: ${band}، بدون جهش.`];
    if (st.spikes) return ["warn", html`${band}، ولی <b>${st.spikes}</b> بار جهش داشته؛ بدترینش <b class="ltr">${ms(st.worst.v)}</b> در ${F.dmhm.format(new Date(st.worst.at * 1000))}.`];
    return ["warn", html`نوسان‌دار: ${band}.`];
  }
  function pingView() {
    const p = S.pm, t = tunnel(p.name);
    if (!t) return html`${modalHead("پینگ")}<div class="body muted">این تانل دیگر وجود ندارد.</div>`;
    const k = "p:" + t.name, m = metrics(p.range, [k]), s = m && m.series && m.series[k], st = pingStats(m, s);
    const fig = (label, val) => html`<div><div class="k">${label}</div><div class="v ltr">${val}</div></div>`;
    const [cls, verdict] = st ? pingVerdict(st) : ["", ""];
    return html`${modalHead("پینگ " + tname(t))}<div class="body stack">
      <div class="seg" role="radiogroup" aria-label="بازه">${PM_RANGES.map(([r, l]) => html`<button type="button" role="radio" aria-checked="${p.range === r}" class="${p.range === r ? "on" : ""}" data-act="pm-range" data-range="${r}">${l}</button>`)}</div>
      ${st ? html`<div class="note ${cls === "warn" ? "warn" : ""}">${verdict}</div>
      <div class="figs" style="grid-template-columns:repeat(auto-fit,minmax(150px,1fr))">${fig("میانگین", ms(st.avg))}${fig("کمترین", ms(st.lo))}${fig("بیشترین", ms(st.hi))}${fig("انحراف معیار", ms(st.sd))}</div>` : ""}
      ${tsChart("pm-ping", m, s ? [{ name: "پینگ", cls: "rx", vals: s.a, peaks: s.pa }] : [], "ms", { size: "tall", fit: true })}
      <div class="lbl" style="font-weight:600;font-size:13px">افت بسته</div>
      ${tsChart("pm-loss", m, s ? [{ name: "افت", cls: "tx", vals: s.b, peaks: s.pb }] : [], "%", { size: "short" })}
      <p class="help" style="margin:0">هر نقطه میانگین پینگ همان بازه‌ی زمانی است و محدوده‌ی روشن بالای خط تا اوج همان بازه می‌رسد. نشانگر را روی نمودار ببرید تا زمان و مقدار هر لحظه را ببینید.</p>
    </div>`;
  }

  // ---------- traffic of one port ----------
  const portKey = (f) => "f:" + f.proto + "/" + f.ports;
  // The CPU window (from the load card): whether the CPU limits the speed,
  // the busiest programs, and the processor, the kernel's packet work and
  // its drops over a range, as short as the last 10 minutes to watch a test.
  const CPU_RANGES = [["10m", "۱۰ دقیقه"], ["30m", "۳۰ دقیقه"], ["1h", "۱ ساعت"], ["6h", "۶ ساعت"], ["24h", "۲۴ ساعت"], ["7d", "۷ روز"]];
  function cpuView() {
    const c = S.cw, n = S.ov.node, p = n.pressure, vd = cpuVerdict(p, n.cpus);
    const m = metrics(c.range, ["cpu", "softirq", "softnet"]), se = (m && m.series) || {};
    const two = (id, key, names, unit) => tsChart(id, m, se[key] ? [{ name: names[0], cls: "rx", vals: se[key].a, peaks: se[key].pa }, { name: names[1], cls: "tx", vals: se[key].b, peaks: se[key].pb }] : [], unit, { size: "short" });
    const fig = (label, val, title) => html`<div title="${title || ""}"><div class="k">${label}</div><div class="v ltr">${val}</div></div>`;
    const u = p && p.usage;
    return html`${modalHead("CPU کجا مصرف می‌شود")}<div class="body stack">
      ${vd ? html`<div class="note ${vd[0] === "ok" ? "" : vd[0]}">${vd[1]}</div>` : html`<p class="muted" style="margin:0">اولین اندازه‌گیری حدود ۱۰ ثانیه بعد از روشن شدن سرویس آماده می‌شود.</p>`}
      ${u ? html`<div class="figs">${fig("همه‌ی هسته‌ها", u.total.toFixed(0) + "%")}${fig("پرکارترین هسته", u.hottest.toFixed(0) + "%")}
        ${fig("کار بسته‌ها، پرکارترین هسته", u.softirq_hot.toFixed(0) + "%", "سهم softirq: پردازش بسته‌ها در کرنل، جدا از برنامه‌ها")}${fig("بسته‌ی دورریخته", p.drops.toFixed(p.drops < 10 ? 1 : 0) + "/s", "بسته‌هایی که کرنل برای کمبود CPU دور ریخت")}
        ${p.conntrack_max ? fig("اتصال‌های ردیابی‌شده", `${p.conntrack.toLocaleString("en")} / ${p.conntrack_max.toLocaleString("en")}`, "جدول ردیابی اتصال‌های کرنل (برای NAT و فوروارد)؛ وقتی پر شود، اتصال‌های تازه دور ریخته می‌شوند") : ""}</div>` : ""}
      ${p && p.procs && p.procs.length ? html`<div><span class="lbl">پرمصرف‌ترین برنامه‌ها، الان</span><div class="procs">${p.procs.map((x) => html`<div><span class="ltr">${x.name}</span><i style="width:${Math.min(100, x.cpu).toFixed(0)}%"></i><b>${x.cpu.toFixed(x.cpu < 10 ? 1 : 0)}%</b></div>`)}</div></div>` : ""}
      <div class="seg" role="radiogroup" aria-label="بازه">${CPU_RANGES.map(([k, l]) => html`<button type="button" role="radio" aria-checked="${c.range === k}" class="${c.range === k ? "on" : ""}" data-act="cw-range" data-range="${k}">${l}</button>`)}</div>
      <div><span class="lbl">پردازنده</span>${two("cw-cpu", "cpu", ["همه‌ی هسته‌ها", "پرکارترین هسته"], "%")}</div>
      <div><span class="lbl">کار بسته‌ها در کرنل (softirq)</span>${two("cw-soft", "softirq", ["همه‌ی هسته‌ها", "پرکارترین هسته"], "%")}</div>
      <div><span class="lbl">کمبود CPU برای بسته‌ها، در ثانیه</span>${two("cw-net", "softnet", ["بسته‌ی دورریخته", "تمام شدن بودجه"], "")}</div>
      <p class="help" style="margin:0">ترافیک تانل‌ها در «کار بسته‌ها»ی کرنل پردازش می‌شود، جدا از برنامه‌ها. وقتی یک هسته از این کار پر شود، اول بودجه‌ی کرنل تمام می‌شود و بعد بسته‌ها دور ریخته می‌شوند. اگر سرعت پایین است و این‌ها صفر است، گلوگاه CPU نیست و باید مسیر یا دیتاسنتر را بررسی کرد. برای دیدن اثر یک تست سرعت، بازه‌ی ۱۰ دقیقه را باز نگه دارید؛ هر نقطه ۱۰ ثانیه است.</p>
    </div>`;
  }

  // A port's own window: its traffic (now, over a range, day by day) and,
  // for those who sell ports, what its buyer gets.
  const PORT_TABS = [["traffic", "ترافیک"], ["clients", "کاربران"], ["sale", "فروش و محدودیت"]];
  const portId = (f) => f.proto + "/" + f.ports; // config.Forward.PortKey
  const fwdOf = (key) => (S.ov.forwards || []).find((x) => portKey(x) === key);
  // "دوشنبه، ۱۱ آبان ۱۴۰۵": with the weekday in one format the parts come out of order.
  const WD = dtf({ weekday: "long" }), dayName = (d) => WD.format(d) + "، " + F.dmy.format(d);
  const CLOSED = { paused: "متوقف شده", expired: "مدتش تمام شده", quota: "حجمش تمام شده" };
  const pairSum = (p) => (p ? p.rx + p.tx : 0);

  // A sold port in the list: whom for, closed or how close to its end.
  function saleTags(f) {
    const pl = f.plan, q = pl.quota_gb * 2 ** 30, frac = q ? pairSum(f.period) / q : 0;
    const days = pl.expires ? Math.ceil((pl.expires - Date.now() / 1000) / 86400) : null;
    return html`<span class="tag lock">${I.tag(11)} ${pl.customer || "فروشی"}</span>${f.closed ? html`<span class="tag bad">بسته: ${CLOSED[f.closed] || f.closed}</span>` : ""}${q && f.closed !== "quota" ? html`<span class="tag ${frac > 0.85 ? "warn" : ""}">${(Math.min(1, frac) * 100).toFixed(0)}٪ حجم</span>` : ""}${days !== null && f.closed !== "expired" ? html`<span class="tag ${days <= 3 ? "warn" : ""}">${days} روز</span>` : ""}`;
  }

  function portView() {
    const p = S.fm, f = fwdOf(p.key);
    if (!f) return html`${modalHead("پورت")}<div class="body muted">این فوروارد دیگر وجود ندارد.</div>`;
    const tab = p.tab || "traffic";
    return html`${modalHead(html`پورت <span class="ltr sens">${f.ports}</span>${f.plan && f.plan.customer ? html` <span class="faint">· ${f.plan.customer}</span>` : ""}`)}<div class="body stack">
      <div class="seg" role="tablist" aria-label="بخش‌های پورت">${PORT_TABS.map(([id, l]) => html`<button type="button" role="tab" aria-selected="${tab === id}" class="${tab === id ? "on" : ""}" data-act="fm-tab" data-tab="${id}">${l}</button>`)}</div>
      ${tab === "sale" ? portSale(f, p) : tab === "clients" ? portClients(p) : portTraffic(f, p)}
    </div>`;
  }

  function portTraffic(f, p) {
    const m = metrics(p.range, [p.key]), s = m && m.series && m.series[p.key];
    const sum = s && s.sum ? { rx: s.sum[0] / 8, tx: s.sum[1] / 8 } : null;
    const r = prate(f), fig = (label, val, sub) => html`<div><div class="k">${label}</div><div class="v ltr">${val}</div>${sub ? html`<div class="k ltr">${sub}</div>` : ""}</div>`;
    const split = (x) => (x ? `↓ ${fmtBytes(x.rx)} · ↑ ${fmtBytes(x.tx)}` : "");
    return html`<div class="figs">${fig("الان", `↓ ${fmtRate(r.rx)}`, `↑ ${fmtRate(r.tx)}`)}${fig("امروز", fmtBytes(pairSum(f.today)), split(f.today))}${fig("این ماه", fmtBytes(pairSum(f.month)), split(f.month))}${fig("ماه قبل", fmtBytes(pairSum(f.last_month)), split(f.last_month))}${fig("از ابتدا", fmtBytes(f.rx + f.tx), `↓ ${fmtBytes(f.rx)} · ↑ ${fmtBytes(f.tx)}`)}${f.plan ? fig("این دوره‌ی فروش", fmtBytes(pairSum(f.period)), split(f.period)) : ""}</div>
      <div class="seg" role="radiogroup" aria-label="بازه">${PM_RANGES.map(([k, l]) => html`<button type="button" role="radio" aria-checked="${p.range === k}" class="${p.range === k ? "on" : ""}" data-act="fm-range" data-range="${k}">${l}</button>`)}</div>
      ${sum ? html`<div class="help" style="margin:0">در این بازه: دانلود <b class="ltr">${fmtBytes(sum.rx)}</b> · آپلود <b class="ltr">${fmtBytes(sum.tx)}</b> · مجموع <b class="ltr">${fmtBytes(sum.rx + sum.tx)}</b></div>` : ""}
      ${rateChart("fm-chart", m, s, "tall")}
      ${portDays(f, p)}
      <p class="help" style="margin:0">دانلود یعنی آنچه کاربران از این پورت گرفته‌اند و آپلود آنچه فرستاده‌اند؛ بایت‌های خود کاربر، پیش از سربار تانل، در هر حالت فوروارد. اتصال‌هایی که خود این سرور از همین شماره‌پورت باز می‌کند حساب نمی‌شوند.${f.counted_since ? ` شمارش از ${F.dmy.format(new Date(f.counted_since * 1000))}.` : ""}</p>`;
  }

  // Who uses the port: addresses with connections open now, busiest first,
  // and those that connected in the last day.
  function portClients(p) {
    const r = p.clients, now = r ? r.clients.filter((c) => c.conns).length : 0;
    const head = html`<div class="row pdays-h"><b>کاربران</b>${r ? html`<span class="faint small">${r.conns} اتصال باز · ${now} IP الان · ${r.day} IP در ۲۴ ساعت گذشته</span>` : ""}<span class="grow"></span><button type="button" class="btn sm" data-act="port-clients" ${p.clientsBusy ? "disabled" : ""}>${I.sync(14)} تازه کردن</button></div>`;
    if (!r) return html`<div class="pdays">${head}<div class="muted small">${p.clientsErr || "در حال خواندن…"}</div></div>`;
    if (!r.clients.length) return html`<div class="pdays">${head}<div class="muted small">الان کسی به این پورت وصل نیست و در ۲۴ ساعت گذشته هم کسی وصل نشده.</div></div>`;
    const rows = r.clients.map((c) => html`<tr class="${c.conns ? "" : "faint"}"><td class="ltr"><b class="sens">${c.ip}</b>${c.ir ? "" : html` <span class="tag warn" title="این IP در فهرست ایران نیست">خارج</span>`}</td><td>${c.conns || "-"}</td><td class="ltr">${c.conns && r.bytes ? fmtBytes(c.rx) : "-"}</td><td class="ltr">${c.conns && r.bytes ? fmtBytes(c.tx) : "-"}</td><td>${c.since ? fmtUptime(Date.now() / 1000 - c.since) : c.conns ? "-" : "وصل نیست"}</td><td>${c.last ? ago(c.last) : "-"}</td></tr>`);
    return html`<div class="pdays">${head}<div class="tbl-wrap pdays-t pclients"><table class="t"><thead><tr><th>IP</th><th>اتصال الان</th><th>دانلود</th><th>آپلود</th><th>وصل از</th><th>آخرین اتصال تازه</th></tr></thead><tbody>${rows}</tbody></table></div></div>
      <p class="help" style="margin:0">دانلود و آپلود هر IP مال اتصال‌های باز همین الانش است. فهرست ۲۴ ساعت را خود کرنل نگه می‌دارد و با هیچ تغییر یا ری‌استارتی پاک نمی‌شود.${r.bytes ? "" : " این سرور حجم هر اتصال را نمی‌شمارد."}${r.bytes && r.times ? " حجم و زمان اتصال‌هایی که پیش از اولین باز کردن این بخش باز شده بودند صفر یا نامعلوم نشان داده می‌شود." : ""}</p>`;
  }

  // Day by day, newest first, with a bar against the busiest day.
  function portDays(f, p) {
    const h = p.hist;
    const head = html`<div class="row pdays-h"><b>روز به روز</b>${h ? html`<span class="faint small">${h.days.length} روز (تا ۶۲ روز نگه داشته می‌شود)</span>` : ""}<span class="grow"></span>
      <a class="btn sm" href="api/forwards/history?format=csv&key=${encodeURIComponent(portId(f))}" download>${I.download(14)} CSV</a>
      <button type="button" class="btn sm ghost" data-act="port-reset" data-key="${p.key}">صفر کردن آمار</button></div>`;
    if (!h) return html`<div class="pdays">${head}<div class="muted small">${p.histErr || "در حال خواندن…"}</div></div>`;
    const days = h.days.slice().reverse(), top = Math.max(1, ...days.map((d) => d.rx + d.tx));
    const rows = days.map((d) => html`<tr><td>${dayName(new Date(d.date + "T12:00:00"))}</td><td class="ltr">${fmtBytes(d.rx)}</td><td class="ltr">${fmtBytes(d.tx)}</td><td class="ltr"><b>${fmtBytes(d.rx + d.tx)}</b><span class="daybar"><i style="transform:scaleX(${((d.rx + d.tx) / top).toFixed(3)})"></i></span></td></tr>`);
    return html`<div class="pdays">${head}<div class="tbl-wrap pdays-t"><table class="t"><thead><tr><th>روز</th><th>دانلود</th><th>آپلود</th><th>مجموع</th></tr></thead><tbody>${rows}</tbody></table></div></div>`;
  }

  // The sale: its state now, then what the buyer gets.
  const planDraft = (f) => {
    const p = f.plan || {}, left = p.expires ? Math.max(0, Math.ceil((p.expires - Date.now() / 1000) / 86400)) : "";
    return { customer: p.customer || "", rate: p.rate_mbit || "", quota: p.quota_gb || "", reset: p.reset_days || "", days: left, days0: left, conns: p.max_conns || "", ips: p.max_ips || "", renew: 30 };
  };
  function portSale(f, p) {
    const d = p.draft || (p.draft = planDraft(f)), pl = f.plan;
    const end = String(d.days) !== String(d.days0) ? (+d.days > 0 ? Date.now() + Math.round(+d.days) * 864e5 : 0) : pl && pl.expires ? pl.expires * 1000 : 0;
    const field = (label, key, attrs, help, ph = "نامحدود") => html`<div class="field"><label for="pl-${key}">${label}</label><input id="pl-${key}" class="input ltr" type="number" min="0" ${raw(attrs)} data-bind="fm.draft.${key}" value="${d[key]}" placeholder="${ph}">${help ? html`<span class="help">${help}</span>` : ""}</div>`;
    return html`${pl ? saleState(f, pl, d) : html`<p class="help" style="margin:0">این پورت را به‌عنوان یک محصول بفروشید: سقف سرعت، حجم، مدت و تعداد کاربر. همه روی همین سرور و در خود کرنل اعمال می‌شوند و پورت وقتی حجم یا مدتش تمام شد خودش بسته می‌شود.</p>`}
      <div class="grid2 psale-f">
        <div class="field"><label for="pl-customer">مشتری</label><input id="pl-customer" class="input" maxlength="100" data-bind="fm.draft.customer" value="${d.customer}" placeholder="نام یا یادداشت برای خودتان"></div>
        ${field("مدت (روز از امروز)", "days", 'max="3650" step="1"', end ? `تا ${F.dmy.format(new Date(end))}` : "بدون تاریخ پایان", "بدون پایان")}
        ${field("حجم (GB)", "quota", 'step="any"', "دانلود + آپلود. وقتی پر شد پورت بسته می‌شود.")}
        ${field("حجم از نو هر … روز", "reset", 'max="3650" step="1"', "خالی: حجم هیچ‌وقت خودکار صفر نمی‌شود، یک حجم برای کل مدت. ۳۰: هر ۳۰ روز از شروع، حجم دوباره کامل می‌شود.", "هرگز")}
        ${field("سقف سرعت (Mbps)", "rate", 'max="100000" step="1"', "هر جهت، همه‌ی کاربران این پورت با هم.")}
        ${field("اتصال هم‌زمان", "conns", 'max="1000000" step="1"', "اتصال‌های باز این پورت. اتصال تازه‌ی بیشتر از این رد می‌شود.")}
        ${field("IP هم‌زمان", "ips", 'max="65535" step="1"', "چند دستگاه با هم. هر IP تا ۵ دقیقه بعد از آخرین بسته‌اش شمرده می‌شود؛ IPv4 و IPv6 هر کدام جدا.")}
      </div>
      <div class="row"><button type="button" class="btn primary" data-act="plan-save">${pl ? "ذخیره‌ی تغییرات" : html`${I.tag(15)} شروع فروش`}</button><span class="grow"></span>
        ${pl ? (p.rmAsk ? html`<span class="small">همه‌ی محدودیت‌ها برداشته شود؟</span><button type="button" class="btn sm danger" data-act="plan-remove">بله، دیگر فروشی نیست</button><button type="button" class="btn sm ghost" data-act="plan-rm-ask">نه</button>` : html`<button type="button" class="btn sm ghost danger" data-act="plan-rm-ask">لغو فروش</button>`) : ""}</div>
      <p class="help" style="margin:0">سقف سرعت مثل محدودکننده‌ی روتر بسته‌های اضافه را دور می‌ریزد؛ یک ثانیه ترافیک ذخیره دارد تا TCP بیشتر سرعت را بگیرد. حجم هر ۱۰ ثانیه بررسی می‌شود. ترافیک پورت‌ها با هیچ تغییر، ری‌استارت یا «صفر کردن همه‌ی آمار» تانل‌ها پاک نمی‌شود؛ فقط «صفر کردن آمار» خود همین پورت، و آن هم به حجم خریدار دست نمی‌زند.</p>`;
  }

  // The path under a tunnel, hop by hop, both ways.
  function mtrView() {
    const m = S.mtrv, t = tunnel(m.name);
    const lossCls = (l) => (l >= 20 ? "bad" : l > 0 ? "warn" : "");
    const table = (title, r) => html`<div class="mtr"><div class="row pdays-h"><b>${title}</b><span class="faint small ltr">${r.target}</span>${r.reached ? "" : html`<span class="tag warn">به مقصد نرسید</span>`}</div>
      <div class="tbl-wrap pdays-t"><table class="t"><thead><tr><th>#</th><th>روتر</th><th>افت</th><th>آخر</th><th>میانگین</th><th>بهترین</th><th>بدترین</th></tr></thead><tbody>
      ${r.hops.map((h) => html`<tr><td>${h.ttl}</td><td class="ltr"><span class="sens">${h.host || "???"}</span>${h.host ? html` <span class="tag ${h.ir ? "" : "lock"}">${h.ir ? "ایران" : "خارج"}</span>` : ""}${(h.others || []).length ? html`<div class="faint small sens">${h.others.join("، ")}</div>` : ""}</td><td class="ltr ${lossCls(h.loss)}"><b>${h.loss.toFixed(0)}٪</b> <span class="faint small">${h.got}/${h.sent}</span></td><td class="ltr">${h.got ? ms(h.last) : "-"}</td><td class="ltr"><b>${h.got ? ms(h.avg) : "-"}</b></td><td class="ltr">${h.got ? ms(h.best) : "-"}</td><td class="ltr">${h.got ? ms(h.worst) : "-"}</td></tr>`)}</tbody></table></div></div>`;
    const body = m.busy ? html`<div class="muted">از هر دو سرور هم‌زمان ردیابی می‌شود؛ حدود ۱۲ ثانیه…</div>`
      : m.err ? html`<div class="bad">${m.err}</div>`
      : html`${table("از این سرور به سرور مقابل", m.rep.there)}${m.rep.back ? table("از سرور مقابل به این سرور", m.rep.back) : html`<div class="muted small">مسیر برگشت: ${m.rep.back_error}</div>`}
        <p class="help" style="margin:0">هر ردیف یک روتر در مسیر است، با ۱۰ بسته‌ی ping با TTL افزایشی. افت در یک روتر میانی که به روترهای بعد نمی‌رسد معمولاً فقط محدودیت پاسخ خود آن روتر است؛ افتی که از یک روتر تا مقصد ادامه دارد یعنی مشکل از همان‌جاست. مسیر رفت و برگشت بین ایران و خارج اغلب یکی نیست، برای همین هر دو جهت نشان داده می‌شود.</p>`;
    return html`${modalHead(html`مسیر ${t ? tname(t) : m.name} (MTR)`)}<div class="body stack">${body}</div>
      <footer><span class="grow"></span><button class="btn" data-act="mtr" data-name="${m.name}" ${m.busy ? "disabled" : ""}>${I.sync(15)} دوباره</button></footer>`;
  }

  function openPort(key, tab) {
    S.fm = { key, range: "24h", tab };
    openModal(portView, { passive: true, wide: true });
    if (tab === "clients") loadClients();
    const f = fwdOf(key); if (!f) return;
    api("GET", "forwards/history?key=" + encodeURIComponent(portId(f)))
      .then((h) => { if (S.fm && S.fm.key === key) { S.fm.hist = h; drawModal(); } })
      .catch((e) => { if (S.fm && S.fm.key === key) { S.fm.histErr = e.message; drawModal(); } });
  }

  async function loadClients() {
    const p = S.fm, f = p && fwdOf(p.key); if (!f || p.clientsBusy) return;
    p.clientsBusy = true; drawModal();
    try { p.clients = await api("GET", "forwards/clients?key=" + encodeURIComponent(portId(f))); p.clientsErr = ""; }
    catch (e) { p.clientsErr = e.message; }
    p.clientsBusy = false;
    if (S.fm === p) drawModal();
  }

  async function planCall(path, body, ok) {
    try {
      const ch = await api("POST", path, body);
      S.fm.draft = null; S.fm.rmAsk = false;
      await refresh(); reportChange(ch, ok); drawModal();
    } catch (e) { toast(e.message, "bad"); }
  }

  function saleState(f, pl, d) {
    const used = pairSum(f.period), q = pl.quota_gb * 2 ** 30, frac = q ? Math.min(1, used / q) : 0;
    const left = pl.expires ? Math.ceil((pl.expires - Date.now() / 1000) / 86400) : 0;
    return html`<div class="psale-st ${f.closed ? "closed" : ""}">
      <div class="row">${f.closed ? html`<span class="tag bad">بسته: ${CLOSED[f.closed] || f.closed}</span>` : html`<span class="tag ok">باز</span>`}${pl.customer ? html`<b>${pl.customer}</b>` : ""}<span class="grow"></span>
        <span class="faint small">${pl.expires ? html`${left > 0 ? `${left} روز مانده · ` : ""}تا ${F.dmy.format(new Date(pl.expires * 1000))}` : "بدون تاریخ پایان"}</span></div>
      ${q ? html`<div class="quota ${f.closed === "quota" ? "full" : frac > 0.85 ? "near" : ""}"><div class="pl"><span><b class="ltr">${fmtBytes(used)}</b> از <span class="ltr">${fmtBytes(q)}</span></span><span class="faint">مانده <span class="ltr">${fmtBytes(Math.max(0, q - used))}</span> · ${(frac * 100).toFixed(0)}٪</span></div><div class="pbar"><i style="transform:scaleX(${frac.toFixed(3)})"></i></div></div>`
        : html`<div class="small">مصرف این دوره: <b class="ltr">${fmtBytes(used)}</b></div>`}
      <div class="faint small">دوره از ${F.dmy.format(new Date(f.period_from * 1000))}${f.period_end ? ` تا ${F.dmy.format(new Date(f.period_end * 1000))}، بعد حجم دوباره کامل می‌شود` : ""} · <span class="ltr">↓ ${fmtBytes(f.period.rx)} · ↑ ${fmtBytes(f.period.tx)}</span></div>
      <div class="row psale-acts"><div class="pf" style="max-width:260px"><input class="input ltr" type="number" min="0" max="3650" data-bind="fm.draft.renew" value="${d.renew}" aria-label="روزهای تمدید"><span class="faint small">روز</span><button type="button" class="btn sm primary" data-act="plan-renew">تمدید</button></div>
        <label class="switch sm"><input type="checkbox" data-act="st-check" data-path="fm.draft.renewReset" ${d.renewReset ? "checked" : ""}>حجم مصرفی هم صفر شود</label>
        <span class="grow"></span><button type="button" class="btn sm" data-act="plan-pause">${pl.paused ? html`${I.play(14)} ادامه` : "توقف موقت"}</button></div>
      <span class="help">تمدید این تعداد روز به پایان اضافه می‌کند (اگر گذشته باشد، از امروز). حجم و ترافیک مصرفی هیچ‌وقت خودکار صفر نمی‌شود؛ فقط اگر «حجم مصرفی هم صفر شود» را بزنید دوره‌ی تازه از همین الان شروع می‌شود و حجم دوباره کامل می‌شود.</span>
    </div>`;
  }
  // ---------- privacy ----------
  // With the eye closed, addresses, keys and setup commands are blurred, for
  // screenshots and screen sharing. Anything that looks like an IPv4 or
  // IPv6 address is found by itself; keys and commands carry "sens".
  const IP4 = /\b\d{1,3}(\.\d{1,3}){3}\b/, IP6 = /[0-9a-f]{0,4}::[0-9a-f:]*|([0-9a-f]{1,4}:){3,}[0-9a-f]{1,4}/i;
  // Short values only (an address, a command), not sentences that mention
  // one; the loopback is no secret.
  const looksSensitive = (t) => !!t && t.length < 160 && (IP4.test(t.replace(/\b127\.0\.0\.1\b|\b0\.0\.0\.0\b/g, "")) || IP6.test(t));
  function markSensitive(root) {
    if (!root) return;
    for (const el of root.querySelectorAll("span, b, small, td, dd, code, pre, div, a, text, small, li, option")) {
      if (el.childElementCount === 0 && looksSensitive(el.textContent)) el.classList.add("sens");
    }
    for (const el of root.querySelectorAll("input, textarea")) {
      if (looksSensitive(el.value)) el.classList.add("sens");
    }
  }

  // A connection tunnel's own settings: who listens, its first port (the
  // two fallback ports stay), the TLS server name and how many sessions.
  function streamSettings(t, st) {
    const path = `tset.${t.name}`, far = pname(t.peer) || t.peer;
    return html`<div class="field"><span class="lbl">جهت اتصال</span><div class="seg" role="radiogroup">
        <button type="button" role="radio" aria-checked="${!st.reverse}" class="${!st.reverse ? "on" : ""}" data-act="set" data-path="${path}.reverse" data-value="">${dirName(false)}</button>
        <button type="button" role="radio" aria-checked="${!!st.reverse}" class="${st.reverse ? "on" : ""}" data-act="set" data-path="${path}.reverse" data-value="1">${dirName(true)}</button></div>
        <span class="help">${st.reverse ? `${far} به این سرور وصل می‌شود، مثل FRP؛ این سرور پورت‌ها را باز می‌کند.` : `این سرور به ${far} وصل می‌شود؛ ${far} پورت‌ها را باز می‌کند.`} تغییرش اول جلسه‌های تازه را بالا می‌آورد و بعد قبلی‌ها را می‌بندد.</span></div>
      <div class="field"><label>پورت اول (TCP)</label><div class="pf"><input class="input ltr" type="number" min="1" max="65535" data-bind="${path}.port" value="${st.port || ""}" style="max-width:220px"><button type="button" class="btn sm" data-act="random-port" data-path="${path}.port" data-proto="tcp">${I.dice(14)} تصادفی</button></div>
        <span class="help">بعد از این، ${t.stream.ports.slice(1).join(" و ") || "پورت‌های پشتیبان"} امتحان می‌شوند.</span></div>
      ${t.kind === "stream-tls" ? html`<div class="field"><label>نام سرور TLS (SNI)</label><input class="input ltr" data-bind="${path}.sni" value="${st.sni || ""}" placeholder="خالی = بدون SNI" style="max-width:320px"></div>` : ""}
      <div class="field"><label>تعداد جلسه‌های هم‌زمان</label><input class="input ltr" type="number" min="1" max="16" data-bind="${path}.conns" value="${st.conns || ""}" placeholder="خودکار (${t.stream.want})" style="max-width:220px"></div>`;
  }

  function tunnelDetail(t) {
    const st = S.tset[t.name] || (S.tset[t.name] = Object.assign({ port: t.base_port || "" }, t.settings, { pmtud: t.settings.ignore_df ? "ignore" : t.settings.no_df ? "off" : "on", mtuMode: t.settings.mtu ? "manual" : t.settings.mtu_probe ? "probe" : "auto", rate: t.limits.rate_mbit || "", quota: t.limits.quota_gb || "", queue: t.limits.queue_mbit || "", queueOn: !!t.limits.queue_mbit }));
    const base = (t.stack && t.stack[0]) || { kind: t.kind };
    const crypto = ["wireguard", "obfs"].includes(base.kind) && !t.auto_encrypt;
    const autoOpt = html`<option value="auto" ${st.encrypt === "auto" ? "selected" : ""}>خودکار (هرچه رد شود)</option>`;
    const autoHelp = st.encrypt === "auto" ? html`<span class="help">از ساده به پیشرفته: بدون رمز، بعد هر رمز IPsec${base.kind === "tcp" ? "" : "، بعد NeoObfs"}. تا وقتی ترافیک رد می‌شود دست نمی‌خورد؛ اگر رد نشد مرحله‌ی بعد را می‌زند، تانل را روی هر دو سرور ری‌استارت می‌کند و ${autoWaitMin()} دقیقه صبر می‌کند. اگر پورت‌های این تانل پشتیبانِ سالم داشته باشند به آن می‌روند و این تانل فقط بررسی می‌شود تا سالم شود و پورت‌ها برگردند.${t.auto_encrypt ? html` الان: <b>${rungTitle(t.encrypt)}</b>.` : ""}</span>` : "";
    const m = S.mtu[t.name];
    const tab = t.managed && S.dtab[t.name] !== "events" ? "overview" : (S.dtab[t.name] || "overview");
    const tabs = t.managed ? [["overview", "نمای کلی"], ["events", "رویدادها"]] : [["overview", "نمای کلی"], ["events", "رویدادها"], ["nightly", "تست‌های شبانه"], ["limits", "محدودیت‌ها"], ["settings", "تنظیمات"]];
    return html`<div class="detail">
        <div class="row dbar">
          <button class="btn sm" data-act="test" data-name="${t.name}">${I.gauge(15)} تست سرعت</button>
          ${t.managed || t.stream ? "" : html`<button class="btn sm" data-act="mtu" data-name="${t.name}">${I.ruler(15)} بهترین MTU</button>`}
          <button class="btn sm" data-act="mtr" data-name="${t.name}">${I.wave(15)} مسیر (MTR)</button>
          <button class="btn sm" data-act="drawer" data-type="tunnel" data-name="${t.name}">نمودار</button>
          <button class="btn sm" data-act="ping-open" data-name="${t.name}">${I.wave(15)} پینگ</button>
          ${t.managed ? "" : html`<button class="btn sm" data-act="tunnel-restart" data-name="${t.name}" ${S.restarting[t.name] || (t.limits && t.limits.disabled) ? "disabled" : ""}>${I.sync(15)} ری‌استارت</button>`}
          ${t.peer && peer(t.peer) ? html`<button class="btn sm ghost" data-act="setup" data-name="${t.peer}">${I.term(15)} دستور اتصال</button>` : ""}
          ${!t.peer && !t.managed ? html`<button class="btn sm ghost" data-act="mikrotik" data-name="${t.name}">${I.term(15)} دستورهای میکروتیک</button>` : ""}
          <span class="grow"></span>
          ${t.managed ? "" : safeField(true)}
          ${t.managed ? "" : html`<button class="btn sm ${t.limits.disabled ? "primary" : "ghost"}" data-act="tunnel-power" data-name="${t.name}">${I.power(15)} ${t.limits.disabled ? "روشن کردن" : "خاموش کردن"}</button>`}
          ${t.managed ? "" : html`<button class="btn sm danger" data-act="del-tunnel" data-name="${t.name}">${I.trash(15)} حذف</button>`}
        </div>
      ${html`<div class="seg dtabs" role="tablist">${tabs.map(([id, l]) => html`<button type="button" role="tab" aria-selected="${tab === id}" class="${tab === id ? "on" : ""}" data-act="dtab" data-name="${t.name}" data-tab="${id}">${l}</button>`)}</div>`}
      ${tab === "events" ? eventsPane(t) : ""}
      ${tab === "nightly" ? nightlyPane(t) : ""}
      ${tab === "overview" ? html`<div class="dpane stack" data-key="ov-${t.name}">
        <div class="figs">
          <div><div class="k">امروز</div><div class="v ltr" data-live="today:${t.name}">${fmtBytes(t.today.rx + t.today.tx)}</div></div>
          <div><div class="k">این ماه</div><div class="v ltr">${fmtBytes(t.month.rx + t.month.tx)}</div></div>
          <div><div class="k">از ابتدا</div><div class="v ltr">${fmtBytes(t.total.rx + t.total.tx)}</div></div>
        </div>
        <dl class="kv">
          ${t.here4 ? html`<dt>IPv4 این طرف ⇄ آن طرف</dt><dd class="ltr">${t.here4.split("/")[0]} ⇄ ${t.there4}</dd>` : ""}
          ${t.here6 ? html`<dt>IPv6</dt><dd class="ltr small">${t.here6.split("/")[0]} ⇄ ${t.there6}</dd>` : ""}
          ${t.stream ? html`<dt>جهت</dt><dd>${dirName(t.settings && t.settings.reverse)}: ${t.stream.listens ? "این سرور گوش می‌دهد و سرور مقابل وصل می‌شود" : "این سرور به سرور مقابل وصل می‌شود"}</dd>
          <dt>پورت‌ها (به ترتیب امتحان)</dt><dd class="ltr">${t.stream.ports.join(", ")}</dd>
          <dt>جلسه‌ها</dt><dd>${t.stream.sessions} باز از ${t.stream.want}</dd>
          <dt>اتصال‌های باز کاربران</dt><dd>${t.stream.conns}</dd>
          ${t.stream.sni ? html`<dt>SNI</dt><dd class="ltr">${t.stream.sni}</dd>` : ""}`
          : html`<dt>لایه‌ها</dt><dd>${(t.stack || []).map((l) => html`<span class="tag ltr">${l.kind} · ${l.mtu || "-"}</span> `)}</dd>
          ${t.relay_ports && t.relay_ports.length ? html`<dt>پورت‌های رله (به ترتیب امتحان)</dt><dd class="ltr">${t.relay_ports.join(", ")}</dd>` : ""}`}
          <dt>حالت سرعت</dt><dd>${(PROFILES.find(([id]) => id === (t.settings.profile || "balanced")) || PROFILES[1])[1]}</dd>
          ${t.settings.lanes > 1 ? html`<dt>مسیرهای موازی</dt><dd>${t.settings.lanes} lane</dd>` : ""}
          ${t.stream ? "" : html`<dt>MTU فعلی</dt><dd class="ltr">${t.mtu || "-"}${t.path_mtu ? ` (path ${t.path_mtu})` : ""}</dd>`}
          ${t.health && t.health.action ? html`<dt>آخرین اقدام خودکار</dt><dd>${actionText(t.health)} · ${F.dmhm.format(new Date(t.health.action_at * 1000))}</dd>` : ""}
          ${t.wg_key ? html`<dt>کلید عمومی WireGuard</dt><dd class="ltr small sens">${t.wg_key} <button class="btn sm ghost icon" data-act="copy" data-text="${t.wg_key}" aria-label="کپی کلید">${I.copy(14)}</button></dd>` : ""}
        </dl>
        ${tunnelForwards(t)}
        ${m ? html`<div class="note ok">مسیر تا <span class="ltr">${m.remote}</span> بسته‌های <b>${m.path_mtu}</b> بایتی را بدون شکستن رد می‌کند. با ${m.overhead} بایت سربار لایه‌ها، بهترین MTU این تانل <b>${m.best}</b> است${m.probing ? " و تانل خودکار با آن تنظیم می‌شود." : "."}</div>` : ""}
        ${t.error ? html`<div class="note bad"><div class="mono">${t.error}</div></div>` : ""}
      </div>` : ""}
      ${tab === "limits" ? html`<div class="dpane" data-key="lim-${t.name}">${limitsBlock(t, st)}</div>` : ""}
      ${tab === "settings" || t.managed ? html`<div class="dpane" data-key="set-${t.name}">
      ${t.managed ? html`<div class="note">این تانل را سرور اصلی (${pname(t.peer)}) تعریف کرده؛ تنظیماتش را از همان‌جا تغییر دهید.</div>` : html`<form class="stack" data-form="tunnel-settings" data-name="${t.name}">
        ${profileField(`tset.${t.name}`, st.profile, base.kind)}
        ${LANE_KINDS.includes(t.kind) ? lanesField(`tset.${t.name}`, st.lanes) : ""}
        ${t.stream ? streamSettings(t, st) : ""}
        <div class="field"><span class="lbl">رمزنگاری</span>
          ${base.kind === "stream-tls" ? html`<div class="help">TLS + mux با TLS 1.3 رمز می‌شود؛ لایه‌ی دیگری لازم نیست.</div>` : crypto ? html`<div class="help">${PROTO[base.kind === "obfs" ? "obfs" : "wireguard"].t} خودش رمزنگاری می‌کند؛ لایه‌ی دیگری لازم نیست.</div>`
            : ["tcp", "stream-tcp"].includes(base.kind) ? html`<select class="input" data-bind="tset.${t.name}.encrypt"><option value="">بدون رمزنگاری (سریع‌ترین)</option>${S.ov.ciphers.map((c) => html`<option value="${c.name}" ${st.encrypt === c.name ? "selected" : ""}>${c.title}</option>`)}${t.peer ? autoOpt : ""}</select>${autoHelp}
            <span class="help">هر قاب روی جریان TCP با کلیدهای تازه‌ی همان اتصال مهر می‌شود؛ کلیدها از رمز مشترک تانل ساخته می‌شوند. هزینه: ۱۶ بایت در هر بسته.</span>`
            : html`<select class="input" data-bind="tset.${t.name}.encrypt"><option value="">بدون رمزنگاری (سریع‌ترین)</option>${S.ov.ciphers.map((c) => html`<option value="${c.name}" ${st.encrypt === c.name ? "selected" : ""}>IPsec ${c.title}${c.name === lightCipher(t.peer).name ? " · سبک‌ترین برای این سرورها" : ""}</option>`)}${t.peer ? autoOpt : ""}</select>${autoHelp}
            <span class="help">IPsec در کرنل روی بسته‌های بیرونی همین تانل؛ کلیدها از رمز مشترک تانل ساخته می‌شوند. هزینه: ${38} بایت از MTU. ${lightCipher(t.peer).why}</span>`}</div>
        ${t.stream ? "" : html`<details class="adv" ${S.tset[t.name].adv ? "open" : ""} data-act="adv-toggle" data-name="${t.name}">
          <summary>تنظیمات پیشرفته <span class="faint">MTU، PMTUD، TTL، پورت${base.kind === "obfs" ? "، padding" : ""}${["obfs", "wireguard", "tcp", "eoip"].includes(base.kind) ? "، keepalive" : ""}</span>${I.chev(16)}</summary>
          <div class="in">${advancedFields(`tset.${t.name}`, st, base.kind, true, false, t.kind)}</div>
        </details>`}
        <div class="form-foot"><button class="btn primary" type="submit">ذخیره‌ی تنظیمات</button><button class="btn ghost" type="button" data-act="reset-tset" data-name="${t.name}">بازگردانی</button><span class="grow"></span>${safeField()}</div>
      </form>`}
      </div>` : ""}
    </div>`;
  }

  // The forwards that go through a tunnel, each editable in place.
  function tunnelForwards(t) {
    const fs = (S.ov.forwards || []).filter((f) => f.tunnel === t.name || (f.backups || []).includes(t.name));
    if (t.managed) return "";
    return html`<div class="tfwd"><div class="tfwd-h"><b>پورت‌ها</b>${fs.length ? "" : html`<span class="faint small">هنوز پورتی از این تانل فوروارد نشده</span>`}<span class="grow"></span><button class="btn sm ghost" data-act="add-forward" data-tunnel="${t.name}">${I.plus(14)} فوروارد</button></div>
      ${fs.map((f) => { const k = f.tunnel + "|" + f.ports; return html`<div class="tfwd-r"><b class="ltr">${f.ports}</b><span class="faint small ltr">${f.mode === "socks" ? proxyAt(f) : `→ ${f.target}:${f.to || f.ports}`}</span>${fwdModeLabel(f)}${(f.backups || []).includes(t.name) ? html`<span class="tag">پشتیبانِ ${tlabel(f.tunnel)}</span>` : ""}${fwdPath(f)}<span class="grow"></span><button class="btn sm" data-act="edit-fwd" data-key="${k}">${I.edit(14)} ویرایش</button></div>`; })}</div>`;
  }

  // Speed limit and monthly quota, with how much of the quota is used.
  function limitsBlock(t, st) {
    const used = t.month.rx + t.month.tx, q = t.limits.quota_gb * 2 ** 30, f = q ? Math.min(1, used / q) : 0, guess = queueGuess(t);
    return html`<div class="limits">
      <div class="grid2">
        <div class="field"><label>سقف سرعت (Mbps)</label><input class="input ltr" type="number" min="0" max="100000" data-bind="tset.${t.name}.rate" value="${st.rate}" placeholder="بدون محدودیت">
          <span class="help">هر دو سر همین سقف را روی ارسال خود می‌گذارند، پس دانلود و آپلود هر دو محدود می‌شوند.</span></div>
        <div class="field"><label>حجم ماهانه (GB)</label><input class="input ltr" type="number" min="0" step="any" data-bind="tset.${t.name}.quota" value="${st.quota}" placeholder="نامحدود">
          <span class="help">دانلود + آپلود این ماه. وقتی پر شد تانل تا اول ماه بعد خاموش می‌شود.</span></div>
      </div>
      <div class="field"><label class="switch"><input type="checkbox" data-act="st-check" data-path="tset.${t.name}.queueOn" ${st.queueOn ? "checked" : ""}>صف هوشمند: پینگ پایین وقتی تانل پر است</label>
        ${st.queueOn ? html`<div class="pf" style="max-width:340px"><input class="input ltr" type="number" min="1" max="100000" data-bind="tset.${t.name}.queue" value="${st.queue}" placeholder="${guess || "Mbps"}" aria-label="سرعت صف هوشمند (Mbps)"><span class="faint small">Mbps</span>${guess ? html`<button type="button" class="btn sm" data-act="set" data-path="tset.${t.name}.queue" data-value="${guess}">از آخرین تست: ${guess}</button>` : ""}</div>` : ""}
        <span class="help">وقتی تانل پر است، بسته‌ها در صف‌های بزرگ مسیر می‌مانند و پینگ تا صدها میلی‌ثانیه بالا می‌رود. صف هوشمند (CAKE) با سرعتی کمی کمتر از سرعت واقعی مسیر، صف را به همین سرور می‌آورد و سهم هر اتصال را جدا نگه می‌دارد؛ پینگ و بازی پشت دانلود منتظر نمی‌مانند. حدود ۹۰٪ سرعتی را بدهید که تست سرعت نشان می‌دهد؛ بیشتر از آن اثری ندارد و خیلی کمترش سرعت را بی‌دلیل پایین می‌آورد.</span></div>
      ${q ? html`<div class="quota ${t.blocked ? "full" : f > 0.85 ? "near" : ""}"><div class="pl"><span>${fmtBytes(used)} از ${fmtBytes(q)}</span><span class="faint">${(f * 100).toFixed(0)}٪</span></div><div class="pbar"><i style="transform:scaleX(${f.toFixed(3)})"></i></div></div>` : ""}
      <div class="row"><button class="btn sm primary" type="button" data-act="save-limits" data-name="${t.name}">ذخیره‌ی محدودیت‌ها</button></div>
    </div>`;
  }

  // Which kinds have a DF bit to set on their outer packets. The wizard gives
  // a mode, whose bottom layer is what counts; IPv6 has no such bit.
  const PMTU_KINDS = ["sit", "ipip", "gre", "gretap", "eoip"], IGNORE_DF_KINDS = ["gre", "gretap"];
  const pmtuKind = (kind, st) => st.underlay === "ipv6" ? "" : { "6to4": "sit", "6to4-gre": "sit" }[kind] || kind;

  // The advanced options, shared by the wizard and the tunnel editor.
  // kind is the bottom layer's (it owns the outer packets), top the tunnel's own.
  function advancedFields(path, st, kind, editing, noPort, top) {
    const mm = st.mtuMode || "auto";
    const pk = pmtuKind(kind, st), canIgnore = IGNORE_DF_KINDS.includes(pk);
    if (st.pmtud === "ignore" && !canIgnore) st.pmtud = "off";
    const pm = st.pmtud || "on";
    return html`<div class="field"><span class="lbl">MTU</span>
        <div class="seg" role="radiogroup">${[["auto", "خودکار"], ["probe", "اندازه‌گیری مسیر"], ["manual", "دستی"]].map(([v, l]) => html`<button type="button" role="radio" aria-checked="${mm === v}" class="${mm === v ? "on" : ""}" data-act="set" data-path="${path}.mtuMode" data-value="${v}">${l}</button>`)}</div>
        ${mm === "manual" ? html`<input class="input ltr" type="number" min="576" max="9000" data-bind="${path}.mtu" value="${st.mtu || ""}" placeholder="1400" aria-label="MTU">` : ""}
        <span class="help">${mm === "auto" ? "از ۱۵۰۰ و سربار همه‌ی لایه‌ها حساب می‌شود." : mm === "probe" ? "NeoTunnel با بسته‌های بدون شکستن، MTU واقعی مسیر تا مقصد را پیدا می‌کند، هر ۳۰ دقیقه دوباره می‌سنجد و تانل را با آن تنظیم می‌کند." : "فقط وقتی مقدار درست را می‌دانید."}</span></div>
      <div class="grid2">
        ${noPort ? "" : html`<div class="field"><label>پورت ${kind === "tcp" ? "TCP" : "UDP"}${editing ? "" : " (اختیاری)"}</label><div class="pf"><input class="input ltr" type="number" min="1" max="65535" data-bind="${path}.port" value="${st.port || ""}" placeholder="${["obfs", "wireguard", "tcp"].includes(kind) ? "خودکار" : "پروتکل خام"}"><button type="button" class="btn sm" data-act="random-port" data-path="${path}.port" data-proto="${kind === "tcp" ? "tcp" : "udp"}" title="یک پورت آزاد تصادفی بین ۲۰۰۰۰ و ۶۰۰۰۰">${I.dice(14)} تصادفی</button></div>
          <span class="help">${PROTO[kind] && (PROTO[kind].udp || PROTO[kind].tcp) || kind === "obfs" || kind === "wireguard" ? "پورتی که دو سر روی آن گوش می‌دهند." : "خالی = پروتکل خام (سریع‌ترین). با پورت، تانل داخل UDP می‌رود؛ وقتی مسیر پروتکل 41 یا 47 را می‌بندد."}</span></div>`}
        <div class="field"><label>TTL</label><input class="input ltr" type="number" min="1" max="255" data-bind="${path}.ttl" value="${st.ttl || ""}" placeholder="255"><span class="help">TTL بسته‌های بیرونی.</span></div>
        ${kind === "obfs" ? html`<div class="field"><label>Padding (بایت)</label><input class="input ltr" type="number" min="0" max="255" data-bind="${path}.padding" value="${st.padding || ""}" placeholder="0"><span class="help">تا این تعداد بایت تصادفی به هر بسته اضافه می‌شود تا اندازه‌ها چیزی را لو ندهند. هزینه‌اش همین مقدار از MTU است.</span></div>` : ""}
        ${["obfs", "wireguard", "eoip"].includes(kind) ? html`<div class="field"><label>Keepalive (ثانیه)</label><input class="input ltr" type="number" min="-1" max="3600" data-bind="${path}.keepalive" value="${st.keepalive || ""}" placeholder="25"><span class="help">نگه‌داشتن مسیر NAT باز؛ ‎-1 یعنی خاموش.</span></div>` : ""}
        ${kind === "tcp" ? html`<div class="field"><label>Keepalive (ثانیه)</label><input class="input ltr" type="number" min="-1" max="3600" data-bind="${path}.keepalive" value="${st.keepalive || ""}" placeholder="15"><span class="help">اتصالی که سه برابر این مدت ساکت بماند قطع و دوباره برقرار می‌شود؛ ‎-1 یعنی فقط keepalive خود TCP.</span></div>` : ""}
        ${kind === "wireguard" ? html`<div class="field full"><label>کلید عمومی طرف مقابل</label><input class="input ltr" data-bind="${path}.peer_key" value="${st.peer_key || ""}" placeholder="فقط وقتی آن طرف NeoTunnel ندارد"><span class="help">برای میکروتیک یا هر WireGuard دیگر؛ بین دو سرور NeoTunnel کلیدها خودکار ساخته می‌شوند.</span></div>` : ""}
        ${top === "eoip" ? html`<div class="field"><label>شناسه‌ی تانل (Tunnel ID)</label><input class="input ltr" type="number" min="0" max="65535" data-bind="${path}.tunnel_id" value="${st.tunnel_id ?? ""}" placeholder="خودکار"><span class="help">همان tunnel-id میکروتیک؛ دو سر باید یکی باشند. ۰ تا ۶۵۵۳۵، و روی EoIPv6 بدون پورت ۰ تا ۴۰۹۵. خالی = از نام تانل ساخته می‌شود.</span></div>` : ""}
        <div class="field"><label>DSCP بسته‌های بیرونی</label><input class="input ltr" type="number" min="0" max="63" data-bind="${path}.dscp" value="${st.dscp || ""}" placeholder="ارث‌بری"><span class="help">کلاس کیفیت سرویس؛ خالی = همان کلاس بسته‌ی داخلی. فقط تانل‌های کرنلی.</span></div>
        ${PMTU_KINDS.includes(pk) ? html`<div class="field full"><span class="lbl">PMTUD (Path MTU Discovery)</span>
          <div class="seg" role="radiogroup">${[["on", "روشن"], ["off", "خاموش"]].concat(canIgnore ? [["ignore", "خاموش + نادیده گرفتن DF"]] : []).map(([v, l]) => html`<button type="button" role="radio" aria-checked="${pm === v}" class="${pm === v ? "on" : ""}" data-act="set" data-path="${path}.pmtud" data-value="${v}">${l}</button>`)}</div>
          <span class="help">${pm === "on" ? "پیش‌فرض و سریع‌ترین: بیت DF روی بسته‌های بیرونی روشن است و مسیر، اندازه‌ی مجاز را با ICMP خبر می‌دهد. وقتی MTU درست است یا ICMP در مسیر باز است همین خوب است." : pm === "off" ? "مثل nopmtudisc: تانل خودش DF نمی‌زند و مسیر می‌تواند بسته‌ی بزرگ را تکه کند. ولی اگر بسته‌ی داخلی خودش DF داشته باشد (بیشتر TCPها) DF به بسته‌ی بیرونی هم می‌رود." : "مثل nopmtudisc به‌همراه ignore-df: بسته‌ی بزرگ همیشه تکه می‌شود، حتی اگر بسته‌ی داخلی DF داشته باشد. برای مسیری که ICMP را می‌بندد و اتصال‌ها سر بسته‌های بزرگ گیر می‌کنند. تکه شدن کمی سرعت و CPU می‌گیرد."}</span></div>` : ""}
      </div>`;
  }

  function settingsBody(st) {
    const out = { profile: st.profile && st.profile !== "balanced" ? st.profile : "", encrypt: st.encrypt || "", ttl: +st.ttl || 0, dscp: +st.dscp || 0, no_df: st.pmtud === "off" || st.pmtud === "ignore", ignore_df: st.pmtud === "ignore", padding: +st.padding || 0, keepalive: +st.keepalive || 0, peer_key: (st.peer_key || "").trim(), lanes: +st.lanes > 1 ? +st.lanes : 0, mtu: 0, mtu_probe: false };
    if (st.mtuMode === "manual") out.mtu = +st.mtu || 0;
    if (st.mtuMode === "probe") out.mtu_probe = true;
    // Connection tunnels: who listens, the TLS server name, sessions.
    Object.assign(out, { reverse: !!st.reverse, sni: (st.sni || "").trim(), conns: +st.conns || 0 });
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
    const master = masters().map((m) => html`<div class="note ${m.paired ? "ok" : "warn"}" style="margin-bottom:16px">این سرور توسط <b>${m.name}</b> (<span class="ltr">${m.address}</span>) مدیریت می‌شود ${m.paired ? "و به آن متصل است." : "ولی هنوز به آن نرسیده؛ سرویس مدام تلاش می‌کند."} تانل‌های آن فقط از پنل همان سرور تغییر می‌کنند.</div>`);
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
      <header><div><span class="pname"><b>${plabel(p)}</b>${renameBtn("server", p.name)}</span><span class="ltr faint small">${p.address}${p.label ? ` · ${p.name}` : ""}</span></div><span class="state"><i class="dot ${cls}"></i>${label}</span></header>
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

  // The cipher that costs least on this pair of servers: AES-GCM when both
  // CPUs have AES instructions (it then adds well under a millisecond), else
  // ChaCha20, which is fast in software.
  function lightCipher(peerName) {
    const p = peerName && peer(peerName), here = S.ov.node.aes_ni, there = p ? p.aes_ni : undefined;
    if (here && there !== false) return { name: "aes-gcm-128", why: there === undefined ? "پردازنده‌ی این سرور AES سخت‌افزاری دارد؛ AES-128-GCM سبک‌ترین است (پردازنده‌ی سرور مقابل هنوز گزارش نشده)." : "پردازنده‌ی هر دو سرور AES سخت‌افزاری دارد؛ AES-128-GCM سبک‌ترین است و کمتر از یک میلی‌ثانیه به پینگ اضافه می‌کند." };
    return { name: "chacha20-poly1305", why: "یکی از دو سرور AES سخت‌افزاری ندارد؛ ChaCha20 در نرم‌افزار سریع‌تر است. اگر کرنل سرور آن را نداشت، پنل خطا نشان می‌دهد و AES-GCM را انتخاب کنید." };
  }

  // A forward's mode in words.
  const modeInfo = (m) => MODES_FWD[m] || (m === "socks" ? ["پروکسی SOCKS/HTTP", "پروکسی روی این سرور برای برنامه‌های همین‌جا؛ اتصال‌ها از سرور مقصد به اینترنت می‌روند."] : [m, ""]);
  const fwdModeLabel = (f) => html`<span class="tag" title="${modeInfo(f.mode)[1]}">${modeInfo(f.mode)[0]}${f.mode === "proxy" ? ` v${f.proxy_version || 2}` : f.mode === "relay" && f.proxy_version ? ` + PROXY v${f.proxy_version}` : ""}</span>${f.to_public ? html`<span class="tag" title="روی IP پابلیک سرور مقصد تحویل می‌شود، از داخل تانل">IP پابلیک</span>` : ""}`;

  // Tunnels that can back up a forward on tunnel name: our other tunnels to
  // the same far server.
  const backupChoices = (name) => {
    const t = tunnel(name), far = t && (t.peer || t.far_ip);
    return far ? visible().filter((x) => !x.managed && x.name !== name && (x.peer || x.far_ip) === far) : [];
  };
  // The forward's backups in order of preference: path is a top-level state
  // object (form, fwdEditor) holding them in .backups.
  function backupField(path, obj, name) {
    const cs = backupChoices(name), list = obj.backups || (obj.backups = []);
    if (!cs.length && !list.length) return html`<div class="field full"><span class="lbl">تانل‌های پشتیبان</span><span class="help">برای مسیر پشتیبان، یک تانل دیگر به همین سرور بسازید؛ اگر تانل اصلی ترافیک رد نکرد، این پورت خودکار از آن می‌رود.</span></div>`;
    const id = path + ".backup", free = cs.filter((x) => !list.includes(x.name));
    const label = (n) => { const x = tunnel(n); return x ? html`${tname(x)} <span class="faint small">(${kindTitle(x)})</span>` : n; };
    return html`<div class="field full"><span class="lbl lbl-i"><label for="${path}-bk">تانل‌های پشتیبان</label> ${infoBtn(id)}</span>
      ${infoPop(id, html`<p>اگر تانلی که پورت روی آن است حدود ۲۰ ثانیه ترافیک رد نکند، پورت به اولین پشتیبانی می‌رود که کار می‌کند؛ به ترتیب همین فهرست. وقتی تانلی بالاتر از آن حدود یک دقیقه سالم ماند، پورت برمی‌گردد. اتصال‌های باز روی همان مسیر قبلی می‌مانند.</p>`)}
      ${list.length ? html`<ol class="bklist">${list.map((n, i) => html`<li><b class="ltr">${i + 1}</b><span>${label(n)}</span><span class="grow"></span>
        <button type="button" class="btn sm ghost icon" data-act="bk-move" data-path="${path}" data-i="${i}" data-dir="-1" ${i ? "" : "disabled"} aria-label="بالاتر" title="بالاتر">${I.up(14)}</button>
        <button type="button" class="btn sm ghost icon" data-act="bk-move" data-path="${path}" data-i="${i}" data-dir="1" ${i < list.length - 1 ? "" : "disabled"} aria-label="پایین‌تر" title="پایین‌تر">${I.down(14)}</button>
        <button type="button" class="btn sm ghost icon danger" data-act="bk-del" data-path="${path}" data-i="${i}" aria-label="برداشتن" title="برداشتن">${I.x(14)}</button></li>`)}</ol>` : ""}
      ${free.length ? html`<select id="${path}-bk" class="input" data-act="bk-add" data-path="${path}"><option value="">${list.length ? "افزودن پشتیبان بعدی…" : "بدون پشتیبان؛ افزودن…"}</option>${free.map((x) => html`<option value="${x.name}">${tname(x)} (${kindTitle(x)})</option>`)}</select>` : ""}
      ${list.length ? html`<label class="switch" style="margin-top:8px"><input type="checkbox" data-act="st-check" data-path="${path}.best" ${obj.best ? "checked" : ""}>بهترین مسیر، نه فقط مسیر سالم</label>
        <span class="help">پورت روی تانلی می‌رود که افت و پینگ بهتری دارد: وقتی تانل دیگری ۵ دقیقه پشت سر هم به‌وضوح بهتر بود، اتصال‌های تازه به آن می‌روند و اتصال‌های باز روی مسیر قبلی می‌مانند و قطع نمی‌شوند (حداکثر هر ۳۰ دقیقه یک بار). تست سرعت شبانه هم بین مسیرهای هم‌اندازه تصمیم می‌گیرد. اگر تانلی قطع شود، مثل همیشه فوراً از آن رد می‌شود.</span>` : ""}</div>`;
  }
  // Which of its tunnels a forward with a backup runs on now.
  const fwdPath = (f) => !f.backup ? "" : f.best
    ? html`<span class="tag" title="بهترین مسیر بین: ${[f.tunnel].concat(f.backups || []).map(tlabel).join("، ")}"><i class="dot ok"></i> بهترین مسیر · روی ${tlabel(f.active)}${f.since ? " · " + ago(f.since) : ""}</span>`
    : f.active !== f.tunnel
    ? html`<span class="tag warn" title="تانل اصلی ${tlabel(f.tunnel)} ترافیک رد نمی‌کند">${I.alert ? I.alert(12) : ""} روی پشتیبان ${tlabel(f.active)}${f.since ? " · " + ago(f.since) : ""}</span>`
    : html`<span class="tag" title="پشتیبان‌ها به ترتیب: ${(f.backups || []).map(tlabel).join("، ")}"><i class="dot ok"></i> مسیر اصلی · پشتیبان ${(f.backups || []).map(tlabel).join("، ")}</span>`;
  // A forward's recent moves between its tunnels, newest first.
  function switchHistory(f) {
    const ss = (f.switches || []).slice().reverse();
    if (!f.backup) return "";
    return html`<div class="field full"><span class="lbl">تاریخچه‌ی جابه‌جایی مسیر</span>${ss.length ? html`<ul class="fhist">${ss.map((s) => html`<li><b>${s.from} ← ${s.to}</b><span class="faint small">${new Date(s.at * 1000).toLocaleString("fa-IR")} · ${ago(s.at)}</span><span class="faint small ltr">${s.reason}</span></li>`)}</ul>` : html`<span class="help">هنوز جابه‌جایی نداشته؛ همیشه از مسیر اصلی رفته.</span>`}</div>`;
  }

  // Everything of a forward or a proxy can change: its tunnel, ports and how
  // it reaches the far end.
  function fwdEditView() {
    const e = S.fwdEditor, f = S.ov.forwards.find((x) => x.tunnel + "|" + x.ports === e.key);
    if (!f) return html`${modalHead("ویرایش پورت")}<div class="body"><div class="note">این فوروارد دیگر وجود ندارد.</div></div>`;
    const socks = f.mode === "socks", ts = visible().filter((x) => !x.managed && (!socks || x.peer)), t = tunnel(e.tunnel), remote = t && !t.peer;
    if (remote && (e.mode === "transparent" || e.mode === "relay")) e.mode = "nat";
    if (!socks) e.mode = fwdModeFor(t, e.mode);
    e.backups = (e.backups || []).filter((n) => backupChoices(e.tunnel).some((x) => x.name === n));
    const open = e.access === "all";
    const tunnelSel = html`<div class="field full"><label for="fe-t">از طریق تانل</label><select id="fe-t" class="input" data-bind="fwdEditor.tunnel" data-redraw="1">${ts.map((x) => html`<option value="${x.name}" ${e.tunnel === x.name ? "selected" : ""}>${tname(x)} به ${pname(x.peer) || x.far_ip} (${kindTitle(x)})</option>`)}</select></div>`;
    const body = socks ? html`<div class="grid2">${tunnelSel}
        <div class="field"><label for="fe-p">پورت</label><div class="pf"><input id="fe-p" class="input ltr" type="number" min="1" max="65535" data-bind="fwdEditor.ports" value="${e.ports}"><button type="button" class="btn sm" data-act="random-port" data-path="fwdEditor.ports" data-proto="tcp" title="یک پورت آزاد تصادفی">${I.dice(14)} تصادفی</button></div></div>
        <div class="field"><span class="lbl lbl-i">چه کسی وصل شود ${infoBtn("fe.access")}</span>${infoPop("fe.access", html`<p>فقط همین سرور: فقط برنامه‌های همین سرور (127.0.0.1). همه، با رمز: روی همه‌ی IPها گوش می‌دهد و پورتش در فایروال باز می‌شود.</p>`)}
          <div class="seg"><button type="button" class="${open ? "" : "on"}" data-act="set" data-path="fwdEditor.access" data-value="local">فقط همین سرور</button><button type="button" class="${open ? "on" : ""}" data-act="set" data-path="fwdEditor.access" data-value="all">همه، با رمز</button></div></div>
        ${open ? html`<div class="field"><label for="fe-u">نام کاربری</label><input id="fe-u" class="input ltr" data-bind="fwdEditor.user" value="${e.user}" autocomplete="off"></div>
        <div class="field"><label for="fe-pw">رمز</label><input id="fe-pw" class="input ltr" data-bind="fwdEditor.pass" value="${e.pass}" autocomplete="off"></div>` : ""}
        ${backupField("fwdEditor", e, e.tunnel)}</div>`
      : html`<div class="grid2">${tunnelSel}
        <div class="field"><label for="fe-p">پورت(های) این سرور</label><input id="fe-p" class="input ltr" data-bind="fwdEditor.ports" value="${e.ports}" placeholder="443, 2053-2096"></div>
        <div class="field"><label for="fe-to">پورت مقصد</label><input id="fe-to" class="input ltr" type="number" min="1" max="65535" data-bind="fwdEditor.to" value="${e.to}" placeholder="همان پورت"></div>
        <div class="field"><span class="lbl">پروتکل</span><div class="seg">${fwdProtos(e.mode).map((v) => html`<button type="button" class="${fwdProto(e.mode, e.proto) === v ? "on" : ""}" data-act="set" data-path="fwdEditor.proto" data-value="${v}">${v === "both" ? "TCP + UDP" : v.toUpperCase()}</button>`)}</div></div>
        ${t && t.stream ? streamModeNote : html`${modeField("fwdEditor", e.mode, remote)}${sitHint(t && !remote && t.mode, e.mode)}`}
        ${pverField("fwdEditor", e, e.mode)}
        ${targetField("fwdEditor", e, e.mode)}
        ${backupField("fwdEditor", e, e.tunnel)}</div>`;
    return html`${modalHead(html`ویرایش ${socks ? "پروکسی" : "پورت"} <span class="ltr">${f.ports}</span>`)}
      <div class="body">${body}
        ${f.backup ? html`<div class="field" style="margin-top:12px"><span class="lbl">مسیر فعلی</span><div>${fwdPath(f)}</div></div>` : ""}
        ${switchHistory(f)}
        <div style="margin-top:12px">${safeField()}</div>
      </div>
      <footer><span class="grow"></span><button class="btn ghost" data-act="close">انصراف</button><button class="btn primary" data-act="save-fwd" ${e.busy ? "disabled" : ""}>${I.check(16)} ذخیره</button></footer>`;
  }

  // ---------- ports ----------
  // Where a proxy listens, and the URLs programs are given.
  const proxyAt = (f) => `${f.listen || "127.0.0.1"}:${f.ports}`;
  function proxyURL(f, scheme) {
    const host = f.listen && f.listen !== "0.0.0.0" && f.listen !== "::" ? f.listen : f.listen ? S.ov.node.address || "SERVER_IP" : "127.0.0.1";
    const h = host.includes(":") ? `[${host}]` : host;
    const [u, p] = (f.auth || "").split(":");
    const cred = f.auth ? `${encodeURIComponent(u)}:${encodeURIComponent(p)}@` : "";
    return `${scheme}://${cred}${h}:${f.ports}`;
  }

  function proxiesPanel(ps) {
    const head = html`<header><h2>پروکسی داخلی (SOCKS5 / HTTP)</h2><span class="aside">برای برنامه‌های همین سرور که باید از سرور مقصد به اینترنت بروند</span><span class="grow"></span><button class="btn sm primary" data-act="add-proxy">${I.plus(14)} پروکسی جدید</button></header>`;
    if (!ps.length) return html`<div class="panel">${head}<div class="in"><p class="muted" style="margin:0">برنامه‌ای روی این سرور دارید که مستقیم به جایی وصل نمی‌شود (مثلاً تلگرام)؟ یک پروکسی بسازید و آدرسش را در تنظیمات آن برنامه بدهید؛ اتصال‌هایش از داخل تانل به سرور مقصد می‌روند و از آنجا به اینترنت.</p></div></div>`;
    return html`<div class="panel">${head}<div class="in proxies">${ps.map((f) => html`<div class="proxy">
        <div class="proxy-h"><b class="ltr">${proxyAt(f)}</b><span class="faint">از تانل</span> ${tlabel(f.tunnel)} <span class="faint">به</span> ${pname(f.peer)}
          ${f.auth ? html`<span class="tag">با رمز</span>` : html`<span class="tag">فقط همین سرور</span>`}${fwdPath(f)}<span class="grow"></span>
          <button class="btn sm" data-act="edit-fwd" data-key="${f.tunnel + "|" + f.ports}">${I.edit(14)} ویرایش</button><button class="btn sm danger icon" data-act="del-forward" data-tunnel="${f.tunnel}" data-ports="${f.ports}" aria-label="حذف پروکسی">${I.trash(15)}</button></div>
        ${["socks5", "http"].map((sc) => html`<div class="code small">${proxyURL(f, sc)}<button class="btn sm ghost icon" data-act="copy" data-text="${proxyURL(f, sc)}" aria-label="کپی">${I.copy(14)}</button></div>`)}
      </div>`)}<p class="help" style="margin:4px 0 0">نام دامنه‌ها روی سرور مقصد پیدا می‌شود. فقط TCP؛ برای SOCKS5 در برنامه‌ها گزینه‌ی «remote DNS» یا socks5h را بزنید.</p></div></div>`;
  }

  // How the ports are listed: in the order the user arranged, or by a column.
  const PORT_SORTS = [["manual", "ترتیب من"], ["port", "شماره پورت"], ["tunnel", "تانل"], ["today", "مصرف امروز"], ["month", "مصرف ماه"]];
  const firstPort = (f) => parseInt(f.ports, 10) || 0;
  function sortedPorts(fs) {
    const by = { port: (a, b) => firstPort(a) - firstPort(b) || a.proto.localeCompare(b.proto),
      tunnel: (a, b) => tlabel(a.tunnel).localeCompare(tlabel(b.tunnel), "fa") || firstPort(a) - firstPort(b),
      today: (a, b) => (b.today.rx + b.today.tx) - (a.today.rx + a.today.tx),
      month: (a, b) => (b.month.rx + b.month.tx) - (a.month.rx + a.month.tx) }[S.portSort];
    return by ? fs.slice().sort(by) : fs;
  }
  // Saves the user's order; proxies keep their own place after the ports.
  async function savePortOrder(list) {
    const rest = S.ov.forwards.filter((f) => !list.includes(f));
    S.ov.forwards = list.concat(rest);
    draw();
    try { await api("POST", "forwards/order", { keys: S.ov.forwards.map((f) => f.proto + "/" + f.ports) }); }
    catch (e) { toast(e.message, "bad"); await refresh(); }
  }
  const moveBtns = (f, i, n) => html`<button type="button" class="btn sm ghost icon" data-act="port-move" data-key="${portKey(f)}" data-dir="-1" ${i ? "" : "disabled"} aria-label="بردن پورت ${f.ports} به بالا" title="بالاتر">${I.up(15)}</button><button type="button" class="btn sm ghost icon" data-act="port-move" data-key="${portKey(f)}" data-dir="1" ${i < n - 1 ? "" : "disabled"} aria-label="بردن پورت ${f.ports} به پایین" title="پایین‌تر">${I.down(15)}</button>`;

  function viewPorts() {
    const all = S.ov.forwards, ps = all.filter((f) => f.mode === "socks"), fs = sortedPorts(all.filter((f) => f.mode !== "socks")), manual = !S.portSort || S.portSort === "manual";
    const proxies = html`<div class="sec-split"><span>پروکسی‌های داخلی</span><small>جدا از پورت‌ها: برای برنامه‌های خود این سرور، نه برای کاربران</small></div>${proxiesPanel(ps)}`;
    if (!fs.length) return html`<div class="panel"><div class="empty">${I.ports(32)}<b>فورواردی تعریف نشده</b>ترافیک ورودی پورت‌های این سرور را از داخل تانل به سرور مقصد بفرستید.<br><button class="btn primary" data-act="add-forward">${I.plus(16)} فوروارد جدید</button></div></div>${proxies}`;
    const rows = fs.map((f, i) => {
      const k = f.tunnel + "|" + f.ports, r = prate(f), month = f.month ? f.month.rx + f.month.tx : 0;
      return html`<tr class="click" data-act="port-chart" data-key="${portKey(f)}" ${manual ? raw(`draggable="true" data-port-drag="${esc(portKey(f))}"`) : ""}>
        ${manual ? html`<td class="pmove"><span class="pgrip" title="برای جابه‌جایی بکشید">${I.grip(15)}</span>${moveBtns(f, i, fs.length)}</td>` : ""}
        <td><b class="ltr">${f.ports}</b><div class="faint small"><span class="ltr">${f.public}</span> · ${f.proto === "both" ? "TCP + UDP" : f.proto.toUpperCase()}</div>${f.plan ? html`<div class="psold">${saleTags(f)}</div>` : ""}</td>
        <td>${tlabel(f.tunnel)} <span class="faint">به</span> ${pname(f.peer)}<div class="faint small"><span class="ltr">${f.target}:${f.to || f.ports}</span></div>${f.backup ? html`<div style="margin-top:4px">${fwdPath(f)}</div>` : ""}</td>
        <td class="hide-sm">${fwdModeLabel(f)}</td>
        <td class="ltr"><span class="dl">↓ ${fmtRate(r.rx)}</span><br><span class="ul">↑ ${fmtRate(r.tx)}</span></td>
        <td class="ltr"><b>${fmtBytes(f.today.rx + f.today.tx)}</b><div class="faint small">↓ ${fmtBytes(f.today.rx)} · ↑ ${fmtBytes(f.today.tx)}</div></td>
        <td class="ltr hide-sm"><b>${fmtBytes(month)}</b><div class="faint small">کل ${fmtBytes(f.rx + f.tx)}</div></td>
        <td class="acts"><button class="btn sm ghost icon" data-act="port-chart" data-key="${portKey(f)}" aria-label="نمودار ترافیک پورت ${f.ports}" title="نمودار ترافیک">${I.chart(15)}</button><button class="btn sm ghost icon ${f.plan ? "on" : ""}" data-act="port-sale" data-key="${portKey(f)}" aria-label="فروش و محدودیت پورت ${f.ports}" title="فروش و محدودیت">${I.tag(15)}</button><button class="btn sm" data-act="edit-fwd" data-key="${k}">${I.edit(14)} ویرایش</button><button class="btn sm danger icon" data-act="del-forward" data-tunnel="${f.tunnel}" data-ports="${f.ports}" aria-label="حذف فوروارد ${f.ports}">${I.trash(15)}</button></td></tr>`;
    });
    const cards = fs.map((f, i) => {
      const k = f.tunnel + "|" + f.ports, r = prate(f), month = f.month ? f.month.rx + f.month.tx : 0;
      return html`<article class="pcard" data-act="port-chart" data-key="${portKey(f)}">
        <div class="pc-h"><b class="ltr pc-port">${f.ports}</b><span class="pc-proto">${f.proto === "both" ? "TCP + UDP" : f.proto.toUpperCase()}</span><span class="grow"></span>${fwdModeLabel(f)}</div>
        <div class="pc-route">${tlabel(f.tunnel)} <span class="faint">به</span> ${pname(f.peer)} <span class="faint">·</span> <span class="ltr faint">${f.target}:${f.to || f.ports}</span></div>
        ${f.backup ? html`<div class="pc-path">${fwdPath(f)}</div>` : ""}
        ${f.plan ? html`<div class="psold">${saleTags(f)}</div>` : ""}
        <div class="pc-live ltr"><span class="dl">↓ ${fmtRate(r.rx)}</span><span class="ul">↑ ${fmtRate(r.tx)}</span></div>
        <div class="pc-stats">
          <div><span>امروز</span><b class="ltr">${fmtBytes(f.today.rx + f.today.tx)}</b><small class="ltr">↓ ${fmtBytes(f.today.rx)} · ↑ ${fmtBytes(f.today.tx)}</small></div>
          <div><span>این ماه</span><b class="ltr">${fmtBytes(month)}</b></div>
          <div><span>از ابتدا</span><b class="ltr">${fmtBytes(f.rx + f.tx)}</b></div>
        </div>
        <div class="pc-acts">${manual ? html`<span class="pc-move">${moveBtns(f, i, fs.length)}</span>` : ""}<span class="grow"></span><button class="btn sm ghost icon" data-act="port-chart" data-key="${portKey(f)}" aria-label="نمودار ترافیک پورت ${f.ports}">${I.chart(16)}</button><button class="btn sm ghost icon ${f.plan ? "on" : ""}" data-act="port-sale" data-key="${portKey(f)}" aria-label="فروش و محدودیت پورت ${f.ports}">${I.tag(16)}</button><button class="btn sm ghost icon" data-act="edit-fwd" data-key="${k}" aria-label="ویرایش ${f.ports}">${I.edit(16)}</button><button class="btn sm ghost icon danger" data-act="del-forward" data-tunnel="${f.tunnel}" data-ports="${f.ports}" aria-label="حذف فوروارد ${f.ports}">${I.trash(16)}</button></div></article>`;
    });
    return html`<div class="panel pports"><header><h2>فورواردها</h2><span class="aside">${fs.length} پورت · ترافیک خود کاربران، پیش از سربار تانل</span></header>
      <div class="psort"><span class="faint small">مرتب‌سازی</span><div class="seg sm" role="radiogroup" aria-label="مرتب‌سازی پورت‌ها">${PORT_SORTS.map(([v, l]) => html`<button type="button" role="radio" aria-checked="${(S.portSort || "manual") === v}" class="${(S.portSort || "manual") === v ? "on" : ""}" data-act="port-sort" data-value="${v}">${l}</button>`)}</div>${manual ? html`<span class="faint small psort-hint">ردیف‌ها را بکشید یا با پیکان‌ها جابه‌جا کنید؛ این ترتیب ذخیره می‌شود.</span>` : ""}</div>
      <div class="pcards">${cards}</div>
      <div class="tbl-wrap pmon-wrap"><table class="t pmon"><thead><tr>${manual ? html`<th class="pmove" aria-label="ترتیب"></th>` : ""}<th>پورت</th><th>تانل و مقصد</th><th class="hide-sm">حالت</th><th>الان</th><th>امروز</th><th class="hide-sm">این ماه</th><th></th></tr></thead><tbody>${rows}</tbody></table></div>
      <p class="help pports-note">روی هر ردیف بزنید تا نمودار ترافیک آن پورت در بازه‌های مختلف باز شود.</p></div>
      ${proxies}`;
  }

  // ---------- speed ----------
  // The page is rendered only when results change. Between renders a frame
  // loop eases the gauge toward the latest sample, so the needle and the
  // trace move continuously although samples arrive five times a second.
  const G = { v: 0, to: 0, phase: "", seq: -1, hist: [], raf: 0, at: 0, sig: "", seen: {}, t0: 0, dur: 10, cap: 1e9 };
  const GR = { cx: 160, cy: 160, R: 120, start: 135, sweep: 270 };
  GR.len = (Math.PI * GR.R * GR.sweep) / 180;
  // Log scale to 1 Gbps, or to 10 Gbps once a measurement passes 1.
  const gfrac = (bps) => Math.max(0, Math.min(1, Math.log10(1 + bps / 1e6) / Math.log10(1 + G.cap / 1e6)));
  // The start of each direction a result leaves out (see probe.Warmup).
  const warmup = (secs) => Math.min(3, secs / 4);
  const isBwPhase = (p) => p === "upload" || p === "download";

  function viewSpeed() {
    const ts = visible(), ps = S.ov.peers.filter((p) => p.paired), sp = S.speed;
    if (!sp.tunnel && ts.length) sp.tunnel = ts[0].name;
    if (!sp.peer && ps.length) sp.peer = ps[0].name;
    const j = S.job, running = j && j.status === "running";
    const warm = warmup(sp.secs);
    const opt = (key, vals, label) => html`<div class="seg sm" role="radiogroup" aria-label="${label}">${vals.map(([v, l]) => html`<button type="button" role="radio" aria-checked="${sp[key] === v}" class="${sp[key] === v ? "on" : ""}" data-act="speed-opt" data-key="${key}" data-value="${v}" ${running ? "disabled" : ""}>${l}</button>`)}</div>`;
    return html`<div class="speed">
      <div class="panel">${gauge(j)}</div>
      <div class="stack">
        <div class="panel"><header><h2>اندازه‌گیری</h2><span class="aside">برای تست تانل، مقایسه و سرعت در ساخت تانل</span></header><div class="in stack">
          <div class="sopts">
            <div class="field"><span class="lbl">مدت هر جهت</span>${opt("secs", [[5, "۵ ثانیه"], [10, "۱۰ ثانیه"], [20, "۲۰ ثانیه"], [30, "۳۰ ثانیه"]], "مدت")}</div>
            <div class="field"><span class="lbl">اتصال هم‌زمان</span>${opt("streams", [[1, "۱"], [4, "۴"], [8, "۸"], [16, "۱۶"], [32, "۳۲"]], "اتصال‌ها")}</div>
          </div>
          <p class="help" style="margin:0">هر جهت ${sp.secs} ثانیه با ${sp.streams} اتصال TCP. ${warm} ثانیه‌ی اول هر جهت در نتیجه حساب نمی‌شود، چون TCP در شروع آهسته بالا می‌آید و روی مسیر دور عدد را خیلی کمتر از ظرفیت نشان می‌داد. چند اتصال مثل «Connection Count» در Bandwidth Test میکروتیک ظرفیت کل مسیر را می‌سنجد؛ ۱ اتصال یعنی سرعت یک دانلود تکی.</p>
        </div></div>
        <div class="panel"><header><h2>تست یک تانل</h2></header><div class="in">
          ${ts.length ? html`<div class="row" style="flex-wrap:nowrap"><select class="input" data-bind="speed.tunnel" aria-label="تانل">${ts.map((t) => html`<option value="${t.name}" ${sp.tunnel === t.name ? "selected" : ""}>${tname(t)}، ${kindTitle(t)} به ${pname(t.peer) || t.far_ip}، حالت ${profileName(t.settings && t.settings.profile)}</option>`)}</select>
            <button class="btn primary" data-act="run-test" ${running ? "disabled" : ""}>${I.play(15)} شروع</button></div>
            <p class="help">پینگ، جیتر و افت؛ بعد آپلود و دانلود، هر کدام ${sp.secs} ثانیه.</p>` : html`<div class="muted">اول یک تانل بسازید.</div>`}
        </div></div>
        <div class="panel"><header><h2>مقایسه‌ی انواع تانل</h2><span class="aside">بهترین را برای این مسیر پیدا کنید</span></header><div class="in">
          ${ps.length ? html`<div class="field"><label for="bench-peer">سرور مقصد</label><select id="bench-peer" class="input" data-bind="speed.peer">${ps.map((p) => html`<option value="${p.name}" ${sp.peer === p.name ? "selected" : ""}>${plabel(p)} (${p.address})</option>`)}</select></div>
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
    for (const mbps of G.cap > 1e9 ? [0, 10, 100, 500, 1000, 2500, 5000, 10000] : [0, 1, 10, 50, 100, 250, 500, 1000]) {
      const a = start + sweep * gfrac(mbps * 1e6), [x0, y0] = pt(a, R + 14), [x1, y1] = pt(a, R + 20), [tx, ty] = pt(a, R + 34);
      ticks += `<line class="tick" x1="${x0}" y1="${y0}" x2="${x1}" y2="${y1}"/><text x="${tx}" y="${ty + 4}" text-anchor="middle">${mbps >= 1000 ? mbps / 1000 + "G" : mbps}</text>`;
    }
    const running = j && j.status === "running", res = j && j.kind === "test" && j.results && j.results[0];
    if (!running) { G.to = res && !res.error ? res.down_bps : 0; if (!G.raf) G.v = G.to; }
    const [big, unit, what] = gaugeText(j);
    const ph = (k, label) => html`<div class="phase ${running && G.phase === k ? "now" : ""}" data-ph="${k}"><b>${phaseValue(j, k)}</b>${label}</div>`;
    return html`<div class="gauge-box ${running ? "running" : ""}">
      ${raw(`<svg class="gauge" viewBox="-24 0 368 290" role="img" aria-label="سرعت لحظه‌ای">${ticks}
        <path class="track" d="${arc(start, start + sweep)}"/>
        <path id="g-fill" class="fill ${G.phase === "upload" ? "tx" : ""}" d="${arc(start, start + sweep)}" stroke-dasharray="${len.toFixed(1)}" stroke-dashoffset="${(len * (1 - gfrac(G.v))).toFixed(1)}"/>
        <text id="g-big" class="big" x="${cx}" y="${cy + 12}" text-anchor="middle" direction="ltr">${esc(big)}</text>
        <text id="g-unit" class="unit" x="${cx}" y="${cy + 40}" text-anchor="middle" direction="ltr">${esc(unit)}</text>
        <text id="g-what" class="what" x="${cx}" y="${cy + 92}" text-anchor="middle">${esc(what)}</text></svg>`)}
      <div class="gtrace-box">${raw(`<svg class="gtrace" viewBox="0 0 300 56" preserveAspectRatio="none" aria-hidden="true"><rect id="g-warm" class="warm" x="0" y="0" height="56" width="${((warmup(G.dur) / G.dur) * 300).toFixed(1)}" style="display:${running && isBwPhase(G.phase) ? "" : "none"}"/><path id="g-area" class="area ${G.phase === "upload" ? "tx" : ""}" d="${sparkPath(true)}"/><path id="g-line" class="line ${G.phase === "upload" ? "tx" : ""}" d="${sparkPath(false)}"/></svg>`)}
        ${running && isBwPhase(G.phase) ? html`<span class="wl" style="left:0;width:${((warmup(G.dur) / G.dur) * 100).toFixed(1)}%" title="این بخش در نتیجه حساب نمی‌شود: TCP در شروع هر اتصال آهسته بالا می‌آید">گرم شدن</span>` : ""}</div>
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
  // The trace: this direction so far on a fixed time axis (the whole run),
  // so a slow start or a drop shows where it happened.
  function sparkPath(area) {
    const h = G.hist;
    if (h.length < 2) return "";
    const max = Math.max(1, ...h.map((p) => p[1])), dur = Math.max(1, G.dur);
    const x = (t) => Math.min(300, (t / dur) * 300).toFixed(1), y = (v) => (54 - (v / max) * 46).toFixed(1);
    let d = h.map(([t, v], i) => `${i ? "L" : "M"}${x(t)} ${y(v)}`).join(" ");
    if (area) d += ` L${x(h[h.length - 1][0])} 56 L${x(h[0][0])} 56 Z`;
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
    const wr = $("#g-warm"); if (wr) wr.style.display = j && j.status === "running" && isBwPhase(G.phase) ? "" : "none";
  }

  const ST = {
    pending: ["در صف", "wait"], checking: ["بررسی اتصال…", "busy"], passed: ["وصل شد، منتظر تست سرعت", "ok"],
    measuring: ["اندازه‌گیری سرعت…", "busy"], done: ["", "done"], failed: ["وصل نشد", "bad"], skipped: ["تست نشد", "wait"],
  };
  // A failure in plain words; the exact error stays in the tooltip.
  function errText(e) {
    e = e || "";
    if (/no traffic passes/.test(e)) return "بسته‌ها از این مسیر رد نشدند: مسیر یا فایروال ارائه‌دهنده این نوع ترافیک را نمی‌گذارد.";
    const m = e.match(/skipped: tunnel ([\w-]+)/);
    if (m) return `تست نشد: تانل ${m[1]} همین دو آدرس را با همین نوع گرفته و کرنل دومی را نمی‌پذیرد.`;
    if (/already runs .* kernel allows only one/.test(e)) return "بین این دو سرور تانلی از همین نوع هست و کرنل دومی را نمی‌پذیرد؛ GRE، GENEVE یا نوع دیگری انتخاب کنید.";
    if (/has no .* for IPsec/.test(e)) return "کرنل یکی از سرورها این الگوریتم رمزنگاری را برای IPsec ندارد؛ برای این تانل AES-GCM را انتخاب کنید.";
    if (/file exists/.test(e)) return "با تانل دیگری بین همین دو آدرس تداخل دارد.";
    if (/l2tp|No such file or directory/i.test(e) && /l2tp/i.test(e)) return "کرنل یکی از سرورها L2TP ندارد.";
    if (/not supported|unknown (link )?kind|Operation not supported/i.test(e)) return "کرنل یکی از سرورها این نوع تانل را ندارد.";
    if (/timeout|deadline/i.test(e)) return "سرور مقصد به‌موقع جواب نداد.";
    if (/refused/.test(e)) return "اتصال رد شد: پورت روی سرور مقابل بسته است.";
    if (/address already in use|already used by another program|port is in use/i.test(e)) return "پورت روی یکی از دو سرور اشغال است و پورت آزاد دیگری هم پیدا نشد.";
    return e;
  }
  const rstatus = (r) => r.status || (r.error ? "failed" : "done");

  function jobResults(j) {
    const all = j.results || [], running = j.status === "running";
    const done = all.filter((r) => rstatus(r) === "done");
    const best = j.kind === "bench" && !running ? done.find((r) => r.name !== "direct") : null;
    // For games: the lowest ping and jitter without loss, of the kinds that
    // carry UDP in the kernel (the results come sorted by speed).
    const game = best ? done.filter((r) => r.mode && GAME_MODES.includes(r.mode) && !r.loss).sort((a, b) => (a.ping_ms + a.jitter_ms) - (b.ping_ms + b.jitter_ms))[0] : null;
    const max = Math.max(1, ...done.map((r) => Math.max(r.down_bps, r.up_bps)));
    const st = { running: "در حال اجرا", done: "تمام شد", failed: "ناموفق", cancelled: "متوقف شد" }[j.status];
    const n = (s) => all.filter((r) => rstatus(r) === s).length;
    const finished = n("done") + n("failed") + n("skipped"), reached = all.length - n("pending") - n("checking") - n("failed") - n("skipped");
    const fresh = (r) => { const s = rstatus(r), was = G.seen[r.name]; G.seen[r.name] = s; return was !== undefined && was !== s; };
    const row = (r) => {
      const s = rstatus(r), [label, cls] = ST[s] || ST.done;
      const meta = s === "failed" || s === "skipped" ? html`<span class="err" title="${r.error || ""}">${errText(r.error)}</span>`
        : r.ping_ms ? html`پینگ ${ms(r.ping_ms)}، جیتر ${ms(r.jitter_ms)}، افت ${r.loss.toFixed(0)}٪${r.mtu ? `، MTU ${r.mtu}` : ""}` : "";
      const moved = r.moved_from ? html`<div class="small moved">پورت <span class="ltr">${r.moved_from}</span> اشغال بود؛ روی پورت <span class="ltr">${r.port}</span> تست شد.</div>` : "";
      const bars = s === "done"
        ? html`<div class="bars"><div class="bar" style="width:${((r.down_bps / max) * 100).toFixed(1)}%" title="دانلود ${fmtRate(r.down_bps)}"></div><div class="bar tx" style="width:${((r.up_bps / max) * 100).toFixed(1)}%" title="آپلود ${fmtRate(r.up_bps)}"></div></div>`
        : html`<div class="bars"><div class="lane ${cls}"><i></i></div></div>`;
      const right = s === "done" ? html`${fmtRate(r.down_bps)}<br><span class="faint">${fmtRate(r.up_bps)}</span>` : html`<span class="st ${cls}">${label}</span>`;
      return html`<div class="r ${best && r.name === best.name ? "best" : ""} ${fresh(r) ? "fresh" : ""}" data-st="${s}">
        <div><b class="nm"><i class="sd ${cls}"></i>${resultTitle(r)}</b><div class="faint small">${meta}</div>${moved}</div>
        ${bars}<div class="small ltr" style="text-align:end">${right}</div></div>`;
    };
    return html`<div class="panel"><header><h2>${j.kind === "bench" ? "مقایسه با " : "تست تانل "}${j.target}</h2><span class="aside">${st}</span></header><div class="in stack">
      ${j.error ? html`<div class="note bad">${j.error}</div>` : ""}
      ${j.kind === "bench" && all.length ? html`<div class="progress"><div class="pl"><span>${running ? (n("checking") ? "بررسی اتصال همه با هم" : n("measuring") ? "اندازه‌گیری سرعت" : "در حال آماده‌سازی") : "نتیجه"}</span>
          <span class="faint">${reached} از ${all.length} وصل شد · ${n("done")} اندازه‌گیری شد</span></div>
        <div class="pbar"><i style="transform:scaleX(${(finished / all.length).toFixed(3)})"></i></div></div>` : ""}
      ${best ? html`<div class="note ok">سریع‌ترین گزینه تا <b>${j.target}</b>: <b>${resultTitle(best)}</b>، دانلود ${fmtRate(best.down_bps)} و پینگ ${ms(best.ping_ms)}.
        ${best.mode ? html` <button class="btn sm primary" data-act="wizard" data-peer="${j.target}" data-mode="${best.mode}" data-port="${best.port || ""}">ساخت همین تانل</button>` : ""}</div>` : ""}
      ${game ? html`<div class="note">برای بازی تا <b>${j.target}</b>: <b>${resultTitle(game)}</b>، پینگ ${ms(game.ping_ms)} و جیتر ${ms(game.jitter_ms)} بدون افت.
        <button class="btn sm" data-act="wizard" data-peer="${j.target}" data-mode="${game.mode}" data-port="${game.port || ""}" data-gaming="1">ساخت برای گیمینگ</button></div>` : ""}
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
      S.job = await api("POST", "jobs", { kind, target, only, seconds: S.speed.secs, streams: S.speed.streams });
      S.pv = {}; G.v = G.to = 0; G.phase = ""; G.seq = -1; G.hist = []; G.sig = ""; G.seen = {}; G.dur = S.speed.secs; G.cap = 1e9;
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
    if (lv.seq !== G.seq) { G.seq = lv.seq; G.hist = []; S.pv = {}; G.to = 0; G.t0 = performance.now(); }
    if (lv.phase) {
      if (lv.phase !== G.phase) { G.phase = lv.phase; G.hist = []; G.t0 = performance.now(); G.sig = ""; }
      if (lv.value) {
        S.pv[lv.phase] = lv.value;
        if (isBwPhase(lv.phase)) {
          G.to = lv.value; G.hist.push([(performance.now() - G.t0) / 1000, lv.value]);
          if (G.hist.length > 400) G.hist.shift();
          if (lv.value > 0.95e9 && G.cap <= 1e9) { G.cap = 1e10; G.sig = ""; }
        }
      }
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
    else {
      toast(S.job.status === "done" ? "تست تمام شد" : S.job.error || "تست متوقف شد", S.job.status === "done" ? "ok" : "bad");
      rememberTest(S.job);
    }
  }

  // Each tunnel's last speed test (the slower direction, Mbit/s), kept in the
  // browser for the smart queue's suggestion.
  function lastTests() { try { return JSON.parse(store("nt-lasttest") || "{}"); } catch { return {}; } }
  function rememberTest(j) {
    const r = j.kind === "test" && j.status === "done" && j.results && j.results[0];
    if (!r || r.error || !r.down_bps || !r.up_bps) return;
    const m = lastTests(); m[j.target] = Math.round(Math.min(r.down_bps, r.up_bps) / 1e6);
    store("nt-lasttest", JSON.stringify(m));
  }
  // 90% of the last test, under which the queue sits in this server.
  const queueGuess = (t) => { const v = lastTests()[t.name]; return v > 1 ? Math.floor(v * 0.9) : 0; };

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
  const SECS = [["node", "این سرور"], ["access", "دسترسی پنل"], ["defaults", "پیش‌فرض تانل‌ها"], ["net", "شبکه و سرعت"], ["guard", "فایروال"], ["telegram", "تلگرام"], ["fetch", "دانلودهای سرور"], ["api", "API و وب‌هوک"], ["history", "تاریخچه‌ی تغییرات"], ["traffic", "ترافیک"], ["maint", "پشتیبان و نگهداری"], ["about", "درباره"]];

  function viewSettings() {
    const pane = { node: setNode, access: setAccess, defaults: setDefaults, net: setNet, guard: setGuard, telegram: setTelegram, fetch: setFetch, api: setAPI, history: setHistory, traffic: setTraffic, maint: setMaint, about: setAbout }[S.sec] || setNode;
    return html`<div class="settings">
      <nav class="snav" aria-label="بخش‌های تنظیمات">${SECS.map(([k, l]) => html`<button class="${S.sec === k ? "on" : ""}" data-act="sec" data-sec="${k}">${l}</button>`)}</nav>
      <div class="spane" data-key="sec-${S.sec}">${pane()}</div></div>`;
  }

  function setNode() {
    const ov = S.ov;
    return html`<div class="panel"><header><h2>این سرور</h2></header><div class="in"><form data-form="node" class="stack">
      <div class="grid2"><div class="field"><label for="s-label">نام نمایشی</label><input id="s-label" class="input" name="label" dir="auto" maxlength="40" value="${ov.node.label || ""}" placeholder="${ov.node.name}"><span class="help">نامی که در پنل و منوها دیده می‌شود؛ هر زبانی، هر وقت. تانل‌ها را تغییر نمی‌دهد.</span></div>
      <div class="field"><label for="s-name">شناسه‌ی سرور</label><input id="s-name" class="input ltr" name="name" value="${ov.node.name}"><span class="help">در دستورهای اتصال و برای سرورهای دیگر. حروف کوچک انگلیسی، عدد و خط تیره؛ تا وقتی تانل دارد عوض نمی‌شود.</span></div>
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
        <div class="form-foot"><button class="btn primary" type="submit">تغییر رمز</button></div></form></div></div>
      ${certPanel()}`;
  }

  // The panel's domain and certificate.
  function certPanel() {
    const c = S.cert;
    if (!c) { loadCert(); return html`<div class="panel"><header><h2>دامنه و SSL</h2></header><div class="in muted">در حال خواندن…</div></div>`; }
    const e = S.ce || (S.ce = { mode: c.mode === "custom" ? "custom" : "acme", domain: c.domain || "", cert: "", key: "", busy: false, check: null });
    const exp = c.expires ? new Date(c.expires) : null, days = exp ? Math.floor((exp - Date.now()) / 864e5) : 0;
    const now = c.mode === "self"
      ? html`<span class="state"><i class="dot warn"></i>گواهی خودامضا؛ مرورگر هشدار می‌دهد</span>`
      : html`<span class="state"><i class="dot ${c.error ? "bad" : days < 15 ? "warn" : "ok"}"></i><span class="ltr">${c.domain}</span></span>`;
    const ck = e.check;
    const checkNote = ck && ck.checked ? (ck.here
      ? html`<div class="note ok">دامنه به همین سرور (<span class="ltr">${ck.address}</span>) اشاره می‌کند.</div>`
      : html`<div class="note warn">${ck.resolves && ck.resolves.length ? html`دامنه به <span class="ltr">${ck.resolves.join("، ")}</span> اشاره می‌کند، نه به IP این سرور (<span class="ltr">${ck.address}</span>).` : "دامنه پیدا نشد."} اول رکورد A دامنه را روی IP همین سرور بگذارید. اگر پشت Cloudflare است، Let's Encrypt معمولاً باز هم کار می‌کند.</div>`) : "";
    return html`<div class="panel"><header><h2>دامنه و SSL</h2><span class="aside">${now}</span></header><div class="in stack">
      <p class="help" style="margin:0">با یک دامنه و گواهی معتبر، پنل بدون هشدار مرورگر باز می‌شود. پورت و آدرس مخفی پنل همان می‌ماند.</p>
      ${c.mode !== "self" ? html`<dl class="kv"><dt>دامنه</dt><dd class="ltr">${c.domain}</dd><dt>صادرکننده</dt><dd>${c.mode === "acme" ? "Let's Encrypt، تمدید خودکار" : "گواهی خودتان"}${c.issuer ? html` <span class="faint small ltr">(${c.issuer})</span>` : ""}</dd>
        ${exp ? html`<dt>اعتبار تا</dt><dd>${exp.toLocaleDateString("fa-IR")} <span class="faint small">(${days} روز دیگر)</span></dd>` : ""}
        ${c.error ? html`<dt>خطا</dt><dd class="ltr small">${c.error}</dd>` : ""}<dt>آدرس پنل</dt><dd><a class="ltr" href="${S.ov.panel.url}">${S.ov.panel.url}</a></dd></dl>` : ""}
      <div class="seg">${[["acme", "Let's Encrypt (رایگان)"], ["custom", "گواهی خودم"]].map(([v, l]) => html`<button type="button" class="${e.mode === v ? "on" : ""}" data-act="set" data-path="ce.mode" data-value="${v}">${l}</button>`)}</div>
      <div class="field"><label for="c-domain">دامنه</label><div class="row" style="flex-wrap:nowrap"><input id="c-domain" class="input ltr" data-bind="ce.domain" value="${e.domain}" placeholder="panel.example.com" style="max-width:320px"><button class="btn" data-act="cert-check" ${e.busy ? "disabled" : ""}>بررسی</button></div></div>
      ${checkNote}
      ${e.mode === "acme"
        ? html`<p class="help" style="margin:0">Let's Encrypt دامنه را روی <b>پورت 80</b> همین سرور بررسی می‌کند؛ این پورت برای یک دقیقه باید آزاد و از بیرون باز باشد. ${c.port80 ? "" : html`<b>الان پورت 80 را برنامه‌ی دیگری گرفته</b> (مثلاً nginx یا Xray)؛ برای گرفتن گواهی چند لحظه متوقفش کنید.`} گواهی هر ۹۰ روز خودکار تمدید می‌شود (وقتی ۳۰ روز مانده)؛ تمدید هم پورت 80 را لازم دارد.</p>
          <div class="form-foot"><button class="btn primary" data-act="cert-issue" ${e.busy ? "disabled" : ""}>${I.lock(16)} ${e.busy ? "در حال گرفتن گواهی…" : "گرفتن گواهی"}</button>${c.mode !== "self" ? html`<span class="grow"></span><button class="btn ghost" data-act="cert-self">برگشت به گواهی خودامضا</button>` : ""}</div>`
        : html`<div class="field"><label for="c-crt">گواهی (fullchain، PEM)</label><textarea id="c-crt" class="input ltr mono" rows="5" data-bind="ce.cert" placeholder="-----BEGIN CERTIFICATE-----">${e.cert}</textarea></div>
          <div class="field"><label for="c-key">کلید خصوصی (PEM)</label><textarea id="c-key" class="input ltr mono" rows="4" data-bind="ce.key" placeholder="-----BEGIN PRIVATE KEY-----">${e.key}</textarea><span class="help">مثلاً گواهی Origin کلادفلر یا گواهی‌ای که با acme.sh گرفته‌اید. کلید فقط روی همین سرور ذخیره می‌شود.</span></div>
          <div class="form-foot"><button class="btn primary" data-act="cert-custom" ${e.busy ? "disabled" : ""}>${I.lock(16)} ذخیره‌ی گواهی</button>${c.mode !== "self" ? html`<span class="grow"></span><button class="btn ghost" data-act="cert-self">برگشت به گواهی خودامضا</button>` : ""}</div>`}
    </div></div>`;
  }

  async function loadCert() {
    try { S.cert = await api("GET", "settings/cert"); if (S.route === "settings" && S.sec === "access") draw(); } catch (e) { toast(e.message, "bad"); }
  }

  // This server's downloads through a tunnel.
  function setFetch() {
    const v = S.fetch;
    if (!v) { loadFetch(); return html`<div class="panel"><div class="in muted">در حال خواندن…</div></div>`; }
    const f = S.fe || (S.fe = Object.assign({}, v.fetch, { groups: (v.fetch.groups || []).slice(), domains: (v.fetch.domains || []).join("\n"), all: v.fetch.all || !v.terminal, test: "github.com", result: null, testing: false }));
    const on = v.fetch.on, st = v.status;
    const state = on && st.running ? html`<span class="state"><i class="dot ok"></i>روشن، از ${v.fetch.tunnel}</span>` : on ? html`<span class="state"><i class="dot warn"></i>روشن، هنوز راه نیفتاده</span>` : html`<span class="state"><i class="dot"></i>خاموش</span>`;
    const via = { tunnel: ["از تانل", "lock"], direct: ["مستقیم", ""], failed: ["ناموفق", "warn"] };
    const probe = (p, l) => html`<div><div class="k">${l}</div><div class="v">${p.ok ? html`<span class="state"><i class="dot ok"></i>${p.ms} ms</span>` : html`<span class="state"><i class="dot bad"></i>وصل نشد</span><div class="faint small ltr">${p.error || ""}</div>`}</div></div>`;
    return html`<div class="panel"><header><h2>دانلودهای این سرور از تانل</h2><span class="aside">${state}</span></header><div class="in stack">
      <p class="help" style="margin:0">وقتی اینترنت مستقیم سرور قطع یا کند است، یا سایتی مثل GitHub فیلتر شده، دانلود از سایت‌هایی که انتخاب می‌کنید (نصب اسکریپت، apt، git clone، pip و <span class="ltr">neotunnel update</span>) از تانل می‌رود و از سرور خارج به اینترنت وصل می‌شود. اسم سایت هم همان‌جا پیدا می‌شود، پس DNS خراب این سرور مشکلی ایجاد نمی‌کند. بقیه‌ی ترافیک، از جمله ترافیک کاربرها، مثل قبل مستقیم می‌رود.</p>
      <label class="switch"><input type="checkbox" data-act="fe-check" data-key="on" ${f.on ? "checked" : ""}>روشن</label>
      ${v.tunnels.length ? html`<div class="field"><label for="f-tun">از کدام تانل</label><select id="f-tun" class="input" data-bind="fe.tunnel" style="max-width:320px">${v.tunnels.map((t) => html`<option value="${t}" ${f.tunnel === t ? "selected" : ""}>${t}</option>`)}</select><span class="help">ترافیک از سرور آن طرف این تانل به اینترنت می‌رود. فقط تانل‌هایی که آن طرفشان NeoTunnel است.</span></div>`
        : html`<div class="note warn">تانلی به یک سرور NeoTunnel ندارید؛ اول یک تانل به سرور خارج بسازید.</div>`}
      <div class="field"><span class="lbl">سایت‌ها</span><div class="checks">${v.groups.map((g) => html`<label class="switch"><input type="checkbox" data-act="fe-group" value="${g.name}" ${f.groups.includes(g.name) ? "checked" : ""}>${g.title} <span class="faint small ltr">${g.domains.join(", ")}</span></label>`)}</div></div>
      <div class="field"><label for="f-dom">سایت‌های دیگر</label><textarea id="f-dom" class="input ltr" rows="3" data-bind="fe.domains" placeholder="download.docker.com&#10;example.org">${f.domains}</textarea><span class="help">هر خط یک دامنه؛ زیردامنه‌هایش هم حساب می‌شوند.</span></div>
      <div class="field"><span class="lbl">کدام برنامه‌ها</span><div class="seg">${[["", "فقط دستورهای ترمینال و SSH"], ["1", "همه‌ی برنامه‌های سرور"]].map(([val, l]) => html`<button type="button" class="${(f.all ? "1" : "") === val ? "on" : ""}" data-act="set" data-path="fe.all" data-value="${val}" ${val === "" && !v.terminal ? "disabled" : ""}>${l}</button>`)}</div>
        <span class="help">${f.all ? "هر برنامه‌ای روی سرور که به این سایت‌ها وصل شود از تانل می‌رود، از جمله سرویس‌ها (مثلاً Docker و به‌روزرسانی خودکار apt)." : "فقط چیزهایی که خودتان در ترمینال یا SSH اجرا می‌کنید. سرویس‌هایی مثل Xray و پنل‌ها دست نمی‌خورند. پیشنهادی."}${v.terminal ? "" : html` <b>این سرور دستورهای ترمینال را جدا تشخیص نمی‌دهد</b> (cgroup v2 ندارد)، پس فقط «همه‌ی برنامه‌ها» ممکن است.`}</span></div>
      <label class="switch"><input type="checkbox" data-act="fe-check" data-key="strict" ${f.strict ? "checked" : ""}>اگر تانل قطع بود، این سایت‌ها مستقیم هم نروند</label>
      <div class="form-foot"><button class="btn primary" data-act="save-fetch">ذخیره</button></div>
    </div></div>
    <div class="panel"><header><h2>آزمایش و وضعیت</h2></header><div class="in stack">
      <div class="row" style="flex-wrap:nowrap"><input class="input ltr" data-bind="fe.test" value="${f.test}" aria-label="سایت" style="max-width:260px"><button class="btn" data-act="fetch-test" ${f.testing ? "disabled" : ""}>${I.play(15)} ${f.testing ? "در حال آزمایش…" : "آزمایش"}</button></div>
      ${f.result ? html`<div class="grid2">${probe(f.result.tunnel, "از تانل")}${probe(f.result.direct, "مستقیم")}</div>` : html`<p class="help" style="margin:0">اتصال HTTPS به سایت را یک بار از تانل و یک بار مستقیم امتحان می‌کند. آزمایش تانل فقط وقتی کار می‌کند که این بخش روشن و ذخیره شده باشد.</p>`}
      <dl class="kv"><dt>از تانل</dt><dd>${st.tunnel}</dd><dt>مستقیم</dt><dd>${st.direct}</dd><dt>ناموفق</dt><dd>${st.failed}</dd></dl>
      ${st.recent.length ? html`<table class="t"><thead><tr><th>سایت</th><th>مسیر</th><th>زمان</th></tr></thead><tbody>${st.recent.map((r) => html`<tr><td class="ltr small" style="text-align:start">${r.host}</td><td><span class="tag ${via[r.via][1]}">${via[r.via][0]}</span>${r.error ? html` <span class="faint small ltr">${r.error}</span>` : ""}</td><td class="small">${ago(r.at / 1000)}</td></tr>`)}</tbody></table>` : html`<div class="muted">هنوز اتصالی نبوده.</div>`}
    </div></div>`;
  }

  // The firewall (package guard): what stays open on the public interface.
  const SCOPES = [["all", "همه"], ["iran", "فقط ایران"], ["closed", "بسته"]];
  const isXray = (p) => /^(xray|v2ray|sing-box)/i.test(p || "");
  function setGuard() {
    const v = S.gv;
    if (!v) { loadGuard(); return html`<div class="panel"><div class="in muted">در حال خواندن…</div></div>`; }
    const g = S.gd || (S.gd = Object.assign({}, v.settings, { ports: Object.assign({}, v.settings.ports || {}), allow: (v.settings.allow || []).join("\n"), block: (v.settings.block || []).join("\n") }));
    const st = v.status, on = v.settings.on, geo = st.geo || {};
    const state = on && st.confirm_by ? html`<span class="state"><i class="dot warn"></i>در انتظار تأیید</span>`
      : on ? html`<span class="state"><i class="dot ok"></i>روشن · ${st.dropped.toLocaleString("en")} بسته رد شد</span>`
      : html`<span class="state"><i class="dot"></i>خاموش</span>`;
    const auto = (p) => (g.config_iran && (p.forward || isXray(p.process)) ? "iran" : "all");
    const what = (p) => [p.forward ? "فوروارد" : "", p.process || ""].filter(Boolean).join(" · ") || "-";
    const label = (sc) => SCOPES.find(([k]) => k === sc)[1];
    const rows = st.ports.map((p) => {
      const k = p.proto + "/" + p.port, cur = g.ports[k] || "auto";
      return html`<tr><td class="ltr" style="text-align:start">${p.port}/${p.proto}</td><td class="small">${what(p)}</td><td><select class="input sm" data-act="gd-port" data-key="${k}" aria-label="دسترسی پورت ${p.port}"><option value="auto" ${cur === "auto" ? "selected" : ""}>خودکار (${label(auto(p))})</option>${SCOPES.map(([val, l]) => html`<option value="${val}" ${cur === val ? "selected" : ""}>${l}</option>`)}</select></td></tr>`;
    });
    return html`<div class="panel"><header><h2>فایروال</h2><span class="aside">${state}</span></header><div class="in stack">
      <p class="help" style="margin:0">همه‌چیز روی کارت شبکه‌ی عمومی (<span class="ltr">${(st.ifaces || []).join(", ") || "-"}</span>) بسته می‌شود، جز آنچه این سرور واقعاً استفاده می‌کند. پورت‌هایی که برنامه‌ها روی آن‌ها گوش می‌دهند هر چند ثانیه خودکار پیدا می‌شوند: اینباند تازه‌ی 3x-ui روی پورت تصادفی خودش باز می‌شود و پورتی که کسی رویش نیست بسته می‌ماند. تانل‌ها و ترافیک داخلشان دست نمی‌خورند.</p>
      <label class="switch"><input type="checkbox" data-act="gd-check" data-key="on" ${g.on ? "checked" : ""}>فایروال روشن</label>
      <div class="note">همیشه باز است و بسته نمی‌شود: SSH (پورت <span class="ltr">${(st.ssh || []).join(", ")}</span>)، پنل (پورت <span class="ltr">${st.panel || "-"}</span>)، و همه‌ی ترافیک از ${st.peers} آدرس سرورهای مقصد و تانل‌ها. اگر بعد از روشن کردن، پنل تا ۶۰ ثانیه جواب ندهد، فایروال خودش خاموش می‌شود. از ترمینال: <code class="ltr">sudo neotunnel guard off</code></div>
      <label class="switch"><input type="checkbox" data-act="gd-check" data-key="config_iran" ${g.config_iran ? "checked" : ""}>پورت‌های کانفیگ (فورواردها و اینباندهای Xray) فقط از ایران (<span class="ltr">geoip:ir</span>)</label>
      <div class="field"><span class="lbl">پینگ (ICMP)</span><div class="seg">${[["closed", "بسته"], ["iran", "فقط ایران"], ["open", "باز"]].map(([val, l]) => html`<button type="button" class="${(g.icmp || "closed") === val ? "on" : ""}" data-act="set" data-path="gd.icmp" data-value="${val}">${l}</button>`)}</div><span class="help">سرورهای مقصد همیشه جواب می‌گیرند. پیام‌هایی که یافتن MTU مسیر به آن‌ها نیاز دارد هم همیشه رد می‌شوند.</span></div>
      <div class="form-foot"><button class="btn primary" data-act="save-guard">ذخیره</button><button class="btn ghost" data-act="guard-refresh">تازه‌سازی</button></div>
    </div></div>
    <div class="panel"><header><h2>پورت‌های باز</h2><span class="aside">${st.ports.length} پورت</span></header><div class="in">
      ${st.ports.length ? html`<div class="tbl-wrap"><table class="t"><thead><tr><th>پورت</th><th>چه چیزی</th><th>چه کسی وصل شود</th></tr></thead><tbody>${rows}</tbody></table></div>` : html`<div class="muted">برنامه‌ای روی پورت عمومی گوش نمی‌دهد.</div>`}
      <p class="help" style="margin:10px 0 0">«خودکار» یعنی فورواردها و اینباندهای Xray طبق کلید «فقط از ایران» و بقیه برای همه باز. پورت‌های خود NeoTunnel فقط برای سرورهای مقصد بازند و اینجا نیستند.</p>
    </div></div>
    <div class="panel"><header><h2>فهرست آدرس‌ها</h2></header><div class="in stack">
      <div class="grid2"><div class="field"><label for="g-allow">همیشه مجاز</label><textarea id="g-allow" class="input ltr" rows="4" data-bind="gd.allow" placeholder="198.51.100.7&#10;203.0.113.0/24&#10;geoip:ir">${g.allow}</textarea><span class="help">به همه‌ی پورت‌ها دسترسی دارند.</span></div>
      <div class="field"><label for="g-block">همیشه مسدود</label><textarea id="g-block" class="input ltr" rows="4" data-bind="gd.block" placeholder="192.0.2.0/24">${g.block}</textarea><span class="help">جز SSH و پنل، به هیچ‌جا دسترسی ندارند.</span></div></div>
      <span class="help">هر خط یک IP، یک رنج (CIDR) یا <span class="ltr">geoip:ir</span>.</span>
      <div class="form-foot"><button class="btn primary" data-act="save-guard">ذخیره</button></div>
    </div></div>
    <div class="panel"><header><h2>فهرست IP ایران</h2><span class="aside">${geo.source === "downloaded" ? "به‌روزشده " + F.dm.format(new Date(geo.updated * 1000)) : "نسخه‌ی همراه برنامه"}</span></header><div class="in"><div class="row">
      <span class="grow">${(geo.ranges || 0).toLocaleString("en")} رنج IPv4 و IPv6، همان داده‌ی <span class="ltr">geoip:ir</span> در Xray. هفته‌ای یک بار خودکار به‌روز می‌شود.</span>
      <button class="btn sm" data-act="guard-geo" ${S.gGeo ? "disabled" : ""}>${S.gGeo ? "در حال دانلود…" : "به‌روزرسانی الان"}</button></div></div></div>`;
  }

  async function loadGuard() {
    try { S.gv = await api("GET", "guard"); if (S.route === "settings" && S.sec === "guard") draw(); } catch (e) { toast(e.message, "bad"); }
  }

  // The Telegram bot (package telegram): alerts, reports and port lookups.
  const TG_ALERTS = [["down", "قطعی یا افت شدید تانل"], ["recover", "سالم شدن دوباره"], ["restart", "ری‌استارت خودکار"], ["failover", "رفتن پورت‌ها به تانل پشتیبان"], ["cipher", "عوض شدن خودکار رمزنگاری"], ["quota", "تمام شدن حجم ماهانه"], ["port", "بسته و باز شدن پورت‌های فروخته"], ["upgrade", "ارتقای خودکار سرورهای مقابل"], ["guard", "خاموش شدن خودکار فایروال"]];
  const TG_EVERY = [["hourly", "هر ساعت"], ["daily", "هر روز"], ["weekly", "هر هفته (شنبه)"]];
  function setTelegram() {
    const v = S.tv;
    if (!v) { loadTelegram(); return html`<div class="panel"><div class="in muted">در حال خواندن…</div></div>`; }
    const s = v.settings, st = v.status;
    const known = (u) => v.proxies.some((p) => p.url === u);
    const d = S.td || (S.td = { on: !!s.on, token: "", route: !s.proxy ? "" : known(s.proxy) ? s.proxy : "custom", proxy: s.proxy || "", admins: (s.admins || []).join("\n"), mute: (s.mute || []).slice(), users: !!s.users, user_ports: s.user_ports || "", reports: (s.reports || []).map((r) => Object.assign({}, r)) });
    const state = st.running ? html`<span class="state"><i class="dot ok"></i>فعال · <span class="ltr">@${st.username}</span></span>`
      : st.error ? html`<span class="state"><i class="dot bad"></i>خطا</span>`
      : s.on ? html`<span class="state"><i class="dot warn"></i>در حال اتصال…</span>`
      : html`<span class="state"><i class="dot"></i>خاموش</span>`;
    const link = s.link_code && st.username ? `https://t.me/${st.username}?start=${s.link_code}` : "";
    const reports = d.reports.map((r, i) => html`<div class="tg-rep" data-key="rep-${i}">
        <select class="input sm" data-bind="td.reports.${i}.every" aria-label="دوره">${TG_EVERY.map(([k, l]) => html`<option value="${k}" ${r.every === k ? "selected" : ""}>${l}</option>`)}</select>
        ${r.every === "hourly" ? html`<input class="input sm ltr" data-bind="td.reports.${i}.at" value="${r.at || ""}" placeholder="00:00" title="دقیقه‌ی هر ساعت؛ فقط دقیقه به کار می‌رود" style="width:84px">`
          : html`<input class="input sm ltr" type="time" data-bind="td.reports.${i}.at" value="${r.at || "09:00"}" aria-label="ساعت (به وقت تهران)" style="width:132px">`}
        <input class="input sm ltr grow" data-bind="td.reports.${i}.ports" value="${r.ports || ""}" placeholder="همه‌ی پورت‌ها" aria-label="پورت‌ها">
        <label class="switch sm"><input type="checkbox" data-act="st-check" data-path="td.reports.${i}.system" ${r.system ? "checked" : ""}>وضعیت سیستم</label>
        <button class="btn sm ghost" data-act="tg-del-report" data-i="${i}" title="حذف">${I.trash ? I.trash(14) : "×"}</button></div>`);
    return html`<div class="panel"><header><h2>ربات تلگرام</h2><span class="aside">${state}</span></header><div class="in stack">
      <p class="help" style="margin:0">ربات هشدار قطعی، ری‌استارت و جابه‌جایی تانل‌ها را برای مدیرها می‌فرستد، گزارش مصرف را در زمان‌هایی که تعیین می‌کنید ارسال می‌کند، و هر کس شماره‌ی پورتش را بفرستد آمار دقیق ترافیک آن را می‌گیرد. یک ربات تازه در <a href="https://t.me/BotFather" target="_blank" rel="noopener" class="ltr">@BotFather</a> بسازید و توکنش را اینجا بگذارید.</p>
      ${st.error ? html`<div class="note bad">${st.error}</div>` : ""}
      <label class="switch"><input type="checkbox" data-act="st-check" data-path="td.on" ${d.on ? "checked" : ""}>ربات روشن</label>
      <div class="grid2"><div class="field"><label for="tg-token">توکن ربات</label><input id="tg-token" class="input ltr" data-bind="td.token" value="${d.token}" autocomplete="off" placeholder="${v.token_set ? "ذخیره شده؛ برای تغییر، توکن تازه را بگذارید" : "123456789:AA..."}"></div>
      <div class="field"><label for="tg-route">مسیر اتصال به تلگرام</label><select id="tg-route" class="input" data-bind="td.route">
        <option value="" ${d.route === "" ? "selected" : ""}>مستقیم از همین سرور</option>
        ${v.proxies.map((p) => html`<option value="${p.url}" ${d.route === p.url ? "selected" : ""}>از طریق تانل ${tname(S.ov.tunnels.find((t) => t.name === p.tunnel) || { name: p.tunnel })} (پروکسی داخلی)</option>`)}
        <option value="custom" ${d.route === "custom" ? "selected" : ""}>پروکسی دلخواه…</option></select>
        <span class="help">${v.proxies.length ? "پروکسی داخلی ترافیک ربات را از تانل رد می‌کند و از سرور خارج به تلگرام می‌رسد؛ برای سرور ایران که تلگرام رویش باز نیست." : "برای رد کردن ترافیک ربات از تانل، اول در صفحه‌ی پورت‌ها یک «پروکسی داخلی» بسازید."}</span></div>
      ${d.route === "custom" ? html`<div class="field full"><label for="tg-proxy">آدرس پروکسی</label><input id="tg-proxy" class="input ltr" data-bind="td.proxy" value="${d.proxy}" placeholder="socks5://user:pass@127.0.0.1:1080"></div>` : ""}</div>
      <div class="form-foot"><button class="btn primary" data-act="save-tg">ذخیره</button><button class="btn" data-act="tg-test" ${v.token_set && (s.admins || []).length ? "" : "disabled"}>پیام آزمایشی</button></div>
    </div></div>
    <div class="panel"><header><h2>مدیرها</h2><span class="aside">${(s.admins || []).length} نفر</span></header><div class="in stack">
      <p class="help" style="margin:0">مدیرها همه‌ی هشدارها و گزارش‌ها را می‌گیرند و وضعیت همه‌ی تانل‌ها و پورت‌ها را می‌بینند. برای اضافه شدن، کد اتصال بسازید و آن را برای ربات بفرستید؛ کد ۱۵ دقیقه و فقط یک بار کار می‌کند.</p>
      ${s.link_code ? html`<div class="note"><div>برای ربات بفرستید: <code class="ltr">/start ${s.link_code}</code></div>${link ? html`<div>یا این لینک را باز کنید: <a class="ltr" href="${link}" target="_blank" rel="noopener">${link}</a></div>` : ""}<div class="faint">تا ${F.hm.format(new Date(s.link_until * 1000))} معتبر است.</div></div>` : ""}
      <div class="field"><label for="tg-admins">شناسه‌ی عددی مدیرها</label><textarea id="tg-admins" class="input ltr" rows="3" data-bind="td.admins" placeholder="123456789">${d.admins}</textarea><span class="help">هر خط یک شناسه. هر کس دکمه‌ی «🆔 شناسه‌ی من» را در ربات بزند شناسه‌اش را می‌بیند.</span></div>
      <div class="form-foot"><button class="btn primary" data-act="save-tg">ذخیره</button><button class="btn" data-act="tg-code" ${v.token_set ? "" : "disabled"}>ساخت کد اتصال</button></div>
    </div></div>
    <div class="panel"><header><h2>هشدارها</h2></header><div class="in stack">
      <div class="grid2">${TG_ALERTS.map(([k, l]) => html`<label class="switch"><input type="checkbox" data-act="td-alert" data-key="${k}" ${d.mute.includes(k) ? "" : "checked"}>${l}</label>`)}</div>
      <span class="help">هر هشدار برای همه‌ی مدیرها فرستاده می‌شود؛ هشدار تکراری یک تانل در کمتر از یک دقیقه فرستاده نمی‌شود.</span>
      <div class="form-foot"><button class="btn primary" data-act="save-tg">ذخیره</button></div>
    </div></div>
    <div class="panel"><header><h2>گزارش‌های زمان‌بندی‌شده</h2><span class="aside">به وقت تهران</span></header><div class="in stack">
      ${reports.length ? html`<div class="stack" style="gap:8px">${reports}</div>` : html`<div class="muted">گزارشی تعریف نشده.</div>`}
      <span class="help">گزارش ساعتی، مصرف یک ساعت گذشته را می‌فرستد؛ روزانه ۲۴ ساعت گذشته و هفتگی ۷ روز گذشته. پورت‌ها را خالی بگذارید تا همه بیایند، یا مثلاً <span class="ltr">443,8080-8090</span>. «وضعیت سیستم» سلامت تانل‌ها، پردازنده و ترافیک کارت شبکه را هم اضافه می‌کند.</span>
      <div class="form-foot"><button class="btn primary" data-act="save-tg">ذخیره</button><button class="btn" data-act="tg-add-report">${I.plus ? I.plus(14) : "+"} گزارش تازه</button></div>
    </div></div>
    <div class="panel"><header><h2>کاربران عادی</h2></header><div class="in stack">
      <label class="switch"><input type="checkbox" data-act="st-check" data-path="td.users" ${d.users ? "checked" : ""}>هر کسی ربات را استارت کند می‌تواند مصرف پورتش را ببیند</label>
      <div class="field"><label for="tg-uports">پورت‌های قابل مشاهده</label><input id="tg-uports" class="input ltr" data-bind="td.user_ports" value="${d.user_ports}" placeholder="همه‌ی پورت‌ها" style="max-width:320px"><span class="help">کاربر شماره‌ی پورت را می‌فرستد و مصرف الان، امروز، ۲۴ ساعت و ۷ روز گذشته، این ماه و از ابتدا را می‌بیند. وضعیت تانل‌ها و پورت‌های دیگر را نمی‌بیند.</span></div>
      <div class="form-foot"><button class="btn primary" data-act="save-tg">ذخیره</button></div>
    </div></div>`;
  }

  // API tokens for programs (a sales bot, a script) and the webhook that
  // tells one what happens.
  const TOKEN_SCOPES = [["read", "فقط دیدن"], ["ports", "پورت‌ها: دیدن و مدیریت پورت‌ها و فروش"], ["full", "کامل: همه‌چیز جز تنظیمات پنل، پشتیبان و توکن‌ها"]];
  function setAPI() {
    const v = S.apiv;
    if (!v) { loadAPI(); return html`<div class="panel"><div class="in muted">در حال خواندن…</div></div>`; }
    const d = S.apid || (S.apid = { name: "", scope: "ports", url: v.webhook.url || "", secret: v.webhook.secret || "" });
    const made = S.apiMade;
    const rows = v.tokens.map((k) => html`<tr><td><b>${k.name}</b><div class="faint small ltr">nt_${k.id}…</div></td><td>${(TOKEN_SCOPES.find((x) => x[0] === k.scope) || [, k.scope])[1].split(":")[0]}</td><td>${F.dmy.format(new Date(k.created * 1000))}</td><td>${k.last_used ? ago(k.last_used) : "هنوز نه"}</td><td class="acts"><button type="button" class="btn sm danger icon" data-act="token-rm" data-id="${k.id}" aria-label="حذف توکن ${k.name}">${I.trash(15)}</button></td></tr>`);
    return html`<div class="panel"><header><h2>توکن‌های API</h2><span class="aside">برای برنامه‌ها، بدون رمز پنل</span></header><div class="in stack">
        <p class="help" style="margin:0">برنامه‌ای مثل ربات فروش با هدر <span class="ltr">Authorization: Bearer nt_…</span> همان API پنل را صدا می‌زند. هر توکن فقط کاری را می‌کند که دسترسی‌اش اجازه می‌دهد و به رمز و تنظیمات پنل، پشتیبان‌ها، توکن‌ها، تلگرام، فایروال و کلیدهای سرورها هیچ‌وقت نمی‌رسد. راهنمای کامل در <span class="ltr">docs/api.md</span>.</p>
        ${made ? html`<div class="psale-st"><b>توکن ساخته شد؛ همین الان کپی کنید، دیگر نشان داده نمی‌شود:</b><div class="code ltr sens">${made}<button class="btn sm ghost icon" data-act="copy" data-text="${made}" aria-label="کپی">${I.copy(15)}</button></div></div>` : ""}
        ${v.tokens.length ? html`<div class="tbl-wrap"><table class="t"><thead><tr><th>نام</th><th>دسترسی</th><th>ساخته شده</th><th>آخرین استفاده</th><th></th></tr></thead><tbody>${rows}</tbody></table></div>` : html`<div class="muted small">هنوز توکنی نساخته‌اید.</div>`}
        <div class="grid2"><div class="field"><label for="tk-name">نام توکن تازه</label><input id="tk-name" class="input" maxlength="60" data-bind="apid.name" value="${d.name}" placeholder="مثلاً ربات فروش"></div>
          <div class="field"><label for="tk-scope">دسترسی</label><select id="tk-scope" class="input" data-bind="apid.scope">${TOKEN_SCOPES.map(([k, l]) => html`<option value="${k}" ${d.scope === k ? "selected" : ""}>${l}</option>`)}</select></div></div>
        <div class="form-foot"><button type="button" class="btn primary" data-act="token-add">${I.plus(15)} ساختن توکن</button></div>
      </div></div>
      <div class="panel"><header><h2>وب‌هوک</h2><span class="aside">هر رویداد، به برنامه‌ی شما</span></header><div class="in stack">
        <p class="help" style="margin:0">هر رویداد این سرور (بسته شدن پورت فروخته به‌خاطر حجم یا مدت، باز شدن دوباره، قطعی و سالم شدن تانل، رفتن به پشتیبان و…) به‌صورت JSON با POST به این آدرس فرستاده می‌شود، با امضای <span class="ltr">X-NeoTunnel-Signature: sha256=…</span> (HMAC-SHA256 بدنه با رمز زیر) تا گیرنده بداند از همین سرور آمده. اگر نرسد تا سه بار دیگر تلاش می‌شود.</p>
        <div class="grid2"><div class="field"><label for="wh-url">آدرس</label><input id="wh-url" class="input ltr" data-bind="apid.url" value="${d.url}" placeholder="https://example.com/neotunnel"><span class="help">خالی: خاموش.</span></div>
          <div class="field"><label for="wh-secret">رمز امضا</label><div class="pf"><input id="wh-secret" class="input ltr sens" data-bind="apid.secret" value="${d.secret}" placeholder="حداقل ۱۶ کاراکتر"><button type="button" class="btn sm" data-act="wh-gen" title="یک رمز تصادفی">${I.dice(14)} تصادفی</button></div></div></div>
        <div class="form-foot"><button type="button" class="btn" data-act="wh-test">ارسال آزمایشی</button><button type="button" class="btn primary" data-act="wh-save">ذخیره</button></div>
      </div></div>`;
  }

  // Every change to tunnels and ports, newest first, each with a way back
  // to before it.
  const DIFF_KIND = { tunnel: "تانل", port: "پورت", pool: "استخر آدرس" };
  const DIFF_OP = { added: "اضافه شد", removed: "حذف شد", changed: "تغییر کرد" };
  const diffText = (d) => d.kind === "defaults" ? "پیش‌فرض تانل‌ها تغییر کرد" : d.kind === "downloads" ? "تنظیم دانلودهای سرور تغییر کرد" : d.kind === "order" ? "ترتیب پورت‌ها عوض شد"
    : html`${DIFF_KIND[d.kind] || d.kind} <b class="ltr">${d.kind === "tunnel" ? tlabel(d.name) : d.name}</b> ${DIFF_OP[d.op] || d.op}`;
  function setHistory() {
    const h = S.hist2;
    if (!h) { loadHistory(); return html`<div class="panel"><div class="in muted">در حال خواندن…</div></div>`; }
    const rows = h.map((x, i) => html`<li class="hrow"><div class="hwhen"><b>${ago(x.at)}</b><span class="faint small">${F.dmhm.format(new Date(x.at * 1000))} · نسخه‌ی ${x.revision}</span></div>
      <div class="hwhat">${x.rollback ? html`<span class="tag warn">بازگردانی</span> ` : ""}${x.changes.slice(0, 6).map((d, j) => html`${j ? "، " : ""}${diffText(d)}`)}${x.changes.length > 6 ? html` <span class="faint">و ${x.changes.length - 6} مورد دیگر</span>` : ""}</div>
      <button type="button" class="btn sm" data-act="rollback-ask" data-id="${x.id}" data-i="${i}">${I.sync(14)} برگرداندن به قبل از این</button></li>`);
    return html`<div class="panel"><header><h2>تاریخچه‌ی تغییرات</h2><span class="aside">${h.length} تغییر · تا ۱۰۰ تغییر یا دو ماه</span></header><div class="in stack">
      <p class="help" style="margin:0">پیش از هر تغییر در تانل‌ها، پورت‌ها، استخرهای آدرس و پیش‌فرض‌ها، وضعیت قبلی نگه داشته می‌شود. «برگرداندن» تانل‌ها و پورت‌ها را روی این سرور و سرورهای مقابل به حال پیش از آن تغییر برمی‌گرداند؛ این کار خودش هم در تاریخچه ثبت می‌شود و می‌شود برش گرداند. رمز و تنظیمات پنل، توکن‌ها، فایروال، تلگرام و ترافیک پورت‌ها دست نمی‌خورند.</p>
      ${h.length ? html`<ul class="hlist">${rows}</ul>` : html`<div class="muted small">هنوز تغییری ثبت نشده؛ از این نسخه به بعد هر تغییر اینجا می‌آید.</div>`}
    </div></div>`;
  }
  async function loadHistory() {
    try { S.hist2 = await api("GET", "history"); if (S.route === "settings" && S.sec === "history") draw(); } catch (e) { toast(e.message, "bad"); }
  }

  async function loadAPI() {
    try {
      const [tokens, webhook] = await Promise.all([api("GET", "tokens"), api("GET", "settings/webhook")]);
      S.apiv = { tokens, webhook }; if (S.route === "settings" && S.sec === "api") draw();
    } catch (e) { toast(e.message, "bad"); }
  }

  async function loadTelegram() {
    try { S.tv = await api("GET", "telegram"); if (S.route === "settings" && S.sec === "telegram") draw(); } catch (e) { toast(e.message, "bad"); }
  }

  async function loadFetch() {
    try { S.fetch = await api("GET", "settings/fetch"); if (S.route === "settings" && S.sec === "fetch") draw(); } catch (e) { toast(e.message, "bad"); }
  }

  // How long each step of automatic encryption gets, in minutes.
  const autoWaitMin = () => (S.ov.defaults && S.ov.defaults.auto_wait) || 5;
  const poolRange = (p) => html`<span class="ltr">${p.net4 || "10.77.0.0/16"}</span>${p.net6 ? html` · <span class="ltr small">${p.net6}</span>` : ""}`;
  const poolTitle = (p) => p.name === "default" ? "پیش‌فرض (10.77.0.0/16)" : p.builtin ? p.net4 : p.name;

  // The address pools: ready-made ones and the user's, which can be edited.
  function setPools(d) {
    const e = S.pe || (S.pe = { name: "", net4: "", net6: "", was: "" });
    return html`<div class="panel"><header><h2>پریست‌های Address pool</h2><span class="aside">محدوده‌ای که آدرس داخلی تانل‌های جدید از آن برداشته می‌شود</span></header><div class="in stack">
      <div class="field"><label for="d-pool">پریست پیش‌فرض تانل‌های جدید</label><select id="d-pool" class="input" data-bind="defs.pool" style="max-width:320px">${S.ov.pools.map((p) => html`<option value="${p.name === "default" ? "" : p.name}" ${(d.pool || "") === (p.name === "default" ? "" : p.name) ? "selected" : ""}>${poolTitle(p)}</option>`)}</select><span class="help">برای اعمالش «ذخیره‌ی پیش‌فرض‌ها» را بزنید. در ساخت هر تانل هم می‌توانید پریست دیگری انتخاب کنید یا محدوده را دستی بدهید.</span></div>
      <div class="tbl-wrap"><table class="t"><thead><tr><th>نام</th><th>محدوده</th><th>تانل‌ها</th><th></th></tr></thead><tbody>${S.ov.pools.map((p) => html`<tr><td><b dir="auto">${poolTitle(p)}</b>${p.builtin ? html` <span class="tag">آماده</span>` : ""}</td><td>${poolRange(p)}</td><td class="ltr">${p.tunnels}</td><td class="acts">${p.builtin ? "" : html`<button class="btn sm" data-act="pool-edit" data-name="${p.name}">${I.edit(14)} ویرایش</button> <button class="btn sm danger icon" data-act="pool-del" data-name="${p.name}" aria-label="حذف پریست ${p.name}">${I.trash(15)}</button>`}</td></tr>`)}</tbody></table></div>
      <p class="help" style="margin:0">هر تانل یک /30 از IPv4 و یک /64 از IPv6 می‌گیرد؛ تانل‌های دولایه (مثل 6to4) یک بلوک هم برای لایه‌ی زیرین می‌گیرند. عوض کردن یا حذف پریست روی تانل‌های ساخته‌شده اثری ندارد. اگر IPv6 ندهید، از محدوده‌ی ULA همین سرور برداشته می‌شود.</p>
      <div class="grid2">
        <div class="field"><label for="pe-n">${e.was ? "ویرایش پریست" : "نام پریست جدید"}</label><input id="pe-n" class="input" dir="auto" maxlength="32" data-bind="pe.name" value="${e.name}" placeholder="مثلاً دفتر مرکزی"></div>
        <div class="field"><label for="pe-4">محدوده‌ی IPv4</label><input id="pe-4" class="input ltr" data-bind="pe.net4" value="${e.net4}" placeholder="10.50.0.0/16"></div>
        <div class="field"><label for="pe-6">محدوده‌ی IPv6 (اختیاری)</label><input id="pe-6" class="input ltr" data-bind="pe.net6" value="${e.net6}" placeholder="fd50::/48"></div>
      </div>
      <div class="form-foot"><button class="btn primary" data-act="pool-save">${e.was ? I.check(16) : I.plus(16)} ${e.was ? "ذخیره‌ی پریست" : "افزودن پریست"}</button>${e.was ? html`<button class="btn ghost" data-act="pool-cancel">انصراف</button>` : ""}</div>
    </div></div>`;
  }

  function setDefaults() {
    const d = S.defs || (S.defs = Object.assign({ encrypt: "", mtu_probe: false, obfs: false, padding: 0, forward_mode: "nat", pool: "", auto_wait: 0 }, S.ov.defaults));
    if (d.recover === undefined) d.recover = !d.no_recover;
    if (d.nightly === undefined) d.nightly = !d.no_nightly;
    if (d.autoup === undefined) d.autoup = !d.no_auto_upgrade;
    if (!d.forward_mode) d.forward_mode = "nat";
    return html`<div class="panel"><header><h2>پیش‌فرض تانل‌های جدید</h2></header><div class="in stack">
      <p class="help" style="margin:0">این مقادیر در ساخت تانل (پنل، منو و دستور quick) از قبل انتخاب می‌شوند و هر بار قابل تغییرند.</p>
      <div class="field"><label for="d-enc">رمزنگاری</label><select id="d-enc" class="input" data-bind="defs.encrypt" style="max-width:320px"><option value="">بدون رمزنگاری</option>${S.ov.ciphers.map((c) => html`<option value="${c.name}" ${d.encrypt === c.name ? "selected" : ""}>IPsec ${c.title}</option>`)}<option value="auto" ${d.encrypt === "auto" ? "selected" : ""}>خودکار (هرچه رد شود)</option></select></div>
      <label class="switch"><input type="checkbox" data-act="def-check" data-key="mtu_probe" ${d.mtu_probe ? "checked" : ""}>اندازه‌گیری خودکار MTU مسیر</label>
      <label class="switch"><input type="checkbox" data-act="def-check" data-key="obfs" ${d.obfs ? "checked" : ""}>اجرای تانل‌ها روی لایه‌ی NeoObfs</label>
      <div class="field"><label for="d-pad">Padding پیش‌فرض NeoObfs (بایت)</label><input id="d-pad" class="input ltr" type="number" min="0" max="255" data-bind="defs.padding" value="${d.padding || ""}" placeholder="0" style="max-width:160px"></div>
      ${modeField("defs", d.forward_mode, false, "forward_mode")}
      <div class="form-foot"><button class="btn primary" data-act="save-defaults">ذخیره‌ی پیش‌فرض‌ها</button></div>
    </div></div>
    ${setPools(d)}
    <div class="panel"><header><h2>بازیابی خودکار تانل‌ها</h2></header><div class="in stack">
      <label class="switch"><input type="checkbox" data-act="def-check" data-key="recover" ${d.recover ? "checked" : ""}>وقتی تانلی بیشتر بسته‌هایش را از دست می‌دهد، خودکار درستش کن</label>
      <p class="help" style="margin:0">هر ۸ ثانیه ۵ پینگ از داخل هر تانل فرستاده می‌شود. اگر تانلی حدود ۲۰ ثانیه بیشتر از ۴۰٪ بسته‌هایش را از دست بدهد، اول فورواردهایش به اولین تانل پشتیبانی می‌روند که واقعاً بهتر باشد، تا قطعی پیش نیاید. حدود ۲۴ ثانیه که شد، خود تانل هم یک بار روی هر دو سرور ری‌استارت می‌شود (هر تانل حداکثر هر ۱۰ دقیقه یک بار). وقتی تانل اصلی حدود یک دقیقه سالم ماند، فورواردها برمی‌گردند. در طول تست سرعت کاری انجام نمی‌شود.</p>
      <label class="switch"><input type="checkbox" data-act="def-check" data-key="autoup" ${d.autoup ? "checked" : ""}>ارتقای خودکار سرورهای مقابل</label>
      <p class="help" style="margin:0">وقتی این سرور به نسخه‌ی تازه‌ای به‌روز شد، سرورهای مقابل یکی‌یکی خودکار به همان نسخه می‌روند. نسخه‌ی قبلی روی هر سرور نگه داشته می‌شود؛ اگر سروری با نسخه‌ی تازه تا ۴ دقیقه به این سرور جواب ندهد، خودش به نسخه‌ی قبلی برمی‌گردد و ارتقای بقیه تا نسخه‌ی بعد متوقف می‌شود.</p>
      <label class="switch"><input type="checkbox" data-act="def-check" data-key="nightly" ${d.nightly ? "checked" : ""}>تست سرعت شبانه</label>
      <p class="help" style="margin:0">هر شب ساعت ۴:۳۰ به وقت تهران، هر تانل یک تست سبک می‌گیرد: ۴ ثانیه هر جهت با ۴ اتصال، بیرون از آمار ترافیک. نتیجه‌ها در صفحه‌ی هر تانل شب به شب می‌ماند تا افت تدریجی یا محدودسازی ارائه‌دهنده دیده شود، و «بهترین مسیر» پورت‌ها هم بین تانل‌های هم‌اندازه از آن کمک می‌گیرد. تانل خاموش و تانل با حجم ماهانه تست نمی‌شوند.</p>
      <div class="field"><label for="d-wait">رمزنگاری خودکار: مدت صبر روی هر مرحله (دقیقه)</label><input id="d-wait" class="input ltr" type="number" min="1" max="1440" data-bind="defs.auto_wait" value="${d.auto_wait || ""}" placeholder="5" style="max-width:160px">
        <span class="help">برای تانل‌هایی که رمزنگاری‌شان روی «خودکار» است. ترتیب مراحل: بدون رمز، AES-128، AES-256، ChaCha20، NeoObfs. تانلی که پشتیبانِ سالم دارد مرحله عوض نمی‌کند؛ فقط بررسی می‌شود تا سالم شود. اگر همه‌ی مراحل پشت‌سرهم جواب ندادند، ۳۰ دقیقه استراحت می‌کند و از نو شروع می‌کند.</span></div>
      <div class="form-foot"><button class="btn primary" data-act="save-defaults">ذخیره</button></div>
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
      ${ts.length ? html`<table class="t"><thead><tr><th>تانل</th><th class="num">امروز</th><th class="num">این ماه</th><th class="num">از ابتدا</th><th></th></tr></thead><tbody>${ts.map((t) => html`<tr><td>${tname(t)}</td><td class="num">${fmtBytes(t.today.rx + t.today.tx)}</td><td class="num">${fmtBytes(t.month.rx + t.month.tx)}</td><td class="num">${fmtBytes(t.total.rx + t.total.tx)}</td>
        <td style="text-align:end"><button class="btn sm ghost" data-act="reset-traffic" data-name="${t.name}">صفر کردن</button></td></tr>`)}</tbody></table>` : html`<div class="muted">تانلی نیست.</div>`}
      <div class="form-foot"><button class="btn danger" data-act="reset-traffic" data-name="">صفر کردن آمار همه‌ی تانل‌ها</button></div>
    </div></div>`;
  }

  function setMaint() {
    const r = S.restore;
    const p = r && r.preview;
    return html`<div class="panel"><header><h2>پشتیبان</h2></header><div class="in stack">
        <p class="help" style="margin:0">همه‌ی تنظیمات، سرورها، تانل‌ها، کلید این سرور، آمار ترافیک، نمودارها و گواهی پنل در یک فایل. کلید و رمزها داخلش هست؛ فایل را جای امن نگه دارید.</p>
        <div><a class="btn" href="api/backup" download>${I.download(16)} دانلود پشتیبان</a></div></div></div>
      <div class="panel"><header><h2>بازگردانی</h2></header><div class="in stack">
        <p class="help" style="margin:0">فایل پشتیبان را روی همین سرور یا یک سرور تازه (بعد از نصب NeoTunnel) بازگردانی کنید. روی سرور تازه، IP قدیمی در همه‌ی تانل‌ها با IP این سرور عوض می‌شود و سرورهای مقصد خودکار به‌روز می‌شوند؛ چون کلید سرور هم برمی‌گردد، لازم نیست آن‌ها را دوباره وصل کنید. ورود و پورت همین پنل عوض نمی‌شود.</p>
        <label class="btn" style="align-self:flex-start">${I.sync(16)} انتخاب فایل پشتیبان<input type="file" accept=".json,application/json" data-act="restore-file" hidden></label>
        ${r && r.error ? html`<div class="note bad">${r.error}</div>` : ""}
        ${p ? html`<div class="note ${p.warnings && p.warnings.length ? "warn" : ""}"><dl class="kv">
            <dt>سرور</dt><dd class="ltr">${p.node}</dd>
            ${p.created ? html`<dt>تاریخ</dt><dd>${new Date(p.created).toLocaleString("fa-IR")}${p.version ? html` <span class="faint small ltr">${p.version}</span>` : ""}</dd>` : ""}
            <dt>تانل‌ها و فورواردها</dt><dd>${p.tunnels} تانل، ${p.forwards} فوروارد</dd>
            <dt>سرورهای مقصد</dt><dd class="ltr">${p.peers.length ? p.peers.join(", ") : "—"}</dd>
            <dt>IP</dt><dd>${p.address && p.here && p.address !== p.here ? html`<span class="ltr">${p.address}</span> ← <span class="ltr">${p.here}</span> <span class="faint small">(در تانل‌ها عوض می‌شود)</span>` : html`<span class="ltr">${p.address || p.here || "—"}</span>`}</dd>
            ${p.domain ? html`<dt>دامنه‌ی پنل</dt><dd class="ltr">${p.domain}</dd>` : ""}</dl>
          ${(p.warnings || []).includes("no-key") ? html`<p style="margin:8px 0 0"><b>این پشتیبان کلید سرور را ندارد</b> (نسخه‌ی قدیمی). بعد از بازگردانی، سرورهای مقصد را با دستور اتصالشان دوباره وصل کنید.</p>` : ""}
          ${(p.warnings || []).includes("managed-moved") ? html`<p style="margin:8px 0 0"><b>این پشتیبانِ یک سرور مقصد است</b> و IP آن عوض شده؛ بعد از بازگردانی، IP تازه را روی سرور اصلی (${(p.managed || []).join("، ")}) هم وارد کنید.</p>` : ""}</div>
          <div class="form-foot"><button class="btn danger" data-act="restore" ${r.busy ? "disabled" : ""}>${I.sync(16)} ${r.busy ? "در حال بازگردانی…" : "بازگردانی روی این سرور"}</button><button class="btn ghost" data-act="restore-cancel">انصراف</button></div>` : ""}
      </div></div>
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
      <dt>نسخه</dt><dd class="ltr">${ov.node.version} ${ov.node.arch}</dd><dt>اثر انگشت کلید سرور</dt><dd class="ltr sens">${ov.node.key}</dd>
      <dt>روشن بودن سرور</dt><dd>${fmtUptime(ov.node.uptime_s)}</dd><dt>کد منبع</dt><dd><a href="${ov.repo}" target="_blank" rel="noopener" class="ltr">${ov.repo.replace("https://", "")}</a></dd>
      <dt>کانال تلگرام</dt><dd><a href="${ov.channel}" target="_blank" rel="noopener" class="ltr">${ov.channel.replace("https://", "")}</a></dd></dl></div></div>
      ${updatePanel()}`;
  }

  function updatePanel() {
    const u = S.upd, run = S.updRun;
    const state = run ? html`<i class="spin"></i> در حال نصب <span class="ltr">${run.to}</span>…`
      : !u ? html`<i class="spin"></i> در حال بررسی…`
      : u.error ? html`${I.alert(16)} بررسی نشد: ${u.error}`
      : u.newer ? html`${I.upgrade(16)} نسخه‌ی <b class="ltr">${u.latest}</b> آمده است${u.url ? html` · <a href="${u.url}" target="_blank" rel="noopener">تغییرات</a>` : ""}`
      : html`${I.check(16)} این سرور آخرین نسخه را دارد (<span class="ltr">${u.current}</span>)`;
    const when = u && u.checked ? `آخرین بررسی ${F.hm.format(new Date(u.checked * 1000))}` : "";
    return html`<div class="panel"><header><h2>به‌روزرسانی</h2>${when ? html`<span class="aside">${when}</span>` : ""}</header><div class="in">
      <div class="upd-row ${u && u.newer && !run ? "new" : ""}"><span class="grow">${state}</span>
        <button class="btn sm ghost" data-act="update-check" ${!u || run ? "disabled" : ""}>بررسی دوباره</button>
        ${u && u.newer && !run ? html`<button class="btn sm primary" data-act="update-ask">${I.upgrade(14)} به‌روزرسانی</button>` : ""}</div>
      <p class="help" style="margin:10px 0 0">پنل هر ساعت نسخه‌ی تازه را از گیت‌هاب بررسی می‌کند. به‌روزرسانی همان کار <code class="ltr">sudo neotunnel update</code> را می‌کند و تانل‌ها و تنظیمات می‌مانند.</p></div></div>`;
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
    morph(root, part(html`<div class="overlay" data-act="close-overlay"><div class="modal ${S.modal.wide ? "wide" : ""}" role="dialog" aria-modal="true">${S.modal.view()}</div></div>`));
    markSensitive(root);
    wireCharts();
    if (first) { const f = $(".modal input, .modal select, .modal button.primary"); if (f) f.focus(); }
  }
  const openModal = (view, opts = {}) => { S.modal = Object.assign({ view }, opts); drawModal(); };
  const closeModal = () => { S.modal = null; S.wiz = null; drawModal(); draw(); };
  const modalHead = (title, extra = "") => html`<header><h3>${title}</h3>${extra}<button class="btn sm ghost icon" data-act="close" style="margin-inline-start:${extra ? "0" : "auto"}" aria-label="بستن">${I.x(16)}</button></header>`;

  function setupView(s, intro) {
    const where = `${s.peer} (${s.address})`;
    return html`${modalHead("دستور اتصال " + pname(s.peer))}
      <div class="body">
        ${intro || (s.paired ? html`<div class="note ok">این سرور متصل است و تغییرات خودکار روی آن اعمال می‌شود. اگر روزی در دسترس نبود، دستور زیر را روی <b>${where}</b> اجرا کنید تا همه‌چیز یکجا اعمال شود.</div>`
          : html`<div class="note warn">این سرور هنوز وصل نشده. یکی از دستورهای زیر را روی <b>${where}</b> با دسترسی root اجرا کنید؛ بعد از آن هر تغییری خودکار روی آن هم اعمال می‌شود.</div>`)}
        ${s.github ? codeBlock("۱. نصب از گیت‌هاب و اعمال تنظیمات", s.github, s.peer) : ""}
        ${codeBlock(s.github ? "۲. اگر NeoTunnel از قبل نصب است" : "اعمال همه‌ی تنظیمات (همگام‌سازی دستی)", s.apply, s.peer)}
        <div class="help">کلید اتصال (NT1.…) با کلید این سرور امضا شده و رمزهای تانل‌ها را هم دارد؛ آن را فقط روی همان سرور اجرا کنید.</div>
      </div>
      <footer><span class="grow"></span><button class="btn primary" data-act="close">بستن</button></footer>`;
  }

  // RouterOS commands for a MikroTik at the far end of a tunnel.
  function mikrotikView(name, commands) {
    return html`${modalHead("دستورهای میکروتیک " + tlabel(name))}
      <div class="body">
        <div class="note">این دستورها را در ترمینال روتر (RouterOS 7) بچسبانید تا آن سر تانل با همین تنظیمات (MTU، کلید، آدرس داخلی) ساخته شود.</div>
        ${codeBlock("RouterOS v7", commands.trimEnd(), "")}
        <div class="help">خط‌های # توضیح‌اند و روتر آن‌ها را نادیده می‌گیرد. راهنمای کامل: docs/mikrotik.md</div>
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
    (ch.peers || []).filter((p) => p.ok).forEach((p) => toast(`${pname(p.peer)} به‌روز شد`));
    if (failed.length && failed[0].setup) {
      const p = failed[0], notPaired = /not paired/.test(p.error || "");
      openModal(() => setupView(p.setup, html`<div class="note ${notPaired ? "warn" : "bad"}">${okMsg ? html`<b>${okMsg}.</b> ` : ""}${notPaired
        ? html`سرور <b>${pname(p.peer)}</b> هنوز وصل نشده. دستور زیر را روی آن اجرا کنید تا آن طرف تانل هم ساخته شود.`
        : html`سرور <b>${pname(p.peer)}</b> در دسترس نبود (<span class="ltr small">${p.error}</span>). با اجرای دستور زیر روی آن، همه‌ی تغییرات یکجا اعمال می‌شود.`}</div>`));
      return;
    }
    if (okMsg) toast(okMsg);
  }

  // Tunnel wizard: destination → protocol → protection → ports and create.
  // With a connected server every choice is checked live: the same stack is
  // brought up on both ends for a few seconds to see that traffic passes.
  const WIZ_STEPS = ["مقصد", "پروتکل", "امنیت و پورت", "فوروارد و ساخت"];
  // A connection tunnel's third step is mostly its direction.
  const wizSteps = (w) => (w.mode && isStreamMode(w.mode) ? ["مقصد", "پروتکل", "جهت و پورت", "فوروارد و ساخت"] : WIZ_STEPS);
  const SIT_MODES = ["6to4", "6to4-gre"];
  // Gaming: the kinds that carry UDP in the kernel with the least delay and
  // jitter, and the ones whose TCP (or user-space path) adds to them.
  const GAME_MODES = ["gre", "ipip", "6to4", "wireguard"];
  const notForGames = (m) => !!(m.tcp || m.stream || m.name === "obfs");
  // The gaming mode the live checks found quickest, of those that passed.
  function gameBest(w) {
    let best = null, bestPing = Infinity;
    for (const name of GAME_MODES) {
      const c = w.checks[checkKey(w, name, false)];
      if (c && c.state === "ok" && !c.existing && c.ping < bestPing && S.ov.modes.some((m) => m.name === name)) { best = name; bestPing = c.ping; }
    }
    return best;
  }
  // Settings of the gaming mode: least delay, UDP forwarded, the path MTU
  // measured; the mode itself is the quickest that passed.
  function applyGaming(w) {
    Object.assign(w, { profile: "lean", mtuMode: "probe", fmode: "nat", proto: "both" });
    if (w.protect === "obfs" || w.protect === "auto") w.protect = "none";
    const m = S.ov.modes.find((x) => x.name === w.mode), c = w.checks[checkKey(w, w.mode, false)];
    if (!m || !GAME_MODES.includes(w.mode) || (c && (c.state === "bad" || c.existing))) {
      const bad = (n) => { const k = w.checks[checkKey(w, n, false)]; return k && (k.state === "bad" || k.existing); };
      w.mode = gameBest(w) || GAME_MODES.find((n) => !bad(n) && S.ov.modes.some((x) => x.name === n)) || "gre";
    }
  }
  // The smart queue a gaming tunnel starts with: 90% of the slower direction
  // of its measured speed, so a download on the same tunnel keeps the ping
  // low (0 = not measured, set it later in the tunnel's limits).
  function gameQueue(w) {
    const r = w.speeds[speedKey(w, w.profile || "balanced")];
    return r && r.state === "ok" ? Math.max(0, Math.floor((0.9 * Math.min(r.up, r.down)) / 1e6)) : 0;
  }

  function wizardView() {
    const w = S.wiz;
    const anim = w.anim ? ` enter-${w.anim}` : "";
    const stepper = html`<ol class="stepper" aria-label="مرحله ${w.step} از ${WIZ_STEPS.length}">${wizSteps(w).map((l, i) => html`<li class="${w.step === i + 1 ? "now" : w.step > i + 1 ? "done" : ""}"><i>${w.step > i + 1 ? I.check(12) : i + 1}</i><span>${l}</span></li>`)}</ol>`;
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
          <b>${I.server(16)} ${plabel(p)}</b><span class="ltr">${p.address}</span><span class="state"><i class="dot ${p.paired ? "ok" : "warn"}"></i>${p.paired ? "متصل؛ هر انتخاب زنده بررسی می‌شود" : "منتظر اتصال"}</span></button>`)}
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
    const groups = [["لایه ۳، ترافیک IP", (m) => m.layer === 3 && !m.crypt && !m.tcp], ["رمزدار", (m) => m.crypt && !m.stream], ["لایه ۲، Ethernet", (m) => m.layer === 2], ["آخرین راه، وقتی فقط TCP رد می‌شود", (m) => m.tcp && !m.stream], ["تانل اتصال (مثل FRP)", (m) => m.stream]];
    const card = (m) => {
      const pi = PROTO[m.name] || {}, c = w.checks[checkKey(w, m.name, false)];
      return html`<button type="button" class="choice ${w.mode === m.name ? "on" : ""}" data-act="wiz-mode" data-mode="${m.name}">
        <b>${pi.t || m.title}${w.gaming ? (m.name === gameBest(w) ? html` <span class="tag lock">کمترین پینگ</span>` : notForGames(m) ? html` <span class="tag warn">برای بازی نه</span>` : "") : pi.rec && !v6 ? html` <span class="tag lock">پیشنهادی</span>` : ""}</b>
        <span>${pi.best || ""}</span>
        <span class="facts"><span class="tag">سربار ${pi.over || "?"} بایت</span>${m.udp ? html`<span class="tag">UDP</span>` : ""}${m.stream ? html`<span class="tag">TCP · اتصال به اتصال</span>` : m.tcp ? html`<span class="tag warn">TCP · کند با افت بسته</span>` : ""}${m.crypt ? html`<span class="tag lock">${I.lock(11)} رمزدار</span>` : ""}</span>
        ${checkChip(c)}</button>`;
    };
    const sel = PROTO[w.mode];
    return html`<div class="field"><span class="lbl">هدف تانل</span><div class="seg" role="radiogroup">
        <button type="button" role="radio" aria-checked="${!w.gaming}" class="${w.gaming ? "" : "on"}" data-act="wiz-gaming" data-value="">معمولی</button>
        <button type="button" role="radio" aria-checked="${!!w.gaming}" class="${w.gaming ? "on" : ""}" data-act="wiz-gaming" data-value="1">گیمینگ، کمترین پینگ</button></div>
        <span class="help">${w.gaming ? "تانل کرنلی که UDP بازی را بدون تأخیر اضافه می‌برد، با کمترین پینگی که بررسی زنده پیدا کند؛ حالت سرعت کم‌تأخیر، MTU اندازه‌گیری‌شده، فوروارد NAT با TCP و UDP، و صف هوشمند روی ۹۰٪ سرعت اندازه‌گیری‌شده تا دانلود هم‌زمان پینگ را بالا نبرد." : "برای بازی و تماس، «گیمینگ» را بزنید."}</span></div>
      <div class="field"><span class="lbl">بستر اتصال</span>
        <div class="seg" role="radiogroup">${[["ipv4", "IPv4"], ["ipv6", "IPv6"]].map(([v, l]) => html`<button type="button" role="radio" aria-checked="${w.underlay === v}" class="${w.underlay === v ? "on" : ""}" data-act="wiz-underlay" data-value="${v}">${l}</button>`)}</div>
        ${v6 ? html`<div class="grid2" style="margin-top:10px">
            <div class="field"><label for="w-l6">IPv6 این سرور</label><input id="w-l6" class="input ltr" data-bind="wiz.local6" value="${w.local6}" placeholder="2001:db8::1"><span class="help">${ov.node.address6 ? "خودکار پیدا شد." : "این سرور IPv6 عمومی ندارد یا پیدا نشد."}</span></div>
            <div class="field"><label for="w-r6">IPv6 مقصد</label><input id="w-r6" class="input ltr" data-bind="wiz.remote6" value="${w.remote6}" placeholder="2001:db8::2"><span class="help">${p6 && p6.address6 ? "از خود سرور مقصد گرفته شد." : "IPv6 عمومی سرور مقصد."}</span></div></div>
          <span class="help">وقتی مسیر IPv4 شلوغ یا محدود است. GRE و IPIP به نسخه‌ی IPv6 خود تبدیل می‌شوند؛ 6to4 که خودش روی IPv4 است اینجا نیست.</span>`
        : html`<span class="help">بیشتر سرورها فقط IPv4 دارند؛ اگر هر دو سرور IPv6 دارند، IPv6 هم امتحان کنید.</span>`}</div>
      <div class="guide">${I.alert(15)}<div><b>چه انتخاب کنم؟</b> اگر مطمئن نیستید <b>${v6 ? "GRE" : "6to4"}</b> را انتخاب کنید. اگر وصل نشد، در مرحله‌ی بعد آن را روی یک پورت UDP اجرا کنید یا WireGuard را امتحان کنید.
        ${canCheck(w) && !v6 ? html`<div style="margin-top:8px"><button type="button" class="btn sm" data-act="wiz-check-all" ${w.checkingAll ? "disabled" : ""}>${w.checkingAll ? html`<i class="spin"></i> در حال بررسی همه…` : html`${I.gauge(14)} بررسی سریع همه`}</button> <span class="faint small">همه با هم، حدود ۱۵ ثانیه</span></div>` : ""}</div></div>
      ${groups.map(([title, f]) => { const ms = ov.modes.filter(ok).filter(f); return ms.length ? html`<div class="group"><span class="lbl">${title}</span><div class="choices">${ms.map(card)}</div></div>` : ""; })}
      ${(() => { const c = w.checks[checkKey(w, w.mode, false)]; return c && c.state === "bad" ? html`<div class="note ${c.cfg ? "warn" : "bad"}"><b>${PROTO[w.mode] ? PROTO[w.mode].t : w.mode}:</b> <span title="${c.err || ""}">${errText(c.err)}</span></div>` : ""; })()}
      ${sel ? html`<div class="proto-info"><div><span class="k">لایه و محتوا</span>لایه ${sel.layer}، ${sel.carry}</div><div><span class="k">سربار</span>${sel.over} بایت</div>
        <div><span class="k">رمزنگاری</span>${sel.crypt}</div><div><span class="k">میکروتیک</span>${sel.mt}</div></div>
        <p class="help" style="margin:0">${sel.d}</p>` : ""}
      ${sel && sel.stream ? html`<div class="note"><b>تانل اتصال.</b> برای فوروارد پورت‌های TCP (رله) و پروکسی؛ UDP و حالت NAT یا IP واقعی رویش نیست. مثل بقیه پیش از ساخت روی هر دو سرور بررسی می‌شود؛ پینگ و سرعتش روی همین جلسه‌ها اندازه گرفته می‌شود.</div>` : ""}
      ${sel && sel.tcp ? html`<div class="note warn"><b>TCP آخرین راه است.</b> ترافیک کاربران (که خودش بیشتر TCP است) داخل یک اتصال TCP دیگر می‌رود؛ با هر بسته‌ی گم‌شده کل تانل منتظر ارسال دوباره می‌ماند و سرعت در مسیرِ با افت بسته خیلی پایین می‌آید. اول 6to4، GRE یا یک پروتکل روی UDP را امتحان کنید؛ اگر هیچ‌کدام رد نشد، TCP را انتخاب کنید.</div>` : ""}
      <div class="field"><label for="w-name">نام تانل (اختیاری)</label><input id="w-name" class="input ltr" data-bind="wiz.name" value="${w.name}" placeholder="${S.ov.next_name || "t1"}" maxlength="8" style="max-width:220px"><span class="help">خالی بگذارید تا <b class="ltr">${S.ov.next_name || "t1"}</b> انتخاب شود.</span></div>`;
  }

  function wizProtect(w) {
    return html`${wizProtectBody(w)}${profileField("wiz", w.profile, w.mode)}${w.dest !== "remote" && LANE_MODES.includes(w.mode) ? lanesField("wiz", w.lanes) : ""}`;
  }

  // Speed profiles: how hard the tunnel works for throughput.
  const PROFILES = [
    ["lean", "کم‌تأخیر", "پینگ‌محور: صف‌های کوتاه و کمترین مصرف پردازنده؛ برای بازی، تماس و ترافیک سبک."],
    ["balanced", "متعادل", "پیش‌فرض: تنظیمات معمول کرنل؛ برای بیشتر کاربردها."],
    ["max", "پرسرعت", "سرعت‌محور: کار تانل روی همه‌ی هسته‌های پردازنده پخش می‌شود و صف ارسال بلندتر است؛ برای دانلود و ترافیک سنگین."],
  ];
  const profileName = (p) => (PROFILES.find(([id]) => id === (p || "balanced")) || PROFILES[1])[1];
  const profileTag = (t) => t.settings && t.settings.profile ? html`<span class="tag ${t.settings.profile === "max" ? "lock" : ""}">${profileName(t.settings.profile)}</span>` : "";

  function profileField(path, value, kind) {
    const v = value || "balanced", tcp = kind === "tcp";
    const conns = { lean: 2, balanced: 4, max: 8 };
    const w = path === "wiz" ? S.wiz : null, live = w && canCheck(w);
    return html`<div class="field"><span class="lbl">حالت سرعت ${live ? html`<span class="faint small">· با انتخاب هر حالت، سرعت همان ترکیب مثل «تست سرعت» اندازه گرفته می‌شود: هر جهت ${S.speed.secs} ثانیه با ${S.speed.streams} اتصال</span>` : ""}</span>
      <div class="profiles" role="radiogroup">${PROFILES.map(([id, l, d]) => html`<button type="button" role="radio" aria-checked="${v === id}" class="profile ${v === id ? "on" : ""}" data-act="set" data-path="${path}.profile" data-value="${id}">
        <b>${l}</b><span>${d.split(":")[0]}${tcp ? ` · ${conns[id]} اتصال` : ""}</span>${live ? speedChip(w.speeds[speedKey(w, id)]) : ""}</button>`)}</div>
      <span class="help">${PROFILES.find(([id]) => id === v)[2]}${tcp ? ` تانل TCP روی ${conns[v]} اتصال هم‌زمان کار می‌کند؛ هر جریان ترافیک روی یکی از آن‌ها می‌ماند.` : ""}</span></div>`;
  }

  // Lanes: one tunnel carried by several parallel GRE tunnels, each flow on
  // one of them, so receive and encryption work spread over CPU cores.
  const LANE_MODES = ["gre", "gretap"], LANE_KINDS = ["gre", "gretap", "ip6gre", "ip6gretap"];
  function lanesField(path, value) {
    const v = String(+value > 1 ? +value : 1);
    return html`<div class="field"><span class="lbl">مسیرهای موازی (lanes)</span>
      <div class="seg" role="radiogroup">${["1", "2", "4", "8"].map((n) => html`<button type="button" role="radio" aria-checked="${v === n}" class="${v === n ? "on" : ""}" data-act="set" data-path="${path}.lanes" data-value="${n}">${n}</button>`)}</div>
      <span class="help">${v === "1" ? "یک تانل، مثل همیشه." : `تانل روی ${v} تانل GRE موازی بین همین دو سرور کار می‌کند؛ هر جریان ترافیک روی یکی از آن‌ها می‌ماند و کار دریافت و رمزنگاری بین هسته‌های پردازنده پخش می‌شود. فقط وقتی کمک می‌کند که ترافیک از چند اتصال هم‌زمان باشد و یک هسته‌ی پردازنده گلوگاه باشد. هر دو سر باید NeoTunnel داشته باشند.`}</span></div>`;
  }

  function wizProtectBody(w) {
    const ov = S.ov, mode = ov.modes.find((m) => m.name === w.mode) || {};
    const canProtect = !mode.crypt && w.dest !== "remote";
    const pi = PROTO[w.mode] || {}, v6 = w.underlay === "ipv6";
    const fou = !mode.udp && !mode.tcp && !["l2tpv3"].includes(w.mode);
    if (mode.stream) return wizStreamBody(w, mode);
    if (mode.tcp) {
      // The TCP transport seals its own frames; NeoObfs (UDP) can't go under it.
      const p = wizProt(w, mode);
      return html`${checkBanner(w)}
      <div class="field"><span class="lbl">محافظت</span><div class="choices">
          <button type="button" class="choice ${p === "none" ? "on" : ""}" data-act="wiz-protect" data-value="none"><b>بدون رمزنگاری</b><span>سریع‌ترین. اتصال با رمز مشترک تانل احراز هویت می‌شود، ولی بسته‌ها خوانا می‌روند؛ برای ترافیکی که خودش رمز شده (TLS، Xray) کافی است.</span></button>
          <button type="button" class="choice ${p === "ipsec" ? "on" : ""}" data-act="wiz-protect" data-value="ipsec"><b>${I.lock(15)} رمزنگاری جریان</b><span>هر قاب با کلید تازه‌ی همان اتصال مهر می‌شود؛ ۱۶ بایت سربار.</span></button>
          ${w.dest !== "remote" ? html`<button type="button" class="choice ${p === "auto" ? "on" : ""}" data-act="wiz-protect" data-value="auto"><b>${I.sync(15)} خودکار</b><span>بدون رمز شروع می‌شود؛ اگر ترافیک رد نشد، رمزها را یکی‌یکی امتحان می‌کند.</span></button>` : ""}
        </div>
        ${p === "ipsec" ? html`<select class="input" data-bind="wiz.encrypt" style="max-width:300px;margin-top:10px" aria-label="الگوریتم">${ov.ciphers.map((c) => html`<option value="${c.name}" ${w.encrypt === c.name ? "selected" : ""}>${c.title}</option>`)}</select>` : ""}</div>
      <div class="field"><span class="lbl lbl-i"><label for="w-port">پورت TCP</label> <span class="tag">اختیاری</span></span>
        <div class="pf"><input id="w-port" class="input ltr" type="number" min="1" max="65535" data-bind="wiz.port" value="${w.port}" placeholder="خودکار" style="max-width:220px"><button type="button" class="btn sm" data-act="random-port" data-path="wiz.port" data-proto="tcp" title="یک پورت آزاد تصادفی بین ۲۰۰۰۰ و ۶۰۰۰۰">${I.dice(14)} تصادفی</button></div>
        <span class="help">خالی بگذارید تا یک پورت آزاد خودکار انتخاب شود. فقط اگر بین دو سرور فقط پورت خاصی (مثلاً 443) باز است، همان را بدهید.</span></div>`;
    }
    return html`${checkBanner(w)}
      ${canProtect ? html`<div class="field"><span class="lbl">محافظت</span><div class="choices">
          <button type="button" class="choice ${w.protect === "none" ? "on" : ""}" data-act="wiz-protect" data-value="none"><b>بدون رمزنگاری</b><span>سریع‌ترین. برای ترافیکی که خودش رمز شده (TLS، Xray) کافی است.</span></button>
          <button type="button" class="choice ${w.protect === "ipsec" ? "on" : ""}" data-act="wiz-protect" data-value="ipsec"><b>${I.lock(15)} IPsec در کرنل</b><span>AES-GCM با سرعت کرنل؛ ۳۸ بایت سربار. ترافیک بین دو سرور خوانا نیست.</span></button>
          <button type="button" class="choice ${w.protect === "obfs" ? "on" : ""}" data-act="wiz-protect" data-value="obfs"><b>${I.mask(15)} NeoObfs</b><span>رمز با XChaCha20 داخل UDP؛ کندتر از کرنل چون در فضای کاربر اجرا می‌شود.</span></button>
          <button type="button" class="choice ${w.protect === "auto" ? "on" : ""}" data-act="wiz-protect" data-value="auto"><b>${I.sync(15)} خودکار</b><span>بدون رمز شروع می‌شود؛ اگر ترافیک رد نشد، از ساده به پیشرفته یکی‌یکی امتحان می‌کند و هر کدام ${autoWaitMin()} دقیقه فرصت دارد.</span></button>
        </div>
        ${w.protect === "auto" ? html`<span class="help">ترتیب: بدون رمز، AES-128، AES-256، ChaCha20، NeoObfs. هر بار تانل روی هر دو سرور ری‌استارت می‌شود. اگر پورت‌ها پشتیبانِ سالم داشته باشند، فوری به آن می‌روند و این تانل فقط بررسی می‌شود تا سالم شود.</span>` : ""}
        ${w.protect === "ipsec" ? html`<select class="input" data-bind="wiz.encrypt" style="max-width:300px;margin-top:10px" aria-label="الگوریتم">${ov.ciphers.map((c) => html`<option value="${c.name}" ${w.encrypt === c.name ? "selected" : ""}>${c.title}${c.name === lightCipher(w.peer).name ? " · سبک‌ترین برای این سرورها" : ""}</option>`)}</select>
          <span class="help">${lightCipher(w.peer).why}</span>` : ""}</div>`
        : html`<div class="note">${mode.crypt ? `${pi.t || w.mode} خودش رمزنگاری می‌کند؛ لایه‌ی دیگری لازم نیست.` : "برای دستگاه دیگر، رمزنگاری را با WireGuard انجام دهید."}</div>`}
      <div class="field"><span class="lbl lbl-i"><label for="w-port">پورت UDP</label> <span class="tag ${mode.udp || w.protect === "obfs" ? "" : "ok"}">${mode.udp || w.protect === "obfs" ? "اختیاری" : "لازم نیست"}</span></span>
        <div class="pf"><input id="w-port" class="input ltr" type="number" min="1" max="65535" data-bind="wiz.port" value="${w.port}" placeholder="${mode.udp || w.protect === "obfs" ? "خودکار" : "خالی = پروتکل خام"}" style="max-width:220px" ${v6 && fou && w.protect !== "obfs" ? "disabled" : ""}><button type="button" class="btn sm" data-act="random-port" data-path="wiz.port" data-proto="udp" title="یک پورت آزاد تصادفی بین ۲۰۰۰۰ و ۶۰۰۰۰" ${v6 && fou && w.protect !== "obfs" ? "disabled" : ""}>${I.dice(14)} تصادفی</button></div>
        <span class="help">${v6 && fou && w.protect !== "obfs" ? "روی IPv6، GRE و IPIP بدون پورت اجرا می‌شوند." : mode.udp || w.protect === "obfs" ? "خالی بگذارید تا یک پورت آزاد خودکار انتخاب شود؛ فقط اگر پورت خاصی می‌خواهید آن را بدهید." : html`<b>${pi.t || w.mode} پورت نمی‌خواهد؛ خالی بگذارید</b>، که سریع‌ترین حالت هم هست. فقط اگر تانل وصل نشد (مسیر این پروتکل را می‌بندد)، یک پورت بدهید تا داخل UDP برود.`}</span></div>`;
  }

  // A connection tunnel's step: which server listens, protection, ports.
  function wizStreamBody(w, mode) {
    const ov = S.ov, p = wizProt(w, mode), far = w.dest === "new" ? (w.newName || "سرور مقصد") : w.peer, here = ov.node.name;
    return html`${checkBanner(w)}
      <div class="field"><span class="lbl">جهت اتصال</span><div class="choices">
          <button type="button" class="choice ${!w.reverse ? "on" : ""}" data-act="set" data-path="wiz.reverse" data-value=""><b>${dirName(false)}</b><span>${here} به ${far} وصل می‌شود و ${far} روی پورت‌ها گوش می‌دهد؛ معمول‌ترین حالت.</span></button>
          <button type="button" class="choice ${w.reverse ? "on" : ""}" data-act="set" data-path="wiz.reverse" data-value="1"><b>${dirName(true)}</b><span>${far} به ${here} وصل می‌شود، مثل FRP، و ${here} گوش می‌دهد؛ وقتی اتصال از این سرور به آن طرف بسته است ولی برعکسش باز است.</span></button>
        </div><span class="help">فوروارد پورت‌ها در هر دو حالت از همین سرور به سرور مقابل است؛ فقط اینکه کدام طرف اتصال را باز می‌کند فرق دارد.</span></div>
      ${mode.crypt ? html`<div class="note">${PROTO[w.mode].t} با TLS 1.3 رمز می‌شود؛ لایه‌ی دیگری لازم نیست.</div>
        <div class="field"><label for="w-sni">نام سرور TLS (SNI، اختیاری)</label><input id="w-sni" class="input ltr" data-bind="wiz.sni" value="${w.sni || ""}" placeholder="خالی = بدون SNI، مثل اتصال به IP" style="max-width:320px"><span class="help">اگر دامنه‌ای دارید که به سرور گوش‌دهنده اشاره می‌کند، همان را بدهید.</span></div>`
      : html`<div class="field"><span class="lbl">محافظت</span><div class="choices">
          <button type="button" class="choice ${p === "none" ? "on" : ""}" data-act="wiz-protect" data-value="none"><b>بدون رمزنگاری</b><span>سریع‌ترین. جلسه با رمز مشترک تانل احراز هویت می‌شود ولی داده خوانا می‌رود؛ برای ترافیکی که خودش رمز شده (TLS، Xray) کافی است.</span></button>
          <button type="button" class="choice ${p === "ipsec" ? "on" : ""}" data-act="wiz-protect" data-value="ipsec"><b>${I.lock(15)} رمزنگاری جلسه</b><span>هر قاب با کلید تازه‌ی همان جلسه مهر می‌شود.</span></button>
        </div>
        ${p === "ipsec" ? html`<select class="input" data-bind="wiz.encrypt" style="max-width:300px;margin-top:10px" aria-label="الگوریتم">${ov.ciphers.map((c) => html`<option value="${c.name}" ${w.encrypt === c.name ? "selected" : ""}>${c.title}</option>`)}</select>` : ""}</div>`}
      <div class="field"><span class="lbl lbl-i"><label for="w-port">پورت TCP</label> <span class="tag">اختیاری</span></span>
        <div class="pf"><input id="w-port" class="input ltr" type="number" min="1" max="65535" data-bind="wiz.port" value="${w.port}" placeholder="تصادفی" style="max-width:220px"><button type="button" class="btn sm" data-act="random-port" data-path="wiz.port" data-proto="tcp" title="یک پورت آزاد تصادفی بین ۲۰۰۰۰ و ۶۰۰۰۰">${I.dice(14)} تصادفی</button></div>
        <span class="help">دو پورت پشتیبان تصادفی هم خودکار اضافه می‌شود؛ اگر اولی بسته بود، دومی و سومی امتحان می‌شوند. فقط اگر بین دو سرور پورت خاصی (مثلاً 443) باز است، آن را بدهید.</span></div>
      <div class="field"><label for="w-conns">تعداد جلسه‌های هم‌زمان (اختیاری)</label><input id="w-conns" class="input ltr" type="number" min="1" max="16" data-bind="wiz.conns" value="${w.conns || ""}" placeholder="خودکار، بر اساس حالت سرعت" style="max-width:220px"><span class="help">هر اتصال کاربر روی یکی از جلسه‌ها می‌رود؛ با جلسه‌ی بیشتر، افت بسته در یک جلسه کاربران کمتری را معطل می‌کند.</span></div>`;
  }

  // The protection that applies to the chosen mode: a TCP tunnel can't run
  // over NeoObfs and encrypts its own stream instead of using IPsec; a
  // connection tunnel's cipher doesn't step automatically either.
  function wizProt(w, mode) {
    if (mode.stream && (w.protect === "obfs" || w.protect === "auto")) return "none";
    return mode.tcp && w.protect === "obfs" ? "none" : w.protect;
  }
  // The forward mode a wizard request uses: a connection tunnel relays.
  const wizFmode = (w) => (isStreamMode(w.mode) ? "relay" : w.fmode);

  function wizFinish(w) {
    const ov = S.ov, pi = PROTO[w.mode] || {};
    const mode = ov.modes.find((m) => m.name === w.mode) || {};
    const prot = { none: "بدون رمزنگاری", ipsec: mode.tcp ? cipherTitle(w.encrypt) : `IPsec ${cipherTitle(w.encrypt)}`, obfs: "NeoObfs", auto: "خودکار" }[wizProt(w, mode)];
    return html`${checkBanner(w)}
      <dl class="summary">
        <div><dt>مقصد</dt><dd>${w.dest === "peer" ? w.peer : w.dest === "new" ? w.newName : html`<span class="ltr">${w.remote}</span>`}</dd></div>
        <div><dt>پروتکل</dt><dd>${pi.t || w.mode}${w.underlay === "ipv6" ? " روی IPv6" : ""}</dd></div>
        <div><dt>محافظت</dt><dd>${mode.crypt ? "رمزنگاری خودِ پروتکل" : prot}</dd></div>
        <div><dt>حالت سرعت</dt><dd>${profileName(w.profile)}</dd></div>
        ${w.gaming ? html`<div><dt>گیمینگ</dt><dd>${gameQueue(w) ? `صف هوشمند روی ${gameQueue(w)} Mbit/s (۹۰٪ سرعت اندازه‌گیری‌شده)` : "صف هوشمند: سرعت اندازه گرفته نشد؛ بعد از ساخت در محدودیت‌های تانل حدود ۹۰٪ سرعت تست را بدهید"}</dd></div>` : ""}
        <div><dt>پورت</dt><dd class="ltr">${w.port ? `${mode.tcp ? "TCP" : "UDP"} ${w.port}` : mode.stream ? "تصادفی" : mode.udp || mode.tcp ? "خودکار" : "پروتکل خام"}${mode.stream ? " + ۲ پورت پشتیبان" : ""}</dd></div>
        ${mode.stream ? html`<div><dt>جهت</dt><dd>${dirName(w.reverse)}: ${w.reverse ? "سرور مقابل به این سرور وصل می‌شود" : "این سرور به سرور مقابل وصل می‌شود"}</dd></div>` : ""}
      </dl>
      <div class="grid2">
        <div class="field"><label for="w-fwd">فوروارد پورت‌های این سرور (اختیاری)</label><input id="w-fwd" class="input ltr" data-bind="wiz.fwd" value="${w.fwd}" placeholder="443, 8443, 2053-2096"><span class="help">ترافیک این پورت‌ها از داخل تانل به مقصد می‌رود.</span></div>
        <div class="field"><label for="w-to">پورت مقصد (اختیاری)</label><input id="w-to" class="input ltr" type="number" data-bind="wiz.fwdTo" value="${w.fwdTo}" placeholder="همان پورت"><span class="help">مثلاً 2053 اینجا به 443 آن طرف.</span></div>
        <div class="field"><span class="lbl">پروتکل فوروارد</span><div class="seg">${fwdProtos(wizFmode(w)).map((p) => html`<button type="button" class="${fwdProto(wizFmode(w), w.proto) === p ? "on" : ""}" data-act="set" data-path="wiz.proto" data-value="${p}">${p === "both" ? "TCP + UDP" : p.toUpperCase()}</button>`)}</div></div>
        ${mode.stream ? streamModeNote
          : html`${modeField("wiz", w.fmode, w.dest === "remote", "fmode")}${sitHint(w.dest !== "remote" && w.mode, w.fmode)}`}
        ${pverField("wiz", w, wizFmode(w))}
        ${w.dest === "remote" ? "" : targetField("wiz", w, wizFmode(w))}
        ${mode.stream ? "" : wizPoolField(w)}
      </div>
      ${mode.stream ? "" : html`<details class="adv" ${w.adv ? "open" : ""} data-act="adv-toggle" data-name="">
        <summary>تنظیمات پیشرفته <span class="faint">MTU، PMTUD، TTL، DSCP${wizProt(w, mode) === "obfs" || w.mode === "obfs" ? "، padding" : ""}</span>${I.chev(16)}</summary>
        <div class="in">${advancedFields("wiz", w, wizProt(w, mode) === "obfs" ? "obfs" : w.mode, false, true, w.mode)}</div>
      </details>`}`;
  }

  // Where the tunnel's inner addresses come from: a preset, or ranges given
  // here, which can also be saved as a new preset.
  function wizPoolField(w) {
    const manual = w.pool === "manual";
    return html`<div class="field full"><label for="w-pool">Address pool (آدرس داخلی تانل)</label>
      <select id="w-pool" class="input" data-bind="wiz.pool">${S.ov.pools.map((p) => html`<option value="${p.name}" ${w.pool === p.name ? "selected" : ""}>${poolTitle(p)}${p.builtin ? "" : ` (${p.net4 || p.net6})`}</option>`)}<option value="manual" ${manual ? "selected" : ""}>دستی یا پریست جدید…</option></select>
      ${manual ? html`<div class="grid2" style="margin-top:10px">
        <div class="field"><label for="w-p4">محدوده‌ی IPv4</label><input id="w-p4" class="input ltr" data-bind="wiz.pool4" value="${w.pool4}" placeholder="10.50.0.0/24"></div>
        <div class="field"><label for="w-p6">محدوده‌ی IPv6 (اختیاری)</label><input id="w-p6" class="input ltr" data-bind="wiz.pool6" value="${w.pool6}" placeholder="fd50::/48"></div>
        <div class="field full"><label for="w-pn">ذخیره به‌عنوان پریست (اختیاری)</label><div class="pf"><input id="w-pn" class="input" dir="auto" maxlength="32" data-bind="wiz.poolName" value="${w.poolName}" placeholder="نام پریست"><button type="button" class="btn sm" data-act="wiz-pool-save">${I.plus(14)} ذخیره</button></div></div>
      </div>` : ""}
      <span class="help">${manual ? "این تانل یک /30 از IPv4 و یک /64 از IPv6 این محدوده را می‌گیرد؛ 6to4 و تانل‌های روی NeoObfs یک بلوک هم برای لایه‌ی زیرین لازم دارند." : "اگر آدرس‌ها با شبکه‌ی سرور یا تانل دیگری تداخل دارند، پریست دیگری بردارید. پریست‌ها در تنظیمات مدیریت می‌شوند."}</span></div>`;
  }

  // The live check of the current choices.
  function checkKey(w, mode, opts) {
    const k = { mode, u: w.underlay, l6: w.underlay === "ipv6" ? w.local6 : "", r6: w.underlay === "ipv6" ? w.remote6 : "" };
    if (opts) Object.assign(k, { p: w.protect, e: w.protect === "ipsec" ? w.encrypt : "", port: +w.port || 0 });
    if (opts && isStreamMode(mode)) Object.assign(k, { rev: !!w.reverse, sni: (w.sni || "").trim() });
    return JSON.stringify(k);
  }
  function checkChip(c) {
    if (!c) return "";
    if (c.state === "run") return html`<span class="chk run"><i class="spin"></i>بررسی اتصال…</span>`;
    if (c.state === "skip") return html`<span class="chk run">بعد از ساخت تست می‌شود</span>`;
    if (c.state === "old") return html`<span class="chk warn" title="${c.err || ""}">${I.alert(12)} سرور مقابل به‌روز نیست</span>`;
    if (c.state === "ok") return html`<span class="chk ok">${I.check(12)} وصل شد · ${ms(c.ping)}</span>`;
    return html`<span class="chk ${c.cfg ? "warn" : "bad"}" title="${c.err || ""}">${I.x(12)} ${c.cfg ? "با تنظیمات فعلی ساخته نمی‌شود" : "وصل نشد"}</span>`;
  }
  function checkBanner(w) {
    if (!canCheck(w)) return "";
    const c = w.checks[checkKey(w, w.mode, true)];
    if (!c || c.state === "run") return html`<div class="check run"><i class="spin"></i><div><b>بررسی اتصال با این تنظیمات…</b><span>تانل چند ثانیه روی هر دو سرور بالا می‌آید و پاک می‌شود.</span></div></div>`;
    if (c.state === "old") return html`<div class="check bad">${I.alert(16)}<div><b>سرور مقابل باید به‌روز شود</b><span>نسخه‌ی NeoTunnel روی ${w.peer} تانل اتصال را پیش از ساخت تست نمی‌کند. آن را از صفحه‌ی سرورها به‌روز کنید؛ ساختن تانل مشکلی ندارد.</span></div></div>`;
    if (c.state === "skip") return html`<div class="check">${I.mask(16)}<div><b>NeoObfs پیش از ساخت سنجیده نمی‌شود</b><span>در فضای کاربر اجرا می‌شود؛ بعد از ساخت، با «تست سرعت» همان تانل بررسی کنید.</span></div></div>`;
    if (c.state === "ok" && c.moved) return html`<div class="check bad">${I.alert(16)}<div><b>پورت <span class="ltr">${c.moved}</span> روی یکی از دو سرور اشغال است</b><span>اتصال روی پورت آزاد <span class="ltr">${c.port}</span> بررسی شد و وصل شد (پینگ ${ms(c.ping)}). تانل با پورت اشغال ساخته نمی‌شود؛ همین پورت را بردارید یا پورت دیگری بدهید.</span>
      <span><button type="button" class="btn sm primary" data-act="set" data-path="wiz.port" data-value="${c.port}">استفاده از پورت ${c.port}</button></span></div></div>`;
    if (c.state === "ok") return html`<div class="check ok">${I.check(16)}<div><b>با این تنظیمات وصل می‌شود</b><span>پینگ ${ms(c.ping)}${c.loss ? `، افت ${c.loss.toFixed(0)}٪` : ""}${c.mtu ? `، MTU ${c.mtu}` : ""}</span></div></div>`;
    if (c.cfg) return html`<div class="check bad">${I.alert(16)}<div><b>این ترکیب ساخته نمی‌شود</b><span title="${c.err || ""}">${errText(c.err)}</span></div></div>`;
    return html`<div class="check bad">${I.alert(16)}<div><b>با این تنظیمات ترافیک رد نمی‌شود</b><span title="${c.err || ""}">${errText(c.err)}</span><span>${isStreamMode(w.mode) ? (w.reverse ? "پورت TCP دیگری بدهید که از آن طرف به این سرور باز است، یا مستقیم (Direct) را امتحان کنید." : "پورت TCP دیگری بدهید که بین دو سرور باز است، یا معکوس (Reverse) را امتحان کنید.") : w.mode === "tcp" ? "پورت TCP دیگری بدهید که بین دو سرور باز است." : +w.port ? "پروتکل دیگری امتحان کنید یا NeoObfs را بزنید." : "یک پورت UDP بدهید یا پروتکل دیگری امتحان کنید."}</span></div></div>`;
  }

  function wizRequest(w, peerName) {
    const set = settingsBody(w);
    const prot = wizProt(w, S.ov.modes.find((m) => m.name === w.mode) || {});
    const req = Object.assign(set, {
      mode: w.mode, name: w.name.trim(), port: +w.port || 0, forward: w.fwd.trim(), forward_to: +w.fwdTo || 0, proto: fwdProto(wizFmode(w), w.proto),
      forward_mode: wizFmode(w), proxy_version: fwdVersion(wizFmode(w), w), forward_public: w.dest !== "remote" && toPublicOf(wizFmode(w), w),
      queue_mbit: w.gaming ? gameQueue(w) : 0,
      obfs: prot === "obfs", encrypt: prot === "ipsec" ? w.encrypt : prot === "auto" ? "auto" : "",
    });
    if (w.pool === "manual") Object.assign(req, { pool4: (w.pool4 || "").trim(), pool6: (w.pool6 || "").trim() });
    else req.pool = w.pool || "";
    if (w.dest === "remote" && req.encrypt === "auto") req.encrypt = ""; // a router's end can't follow
    if (w.underlay === "ipv6") Object.assign(req, { underlay: "ipv6", local6: w.local6.trim(), remote6: w.remote6.trim() });
    if (w.dest === "remote") req.remote = w.remote.trim(); else req.peer = peerName;
    return req;
  }

  // A far server too old to test connection tunnels (see bench.errOldPeer).
  const oldPeer = (err) => /can't test connection tunnels/.test(err || "");

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
      w.checks[key] = r.status === "passed" ? { state: "ok", ping: r.ping_ms, loss: r.loss, mtu: r.mtu, moved: r.moved_from, port: r.port } : oldPeer(r.error) ? { state: "old", err: r.error } : r.status === "skipped" ? (r.error && /NeoObfs/.test(r.error) ? { state: "skip" } : { state: "bad", err: r.error, cfg: true }) : { state: "bad", err: r.error };
    } catch (e) {
      if (/در حال اجراست/.test(e.message) && tries < 8) { setTimeout(() => runCheck(opts, tries + 1), 1500); return; }
      w.checks[key] = { state: "bad", err: e.message, cfg: true };
    }
    // In gaming mode a mode that does not pass gives way to one that does.
    if (!opts && w.gaming && w.checks[key].state === "bad" && key === checkKey(w, w.mode, false)) {
      const was = w.mode;
      applyGaming(w);
      if (w.mode !== was) setTimeout(() => runCheck(false), 0);
    }
    if (S.wiz === w) drawModal();
    if (opts) runSpeed();
  }
  // Speed of the current combination under one profile, measured with a
  // temporary tunnel; the wizard keeps all three so they can be compared.
  // It runs as the speed page is set (seconds, connections), so the numbers
  // compare with that page's.
  const speedKey = (w, profile) => checkKey(w, w.mode, true) + "|" + profile + "|" + S.speed.secs + "x" + S.speed.streams;
  function speedChip(r) {
    if (!r) return html`<i class="sp faint">اندازه گرفته نشده</i>`;
    if (r.state === "run") return html`<i class="sp"><i class="spin"></i> در حال اندازه‌گیری…</i>`;
    if (r.state === "bad") return html`<i class="sp bad" title="${r.err || ""}">اندازه‌گیری نشد</i>`;
    return html`<i class="sp ok ltr">↓ ${fmtRate(r.down)} · ↑ ${fmtRate(r.up)}</i>`;
  }
  async function runSpeed(tries = 0) {
    const w = S.wiz;
    if (!w || !canCheck(w) || w.step < 3 || w.protect === "obfs" || w.mode === "obfs") return;
    const c = w.checks[checkKey(w, w.mode, true)];
    if (!c || c.state !== "ok") return; // only once it connects
    const profile = w.profile || "balanced", key = speedKey(w, profile);
    if (w.speeds[key] && w.speeds[key].state !== "run") return;
    w.speeds[key] = { state: "run" }; drawModal();
    const req = Object.assign(wizRequest(w, w.peer), { forward: "", speed: true, seconds: S.speed.secs, streams: S.speed.streams });
    try {
      const r = ((await api("POST", "check", req)) || [])[0] || {};
      w.speeds[key] = r.status === "done" ? { state: "ok", up: r.up_bps, down: r.down_bps } : { state: "bad", err: r.error };
    } catch (e) {
      if (/در حال اجراست/.test(e.message) && tries < 15) { setTimeout(() => runSpeed(tries + 1), 1500); return; }
      w.speeds[key] = { state: "bad", err: e.message };
    }
    if (S.wiz === w) drawModal();
  }

  let checkTimer = 0;
  const checkSoon = (opts) => { clearTimeout(checkTimer); checkTimer = setTimeout(() => runCheck(opts), 500); };

  async function checkAll(tries = 0) {
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
        // A kind measured on a tunnel that exists already ("6to4 [t1]") can
        // not be made a second time between the same servers.
        w.checks[k] = r.status === "passed" ? { state: "ok", ping: r.ping_ms, loss: r.loss, mtu: r.mtu, existing: / [[^]]+]$/.test(r.title || "") } : oldPeer(r.error) ? { state: "old", err: r.error } : { state: "bad", err: r.error, cfg: r.status === "skipped" };
      }
    } catch (e) {
      if (/در حال اجراست/.test(e.message) && tries < 8) {
        // A single check is still running: wait for it.
        w.checkingAll = false;
        setTimeout(() => checkAll(tries + 1), 1500);
        return;
      }
      toast(e.message, "bad");
    }
    for (const k of Object.keys(w.checks)) if (w.checks[k].state === "run") delete w.checks[k];
    w.checkingAll = false;
    if (w.gaming && gameBest(w) && w.mode !== gameBest(w)) { w.mode = gameBest(w); runCheck(true); }
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
      underlay: "ipv4", local6: ov.node.address6 || "", remote6: "", checks: {}, speeds: {}, checkingAll: false,
      protect: d.obfs ? "obfs" : d.encrypt === "auto" ? "auto" : d.encrypt ? "ipsec" : "none", encrypt: d.encrypt && d.encrypt !== "auto" ? d.encrypt : "aes-gcm-128",
      pool: d.pool || "default", pool4: "", pool6: "", poolName: "",
      fmode: d.forward_mode || "nat", reverse: false, sni: "", conns: "", pver: 2, profile: "balanced", lanes: 1, mtuMode: d.mtu_probe ? "probe" : "auto", mtu: "", ttl: "", dscp: "", pmtud: "on", padding: d.padding || "", keepalive: "", peer_key: "", adv: false,
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
    f.mode = fwdModeFor(t, f.mode);
    return html`${modalHead("فوروارد پورت جدید")}
      <div class="body">${ts.length ? html`<div class="grid2">
          <div class="field full"><label for="f-t">از طریق تانل</label><select id="f-t" class="input" data-bind="form.tunnel" data-redraw="1">${ts.map((x) => html`<option value="${x.name}" ${f.tunnel === x.name ? "selected" : ""}>${tname(x)} به ${pname(x.peer) || x.far_ip} (${x.there4 || x.there6 || kindTitle(x)})</option>`)}</select></div>
          <div class="field"><label for="f-p">پورت(های) این سرور</label><input id="f-p" class="input ltr" data-bind="form.ports" value="${f.ports || ""}" placeholder="443, 2053-2096"></div>
          <div class="field"><label for="f-to">پورت مقصد (اختیاری)</label><input id="f-to" class="input ltr" type="number" data-bind="form.to" value="${f.to || ""}" placeholder="همان پورت"></div>
          <div class="field"><span class="lbl">پروتکل</span><div class="seg">${fwdProtos(f.mode).map((p) => html`<button type="button" class="${fwdProto(f.mode, f.proto || "both") === p ? "on" : ""}" data-act="set" data-path="form.proto" data-value="${p}">${p === "both" ? "TCP + UDP" : p.toUpperCase()}</button>`)}</div></div>
          ${t && t.stream ? streamModeNote : html`${modeField("form", f.mode, remote)}${sitHint(t && !remote && t.mode, f.mode)}`}
          ${pverField("form", f, f.mode)}
          ${targetField("form", f, f.mode)}
          ${backupField("form", f, f.tunnel)}
        </div>` : html`<div class="muted">اول یک تانل بسازید.</div>`}</div>
      <footer><span class="grow"></span><button class="btn ghost" data-act="close">انصراف</button><button class="btn primary" data-act="add-forward-go" ${ts.length ? "" : "disabled"}>${I.plus(16)} افزودن فوروارد</button></footer>`;
  }

  function addProxyView() {
    const f = S.form, ts = visible().filter((t) => !t.managed && t.peer);
    if (!f.tunnel && ts.length) f.tunnel = ts[0].name;
    const open = f.access === "all";
    return html`${modalHead("پروکسی جدید")}
      <div class="body">${ts.length ? html`<div class="grid2">
          <div class="field full"><label for="p-t">از طریق تانل</label><select id="p-t" class="input" data-bind="form.tunnel" data-redraw="1">${ts.map((x) => html`<option value="${x.name}" ${f.tunnel === x.name ? "selected" : ""}>${tname(x)} به ${pname(x.peer)} (${kindTitle(x)})</option>`)}</select>
            <span class="help">اتصال‌ها از داخل همین تانل به ${tunnel(f.tunnel) ? tunnel(f.tunnel).peer : "سرور مقصد"} می‌روند و از آنجا به اینترنت؛ مقصد، IP آن سرور را می‌بیند.</span></div>
          <div class="field"><label for="p-port">پورت</label><div class="pf"><input id="p-port" class="input ltr" type="number" min="1" max="65535" data-bind="form.ports" value="${f.ports || ""}" placeholder="1080"><button type="button" class="btn sm" data-act="random-port" data-path="form.ports" data-proto="tcp" title="یک پورت آزاد تصادفی بین ۲۰۰۰۰ و ۶۰۰۰۰">${I.dice(14)} تصادفی</button></div>
            <span class="help">یک پورت هم SOCKS5 است هم HTTP.</span></div>
          <div class="field"><span class="lbl">چه کسی وصل شود</span><div class="seg"><button type="button" class="${open ? "" : "on"}" data-act="set" data-path="form.access" data-value="local">فقط همین سرور</button><button type="button" class="${open ? "on" : ""}" data-act="set" data-path="form.access" data-value="all">همه، با رمز</button></div>
            <span class="help">${open ? "روی همه‌ی IPهای سرور گوش می‌دهد و پورتش در فایروال باز می‌شود؛ بدون نام کاربری و رمز ساخته نمی‌شود." : "فقط برنامه‌های همین سرور (127.0.0.1)؛ از بیرون دیده نمی‌شود."}</span></div>
          ${open ? html`<div class="field"><label for="p-u">نام کاربری</label><input id="p-u" class="input ltr" data-bind="form.user" value="${f.user || ""}" autocomplete="off"></div>
          <div class="field"><label for="p-pw">رمز</label><input id="p-pw" class="input ltr" data-bind="form.pass" value="${f.pass || ""}" autocomplete="off"></div>` : ""}
          ${backupField("form", f, f.tunnel)}
        </div>` : html`<div class="muted">پروکسی به تانلی نیاز دارد که سرور مقصدش NeoTunnel دارد؛ اول یکی بسازید.</div>`}</div>
      <footer><span class="grow"></span><button class="btn ghost" data-act="close">انصراف</button><button class="btn primary" data-act="add-proxy-go" ${ts.length ? "" : "disabled"}>${I.plus(16)} ساخت پروکسی</button></footer>`;
  }

  // Renaming changes only the display name; the ID (name) stays, so the
  // running tunnel and everything keyed by it are untouched.
  const renameBtn = (kind, name) => html`<button type="button" class="btn sm ghost icon rename" data-act="rename" data-kind="${kind}" data-name="${name}" title="تغییر نام" aria-label="تغییر نام">${I.edit(14)}</button>`;
  function renameView() {
    const m = S.modal, t = m.kind === "tunnel" ? tunnel(m.name) : null, p = m.kind === "server" ? peer(m.name) : null;
    const cur = (t || p || {}).label || "";
    return html`${modalHead(m.kind === "tunnel" ? "تغییر نام تانل" : "تغییر نام سرور")}<form data-form="label"><div class="body stack">
        <div class="field"><label for="r-label">نام نمایشی</label><input id="r-label" class="input" name="label" dir="auto" maxlength="40" value="${cur}" placeholder="${m.name}" autocomplete="off">
        <span class="help">شناسه <span class="ltr">${m.name}</span> می‌ماند و تانل‌ها، کلیدها و پورت‌ها با همان کار می‌کنند؛ فقط نامی که در پنل می‌بینید عوض می‌شود.${m.kind === "tunnel" ? " سرور مقصد هم همین نام را می‌بیند." : ""} خالی بگذارید تا خود شناسه نشان داده شود.</span></div></div>
      <footer><span class="grow"></span><button class="btn ghost" type="button" data-act="close">انصراف</button><button class="btn primary" type="submit">${I.check(16)} ذخیره</button></footer></form>`;
  }

  function confirmView(title, text, act, data, label, icon = I.trash) {
    return html`${modalHead(title)}<div class="body"><div>${text}</div></div>
      <footer><span class="grow"></span><button class="btn ghost" data-act="close">انصراف</button><button class="btn danger" data-act="${act}" ${raw(Object.entries(data).map(([k, v]) => `data-${k}="${esc(v)}"`).join(" "))}>${icon(16)} ${label}</button></footer>`;
  }

  // ---------- events ----------
  // data-bind paths: "wiz.port", "form.ports", "tset.<name>.mtu", "fwdEdit.<k>.mode", "defs.padding".
  function setPath(path, value) {
    const parts = path.split(".");
    const roots = { wiz: S.wiz, form: S.form, speed: S.speed, tset: S.tset, fwdEdit: S.fwdEdit, defs: S.defs, fwdEditor: S.fwdEditor, fe: S.fe, ce: S.ce, gd: S.gd, td: S.td, fm: S.fm, apid: S.apid };
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
    if (t.dataset.act === "st-subject") { S.st.subject = t.value; draw(); }
    if (t.dataset.act === "safe-pref") { S.safe = t.checked; store("nt-safe", S.safe ? "1" : "0"); if (t.closest(".modal")) drawModal(); else draw(); }
    if (t.dataset.act === "def-check") { S.defs[t.dataset.key] = t.checked; S.edit = true; }
    if (t.dataset.act === "fe-check") { S.fe[t.dataset.key] = t.checked; S.edit = true; }
    if (t.dataset.act === "gd-check") { S.gd[t.dataset.key] = t.checked; S.edit = true; draw(); }
    if (t.dataset.act === "td-alert") { const m = S.td.mute.filter((x) => x !== t.dataset.key); S.td.mute = t.checked ? m : m.concat(t.dataset.key); S.edit = true; }
    if (t.dataset.act === "gd-port") { if (t.value === "auto") delete S.gd.ports[t.dataset.key]; else S.gd.ports[t.dataset.key] = t.value; S.edit = true; }
    if (t.dataset.act === "fe-group") { const g = S.fe.groups.filter((x) => x !== t.value); S.fe.groups = t.checked ? g.concat(t.value) : g; S.edit = true; }
    if (t.dataset.act === "restore-file" && t.files && t.files[0]) inspectBackup(t.files[0]);
    if (t.dataset.act === "st-check") { setPath(t.dataset.path, t.checked); if (!t.closest(".modal")) S.edit = true; }
    if (t.dataset.act === "bk-add" && t.value) { S[t.dataset.path].backups.push(t.value); drawModal(); }
  });
  // Dragging a row of the ports table to another place.
  let portDrag = null;
  const portRow = (e) => e.target instanceof Element ? e.target.closest("tr[data-port-drag]") : null;
  document.addEventListener("dragstart", (e) => {
    const tr = portRow(e);
    if (!tr) return;
    portDrag = tr.dataset.portDrag;
    e.dataTransfer.effectAllowed = "move";
    e.dataTransfer.setData("text/plain", portDrag);
    tr.classList.add("dragging");
  });
  document.addEventListener("dragover", (e) => {
    const tr = portRow(e);
    if (!tr || !portDrag) return;
    e.preventDefault();
    for (const el of document.querySelectorAll("tr.drag-over")) if (el !== tr) el.classList.remove("drag-over");
    if (tr.dataset.portDrag !== portDrag) tr.classList.add("drag-over");
  });
  document.addEventListener("drop", (e) => {
    const tr = portRow(e);
    if (!tr || !portDrag) return;
    e.preventDefault();
    const list = S.ov.forwards.filter((f) => f.mode !== "socks");
    const from = list.findIndex((f) => portKey(f) === portDrag), to = list.findIndex((f) => portKey(f) === tr.dataset.portDrag);
    portDrag = null;
    if (from < 0 || to < 0 || from === to) { draw(); return; }
    list.splice(to, 0, list.splice(from, 1)[0]);
    savePortOrder(list);
  });
  document.addEventListener("dragend", () => {
    portDrag = null;
    for (const el of document.querySelectorAll("tr.dragging, tr.drag-over")) el.classList.remove("dragging", "drag-over");
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
      if (form === "node") {
        if ((d.label || "").trim() !== (S.ov.node.label || "")) await api("POST", "labels", { kind: "self", label: d.label });
        await api("POST", "settings/node", { name: d.name.trim(), address: d.address.trim() }); await refresh(); toast("ذخیره شد");
      }
      if (form === "label") {
        const m = S.modal, ch = await api("POST", "labels", { kind: m.kind, name: m.name, label: d.label });
        closeModal(); await refresh(); reportChange(ch, "نام ذخیره شد"); return;
      }
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
        const ch = await api("PATCH", "tunnels/" + encodeURIComponent(name) + safeQ(), Object.assign(settingsBody(st), { port: +st.port || 0 }));
        delete S.tset[name];
        reportChange(ch, "تنظیمات تانل ذخیره شد"); safeStarted(ch);
      }
      S.edit = false; await refresh();
    } catch (err) { toast(err.message, "bad"); }
  });

  const actions = {
    menu() { if (S.menu && !S.menuLeaving) closeMenu(); else openMenu(); },
    "menu-close"() { closeMenu(); },
    theme() { S.theme = S.theme === "dark" ? "light" : "dark"; store("nt-theme", S.theme); document.documentElement.dataset.theme = S.theme; draw(); },
    async logout() { S.menu = false; await api("POST", "logout").catch(() => {}); S.authed = false; S.ov = null; draw(); },
    copy(el) { copy(el.dataset.text); },
    close() { closeModal(); },
    privacy() { S.priv = !S.priv; store("nt-priv", S.priv ? "1" : "0"); document.documentElement.classList.toggle("private", S.priv); draw(); },
    "ping-open"(el) { S.pm = { name: el.dataset.name, range: S.tpr || "24h" }; openModal(pingView, { passive: true, wide: true }); },
    "pm-range"(el) { S.pm.range = el.dataset.range; drawModal(); },
    "port-chart"(el) { openPort(el.dataset.key, "traffic"); },
    async mtr(el) {
      const name = el.dataset.name;
      S.mtrv = { name, busy: true };
      if (!S.modal || S.modal.view !== mtrView) openModal(mtrView, { wide: true }); else drawModal();
      try { S.mtrv.rep = await api("POST", `tunnels/${encodeURIComponent(name)}/mtr`); } catch (e) { S.mtrv.err = e.message; }
      S.mtrv.busy = false;
      if (S.modal) drawModal();
    },
    "port-sale"(el) { openPort(el.dataset.key, "sale"); },
    "fm-tab"(el) { S.fm.tab = el.dataset.tab; if (S.fm.tab === "clients") loadClients(); drawModal(); },
    "port-clients"() { loadClients(); },
    "port-reset"(el) {
      const f = fwdOf(el.dataset.key); if (!f) return;
      openModal(() => confirmView("صفر کردن آمار پورت", html`آمار ترافیک پورت <b class="ltr">${f.ports}</b> (امروز، ماه‌ها، روز به روز و از ابتدا) صفر شود؟${f.plan ? " مصرف دوره‌ی فروش خریدار دست نمی‌خورد." : ""}`, "reset-traffic-go", { name: "@port:" + portId(f) }, "صفر کردن"));
    },
    async "plan-save"() {
      const p = S.fm, f = fwdOf(p.key), d = p.draft; if (!f || !d) return;
      const cur = f.plan || {};
      let expires = cur.expires || 0;
      if (String(d.days) !== String(d.days0)) expires = +d.days > 0 ? Math.floor(Date.now() / 1000) + Math.round(+d.days) * 86400 : 0;
      const plan = { customer: String(d.customer || "").trim(), rate_mbit: Math.round(+d.rate || 0), quota_gb: +d.quota || 0, reset_days: Math.round(+d.reset || 0), expires, max_conns: Math.round(+d.conns || 0), max_ips: Math.round(+d.ips || 0), paused: !!cur.paused };
      await planCall("forwards/plan", { tunnel: f.tunnel, ports: f.ports, plan }, f.plan ? "تغییرات فروش ذخیره شد" : "پورت فروشی شد");
    },
    async "plan-renew"() {
      const p = S.fm, f = fwdOf(p.key); if (!f) return;
      const reset = !!p.draft.renewReset;
      await planCall("forwards/renew", { tunnel: f.tunnel, ports: f.ports, days: Math.round(+p.draft.renew || 0), reset }, reset ? "تمدید شد؛ حجم دوباره کامل است" : "تمدید شد؛ حجم مصرفی سر جایش ماند");
    },
    async "plan-pause"() {
      const f = fwdOf(S.fm.key); if (!f || !f.plan) return;
      await planCall("forwards/plan", { tunnel: f.tunnel, ports: f.ports, plan: Object.assign({}, f.plan, { paused: !f.plan.paused }) }, f.plan.paused ? "پورت دوباره باز شد" : "پورت تا ادامه بسته شد");
    },
    "plan-rm-ask"() { S.fm.rmAsk = !S.fm.rmAsk; drawModal(); },
    async "plan-remove"() {
      const f = fwdOf(S.fm.key); if (!f) return;
      await planCall("forwards/plan", { tunnel: f.tunnel, ports: f.ports, plan: null }, "فروش لغو شد؛ پورت بدون محدودیت است");
    },
    "fm-range"(el) { S.fm.range = el.dataset.range; drawModal(); },
    "cpu-open"() { S.cw = { range: "10m" }; openModal(cpuView, { passive: true, wide: true }); },
    "cw-range"(el) { S.cw.range = el.dataset.range; drawModal(); },
    "update-ask"() { if (S.menu) closeMenu(true); openModal(updateView); },
    "update-check"() { S.upd = null; draw(); loadUpdate(true); },
    async "update-run"(el) {
      el.disabled = true;
      try {
        const r = await api("POST", "update");
        S.upd = r; S.updRun = { to: r.updating || r.latest, since: Date.now() };
        drawModal(); draw();
        setTimeout(pollUpdate, 4000);
      } catch (e) { el.disabled = false; toast(e.message, "bad"); }
    },
    "update-done"() { S.updRun = null; closeModal(); loadUpdate(true); },
    "close-overlay"(el, e) { if (e.target === el) closeModal(); },
    set(el) {
      setPath(el.dataset.path, el.dataset.value);
      if (el.dataset.path === "wiz.profile") setTimeout(() => runSpeed(), 0);
      if (["wiz.port", "wiz.reverse"].includes(el.dataset.path)) checkSoon(true);
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
    "toggle-tunnel"(el) {
      const n = el.dataset.name; S.open = S.open === n ? null : n; if (S.open !== n) delete S.tset[n]; S.edit = false; draw();
      const d = S.open && $(".tdetail");
      if (d) d.scrollIntoView({ behavior: reduceMotion ? "auto" : "smooth", block: "nearest" });
    },
    dtab(el) { S.dtab[el.dataset.name] = el.dataset.tab; if (el.dataset.tab === "events") delete S.ev[el.dataset.name]; if (el.dataset.tab === "nightly") delete S.night[el.dataset.name]; draw(); },
    tpr(el) { S.tpr = el.dataset.range; store("nt-tpr", S.tpr); draw(); },
    "reset-tset"(el) { delete S.tset[el.dataset.name]; S.edit = false; draw(); },
    async mtu(el) {
      const n = el.dataset.name;
      toast("در حال اندازه‌گیری مسیر؛ تا یک دقیقه طول می‌کشد…");
      try { S.mtu[n] = await api("POST", `tunnels/${encodeURIComponent(n)}/mtu`); S.open = n; S.drawer = null; location.hash = "#/tunnels"; await refresh(); draw(); }
      catch (e) { toast(e.message, "bad"); }
    },
    wizard(el) { if (S.menu) closeMenu(true); openWizard(el.dataset.peer ? { peer: el.dataset.peer, mode: el.dataset.mode || "", port: el.dataset.port || "" } : {}); if (el.dataset.gaming) applyGaming(Object.assign(S.wiz, { gaming: true, mode: el.dataset.mode || S.wiz.mode })); if (el.dataset.peer && !el.dataset.mode) S.wiz.mode = "6to4"; S.drawer = null; drawModal(); },
    "wiz-dest"(el) {
      Object.assign(S.wiz, { dest: el.dataset.dest, peer: el.dataset.name || S.wiz.peer });
      const p = wizPeer(S.wiz); if (p && p.address6) S.wiz.remote6 = p.address6;
      drawModal();
    },
    "wiz-mode"(el) { S.wiz.mode = el.dataset.mode; drawModal(); runCheck(false); },
    "wiz-gaming"(el) {
      const w = S.wiz;
      w.gaming = !!el.dataset.value;
      if (w.gaming) {
        applyGaming(w);
        // The quickest mode comes from checking them all, once.
        if (canCheck(w) && w.underlay !== "ipv6" && GAME_MODES.some((n) => !w.checks[checkKey(w, n, false)])) checkAll();
        else runCheck(false);
      }
      drawModal();
    },
    "wiz-underlay"(el) {
      const w = S.wiz; w.underlay = el.dataset.value;
      if (w.underlay === "ipv6" && SIT_MODES.includes(w.mode)) w.mode = "gre";
      drawModal(); runCheck(false);
    },
    "wiz-protect"(el) { S.wiz.protect = el.dataset.value; drawModal(); runCheck(true); },
    "wiz-check-all"() { checkAll(); },
    "wiz-next"() { const w = S.wiz; w.step++; stepAnim(w, "next"); drawModal(); if (w.step === 2) runCheck(false); if (w.step === 3) { runCheck(true); runSpeed(); } },
    "wiz-back"() { S.wiz.step--; stepAnim(S.wiz, "back"); drawModal(); },
    "wiz-create"() { createTunnel(); },
    setup(el) { showSetup(el.dataset.name); },
    async mikrotik(el) {
      const n = el.dataset.name;
      try { const r = await api("GET", `tunnels/${encodeURIComponent(n)}/mikrotik`); openModal(() => mikrotikView(n, r.commands)); }
      catch (e) { toast(e.message, "bad"); }
    },
    "add-server"() { S.form = {}; openModal(addServerView); },
    async "add-server-go"() {
      try {
        const s = await api("POST", "peers", { name: (S.form.name || "").trim(), address: (S.form.address || "").trim() });
        await refresh(); openModal(() => setupView(s)); location.hash = "#/servers";
      } catch (e) { toast(e.message, "bad"); }
    },
    "add-proxy"() { S.form = { ports: "1080", access: "local" }; openModal(addProxyView); },
    async "add-proxy-go"() {
      const f = S.form, open = f.access === "all";
      try {
        const ch = await api("POST", "forwards", { tunnel: f.tunnel, ports: String(f.ports || "").trim(), proto: "tcp", mode: "socks",
          listen: open ? "0.0.0.0" : "", auth: open || f.user ? `${(f.user || "").trim()}:${f.pass || ""}` : "", backups: f.backups || [] });
        closeModal(); await refresh(); reportChange(ch, "پروکسی ساخته شد");
      } catch (e) { toast(errText(e.message), "bad"); }
    },
    "add-forward"(el) { S.form = { tunnel: (el && el.dataset.tunnel) || undefined, proto: "both", mode: (S.ov.defaults && S.ov.defaults.forward_mode) || "nat", pver: 2 }; openModal(addForwardView); },
    async "add-forward-go"() {
      const f = S.form;
      try {
        const ch = await api("POST", "forwards", { tunnel: f.tunnel, ports: (f.ports || "").trim(), proto: fwdProto(f.mode, f.proto || "both"), to: +f.to || 0, mode: f.mode, proxy_version: fwdVersion(f.mode, f), public: toPublicOf(f.mode, f), backups: f.backups || [], best: !!(f.best && (f.backups || []).length) });
        closeModal(); await refresh(); reportChange(ch, "فوروارد اضافه شد");
      } catch (e) { toast(e.message, "bad"); }
    },
    "edit-fwd"(el) {
      const f = S.ov.forwards.find((x) => x.tunnel + "|" + x.ports === el.dataset.key);
      if (!f) return;
      const pv = f.proxy_version || 0, cut = f.auth ? f.auth.indexOf(":") : -1;
      S.tip = null;
      S.fwdEditor = { key: el.dataset.key, tunnel: f.tunnel, ports: f.ports, to: f.to || "", proto: f.proto, mode: f.mode,
        pver: f.mode === "proxy" ? pv || 2 : 2, rver: f.mode === "relay" ? pv : 0, toPublic: !!f.to_public, backups: (f.backups || []).slice(), best: !!f.best,
        access: f.listen && f.listen !== "127.0.0.1" ? "all" : "local", user: cut < 0 ? "" : f.auth.slice(0, cut), pass: cut < 0 ? "" : f.auth.slice(cut + 1), busy: false };
      openModal(fwdEditView);
    },
    "cancel-fwd"(el) { delete S.fwdEdit[el.dataset.key]; S.edit = false; draw(); },
    async "save-fwd"() {
      const e = S.fwdEditor, f = e && S.ov.forwards.find((x) => x.tunnel + "|" + x.ports === e.key);
      if (!f) return;
      const socks = f.mode === "socks", open = e.access === "all";
      const req = socks
        ? { proto: "tcp", mode: "socks", listen: open ? (f.listen && f.listen !== "127.0.0.1" ? f.listen : "0.0.0.0") : "", auth: open || e.user ? `${(e.user || "").trim()}:${e.pass || ""}` : "" }
        : { proto: fwdProto(e.mode, e.proto), to: +e.to || 0, mode: e.mode, proxy_version: fwdVersion(e.mode, e), public: toPublicOf(e.mode, e) };
      e.busy = true; drawModal();
      try {
        const ch = await api("POST", "forwards/update" + safeQ(), Object.assign(req, { was_tunnel: f.tunnel, was_ports: f.ports, tunnel: e.tunnel, ports: String(e.ports || "").trim(), backups: e.backups || [], best: !!(e.best && (e.backups || []).length) }));
        S.modal = null; S.fwdEditor = null; drawModal();
        await refresh(); reportChange(ch, socks ? "پروکسی ذخیره شد" : "پورت ذخیره شد"); safeStarted(ch); draw();
      } catch (err) { e.busy = false; drawModal(); toast(errText(err.message), "bad"); }
    },
    "bk-move"(el) {
      const list = S[el.dataset.path].backups, i = +el.dataset.i, j = i + +el.dataset.dir;
      if (j < 0 || j >= list.length) return;
      [list[i], list[j]] = [list[j], list[i]];
      drawModal();
    },
    "bk-del"(el) { S[el.dataset.path].backups.splice(+el.dataset.i, 1); drawModal(); },
    "port-sort"(el) { S.portSort = el.dataset.value; store("nt-port-sort", S.portSort); draw(); },
    "port-move"(el) {
      const list = S.ov.forwards.filter((f) => f.mode !== "socks"), i = list.findIndex((f) => portKey(f) === el.dataset.key), j = i + +el.dataset.dir;
      if (i < 0 || j < 0 || j >= list.length) return;
      [list[i], list[j]] = [list[j], list[i]];
      savePortOrder(list);
    },
    async "pool-save"() {
      const e = S.pe;
      try {
        await api("POST", "pools", { name: (e.name || "").trim(), net4: (e.net4 || "").trim(), net6: (e.net6 || "").trim(), was: e.was || "" });
        S.pe = null;
        await refresh(); toast("پریست ذخیره شد"); draw();
      } catch (err) { toast(errText(err.message), "bad"); }
    },
    "pool-edit"(el) { const p = S.ov.pools.find((x) => x.name === el.dataset.name); if (p) { S.pe = { name: p.name, net4: p.net4 || "", net6: p.net6 || "", was: p.name }; draw(); } },
    "pool-cancel"() { S.pe = null; draw(); },
    async "pool-del"(el) {
      try { await api("POST", "pools/remove", { name: el.dataset.name }); if (S.defs && S.defs.pool === el.dataset.name) S.defs.pool = ""; await refresh(); toast("پریست حذف شد"); draw(); }
      catch (err) { toast(errText(err.message), "bad"); }
    },
    async "wiz-pool-save"() {
      const w = S.wiz, name = (w.poolName || "").trim();
      try {
        await api("POST", "pools", { name, net4: (w.pool4 || "").trim(), net6: (w.pool6 || "").trim() });
        await refresh();
        w.pool = name; w.pool4 = w.pool6 = w.poolName = ""; toast("پریست ذخیره شد"); drawModal();
      } catch (err) { toast(errText(err.message), "bad"); }
    },
    tip(el) { S.tip = S.tip === el.dataset.id ? null : el.dataset.id; if (el.closest(".modal")) drawModal(); else draw(); },
    async "safe-keep"(el) {
      try { const r = await api("POST", `safe/${el.dataset.id}/keep`); await refresh(); if (r.outcome && r.outcome.kept) toast("تغییر نگه داشته شد"); else if (r.pending) toast("پنل تأیید کرد؛ منتظر تأیید سرور مقابل…"); }
      catch (e) { toast(e.message, "bad"); await refresh(); }
    },
    async "safe-undo"(el) {
      try { await api("POST", `safe/${el.dataset.id}/undo`); await refresh(); toast("تنظیمات قبلی برگردانده شد"); }
      catch (e) { toast(e.message, "bad"); await refresh(); }
    },
    "safe-dismiss"(el) { S.safeSeen = el.dataset.id; store("nt-safe-seen", S.safeSeen); draw(); },
    "del-forward"(el) { openModal(() => confirmView("حذف فوروارد", html`فوروارد پورت <b class="ltr">${el.dataset.ports}</b> روی تانل <b>${el.dataset.tunnel}</b> حذف شود؟`, "del-forward-go", { tunnel: el.dataset.tunnel, ports: el.dataset.ports }, "حذف فوروارد")); },
    async "del-forward-go"(el) {
      try { const ch = await api("POST", "forwards/remove", { tunnel: el.dataset.tunnel, ports: el.dataset.ports }); closeModal(); await refresh(); reportChange(ch, "فوروارد حذف شد"); }
      catch (e) { toast(e.message, "bad"); }
    },
    async "random-port"(el) {
      try {
        const r = await api("GET", `ports/random?proto=${el.dataset.proto}`);
        setPath(el.dataset.path, String(r.port));
        if (el.closest(".modal")) { drawModal(); if (S.wiz && S.wiz.step >= 3) checkSoon(true); }
        else { S.edit = true; draw(); }
      } catch (e) { toast(e.message, "bad"); }
    },
    async "tunnel-restart"(el) {
      const n = el.dataset.name;
      if (S.restarting[n]) return;
      S.restarting[n] = true; draw();
      try { await api("POST", `tunnels/${encodeURIComponent(n)}/restart`); toast(`${n} ری‌استارت شد`); }
      catch (e) { toast(`ری‌استارت ${n}: ${e.message}`, "bad"); }
      delete S.restarting[n]; await refresh();
    },
    async "tunnel-power"(el) {
      const t = tunnel(el.dataset.name); if (!t) return;
      if (S.modal) closeModal();
      const l = Object.assign({}, t.limits, { disabled: !t.limits.disabled });
      try { const ch = await api("POST", `tunnels/${encodeURIComponent(t.name)}/limits` + safeQ(), l); await refresh(); reportChange(ch, l.disabled ? "تانل خاموش شد" : "تانل روشن شد"); safeStarted(ch); draw(); }
      catch (e) { toast(e.message, "bad"); }
    },
    async "save-limits"(el) {
      const t = tunnel(el.dataset.name), st = S.tset[el.dataset.name]; if (!t || !st) return;
      const queue = st.queueOn ? Math.round(+st.queue || queueGuess(t)) : 0;
      if (st.queueOn && !queue) { toast("سرعت صف هوشمند را بدهید (حدود ۹۰٪ سرعت تست)", "bad"); return; }
      const l = { disabled: t.limits.disabled, rate_mbit: Math.round(+st.rate || 0), quota_gb: +st.quota || 0, queue_mbit: queue };
      try { const ch = await api("POST", `tunnels/${encodeURIComponent(t.name)}/limits` + safeQ(), l); S.edit = false; await refresh(); reportChange(ch, "محدودیت‌ها ذخیره شد"); safeStarted(ch); draw(); }
      catch (e) { toast(e.message, "bad"); }
    },
    rename(el) { openModal(renameView, { kind: el.dataset.kind, name: el.dataset.name }); },
    "del-tunnel"(el) {
      const n = el.dataset.name, fs = S.ov.forwards || [];
      const moved = fs.filter((f) => f.tunnel === n && f.backup).map((f) => html`<span class="ltr">${f.ports}</span> به ${tlabel(f.backup)}`);
      const gone = fs.filter((f) => f.tunnel === n && !f.backup).length;
      const note = moved.length ? html`<p class="help" style="margin:10px 0 0">پورت‌هایی که پشتیبان دارند حذف نمی‌شوند و روی پشتیبانشان می‌مانند: ${moved.map((m, i) => html`${i ? "، " : ""}${m}`)}.${gone ? ` ${gone} فوروارد بدون پشتیبان حذف می‌شود.` : ""}</p>` : "";
      openModal(() => confirmView("حذف تانل", html`تانل <b>${tlabel(n)}</b> با لایه‌های زیرین و فورواردهایش، روی هر دو سرور حذف شود؟${note}`, "del-tunnel-go", { name: n }, "حذف تانل"));
    },
    async "del-tunnel-go"(el) {
      try { const ch = await api("DELETE", "tunnels/" + encodeURIComponent(el.dataset.name)); closeModal(); S.open = null; S.drawer = null; await refresh(); reportChange(ch, "تانل حذف شد"); }
      catch (e) { toast(e.message, "bad"); }
    },
    "del-server"(el) { openModal(() => confirmView("حذف سرور", html`سرور <b>${pname(el.dataset.name)}</b> و تانل‌های این سرور به آن حذف شوند؟ روی خود آن سرور چیزی پاک نمی‌شود.`, "del-server-go", { name: el.dataset.name }, "حذف سرور")); },
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
    // Graph ranges and subjects.
    "hist-range"(el) { S.hist.range = el.dataset.value; store("nt-hist", S.hist.range); draw(); },
    "hist-src"(el) { S.hist.src = el.dataset.value; store("nt-hist-src", S.hist.src); draw(); },
    "st-range"(el) { S.st.range = el.dataset.value; store("nt-st", S.st.range); draw(); },
    "dr-range"(el) { S.dr.range = el.dataset.value; draw(); },
    "st-pick"(el) { S.st.subject = el.dataset.name; S.drawer = null; if (S.route === "stats") { draw(); window.scrollTo(0, 0); } else location.hash = "#/stats"; },
    "live-toggle"() { S.paused = !S.paused; if (!S.paused) refresh(); else draw(); },
    // The flow card's switches: turning a tunnel off cuts its users, so it
    // asks first; turning it on doesn't.
    "power-ask"(el) {
      const t = tunnel(el.dataset.name); if (!t || t.managed) return;
      if (t.limits && t.limits.disabled) { actions["tunnel-power"](el); return; }
      openModal(() => confirmView("خاموش کردن تانل", html`تانل <b>${tname(t)}</b> روی هر دو سرور خاموش شود؟ اتصال کاربرانی که از آن می‌روند قطع می‌شود.`, "tunnel-power", { name: t.name }, "خاموش کردن", I.power));
    },
    "speed-opt"(el) {
      S.speed[el.dataset.key] = +el.dataset.value;
      store("nt-speed", JSON.stringify({ secs: S.speed.secs, streams: S.speed.streams }));
      draw();
    },
    test(el) { startJob("test", el.dataset.name); },
    bench(el) { S.speed.peer = el.dataset.name; startJob("bench", el.dataset.name, S.speed.only); },
    "run-test"() { startJob("test", S.speed.tunnel); },
    "run-bench"() { startJob("bench", S.speed.peer, S.speed.only); },
    async "cancel-job"() { if (S.job) await api("POST", `jobs/${S.job.id}/cancel`).catch(() => {}); },
    sec(el) { S.sec = el.dataset.sec; store("nt-settings", S.sec); S.edit = false; S.defs = null; S.fetch = S.fe = S.cert = S.ce = S.restore = S.gv = S.gd = S.tv = S.td = S.apiv = S.apid = S.apiMade = S.hist2 = null; draw(); },
    "rollback-ask"(el) {
      const x = (S.hist2 || [])[+el.dataset.i]; if (!x) return;
      openModal(() => confirmView("برگرداندن تغییرات", html`تانل‌ها و پورت‌ها به حال پیش از تغییرِ <b>${ago(x.at)}</b> برمی‌گردند (${x.changes.slice(0, 3).map((d, j) => html`${j ? "، " : ""}${diffText(d)}`)}${x.changes.length > 3 ? " و…" : ""})، و هر تغییری که بعد از آن داده‌اید هم برمی‌گردد. روی این سرور و سرورهای مقابل اعمال می‌شود. این کار خودش در تاریخچه ثبت می‌شود و می‌شود برش گرداند.`, "rollback-go", { id: x.id }, "برگرداندن", I.sync));
    },
    async "rollback-go"(el) {
      try { const ch = await api("POST", "history/rollback" + safeQ(), { id: el.dataset.id }); closeModal(); S.hist2 = null; await refresh(); reportChange(ch, "برگردانده شد"); safeStarted(ch); draw(); }
      catch (e) { toast(e.message, "bad"); }
    },
    async "token-add"() {
      const d = S.apid; if (!d) return;
      try { const r = await api("POST", "tokens", { name: d.name, scope: d.scope }); S.apiMade = r.token; d.name = ""; S.apiv = null; S.edit = false; draw(); }
      catch (e) { toast(e.message, "bad"); }
    },
    async "token-rm"(el) {
      try { await api("POST", "tokens/remove", { id: el.dataset.id }); S.apiv = null; S.apiMade = null; toast("توکن حذف شد؛ برنامه‌ای که با آن کار می‌کرد دیگر راه ندارد"); draw(); }
      catch (e) { toast(e.message, "bad"); }
    },
    "wh-gen"() {
      const b = new Uint8Array(24); crypto.getRandomValues(b);
      S.apid.secret = Array.from(b, (x) => x.toString(16).padStart(2, "0")).join(""); S.edit = true; draw();
    },
    async "wh-save"() {
      const d = S.apid; if (!d) return;
      try { await api("POST", "settings/webhook", { url: d.url.trim(), secret: d.secret.trim() }); S.edit = false; S.apiv = null; S.apid = null; toast(d.url.trim() ? "وب‌هوک ذخیره شد" : "وب‌هوک خاموش شد"); draw(); }
      catch (e) { toast(e.message, "bad"); }
    },
    async "wh-test"() {
      const d = S.apid; if (!d) return;
      try { await api("POST", "settings/webhook/test", { url: d.url.trim(), secret: d.secret.trim() }); toast("رسید؛ گیرنده جواب ۲xx داد"); }
      catch (e) { toast(e.message, "bad"); }
    },
    async "save-guard"() {
      const g = S.gd, was = S.gv && S.gv.settings.on;
      const lines = (t) => String(t || "").split(/[\s,]+/).filter(Boolean);
      const body = { on: !!g.on, config_iran: !!g.config_iran, icmp: g.icmp || "closed", ports: g.ports, allow: lines(g.allow), block: lines(g.block), ifaces: g.ifaces || [] };
      try {
        S.gv = await api("POST", "guard", body); S.gd = null; S.edit = false; draw();
        if (S.gv.status.error) { toast(S.gv.status.error, "bad"); return; }
        if (body.on && !was) {
          // A request that gets through proves the panel still reaches the
          // server; without it the daemon turns the firewall off again.
          setTimeout(async () => {
            try { await api("POST", "guard/confirm"); await loadGuard(); toast("فایروال روشن شد و پنل در دسترس ماند"); }
            catch (e) { toast("تأیید فایروال انجام نشد؛ تا یک دقیقه‌ی دیگر خودش خاموش می‌شود", "bad"); }
          }, 2500);
        } else toast("ذخیره شد");
      } catch (e) { toast(e.message, "bad"); }
    },
    async "save-tg"() {
      const d = S.td;
      const body = { on: !!d.on, token: d.token.trim(), proxy: d.route === "custom" ? d.proxy.trim() : d.route, admins: String(d.admins || "").split(/[\s,]+/).filter(Boolean).map(Number).filter((n) => Number.isInteger(n) && n !== 0),
        mute: d.mute, users: !!d.users, user_ports: d.user_ports.trim(), reports: d.reports.map((r) => ({ every: r.every, at: r.every === "hourly" ? (r.at || "").trim() : (r.at || "09:00"), ports: (r.ports || "").trim(), system: !!r.system })) };
      try { S.tv = await api("POST", "telegram", body); S.td = null; S.edit = false; draw(); toast("ذخیره شد"); setTimeout(loadTelegram, 3000); } catch (e) { toast(e.message, "bad"); }
    },
    async "tg-test"() {
      try { await api("POST", "telegram/test"); toast("پیام آزمایشی فرستاده شد"); } catch (e) { toast(e.message, "bad"); }
    },
    async "tg-code"() {
      try { S.tv = await api("POST", "telegram/code"); draw(); } catch (e) { toast(e.message, "bad"); }
    },
    "tg-add-report"() { S.td.reports.push({ every: "daily", at: "09:00", ports: "", system: true }); S.edit = true; draw(); },
    "tg-del-report"(el) { S.td.reports.splice(+el.dataset.i, 1); S.edit = true; draw(); },
    "spot-hide"() { store("nt-ad-hide", String(Date.now() + AD_HIDE_H * 3600e3)); draw(); },
    async "guard-refresh"() { S.gd = null; S.edit = false; await loadGuard(); },
    async "guard-geo"() {
      S.gGeo = true; draw();
      try { const r = await api("POST", "guard/geo"); toast("فهرست ایران به‌روز شد: " + r.ranges + " رنج"); await loadGuard(); } catch (e) { toast(e.message, "bad"); }
      S.gGeo = false; draw();
    },
    async "save-fetch"() {
      const f = S.fe;
      const body = { on: !!f.on, tunnel: f.tunnel || "", groups: f.groups, domains: f.domains.split(/[\s,]+/).filter(Boolean), strict: !!f.strict, all: !!f.all };
      try {
        const ch = await api("POST", "settings/fetch", body);
        S.edit = false; S.fe = null; S.fetch = null;
        reportChange(ch, body.on ? "دانلودها از تانل می‌روند" : "دانلودها دوباره مستقیم می‌روند");
        setTimeout(loadFetch, 1500);
      } catch (e) { toast(e.message, "bad"); }
    },
    async "fetch-test"() {
      const f = S.fe; f.testing = true; f.result = null; draw();
      try { f.result = await api("POST", "settings/fetch/test", { host: f.test }); S.fetch = await api("GET", "settings/fetch"); }
      catch (e) { toast(e.message, "bad"); }
      f.testing = false; draw();
    },
    async "cert-check"() {
      const e = S.ce;
      try { e.check = await api("GET", "settings/cert?domain=" + encodeURIComponent(e.domain.trim())); draw(); } catch (err) { toast(err.message, "bad"); }
    },
    async "cert-issue"() { await certSave({ mode: "acme", domain: S.ce.domain.trim() }, "گواهی Let's Encrypt گرفته شد"); },
    async "cert-custom"() { await certSave({ mode: "custom", domain: S.ce.domain.trim(), cert: S.ce.cert, key: S.ce.key }, "گواهی ذخیره شد"); },
    async "cert-self"() { await certSave({ mode: "self" }, "پنل دوباره با گواهی خودامضا و IP باز می‌شود"); },
    "restore-cancel"() { S.restore = null; draw(); },
    restore() {
      const p = S.restore.preview;
      openModal(() => confirmView("بازگردانی پشتیبان", html`تنظیمات، تانل‌ها و کلید این سرور با پشتیبانِ <b class="ltr">${p.node}</b> جایگزین می‌شود و سرویس ری‌استارت می‌شود. ادامه می‌دهید؟`, "restore-go", {}, "بازگردانی", I.sync));
    },
    async "restore-go"() {
      const r = S.restore; r.busy = true; closeModal(); draw();
      try {
        const res = await apiUpload("backup/restore", r.text);
        (res.peers || []).forEach((p) => toast(p.ok ? `${p.peer} به‌روز شد` : `${p.peer}: ${p.error}`, p.ok ? "ok" : "bad"));
        toast("پشتیبان بازگردانی شد؛ سرویس ری‌استارت می‌شود و پنل چند ثانیه دیگر دوباره بالا می‌آید");
        S.restore = null;
        setTimeout(() => location.reload(), 8000);
      } catch (e) { r.busy = false; toast(e.message, "bad"); draw(); }
    },
    "goto-sec"(el, e) { e.preventDefault(); S.sec = el.dataset.sec; location.hash = "#/settings"; },
    async "save-defaults"() {
      const d = S.defs;
      try { await api("POST", "settings/defaults", { encrypt: d.encrypt, mtu_probe: !!d.mtu_probe, obfs: !!d.obfs, padding: +d.padding || 0, forward_mode: d.forward_mode === "nat" ? "" : d.forward_mode, no_recover: !d.recover, no_nightly: !d.nightly, no_auto_upgrade: !d.autoup, pool: d.pool || "", auto_wait: +d.auto_wait || 0 }); S.edit = false; S.defs = null; await refresh(); toast("پیش‌فرض‌ها ذخیره شد"); }
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
    const sp = e.target.closest("a[data-hl]");
    if (sp) api("POST", "spot/open", { id: sp.dataset.hl, placement: sp.dataset.place }).catch(() => {});
    const el = e.target.closest("[data-act]");
    if (!el || el.type === "checkbox" || el.tagName === "DETAILS") return;
    const fn = actions[el.dataset.act];
    if (fn) { if (el.tagName === "A" && el.dataset.act !== "goto-sec") e.preventDefault(); fn(el, e); }
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") { if (S.modal) closeModal(); else if (S.menu) closeMenu(); else if (S.drawer) { S.drawer = null; draw(); } return; }
    // Tab stays inside the open menu.
    if (e.key === "Tab" && S.menu && !S.modal) {
      const els = $(".sheet a[href], .sheet button:not(:disabled)"); if (!els.length) return;
      const first = els[0], last = els[els.length - 1];
      if (!e.target.closest(".sheet")) { e.preventDefault(); first.focus(); }
      else if (e.shiftKey && e.target === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && e.target === last) { e.preventDefault(); first.focus(); }
    }
    // SVG nodes act as buttons.
    if ((e.key === "Enter" || e.key === " ") && e.target.matches && e.target.matches('g[role="button"], div[role="button"]')) { e.preventDefault(); e.target.dispatchEvent(new MouseEvent("click", { bubbles: true })); }
  });

  window.addEventListener("hashchange", () => {
    S.route = location.hash.slice(2) || "dashboard";
    S.edit = false; S.drawer = null; S.defs = null;
    if (S.route === "settings") S.system = null;
    draw(); window.scrollTo(0, 0);
    if (S.menu) closeMenu();
  });

  // ---------- boot ----------
  async function boot() {
    await refresh();
    if (S.authed) {
      await refreshTraffic();
      try { const jobs = await api("GET", "jobs"); if (jobs.length) { S.job = jobs[0]; pollJob(); } } catch { /* none */ }
      draw();
    }
    setInterval(() => { if (S.authed && !document.hidden && !S.paused) refresh(); }, 3000);
    let resized = 0;
    window.addEventListener("resize", () => { clearTimeout(resized); resized = setTimeout(() => { if (S.menu && window.innerWidth > 980) S.menu = S.menuLeaving = false; if (!S.modal && !S.edit) draw(); }, 150); });
    setInterval(() => { if (S.authed && !document.hidden) refreshTraffic(); }, 30000);
  }
  boot();
})();
