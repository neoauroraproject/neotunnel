// Package telegram is NeoTunnel's Telegram bot: alerts when tunnels go bad,
// come back, restart or move to a backup; reports on a schedule; and
// answers about traffic. Admins (linked with a one-time code from the
// panel) see and get everything; anyone else who starts the bot, when the
// panel allows it, can send a port number and see that port's traffic.
// The bot can reach Telegram through a proxy, such as one of the server's
// internal proxies, which goes through a tunnel and leaves from the far
// server.
package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // Tehran time on servers without zone files

	"neotunnel/internal/config"
	"neotunnel/internal/events"
	"neotunnel/internal/forward"
	"neotunnel/internal/health"
	"neotunnel/internal/metrics"
	"neotunnel/internal/traffic"
)

// Deps are the daemon's live data the bot reads.
type Deps struct {
	Summaries func() map[string]traffic.Summary
	Metrics   func(rng string, keys []string) metrics.View
	Health    func() map[string]health.State
}

// Status is what the panel shows about the bot.
type Status struct {
	Running  bool   `json:"running"`
	Username string `json:"username,omitempty"`
	Error    string `json:"error,omitempty"`
	Polled   int64  `json:"polled,omitempty"` // unix of the last answer from Telegram
}

var (
	statusMu sync.Mutex
	status   Status
)

// Current is the bot's status now.
func Current() Status {
	statusMu.Lock()
	defer statusMu.Unlock()
	return status
}

func setStatus(f func(*Status)) {
	statusMu.Lock()
	f(&status)
	statusMu.Unlock()
}

// Labels of the keyboard buttons.
const (
	btnStatus = "📊 وضعیت تانل‌ها"
	btnPorts  = "🔌 ترافیک پورت‌ها"
	btnReport = "📈 گزارش ۲۴ ساعت"
	btnID     = "🆔 شناسه‌ی من"
)

var (
	adminKeys = keyboard{{btnStatus, btnPorts}, {btnReport, btnID}}
	userKeys  = keyboard{{btnID}}
)

type bot struct {
	c    *client
	deps Deps

	mu   sync.Mutex
	sent map[string]time.Time // last alert, by tunnel+kind, for the throttle
}

// Watch runs the bot while the settings turn it on, restarting it when the
// token or the proxy changes, and sends it the events of this process.
func Watch(ctx context.Context, deps Deps) {
	var cur *bot
	var stop context.CancelFunc
	var sig string
	events.OnAdd(func(e events.Event) {
		if b := running(); b != nil {
			b.alert(context.Background(), e)
		}
	})
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		st, err := config.Load()
		var tg config.Telegram
		if err == nil && st.Telegram != nil {
			tg = *st.Telegram
		}
		want := ""
		if tg.On && tg.Token != "" {
			want = tg.Token + "\x00" + tg.Proxy
		}
		if want != sig {
			if stop != nil {
				stop()
				stop, cur = nil, nil
				setRunning(nil)
				setStatus(func(s *Status) { *s = Status{} })
			}
			sig = want
			if want != "" {
				c, err := newClient(tg.Token, tg.Proxy)
				if err != nil {
					setStatus(func(s *Status) { *s = Status{Error: err.Error()} })
				} else {
					cur = &bot{c: c, deps: deps, sent: map[string]time.Time{}}
					var bctx context.Context
					bctx, stop = context.WithCancel(ctx)
					setRunning(cur)
					go cur.run(bctx)
				}
			}
		}
		select {
		case <-ctx.Done():
			if stop != nil {
				stop()
			}
			return
		case <-t.C:
		}
	}
}

var (
	runMu  sync.Mutex
	runBot *bot
)

func setRunning(b *bot) { runMu.Lock(); runBot = b; runMu.Unlock() }
func running() *bot     { runMu.Lock(); defer runMu.Unlock(); return runBot }

func (b *bot) run(ctx context.Context) {
	go b.schedule(ctx)
	var offset int64
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		if Current().Username == "" {
			if u, err := b.c.me(ctx); err == nil {
				setStatus(func(s *Status) { s.Running, s.Username, s.Error = true, u.Username, "" })
			} else {
				setStatus(func(s *Status) { s.Running, s.Error = false, err.Error() })
				sleep(ctx, backoff)
				backoff = min(2*backoff, time.Minute)
				continue
			}
		}
		us, err := b.c.updates(ctx, offset, 50*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			setStatus(func(s *Status) { s.Error = err.Error() })
			sleep(ctx, backoff)
			backoff = min(2*backoff, time.Minute)
			continue
		}
		backoff = 5 * time.Second
		setStatus(func(s *Status) { s.Running, s.Error, s.Polled = true, "", time.Now().Unix() })
		for _, u := range us {
			offset = u.ID + 1
			if u.Message != nil && u.Message.Text != "" {
				b.handle(ctx, u.Message)
			}
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func settings() (*config.State, config.Telegram) {
	st, err := config.Load()
	if err != nil || st.Telegram == nil {
		return st, config.Telegram{}
	}
	return st, *st.Telegram
}

func isAdmin(tg config.Telegram, id int64) bool {
	for _, a := range tg.Admins {
		if a == id {
			return true
		}
	}
	return false
}

// handle answers one message.
func (b *bot) handle(ctx context.Context, m *message) {
	st, tg := settings()
	chat, text := m.Chat.ID, strings.TrimSpace(m.Text)
	cmd, arg, _ := strings.Cut(text, " ")
	if i := strings.IndexByte(cmd, '@'); i > 0 && strings.HasPrefix(cmd, "/") {
		cmd = cmd[:i] // /start@SomeBot
	}
	admin := isAdmin(tg, m.From.ID)
	reply := func(s string) {
		kb := userKeys
		if admin {
			kb = adminKeys
		}
		if err := b.c.send(ctx, chat, s, kb); err != nil {
			log.Printf("telegram: %v", err)
		}
	}
	switch {
	case cmd == "/start" && arg != "" && tg.LinkCode != "" && arg == tg.LinkCode && time.Now().Unix() < tg.LinkUntil:
		config.Update(func(s *config.State) error {
			if s.Telegram != nil && !isAdmin(*s.Telegram, m.From.ID) {
				s.Telegram.Admins = append(s.Telegram.Admins, m.From.ID)
			}
			if s.Telegram != nil {
				s.Telegram.LinkCode, s.Telegram.LinkUntil = "", 0
			}
			return nil
		})
		admin = true
		reply(fmt.Sprintf("✅ شما مدیر ربات NeoTunnel روی <b>%s</b> شدید.\nهشدارها و گزارش‌ها برای شما فرستاده می‌شود. از دکمه‌های پایین استفاده کنید یا شماره‌ی یک پورت را بفرستید.", esc(nodeName(st))))
	case cmd == "/id" || text == btnID:
		reply(fmt.Sprintf("شناسه‌ی عددی شما: <code>%d</code>", m.From.ID))
	case admin && (cmd == "/status" || text == btnStatus):
		reply(b.statusText(st))
	case admin && (cmd == "/ports" || text == btnPorts):
		reply(b.portsText(st))
	case admin && (cmd == "/report" || text == btnReport):
		reply(b.reportText(st, "24h", "", true))
	case isPort(cmd) || (cmd == "/port" && isPort(arg)):
		p := cmd
		if cmd == "/port" {
			p = arg
		}
		n, _ := strconv.Atoi(p)
		if !admin && (!tg.Users || !userMayAsk(tg, n)) {
			reply("این پورت را نمی‌توانید ببینید.")
			return
		}
		reply(b.portText(st, n))
	case admin:
		reply(fmt.Sprintf("🖥 NeoTunnel روی <b>%s</b>\nاز دکمه‌های پایین استفاده کنید، یا شماره‌ی یک پورت را بفرستید تا مصرفش را ببینید.", esc(nodeName(st))))
	case tg.Users:
		reply("👋 سلام! شماره‌ی پورت خود را بفرستید (مثلاً <code>443</code>) تا ترافیک مصرفی آن را ببینید.")
	default:
		reply("این ربات خصوصی است.")
	}
}

func isPort(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n > 0 && n < 65536
}

func userMayAsk(tg config.Telegram, port int) bool {
	if strings.TrimSpace(tg.UserPorts) == "" {
		return true
	}
	for _, p := range forward.Expand(tg.UserPorts) {
		if p == port {
			return true
		}
	}
	return false
}

func nodeName(st *config.State) string {
	if st == nil {
		return ""
	}
	if st.Label != "" {
		return st.Label
	}
	return st.Self
}

func esc(s string) string { return html.EscapeString(s) }

// ---------- alerts ----------

// alertKind maps an event to the setting that mutes it ("" = not sent).
func alertKind(e events.Event) string {
	switch e.Kind {
	case events.Health:
		if e.To == "ok" {
			return "recover"
		}
		return "down"
	case events.Restart:
		if e.Auto {
			return "restart"
		}
	case events.Failover:
		if e.Tunnel == e.From { // recorded on both tunnels; tell it once
			return "failover"
		}
	case events.Quota:
		return "quota"
	case events.Guard:
		return "guard"
	}
	return ""
}

func (b *bot) alert(ctx context.Context, e events.Event) {
	kind := alertKind(e)
	if kind == "" {
		return
	}
	st, tg := settings()
	if !tg.On || len(tg.Admins) == 0 {
		return
	}
	for _, m := range tg.Mute {
		if m == kind {
			return
		}
	}
	key := e.Tunnel + "|" + e.Kind + "|" + e.To
	b.mu.Lock()
	if time.Since(b.sent[key]) < time.Minute {
		b.mu.Unlock()
		return
	}
	b.sent[key] = time.Now()
	b.mu.Unlock()
	text := alertText(st, e)
	if text == "" {
		return
	}
	for _, a := range tg.Admins {
		if err := b.c.send(ctx, a, text, nil); err != nil {
			log.Printf("telegram alert: %v", err)
		}
	}
}

func tunnelLabel(st *config.State, name string) string {
	if st != nil {
		if t, ok := st.Tunnel(name); ok && t.Label != "" {
			return t.Label
		}
	}
	return name
}

func alertText(st *config.State, e events.Event) string {
	t := "<b>" + esc(tunnelLabel(st, e.Tunnel)) + "</b>"
	where := "\n<i>" + esc(nodeName(st)) + "</i>"
	switch e.Kind {
	case events.Health:
		switch e.To {
		case "down":
			return "🔴 " + t + " قطع شد: ترافیک رد نمی‌شود." + where
		case "bad":
			return fmt.Sprintf("⚠️ %s افت شدید دارد (%.0f٪)."+where, t, e.Loss)
		}
		return "🟢 " + t + " دوباره سالم شد." + where
	case events.Restart:
		s := fmt.Sprintf("🔄 %s خودکار روی هر دو سرور ری‌استارت شد (افت %.0f٪).", t, e.Loss)
		if e.Detail != "" {
			s += "\nسرور مقابل: " + esc(e.Detail)
		}
		return s + where
	case events.Failover:
		ports, why, _ := strings.Cut(e.Detail, "|")
		s := fmt.Sprintf("↪️ پورت <code>%s</code> از %s به <b>%s</b> رفت.", esc(ports), t, esc(tunnelLabel(st, e.To)))
		if why != "" {
			s += "\n" + esc(why)
		}
		return s + where
	case events.Quota:
		if e.To == "blocked" {
			return "📦 حجم ماهانه‌ی " + t + " تمام شد؛ تا ماه بعد خاموش است." + where
		}
		return "📦 حجم " + t + " آزاد شد و دوباره روشن است." + where
	case events.Guard:
		return "🛡 فایروال خودش خاموش شد: بعد از روشن شدن، پنل تأیید نکرد که هنوز به سرور می‌رسد." + where
	}
	return ""
}

// Test sends a test message to every admin with the current settings.
func Test(ctx context.Context) error {
	st, tg := settings()
	if tg.Token == "" {
		return fmt.Errorf("توکن ربات وارد نشده")
	}
	if len(tg.Admins) == 0 {
		return fmt.Errorf("هنوز مدیری به ربات وصل نشده؛ اول کد اتصال بسازید و برای ربات بفرستید")
	}
	c, err := newClient(tg.Token, tg.Proxy)
	if err != nil {
		return err
	}
	for _, a := range tg.Admins {
		if err := c.send(ctx, a, "✅ پیام آزمایشی NeoTunnel از <b>"+esc(nodeName(st))+"</b>. ربات درست کار می‌کند.", adminKeys); err != nil {
			return err
		}
	}
	return nil
}

// ---------- texts ----------

func fmtBytes(n float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", n, units[i])
	}
	return fmt.Sprintf("%.1f %s", n, units[i])
}

func fmtRate(bps float64) string {
	units := []string{"bps", "Kbps", "Mbps", "Gbps"}
	i := 0
	for bps >= 1000 && i < len(units)-1 {
		bps /= 1000
		i++
	}
	return fmt.Sprintf("%.1f %s", bps, units[i])
}

func pairText(rx, tx float64) string {
	return fmt.Sprintf("↓ %s · ↑ %s (مجموع %s)", fmtBytes(rx), fmtBytes(tx), fmtBytes(rx+tx))
}

// forwardsOn are the user-facing forwards (not proxies) that serve port.
func forwardsOn(st *config.State, port int) []config.Forward {
	_, fs := st.Effective()
	var out []config.Forward
	for _, f := range fs {
		if f.ModeOf() == config.ForwardSocks {
			continue
		}
		for _, p := range forward.Expand(f.Ports) {
			if p == port {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

func fkey(f config.Forward) string { return f.Proto + "/" + f.Ports }

// sums adds up the bytes of the metrics series keys over rng.
func (b *bot) sums(rng string, keys []string) (rx, tx float64) {
	v := b.deps.Metrics(rng, keys)
	for _, k := range keys {
		if l, ok := v.Series[k]; ok {
			rx += l.Sum[0] / 8
			tx += l.Sum[1] / 8
		}
	}
	return rx, tx
}

// rates is the latest rate of the series keys.
func (b *bot) rates(keys []string) (rx, tx float64) {
	v := b.deps.Metrics("1h", keys)
	for _, k := range keys {
		l, ok := v.Series[k]
		if !ok {
			continue
		}
		for i := len(l.A) - 1; i >= 0; i-- {
			if !isNaN(float64(l.A[i])) {
				rx += float64(l.A[i])
				if i < len(l.B) && !isNaN(float64(l.B[i])) {
					tx += float64(l.B[i])
				}
				break
			}
		}
	}
	return rx, tx
}

func isNaN(f float64) bool { return f != f }

func (b *bot) portText(st *config.State, port int) string {
	fs := forwardsOn(st, port)
	if len(fs) == 0 {
		return fmt.Sprintf("پورت <code>%d</code> روی این سرور فوروارد نیست.", port)
	}
	var keys []string
	var today, month, total traffic.Pair
	var since int64
	sums := b.deps.Summaries()
	for _, f := range fs {
		keys = append(keys, "f:"+fkey(f))
		if s, ok := sums[traffic.Port+fkey(f)]; ok {
			today.Rx, today.Tx = today.Rx+s.Today.Rx, today.Tx+s.Today.Tx
			month.Rx, month.Tx = month.Rx+s.Month.Rx, month.Tx+s.Month.Tx
			total.Rx, total.Tx = total.Rx+s.Total.Rx, total.Tx+s.Total.Tx
			if since == 0 || (s.Since != 0 && s.Since < since) {
				since = s.Since
			}
		}
	}
	rr, rt := b.rates(keys)
	d1r, d1t := b.sums("24h", keys)
	w1r, w1t := b.sums("7d", keys)
	var sb strings.Builder
	fmt.Fprintf(&sb, "🔌 <b>پورت %d</b>\n", port)
	fmt.Fprintf(&sb, "الان: ↓ %s · ↑ %s\n", fmtRate(rr), fmtRate(rt))
	fmt.Fprintf(&sb, "امروز: %s\n", pairText(float64(today.Rx), float64(today.Tx)))
	fmt.Fprintf(&sb, "۲۴ ساعت گذشته: %s\n", pairText(d1r, d1t))
	fmt.Fprintf(&sb, "۷ روز گذشته: %s\n", pairText(w1r, w1t))
	fmt.Fprintf(&sb, "این ماه: %s\n", pairText(float64(month.Rx), float64(month.Tx)))
	fmt.Fprintf(&sb, "از ابتدا: %s", pairText(float64(total.Rx), float64(total.Tx)))
	if since > 0 {
		fmt.Fprintf(&sb, "\n<i>شمارش از %s</i>", tehran(time.Unix(since, 0)).Format("2006-01-02"))
	}
	return sb.String()
}

func (b *bot) portsText(st *config.State) string {
	_, fs := st.Effective()
	sums := b.deps.Summaries()
	var sb strings.Builder
	sb.WriteString("🔌 <b>ترافیک پورت‌ها</b> (امروز · این ماه)\n")
	n := 0
	for _, f := range fs {
		if f.ModeOf() == config.ForwardSocks {
			continue
		}
		s := sums[traffic.Port+fkey(f)]
		fmt.Fprintf(&sb, "\n<code>%s</code> ← %s\n   %s · %s", esc(f.Ports), esc(tunnelLabel(st, f.Tunnel)), fmtBytes(float64(s.Today.Rx+s.Today.Tx)), fmtBytes(float64(s.Month.Rx+s.Month.Tx)))
		n++
	}
	if n == 0 {
		return "فورواردی روی این سرور نیست."
	}
	sb.WriteString("\n\nبرای جزئیات، شماره‌ی پورت را بفرستید.")
	return sb.String()
}

func (b *bot) statusText(st *config.State) string {
	ts, _ := st.Effective()
	hs := b.deps.Health()
	var lines []string
	ok, all := 0, 0
	for _, t := range ts {
		if t.Over != "" || isCarrier(ts, t.Name) && t.Label == "" {
			// a stack is shown by its top tunnel
		}
		if isCarrier(ts, t.Name) {
			continue
		}
		all++
		h := hs[t.Name]
		icon, state := "⚪", "بدون داده"
		switch {
		case t.Disabled:
			icon, state = "⏸", "خاموش"
		case h.Grade == health.GradeOK || h.Grade == health.GradeDegraded:
			ok++
			icon = "🟢"
			if h.Grade == health.GradeDegraded {
				icon = "🟡"
			}
			state = fmt.Sprintf("%.0f ms · افت %.0f٪", h.AvgMs, h.Loss)
		case h.Grade == health.GradeBad:
			icon, state = "🟠", fmt.Sprintf("افت شدید %.0f٪", h.Loss)
		case h.Grade == health.GradeDown:
			icon, state = "🔴", "قطع"
		}
		lines = append(lines, fmt.Sprintf("%s <b>%s</b> · %s", icon, esc(tunnelLabel(st, t.Name)), state))
	}
	if all == 0 {
		return "تانلی روی این سرور نیست."
	}
	return fmt.Sprintf("📊 <b>وضعیت تانل‌ها</b> · %d از %d سالم\n<i>%s</i>\n\n%s", ok, all, esc(nodeName(st)), strings.Join(lines, "\n"))
}

func isCarrier(ts []config.Tunnel, name string) bool {
	for _, t := range ts {
		if t.Over == name {
			return true
		}
	}
	return false
}

var periodTitle = map[string]string{"1h": "یک ساعت گذشته", "24h": "۲۴ ساعت گذشته", "7d": "۷ روز گذشته"}

// reportText summarizes rng: the ports (those of only, "" = all) and, with
// system, the tunnels, CPU and the card.
func (b *bot) reportText(st *config.State, rng, only string, system bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "📈 <b>گزارش %s</b>\n<i>%s</i>\n", periodTitle[rng], esc(nodeName(st)))
	if system {
		ts, _ := st.Effective()
		hs := b.deps.Health()
		ok, all, ping, pn := 0, 0, 0.0, 0
		for _, t := range ts {
			if isCarrier(ts, t.Name) || t.Disabled {
				continue
			}
			all++
			h := hs[t.Name]
			if h.Grade == health.GradeOK || h.Grade == health.GradeDegraded {
				ok++
				ping += h.AvgMs
				pn++
			}
		}
		if all > 0 {
			fmt.Fprintf(&sb, "\nتانل‌ها: %d از %d سالم", ok, all)
			if pn > 0 {
				fmt.Fprintf(&sb, " · میانگین پینگ %.0f ms", ping/float64(pn))
			}
		}
		v := b.deps.Metrics(rng, []string{"cpu", "nic"})
		if c, ok := v.Series["cpu"]; ok {
			fmt.Fprintf(&sb, "\nپردازنده: میانگین %.0f٪ · اوج %.0f٪", c.Avg[0], c.Peak[0])
		}
		if n, ok := v.Series["nic"]; ok {
			fmt.Fprintf(&sb, "\nکارت شبکه: %s", pairText(n.Sum[0]/8, n.Sum[1]/8))
		}
		sb.WriteString("\n")
	}
	_, fs := st.Effective()
	want := map[int]bool{}
	for _, p := range forward.Expand(only) {
		want[p] = true
	}
	type row struct {
		ports  string
		rx, tx float64
	}
	var rows []row
	for _, f := range fs {
		if f.ModeOf() == config.ForwardSocks {
			continue
		}
		if len(want) > 0 {
			hit := false
			for _, p := range forward.Expand(f.Ports) {
				hit = hit || want[p]
			}
			if !hit {
				continue
			}
		}
		rx, tx := b.sums(rng, []string{"f:" + fkey(f)})
		rows = append(rows, row{f.Ports, rx, tx})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].rx+rows[i].tx > rows[j].rx+rows[j].tx })
	if len(rows) > 0 {
		sb.WriteString("\n<b>پورت‌ها</b>")
		for _, r := range rows {
			fmt.Fprintf(&sb, "\n<code>%s</code>: %s", esc(r.ports), pairText(r.rx, r.tx))
		}
	}
	return sb.String()
}

// ---------- scheduled reports ----------

var tehranZone = func() *time.Location {
	if l, err := time.LoadLocation("Asia/Tehran"); err == nil {
		return l
	}
	return time.FixedZone("IRST", 3*3600+1800)
}()

func tehran(t time.Time) time.Time { return t.In(tehranZone) }

func reportKey(r config.TelegramReport) string { return r.Every + "|" + r.At + "|" + r.Ports }

func reportsPath() string { return filepath.Join(config.Dir, "telegram-reports.json") }

// Due returns the latest time report r was due at or before now.
func Due(r config.TelegramReport, now time.Time) time.Time {
	now = tehran(now)
	h, m := 9, 0
	if hh, mm, ok := strings.Cut(r.At, ":"); ok {
		h, _ = strconv.Atoi(hh)
		m, _ = strconv.Atoi(mm)
	} else if r.At != "" {
		m, _ = strconv.Atoi(r.At) // hourly: just the minute
	}
	switch r.Every {
	case "hourly":
		d := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), m, 0, 0, tehranZone)
		if d.After(now) {
			d = d.Add(-time.Hour)
		}
		return d
	case "weekly":
		d := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, tehranZone)
		for d.Weekday() != time.Saturday || d.After(now) {
			d = d.AddDate(0, 0, -1)
		}
		return d
	}
	d := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, tehranZone)
	if d.After(now) {
		d = d.AddDate(0, 0, -1)
	}
	return d
}

func (b *bot) schedule(ctx context.Context) {
	last := map[string]int64{}
	if raw, err := os.ReadFile(reportsPath()); err == nil {
		json.Unmarshal(raw, &last)
	}
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		st, tg := settings()
		changed := false
		now := time.Now()
		for _, r := range tg.Reports {
			k := reportKey(r)
			due := Due(r, now).Unix()
			if _, seen := last[k]; !seen {
				last[k], changed = due, true // a new report starts with the next time
				continue
			}
			if last[k] >= due || len(tg.Admins) == 0 {
				continue
			}
			last[k], changed = due, true
			rng := map[string]string{"hourly": "1h", "weekly": "7d"}[r.Every]
			if rng == "" {
				rng = "24h"
			}
			text := b.reportText(st, rng, r.Ports, r.System)
			for _, a := range tg.Admins {
				if err := b.c.send(ctx, a, text, nil); err != nil {
					log.Printf("telegram report: %v", err)
				}
			}
		}
		if changed {
			if raw, err := json.Marshal(last); err == nil {
				os.WriteFile(reportsPath(), raw, 0o600)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
