# دستورهای میکروتیک

وقتی سر دیگر تانل یک روتر میکروتیک است (در ویزارد مقصد «دستگاه دیگر / IP دلخواه»، بدون NeoTunnel)، NeoTunnel دستورهای RouterOS 7 همان تانل را می‌سازد تا لازم نباشد سمت روتر را دستی تنظیم کنید. MTU، کلید/شناسه‌ی تانل، پورت و آدرس داخلی دقیقاً با سمت NeoTunnel یکی است.

- **پنل:** جزئیات تانل ← دکمه‌ی **دستورهای میکروتیک** ← کپی و چسباندن در ترمینال روتر.
- **خط فرمان:**

```bash
neotunnel tunnel mikrotik <name>
```

خط‌هایی که با `#` شروع می‌شوند توضیح‌اند و RouterOS آن‌ها را نادیده می‌گیرد. اسم اینترفیس روی روتر هم مثل سرور `nt-<name>` است.

## چه چیزی ساخته می‌شود

| نوع تانل | منوی RouterOS | نکته |
|---|---|---|
| GRE | `/interface gre` | بدون کلید (میکروتیک GRE را بی‌کلید می‌فرستد) |
| GRE روی IPv6 | `/interface gre6` | |
| IPIP | `/interface ipip` | |
| IPIP6 / IP6IP6 | `/interface ipipv6` | |
| 6to4 / SIT | `/interface 6to4` | در حالت 6to4 تانل رویی (`ipipv6` یا `gre6`) هم با آدرس‌های داخلی ساخته می‌شود |
| EoIP / EoIPv6 | `/interface eoip` / `eoipv6` | با همان `tunnel-id` |
| VXLAN | `/interface vxlan` + `vteps` | پورت همیشه نوشته می‌شود؛ پیش‌فرض میکروتیک 8472 است و NeoTunnel 4789 |
| L2TPv3 | `/interface l2tp-ether` با `unmanaged-mode=yes` | استاتیک، یک شناسه برای tunnel و session در هر دو سر |
| WireGuard | `/interface wireguard` + `peers` | کلید عمومی این سرور، endpoint، allowed-address و keepalive |

بعد از اینترفیس‌ها، آدرس داخلی سمت روتر (`/ip address` و برای IPv6 `/ipv6 address ... advertise=no`) روی همان اینترفیس گذاشته می‌شود. اگر DSCP ثابت تنظیم شده باشد، `dscp=` هم اضافه می‌شود.

**پشتیبانی نمی‌شود** (دستور خطا می‌دهد و دلیلش را می‌گوید): GRETAP و GENEVE (میکروتیک ندارد؛ به جایش EoIP، VXLAN یا L2TPv3)، اجرای GRE/IPIP/SIT روی پورت UDP (FOU)، EoIP روی پورت UDP، و NeoObfs و TCP که فقط بین دو سرور NeoTunnel کار می‌کنند.

## مثال: 6to4

سرور NeoTunnel با آدرس `203.0.113.1` و روتر با آدرس `198.51.100.7`:

```
# NeoTunnel mk (6to4): RouterOS v7 commands for the router at 198.51.100.7
# NeoTunnel server: 203.0.113.1
/interface 6to4 add name=nt-mk-6 remote-address=203.0.113.1 mtu=1480 comment="NeoTunnel mk-6"
/interface ipipv6 add name=nt-mk remote-address=fd00:1:2:3::1 local-address=fd00:1:2:3::2 mtu=1440 comment="NeoTunnel mk"
/ipv6 address add address=fd00:1:2:3::2/64 interface=nt-mk-6 advertise=no comment="NeoTunnel mk-6"
/ip address add address=10.77.0.6/30 interface=nt-mk comment="NeoTunnel mk"
```

## مثال‌های دیگر

GRE:

```
/interface gre add name=nt-gre remote-address=203.0.113.1 mtu=1476 comment="NeoTunnel gre"
/ip address add address=10.77.0.6/30 interface=nt-gre comment="NeoTunnel gre"
```

IPIP همراه با پورت‌های فورواردشده:

```
/interface ipip add name=nt-ip remote-address=203.0.113.1 mtu=1480 comment="NeoTunnel ip"
/ip address add address=10.77.0.6/30 interface=nt-ip comment="NeoTunnel ip"
# Ports forwarded here arrive on 10.77.0.6 from 10.77.0.5 (connected, no route needed):
#   tcp 443,8080-8090 -> same port
#   both 2053 -> port 53, with a PROXY protocol v2 header
# to pass them on to a host behind the router, e.g.:
# /ip firewall nat add chain=dstnat in-interface=nt-ip protocol=tcp dst-port=443,8080-8090 action=dst-nat to-addresses=192.168.88.10
```

EoIP و EoIPv6:

```
/interface eoip add name=nt-eo remote-address=203.0.113.1 tunnel-id=42 mtu=1458 comment="NeoTunnel eo"
/interface eoipv6 add name=nt-eo6 remote-address=2001:db8::1 tunnel-id=1656 mtu=1444 comment="NeoTunnel eo6"
```

VXLAN:

```
/interface vxlan add name=nt-vx vni=4242 port=4789 mtu=1450 comment="NeoTunnel vx"
/interface vxlan vteps add interface=nt-vx remote-ip=203.0.113.1
/ip address add address=10.77.0.6/30 interface=nt-vx comment="NeoTunnel vx"
```

L2TPv3 (روی IP، پروتکل 115):

```
# local-address is the router's own address; behind NAT, use the address on its WAN interface
/interface l2tp-ether add name=nt-l2 connect-to=203.0.113.1 local-address=198.51.100.7 unmanaged-mode=yes l2tp-proto-version=l2tpv3-ip local-tunnel-id=305419896 remote-tunnel-id=305419896 local-session-id=305419896 remote-session-id=305419896 mtu=1462 disabled=no comment="NeoTunnel l2"
/ip address add address=10.77.0.6/30 interface=nt-l2 comment="NeoTunnel l2"
```

WireGuard:

```
/interface wireguard add name=nt-wg listen-port=51821 mtu=1440 comment="NeoTunnel wg"
/interface wireguard peers add interface=nt-wg public-key="<کلید عمومی این سرور>" endpoint-address=203.0.113.1 endpoint-port=51821 allowed-address=10.77.0.5/32,fd00:1:2:4::1/128 persistent-keepalive=25s comment="NeoTunnel wg"
# give NeoTunnel the router's public key (peer key):
:put [/interface wireguard get nt-wg public-key]
/ip address add address=10.77.0.6/30 interface=nt-wg comment="NeoTunnel wg"
/ipv6 address add address=fd00:1:2:4::2/64 interface=nt-wg advertise=no comment="NeoTunnel wg"
```

## نکته‌ها

- **کلید WireGuard روتر:** روتر هنگام ساخت اینترفیس، کلید خصوصی خودش را می‌سازد. خط `:put` کلید عمومی آن را نشان می‌دهد؛ اگر با «کلید عمومی طرف مقابل» که در NeoTunnel داده‌اید فرق دارد، کلید تازه را در تنظیمات تانل بگذارید.
- **فوروارد پورت:** بسته‌های فورواردشده با آدرس داخلی سرور (مثلاً `10.77.0.5`) به روتر می‌رسند که روی خود تانل است، پس **مسیر (route) اضافه لازم نیست**. اگر سرویس پشت روتر است، یک قانون `dst-nat` مثل نمونه‌ی بالا بسازید. حالت‌های transparent و relay به NeoTunnel در هر دو سر نیاز دارند و برای میکروتیک در دسترس نیستند.
- **L2TPv3 روی UDP:** میکروتیک L2TPv3 روی UDP را روی پورت 1701 می‌فرستد؛ پورت تانل را در NeoTunnel همان 1701 بگذارید (در غیر این صورت دستورها هشدار می‌دهند).
- **روتر پشت NAT:** برای L2TPv3، `local-address` را آدرس اینترفیس WAN روتر بگذارید.
- **فایروال روتر:** اگر زنجیره‌ی input روتر همه‌چیز را می‌بندد، پروتکل تانل (41 برای 6to4، 4 برای IPIP، 47 برای GRE/EoIP، 115 برای L2TPv3 یا پورت UDP) را از آدرس سرور باز کنید.
