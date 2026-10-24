package web

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/forward"
	"neotunnel/internal/telegram"
)

// TelegramView is the Telegram section: the settings (without the token),
// the bot's status, and the internal proxies it can reach Telegram through.
type TelegramView struct {
	Settings config.Telegram `json:"settings"`
	TokenSet bool            `json:"token_set"`
	Status   telegram.Status `json:"status"`
	Proxies  []ProxyChoice   `json:"proxies"`
}

// ProxyChoice is an internal proxy of this server, as a proxy URL.
type ProxyChoice struct {
	URL    string `json:"url"`
	Tunnel string `json:"tunnel"`
}

func telegramView() (TelegramView, error) {
	st, err := config.Load()
	if err != nil {
		return TelegramView{}, err
	}
	v := TelegramView{Status: telegram.Current()}
	if st.Telegram != nil {
		v.Settings = *st.Telegram
	}
	v.TokenSet = v.Settings.Token != ""
	v.Settings.Token = ""
	if v.Settings.LinkUntil < time.Now().Unix() {
		v.Settings.LinkCode, v.Settings.LinkUntil = "", 0
	}
	v.Proxies = proxyChoices(st)
	return v, nil
}

func proxyChoices(st *config.State) []ProxyChoice {
	out := []ProxyChoice{}
	_, fs := st.Effective()
	for _, f := range fs {
		if f.ModeOf() != config.ForwardSocks || f.Fetch {
			continue
		}
		ps := forward.Expand(f.Ports)
		if len(ps) == 0 {
			continue
		}
		host := f.Listen
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		u := url.URL{Scheme: "socks5", Host: host + ":" + strconv.Itoa(ps[0])}
		if user, pass, ok := strings.Cut(f.Auth, ":"); ok {
			u.User = url.UserPassword(user, pass)
		}
		out = append(out, ProxyChoice{URL: u.String(), Tunnel: f.Tunnel})
	}
	return out
}

func checkTelegram(t config.Telegram) error {
	if t.Proxy != "" {
		u, err := url.Parse(t.Proxy)
		if err != nil || (u.Scheme != "socks5" && u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("proxy must look like socks5://127.0.0.1:1080")
		}
	}
	if t.UserPorts != "" && len(forward.Expand(t.UserPorts)) == 0 {
		return errors.New("allowed ports must look like 443,8080-8090")
	}
	for _, r := range t.Reports {
		switch r.Every {
		case "hourly", "daily", "weekly":
		default:
			return fmt.Errorf("unknown report period %q", r.Every)
		}
		if r.Every == "hourly" && !strings.Contains(r.At, ":") {
			if m, err := strconv.Atoi(r.At); r.At != "" && (err != nil || m < 0 || m > 59) {
				return fmt.Errorf("hourly report minute %q must be 0-59", r.At)
			}
		} else if r.At != "" {
			h, m, ok := strings.Cut(r.At, ":")
			hh, e1 := strconv.Atoi(h)
			mm, e2 := strconv.Atoi(m)
			if !ok || e1 != nil || e2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
				return fmt.Errorf("report time %q must be HH:MM", r.At)
			}
		}
		if r.Ports != "" && len(forward.Expand(r.Ports)) == 0 {
			return fmt.Errorf("report ports %q must look like 443,8080-8090", r.Ports)
		}
	}
	return nil
}

func linkCode() string {
	const abc = "abcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, 10)
	rand.Read(b)
	for i := range b {
		b[i] = abc[int(b[i])%len(abc)]
	}
	return string(b)
}

func (s *Server) telegramRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/telegram", s.authed(func(w http.ResponseWriter, r *http.Request) {
		v, err := telegramView()
		reply(w, v, err)
	}))
	// POST saves the settings. An empty token keeps the saved one; the link
	// code is only made by /code and cleared by the bot.
	m.HandleFunc("POST /api/telegram", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var t config.Telegram
		if readJSON(r, &t) != nil {
			fail(w, http.StatusBadRequest, "bad request")
			return
		}
		t.Token = strings.TrimSpace(t.Token)
		if err := checkTelegram(t); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		_, err := config.Update(func(st *config.State) error {
			old := config.Telegram{}
			if st.Telegram != nil {
				old = *st.Telegram
			}
			if t.Token == "" {
				t.Token = old.Token
			}
			if t.On && t.Token == "" {
				return errors.New("enter the bot's token from @BotFather first")
			}
			t.LinkCode, t.LinkUntil = old.LinkCode, old.LinkUntil
			st.Telegram = &t
			return nil
		})
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		v, err := telegramView()
		reply(w, v, err)
	}))
	m.HandleFunc("POST /api/telegram/test", s.authed(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		reply(w, map[string]bool{"ok": true}, telegram.Test(ctx))
	}))
	// /code makes a one-time code, valid for 15 minutes: whoever sends the
	// bot "/start <code>" becomes an admin.
	m.HandleFunc("POST /api/telegram/code", s.authed(func(w http.ResponseWriter, r *http.Request) {
		_, err := config.Update(func(st *config.State) error {
			if st.Telegram == nil || st.Telegram.Token == "" {
				return errors.New("save the bot's token first")
			}
			st.Telegram.LinkCode, st.Telegram.LinkUntil = linkCode(), time.Now().Add(15*time.Minute).Unix()
			return nil
		})
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		v, err := telegramView()
		reply(w, v, err)
	}))
}
