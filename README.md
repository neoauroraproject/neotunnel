<div dir="rtl">

<p align="center"><img src="internal/web/static/logo.svg" width="84" alt="NeoTunnel"></p>
<h1 align="center">NeoTunnel</h1>
<p align="center">
تانل‌های کرنلی سریع بین سرورها (6to4، IPIP، GRE، VXLAN و …) با پنل وب فارسی، نصب یک‌خطی و تست سرعت داخلی
</p>
<p align="center">
<a href="https://github.com/neoauroraproject/neotunnel/releases/latest"><img src="https://img.shields.io/github/v/release/neoauroraproject/neotunnel?style=flat-square&color=2dd4bf" alt="release"></a>
<a href="https://github.com/neoauroraproject/neotunnel/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/neoauroraproject/neotunnel/ci.yml?branch=main&style=flat-square&label=tests" alt="tests"></a>
<img src="https://img.shields.io/badge/Ubuntu-20.04%2B-38bdf8?style=flat-square" alt="ubuntu">
<a href="LICENSE"><img src="https://img.shields.io/github/license/neoauroraproject/neotunnel?style=flat-square&color=a78bfa" alt="license"></a>
</p>

<p align="center"><img src="docs/panel-dashboard.png" alt="داشبورد پنل NeoTunnel"></p>

## NeoTunnel چیست؟

NeoTunnel تانل‌هایی را که روی میکروتیک می‌زنید، روی **اوبونتو** و بدون پیچیدگی می‌سازد. یک سرور (مثلاً ایران) **سرور اصلی** است و پنل روی آن نصب می‌شود؛ سرورهای مقصد (خارج) فقط با یک دستور به آن وصل می‌شوند و از آن به بعد **هر تغییری که در پنل بدهید، خودکار روی آن‌ها هم اعمال می‌شود**.

- ⚡ **سریع**: همه‌ی تانل‌ها مستقیم در کرنل لینوکس ساخته می‌شوند (بدون پردازش اضافه در برنامه)؛ MTU و MSS خودکار محاسبه می‌شوند و BBR فعال می‌شود.
- 🧭 **6to4 در چند ثانیه**: SIT با IPv6 خصوصی + IPIP6 برای IPv4، با یک کلیک یا یک دستور.
- 🔌 **تانل روی پورت**: هر تانل می‌تواند داخل UDP روی پورت دلخواه اجرا شود (FOU)؛ پس روی یک سرور، پورت‌های مختلف به تانل‌ها و لوکیشن‌های مختلف می‌روند. وقتی مسیر پروتکل‌های 41 یا 47 را می‌بندد هم کار می‌کند.
- 🔁 **فوروارد پورت**: پورت‌های سرور ایران را به سرور خارج بفرستید؛ حتی با تغییر پورت (مثلاً 2053 اینجا ← 443 آن طرف).
- 📈 **تست سرعت داخلی**: پینگ، جیتر، افت بسته و دانلود/آپلود واقعی داخل هر تانل؛ و **مقایسه‌ی خودکار همه‌ی انواع تانل** بین دو سرور برای پیدا کردن سریع‌ترین.
- 🖥️ **پنل فارسی**: نقشه‌ی اتصال‌ها، ترافیک زنده، ساخت تانل مرحله‌به‌مرحله، و **همه‌ی دستورها با IP و نام واقعی سرورها** آماده‌ی کپی.
- 🔒 **امن**: ارتباط سرورها با TLS 1.3 دوطرفه و کلیدهای pin‌شده، لینک‌های اتصال امضاشده، پنل روی پورت تصادفی و آدرس مخفی.
- ♻️ **ماندگار**: سرویس systemd؛ بعد از ریبوت همه‌چیز خودکار برمی‌گردد و هر ۱۵ ثانیه بررسی و ترمیم می‌شود.

## نصب (یک خط)

روی **سرور اصلی** (مثلاً سرور ایران) با دسترسی root اجرا کنید:

</div>

```bash
curl -fsSL https://raw.githubusercontent.com/neoauroraproject/neotunnel/main/install.sh | sudo bash
```

<div dir="rtl">

نصاب، نسخه‌ی مناسب پردازنده (amd64 یا arm64) را دانلود و **checksum** آن را بررسی می‌کند، ماژول‌های کرنل را فعال می‌کند، پنل را روی یک **پورت تصادفی آزاد** بالا می‌آورد و در پایان آدرس، نام کاربری و رمز پنل را نشان می‌دهد:

</div>

```text
  address   https://203.0.113.10:34127/k7m2xq9p4d/
  username  admin
  password  9hq4m2xkp7wtrn
```

<div dir="rtl">

> گواهی پنل self-signed است؛ بار اول هشدار مرورگر را تأیید کنید. رمز فقط همین یک بار نمایش داده می‌شود (بازنشانی: `sudo neotunnel panel password`).

گزینه‌های نصب:

</div>

```bash
# پورت پنل دلخواه
curl -fsSL https://raw.githubusercontent.com/neoauroraproject/neotunnel/main/install.sh | sudo bash -s -- --port 2096
# بدون پنل (برای سرور مقصد)
curl -fsSL https://raw.githubusercontent.com/neoauroraproject/neotunnel/main/install.sh | sudo bash -s -- --no-panel
# نسخه‌ی مشخص
curl -fsSL https://raw.githubusercontent.com/neoauroraproject/neotunnel/main/install.sh | sudo bash -s -- --version v0.1.0
```

<div dir="rtl">

اجرای دوباره‌ی همین دستور، NeoTunnel را **به‌روزرسانی** می‌کند و تنظیمات و رمز را نگه می‌دارد.

## راه‌اندازی تانل ایران ⇄ خارج

1. وارد پنل شوید و در صفحه‌ی **سرورها** روی «افزودن سرور» بزنید؛ نام (مثلاً `germany`) و IP سرور خارج را وارد کنید.
2. پنل یک **دستور یک‌خطی** دقیقاً برای همان سرور می‌سازد. آن را روی سرور خارج اجرا کنید. این دستور NeoTunnel را **مستقیم از سرور ایران** دانلود و بررسی می‌کند، نصب می‌کند و به سرور اصلی وصل می‌شود (نیازی به دسترسی سرور خارج به گیت‌هاب نیست).
3. در صفحه‌ی **تانل‌ها** روی «تانل جدید» بزنید: مقصد ← نوع (6to4 پیشنهادی) ← پورت و فوروارد. تمام؛ هر دو طرف ساخته می‌شوند.

اگر سرور خارج موقتاً در دسترس نبود، تغییرات را بدهید؛ پنل یک دستور `sudo neotunnel apply '…'` نشان می‌دهد که با اجرای آن روی سرور خارج، **همه‌ی تغییرات یک‌جا** اعمال می‌شود. لینک با کلید سرور اصلی امضا شده و سرور مقصد فقط لینک سرور اصلی خودش را (و نه نسخه‌ی قدیمی‌تر) قبول می‌کند.

<table>
<tr>
<td><img src="docs/panel-tunnels.png" alt="تانل‌ها"></td>
<td><img src="docs/panel-servers.png" alt="سرورها"></td>
</tr>
<tr>
<td align="center">تانل‌ها: آدرس‌های داخلی، پورت، MTU و ترافیک زنده</td>
<td align="center">سرورها: وضعیت اتصال، همگام‌سازی، ارتقا و دستور اتصال</td>
</tr>
<tr>
<td><img src="docs/panel-speed.png" alt="تست سرعت"></td>
<td><img src="docs/panel-ports.png" alt="پورت‌ها"></td>
</tr>
<tr>
<td align="center">مقایسه‌ی همه‌ی انواع تانل و انتخاب سریع‌ترین</td>
<td align="center">پورت‌های مختلف به لوکیشن‌های مختلف</td>
</tr>
</table>

## چند لوکیشن روی یک سرور

هر تانل می‌تواند روی پورت UDP خودش اجرا شود و هر پورت ورودی می‌تواند به یک تانل دیگر برود. مثال: پورت 443 به آلمان، پورت 2053 به هلند (روی 443 مقصد):

</div>

```bash
sudo neotunnel quick germany     --address 198.51.100.24 --port 3000 --forward 443
sudo neotunnel quick netherlands --address 203.0.113.57  --port 3001 --forward 2053 --forward-to 443
```

<div dir="rtl">

## تانل‌های پشتیبانی‌شده

| نوع | لایه | روی | قابل اجرا روی پورت UDP | معادل میکروتیک |
|---|:-:|---|:-:|---|
| **6to4** (SIT + IPIP6) | 3 | IPv4 | ✅ | 6to4 + IPIPv6 |
| 6to4-gre (SIT + GRE6) | 3 | IPv4 | ✅ | 6to4 + GREv6 |
| SIT | 3 | IPv4 | ✅ | 6to4 |
| IPIP | 3 | IPv4 | ✅ | IPIP |
| GRE | 3 | IPv4 | ✅ | GRE |
| GRETAP | 2 | IPv4 | ✅ | — (مشابه EoIP) |
| IP6GRE / IP6GRETAP | 3 / 2 | IPv6 | — | GREv6 |
| IPIP6 / IP6IP6 | 3 | IPv6 | — | IPIPv6 |
| VXLAN | 2 | IPv4/IPv6 | همیشه | VXLAN |
| GENEVE | 2 | IPv4/IPv6 | همیشه | — |

برای وصل شدن به دستگاهی که NeoTunnel ندارد (مثلاً میکروتیک)، در ویزارد «دستگاه دیگر» را انتخاب کنید یا:

</div>

```bash
sudo neotunnel tunnel add mk1 --kind gre --remote 203.0.113.99
```

<div dir="rtl">

## تست سرعت

- **تست یک تانل**: ۱۰۰ بسته برای پینگ/جیتر/افت و سپس دانلود و آپلود هر کدام ۵ ثانیه با ۴ اتصال موازی، داخل خود تانل.
- **مقایسه‌ی همه‌ی انواع**: تانل‌های موقت از هر نوع بین دو سرور ساخته، اندازه‌گیری و پاک می‌شوند و نتیجه رتبه‌بندی می‌شود؛ با یک کلیک «ساخت» همان تانل ساخته می‌شود. تانل‌های فعلی دست نمی‌خورند.

</div>

```bash
sudo neotunnel test t1          # تست یک تانل
sudo neotunnel bench germany    # مقایسه‌ی همه‌ی انواع تا سرور germany
sudo neotunnel traffic          # ترافیک زنده
```

<div dir="rtl">

## خط فرمان

همه‌ی کارهای پنل از خط فرمان هم انجام می‌شود (و در صفحه‌ی «دستورها»ی پنل، با مقادیر واقعی سرور شما آماده‌ی کپی است). با `sudo neotunnel` منوی تعاملی باز می‌شود.

| دستور | کار |
|---|---|
| `neotunnel quick <server> --address <ip> [--mode 6to4] [--port N] [--forward ports]` | ساخت تانل در یک مرحله |
| `neotunnel tunnel add\|list\|remove\|kinds` | مدیریت تانل‌ها با جزئیات |
| `neotunnel forward add <tunnel> <ports> [--to port] [--proto tcp\|udp\|both]` | فوروارد پورت |
| `neotunnel peer add\|list\|link\|sync\|upgrade\|remove` | مدیریت سرورهای مقصد |
| `neotunnel apply 'neotunnel://…'` | اعمال لینک سرور اصلی روی سرور مقصد |
| `neotunnel test` / `bench` / `traffic` / `status` | تست و وضعیت |
| `neotunnel panel info\|setup\|password\|port\|disable` | مدیریت پنل |
| `neotunnel optimize` | BBR، fq و بافرهای شبکه (ماندگار) |
| `neotunnel down` / `uninstall [--purge]` | حذف تانل‌ها / حذف کامل |

## پورت‌ها و امنیت

| پورت | کاربرد |
|---|---|
| پورت پنل (تصادفی) / tcp | پنل وب، فقط روی یک آدرس مخفی؛ رمز با bcrypt، قفل شدن بعد از ۵ تلاش ناموفق |
| 7710/tcp | ارتباط سرور اصلی و سرورهای مقصد (TLS 1.3 دوطرفه، کلید هر دو طرف pin شده) |
| 7711/tcp+udp | ترافیک تست سرعت؛ فقط به نشست‌هایی که سرور اصلی باز کرده جواب می‌دهد |

اگر ufw فعال باشد، سرویس پورت‌ها و رابط‌های تانل لازم را خودش باز می‌کند. پورت‌های 22، 7710 و 7711 هرگز فوروارد نمی‌شوند تا دسترسی شما قطع نشود.

## سؤال‌های رایج

**تانل 6to4 بالا نمی‌آید و پینگ ندارد.** احتمالاً پروتکل 41 روی مسیر بسته است. تانل را روی یک پورت UDP بسازید (فیلد «پورت UDP» در ویزارد یا `--port 3000`) یا با «مقایسه‌ی سرعت» ببینید کدام نوع از مسیر عبور می‌کند.

**رمزنگاری دارد؟** این نسخه تانل‌های خام کرنلی (بیشترین سرعت) را می‌سازد. رمزنگاری کرنلی (IPsec با AES-GCM) در نسخه‌ی بعد اضافه می‌شود.

**حذف کامل:** `sudo neotunnel uninstall --purge && sudo rm /usr/local/bin/neotunnel`

## ساخت از سورس

</div>

```bash
go test ./...
GOOS=linux GOARCH=amd64 go build -o dist/neotunnel-linux-amd64 ./cmd/neotunnel
sudo test/e2e.sh dist/neotunnel-linux-amd64   # تست کامل روی کرنل واقعی با network namespace
go run ./cmd/paneldemo                        # پنل با داده‌ی نمونه روی http://127.0.0.1:8099/demo/
```

<div dir="rtl">

## مجوز

[MIT](LICENSE)

</div>
