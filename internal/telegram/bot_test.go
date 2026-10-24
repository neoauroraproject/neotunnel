package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/events"
	"neotunnel/internal/health"
	"neotunnel/internal/metrics"
	"neotunnel/internal/traffic"
)

// fakeAPI records what the bot sends.
type fakeAPI struct {
	mu   sync.Mutex
	sent []map[string]any
}

func (f *fakeAPI) serve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			f.mu.Lock()
			f.sent = append(f.sent, in)
			f.mu.Unlock()
			w.Write([]byte(`{"ok":true,"result":{}}`))
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			w.Write([]byte(`{"ok":true,"result":{"id":1,"username":"test_bot"}}`))
		default:
			w.Write([]byte(`{"ok":false,"description":"Not Found"}`))
		}
	}))
	t.Cleanup(srv.Close)
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old })
}

func (f *fakeAPI) last() (chat int64, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return 0, ""
	}
	m := f.sent[len(f.sent)-1]
	return int64(m["chat_id"].(float64)), m["text"].(string)
}

func (f *fakeAPI) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.sent) }

func setup(t *testing.T, tg config.Telegram) (*bot, *fakeAPI) {
	t.Helper()
	config.Dir = t.TempDir()
	ends := [2]config.Side{{Node: "ir"}, {Node: "de"}}
	st := &config.State{Self: "ir", Label: "Tehran",
		Tunnels:  []config.Tunnel{{Name: "g1", Kind: config.KindGRE, Sides: ends, Label: "Main"}},
		Forwards: []config.Forward{{Node: "ir", Tunnel: "g1", Proto: "tcp", Ports: "443,8080-8090"}},
		Telegram: &tg}
	if err := config.Save(st); err != nil {
		t.Fatal(err)
	}
	f := &fakeAPI{}
	f.serve(t)
	c, _ := newClient("123:abc", "")
	b := &bot{c: c, sent: map[string]time.Time{}, deps: Deps{
		Summaries: func() map[string]traffic.Summary {
			return map[string]traffic.Summary{traffic.Port + "tcp/443,8080-8090": {Today: traffic.Pair{Rx: 3 << 30, Tx: 1 << 30}}}
		},
		Metrics: func(rng string, keys []string) metrics.View {
			v := metrics.View{Series: map[string]metrics.Line{}}
			for _, k := range keys {
				v.Series[k] = metrics.Line{A: metrics.Values{8e6}, B: metrics.Values{8e5}, Sum: [2]float64{8 << 30, 8 << 20}}
			}
			return v
		},
		Health: func() map[string]health.State { return map[string]health.State{} },
	}}
	return b, f
}

func msg(from int64, text string) *message {
	m := &message{Text: text}
	m.Chat.ID, m.From.ID = from, from
	return m
}

func TestLinkCodeMakesAdmin(t *testing.T) {
	b, f := setup(t, config.Telegram{On: true, Token: "123:abc", LinkCode: "secret", LinkUntil: time.Now().Add(time.Minute).Unix()})
	ctx := context.Background()

	b.handle(ctx, msg(7, "/start wrong"))
	if _, text := f.last(); !strings.Contains(text, "خصوصی") {
		t.Fatalf("a wrong code got %q", text)
	}
	b.handle(ctx, msg(7, "/start secret"))
	st, _ := config.Load()
	if len(st.Telegram.Admins) != 1 || st.Telegram.Admins[0] != 7 || st.Telegram.LinkCode != "" {
		t.Fatalf("after linking: %+v", st.Telegram)
	}
	// The code works once.
	b.handle(ctx, msg(8, "/start secret"))
	if st, _ := config.Load(); len(st.Telegram.Admins) != 1 {
		t.Fatalf("the code worked twice: %v", st.Telegram.Admins)
	}
	b.handle(ctx, msg(7, btnStatus))
	if chat, text := f.last(); chat != 7 || !strings.Contains(text, "Main") {
		t.Fatalf("status: %d %q", chat, text)
	}
}

func TestExpiredCodeDoesNothing(t *testing.T) {
	b, _ := setup(t, config.Telegram{On: true, Token: "123:abc", LinkCode: "secret", LinkUntil: time.Now().Add(-time.Second).Unix()})
	b.handle(context.Background(), msg(7, "/start secret"))
	if st, _ := config.Load(); len(st.Telegram.Admins) != 0 {
		t.Fatalf("an expired code linked: %v", st.Telegram.Admins)
	}
}

func TestUserSeesOnlyAllowedPorts(t *testing.T) {
	b, f := setup(t, config.Telegram{On: true, Token: "123:abc", Users: true, UserPorts: "8080-8090"})
	ctx := context.Background()

	b.handle(ctx, msg(9, "8085"))
	_, text := f.last()
	if !strings.Contains(text, "پورت 8085") || !strings.Contains(text, "3.0 GB") || !strings.Contains(text, "1.0 GB") {
		t.Fatalf("port answer: %q", text)
	}
	b.handle(ctx, msg(9, "443"))
	if _, text := f.last(); !strings.Contains(text, "نمی‌توانید") {
		t.Fatalf("a port outside the allowed list: %q", text)
	}
	b.handle(ctx, msg(9, btnStatus))
	if _, text := f.last(); strings.Contains(text, "Main") {
		t.Fatalf("a user saw the tunnels: %q", text)
	}
}

func TestUsersOffHidesPorts(t *testing.T) {
	b, f := setup(t, config.Telegram{On: true, Token: "123:abc"})
	b.handle(context.Background(), msg(9, "443"))
	if _, text := f.last(); !strings.Contains(text, "نمی‌توانید") {
		t.Fatalf("users off, but: %q", text)
	}
}

func TestAlertsMuteAndThrottle(t *testing.T) {
	b, f := setup(t, config.Telegram{On: true, Token: "123:abc", Admins: []int64{5, 6}, Mute: []string{"recover"}})
	ctx := context.Background()
	down := events.Event{Tunnel: "g1", Kind: events.Health, From: "ok", To: "down"}
	b.alert(ctx, down)
	if f.count() != 2 {
		t.Fatalf("want one alert per admin, got %d", f.count())
	}
	if _, text := f.last(); !strings.Contains(text, "Main") || !strings.Contains(text, "قطع") {
		t.Fatalf("alert text: %q", text)
	}
	b.alert(ctx, down) // within a minute
	b.alert(ctx, events.Event{Tunnel: "g1", Kind: events.Health, From: "down", To: "ok"})
	b.alert(ctx, events.Event{Tunnel: "g1", Kind: events.Restart}) // by hand
	if f.count() != 2 {
		t.Fatalf("throttled, muted or manual events were sent: %d", f.count())
	}
	b.alert(ctx, events.Event{Tunnel: "g1", Kind: events.Failover, From: "g1", To: "g2", Detail: "443|heavy loss"})
	b.alert(ctx, events.Event{Tunnel: "g2", Kind: events.Failover, From: "g1", To: "g2", Detail: "443|heavy loss"})
	if f.count() != 4 {
		t.Fatalf("a failover should be told once per admin: %d", f.count())
	}
}

func TestDue(t *testing.T) {
	at := func(s string) time.Time {
		tm, err := time.ParseInLocation("2006-01-02 15:04", s, tehranZone)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	cases := []struct {
		r    config.TelegramReport
		now  string
		want string
	}{
		{config.TelegramReport{Every: "daily", At: "09:00"}, "2026-10-24 10:00", "2026-10-24 09:00"},
		{config.TelegramReport{Every: "daily", At: "09:00"}, "2026-10-24 08:59", "2026-10-23 09:00"},
		{config.TelegramReport{Every: "hourly", At: "15"}, "2026-10-24 10:20", "2026-10-24 10:15"},
		{config.TelegramReport{Every: "hourly", At: "00:15"}, "2026-10-24 10:10", "2026-10-24 09:15"},
		// 2026-10-24 is a Saturday.
		{config.TelegramReport{Every: "weekly", At: "20:00"}, "2026-10-24 19:00", "2026-10-17 20:00"},
		{config.TelegramReport{Every: "weekly", At: "20:00"}, "2026-10-26 08:00", "2026-10-24 20:00"},
	}
	for _, c := range cases {
		if got := Due(c.r, at(c.now)); !got.Equal(at(c.want)) {
			t.Errorf("%+v at %s: got %s, want %s", c.r, c.now, got.In(tehranZone).Format("2006-01-02 15:04"), c.want)
		}
	}
}
