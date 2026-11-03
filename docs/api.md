# API نئوتانل

پنل نئوتانل هر کاری را که انجام می‌دهد از راه یک API ساده‌ی JSON روی HTTPS انجام می‌دهد. برنامه‌ی شما (ربات فروش، اسکریپت مانیتورینگ، سرویس پرداخت) می‌تواند همان API را با یک **توکن** صدا بزند، بدون اینکه رمز پنل را داشته باشد.

این راهنما برای کسی است که می‌خواهد فروش پورت را خودکار کند: ساختن پورت، فروختنش با حجم و مدت، تمدید، دیدن مصرف و کاربران، و شنیدن رویدادها (مثلاً «حجم این پورت تمام شد»).

## ۱. ساختن توکن

پنل ← تنظیمات ← **API و وب‌هوک** ← نام بدهید، دسترسی را انتخاب کنید، «ساختن توکن». توکن (مثل `nt_Zx81kQ0m…`) فقط همان لحظه نشان داده می‌شود؛ کپی‌اش کنید. روی سرور فقط هش آن نگه داشته می‌شود.

| دسترسی | چه می‌تواند |
|---|---|
| `read` (فقط دیدن) | همه‌ی درخواست‌های `GET` |
| `ports` (پورت‌ها) | دیدن، و هر چیزی زیر `/api/forwards…` (ساختن، ویرایش، حذف، فروش، تمدید پورت) و صفر کردن آمار |
| `full` (کامل) | همه‌چیز، جز آنچه زیر آمده |

هیچ توکنی، با هیچ دسترسی‌ای، به این‌ها نمی‌رسد: رمز و تنظیمات پنل (`/api/settings/…`)، پشتیبان‌ها (همه‌ی کلیدها در آن است)، خود توکن‌ها، ربات تلگرام، فایروال، به‌روزرسانی، کلید راه‌اندازی سرورها و دستورهای میکروتیک (که کلید IPsec دارند).

اگر توکنی لو رفت، همان‌جا حذفش کنید؛ از همان لحظه کار نمی‌کند. ستون «آخرین استفاده» نشان می‌دهد کدام توکن هنوز استفاده می‌شود.

## ۲. صدا زدن API

آدرس پایه همان آدرس پنل است، با مسیر مخفی‌اش:

```
https://<سرور>:<پورت پنل>/<مسیر مخفی>/api/...
```

توکن در هدر `Authorization` می‌رود:

```bash
TOKEN=nt_...
BASE=https://203.0.113.10:2096/k3x9q2
curl -s -H "Authorization: Bearer $TOKEN" "$BASE/api/overview"
```

اگر پنل گواهی خودامضا دارد (با IP باز می‌شود)، در curl از `-k` استفاده کنید، یا در پنل یک دامنه با گواهی Let's Encrypt بگیرید.

- بدنه‌ی درخواست‌ها و پاسخ‌ها JSON است.
- خطا: کد HTTP غیر ۲xx و `{"error": "..."}`.
- `401`: توکن نیست یا درست نیست. `403`: توکن این اجازه را ندارد.
- `409`: یک «تغییر امن» در پنل منتظر تأیید است؛ چند ثانیه بعد دوباره امتحان کنید.

## ۳. پورت‌ها

هر پورت با **تانل** و **رشته‌ی پورت‌ها** شناخته می‌شود (مثلاً `t1` و `"443"` یا `"2053-2056"`). برای آمار و کاربران، **کلید** پورت به کار می‌رود: `پروتکل/پورت‌ها`، مثلاً `tcp/443` یا `both/2053-2056`.

### فهرست پورت‌ها و مصرفشان

```bash
curl -s -H "Authorization: Bearer $TOKEN" "$BASE/api/overview" | jq '.forwards'
```

هر پورت در `forwards`:

| فیلد | معنی |
|---|---|
| `tunnel`، `ports`، `proto` | تانل، پورت‌ها، `tcp` یا `udp` یا `both` |
| `rx`، `tx` | کل دانلود و آپلود کاربران از ابتدای شمارش (بایت) |
| `today`، `month`، `last_month` | امروز، این ماه، ماه قبل: `{"rx":…, "tx":…}` |
| `counted_since` | شروع شمارش (ثانیه‌ی یونیکس) |
| `plan` | فروش این پورت (اگر فروخته شده)؛ پایین‌تر |
| `closed` | اگر بسته است، چرا: `paused`، `expired` یا `quota` |
| `period`، `period_from`، `period_end` | مصرف دوره‌ی فروش، شروع و پایان دوره (`period_end` صفر یعنی یک دوره برای کل مدت) |

`rx` یعنی آنچه کاربران از این پورت گرفته‌اند (دانلود)، `tx` آنچه فرستاده‌اند (آپلود). این‌ها بایت‌های خود کاربر است، پیش از سربار تانل. ترافیک پورت‌ها با هیچ تغییر و ری‌استارتی صفر نمی‌شود؛ فقط با صفر کردن خود همان پورت.

### ساختن پورت

```bash
curl -s -X POST -H "Authorization: Bearer $TOKEN" "$BASE/api/forwards" \
  -d '{"tunnel":"t1","ports":"8443","proto":"tcp","to":443}'
```

| فیلد | معنی |
|---|---|
| `tunnel`، `ports` | تانل و پورت یا پورت‌ها (`"443"`، `"8080,8443"`، `"2053-2056"`) |
| `proto` | `tcp`، `udp` یا `both` |
| `to` | پورت مقصد روی سرور خارج (۰ = همان پورت) |
| `mode` | `nat` (پیش‌فرض)، `transparent`، `proxy`، `relay` |
| `backups` | تانل‌های پشتیبان به ترتیب، مثلاً `["t2","t3"]` |

پورت تصادفی آزاد: `GET /api/ports/random?proto=tcp` ← `{"port": 41234}`.

### ویرایش و حذف

```bash
# پورت 8443 روی t1 را روی 9443 ببر (اگر پورت عوض شود، آمار و حجم خریدار با آن می‌رود)
curl -s -X POST -H "Authorization: Bearer $TOKEN" "$BASE/api/forwards/update" \
  -d '{"was_tunnel":"t1","was_ports":"8443","tunnel":"t1","ports":"9443","proto":"tcp","to":443}'

curl -s -X POST -H "Authorization: Bearer $TOKEN" "$BASE/api/forwards/remove" \
  -d '{"tunnel":"t1","ports":"9443"}'
```

در `update` همه‌ی فیلدهای پورت را بفرستید (هر چه نفرستید پیش‌فرض می‌شود).

## ۴. فروش پورت

```bash
curl -s -X POST -H "Authorization: Bearer $TOKEN" "$BASE/api/forwards/plan" -d '{
  "tunnel": "t1", "ports": "8443",
  "plan": {
    "customer": "علی",
    "quota_gb": 100,
    "expires": 1798761600,
    "rate_mbit": 50,
    "max_ips": 2,
    "max_conns": 0,
    "reset_days": 0
  }
}'
```

| فیلد `plan` | معنی |
|---|---|
| `customer` | یادداشت شما برای خودتان (تا ۱۰۰ نویسه) |
| `quota_gb` | حجم، دانلود + آپلود؛ وقتی پر شد پورت بسته می‌شود (۰ = نامحدود) |
| `reset_days` | حجم هر چند روز یک بار از نو (۰ = هیچ‌وقت؛ یک حجم برای کل مدت) |
| `expires` | پایان، ثانیه‌ی یونیکس؛ بعد از آن پورت بسته می‌شود (۰ = بدون پایان) |
| `rate_mbit` | سقف سرعت هر جهت، همه‌ی کاربران این پورت با هم (Mbps) |
| `max_conns` | حداکثر اتصال باز هم‌زمان |
| `max_ips` | حداکثر IP هم‌زمان (هر IP تا ۵ دقیقه بعد از آخرین بسته‌اش شمرده می‌شود؛ IPv4 و IPv6 جدا) |
| `paused` | `true`: پورت تا وقتی دوباره `false` شود بسته است |

- همه‌ی محدودیت‌ها روی همین سرور و در خود کرنل اعمال می‌شوند. سقف سرعت مثل محدودکننده‌ی روتر بسته‌های اضافه را دور می‌ریزد.
- حجم هر ۱۰ ثانیه بررسی می‌شود.
- دوره‌ی حجم با اولین فروش شروع می‌شود. ویرایش بعدی دوره را عوض نمی‌کند؛ برای شروع دوره‌ی تازه `"restart": true` کنار `plan` بفرستید.
- لغو فروش (برداشتن همه‌ی محدودیت‌ها): `"plan": null`.

### تمدید

```bash
curl -s -X POST -H "Authorization: Bearer $TOKEN" "$BASE/api/forwards/renew" \
  -d '{"tunnel":"t1","ports":"8443","days":30,"reset":true}'
```

- `days`: این تعداد روز به پایان اضافه می‌شود (اگر گذشته باشد، از امروز). ۰ = پایان دست نمی‌خورد.
- `reset`: `true` یعنی دوره‌ی تازه از همین الان و حجم دوباره کامل. بدون آن، حجم مصرف‌شده سر جایش می‌ماند.
- پورتِ متوقف‌شده با تمدید باز می‌شود.

## ۵. مصرف روزانه و کاربران

```bash
# روز به روز، تا ۶۲ روز
curl -s -H "Authorization: Bearer $TOKEN" "$BASE/api/forwards/history?key=tcp/8443"
# همان، به‌صورت CSV
curl -s -H "Authorization: Bearer $TOKEN" "$BASE/api/forwards/history?key=tcp/8443&format=csv" -o port-8443.csv

# کاربران: IPهای وصل الان و IPهای ۲۴ ساعت گذشته
curl -s -H "Authorization: Bearer $TOKEN" "$BASE/api/forwards/clients?key=tcp/8443"
```

پاسخ `clients`:

```json
{
  "conns": 3, "day": 4, "bytes": true, "times": true,
  "clients": [
    {"ip": "5.160.12.44", "conns": 2, "rx": 3400000000, "tx": 310000000, "since": 1793600000, "last": 1793640000, "ir": true}
  ]
}
```

`conns` و `rx`/`tx` و `since` مال اتصال‌های باز همین الان است؛ `last` آخرین باری که این IP اتصال تازه باز کرد (در ۲۴ ساعت گذشته)؛ `ir` یعنی IP در فهرست ایران است.

صفر کردن آمار یک پورت (مصرف دوره‌ی خریدار دست نمی‌خورد):

```bash
curl -s -X POST -H "Authorization: Bearer $TOKEN" "$BASE/api/traffic/reset" -d '{"tunnel":"@port:tcp/8443"}'
```

## ۶. وب‌هوک

پنل ← تنظیمات ← API و وب‌هوک ← آدرس و رمز امضا (حداقل ۱۶ نویسه). هر رویداد این سرور با `POST` به آدرس شما می‌رود:

```json
{
  "node": "ir",
  "event": {"at": 1793640000, "tunnel": "t1", "kind": "port", "to": "quota", "detail": "8443", "auto": true}
}
```

رویدادهای مهم برای فروش:

| `kind` | `to` | یعنی |
|---|---|---|
| `port` | `quota` | حجم پورت `detail` تمام شد و بسته شد |
| `port` | `expired` | مدت پورت تمام شد و بسته شد |
| `port` | `paused` | پورت متوقف شد |
| `port` | `open` | پورت دوباره باز شد (`from`: چرا بسته بود) |
| `changed` | | `detail`: `plan 8443` (فروش تغییر کرد) یا `renew 8443` (تمدید شد) |
| `health` | `down`، `bad`، `ok` | قطعی، افت شدید یا سالم شدن تانل |
| `failover` | | پورت‌ها به تانل پشتیبان رفتند یا برگشتند |

هدر `X-NeoTunnel-Signature: sha256=<hex>` امضای HMAC-SHA256 بدنه با رمز شماست. همیشه بررسی‌اش کنید:

```python
import hmac, hashlib

def valid(body: bytes, header: str, secret: str) -> bool:
    want = "sha256=" + hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(want, header)
```

```js
const crypto = require("crypto");
const valid = (body, header, secret) =>
  crypto.timingSafeEqual(Buffer.from(header), Buffer.from("sha256=" + crypto.createHmac("sha256", secret).update(body).digest("hex")));
```

پاسخ ۲xx یعنی رسید. اگر نرسد یا جواب دیگری بیاید، بعد از ۵ ثانیه، ۳۰ ثانیه و ۲ دقیقه دوباره فرستاده می‌شود. دکمه‌ی «ارسال آزمایشی» یک رویداد با `"test": true` می‌فرستد.

## ۷. نمونه: ربات فروش در چند خط

```python
import time
import requests

BASE = "https://203.0.113.10:2096/k3x9q2"
H = {"Authorization": "Bearer nt_..."}

def sell(customer: str, gb: float, days: int) -> int:
    """یک پورت تازه می‌سازد و می‌فروشد؛ شماره‌اش را برمی‌گرداند."""
    port = requests.get(f"{BASE}/api/ports/random", params={"proto": "tcp"}, headers=H).json()["port"]
    requests.post(f"{BASE}/api/forwards", headers=H, json={"tunnel": "t1", "ports": str(port), "proto": "tcp"}).raise_for_status()
    requests.post(f"{BASE}/api/forwards/plan", headers=H, json={
        "tunnel": "t1", "ports": str(port),
        "plan": {"customer": customer, "quota_gb": gb, "expires": int(time.time()) + days * 86400, "max_ips": 2},
    }).raise_for_status()
    return port

def renew(port: int, days: int = 30):
    requests.post(f"{BASE}/api/forwards/renew", headers=H, json={"tunnel": "t1", "ports": str(port), "days": days, "reset": True}).raise_for_status()

def left(port: int) -> float:
    f = next(f for f in requests.get(f"{BASE}/api/overview", headers=H).json()["forwards"] if f["ports"] == str(port))
    used = f["period"]["rx"] + f["period"]["tx"]
    return max(0, f["plan"]["quota_gb"] * 2**30 - used) / 2**30   # گیگابایت مانده
```

(در پنل GB یعنی ۲ به توان ۳۰ بایت.)
