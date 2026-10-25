package web

// Sponsored messages in the panel. The server asks the NeoAurora ads
// service for one (the panel's own policy allows no other origin, and the
// browser may not reach it when the server can), keeps it a while, and
// passes clicks on. Nothing about this server is sent: only the product
// and where the message is shown.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	adsBase    = "https://license.hossein-m466.workers.dev/v1/ads"
	adsProduct = "neotunnel"
	adsTTL     = 15 * time.Minute
	adsRetry   = 3 * time.Minute // after a failed look
)

// Ad is one sponsored message.
type Ad struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Body   string `json:"body,omitempty"`
	Button string `json:"button,omitempty"`
	Link   string `json:"link,omitempty"`
	Badge  string `json:"badge,omitempty"`
}

type adsCache struct {
	mu sync.Mutex
	at map[string]time.Time
	ad map[string]*Ad
}

var ads = &adsCache{at: map[string]time.Time{}, ad: map[string]*Ad{}}

var adsClient = &http.Client{Timeout: 8 * time.Second}

// placements: "home" is the dashboard's card, "sidebar" the slim line on
// the other pages (which shows the home one when it has none of its own).
var adPlacements = map[string]bool{"home": true, "sidebar": true}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func fetchAd(ctx context.Context, placement string) (*Ad, error) {
	q := url.Values{"product": {adsProduct}, "placement": {placement}, "mode": {"rotate"}}
	req, _ := http.NewRequestWithContext(ctx, "GET", adsBase+"?"+q.Encode(), nil)
	res, err := adsClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var r struct {
		Ad *struct {
			ID         string `json:"id"`
			Title      string `json:"title"`
			Body       string `json:"body"`
			ButtonText string `json:"buttonText"`
			LinkURL    string `json:"linkUrl"`
			Badge      string `json:"badge"`
		} `json:"ad"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&r); err != nil || r.Ad == nil || r.Ad.Title == "" {
		return nil, err
	}
	a := &Ad{ID: clip(r.Ad.ID, 64), Title: clip(r.Ad.Title, 80), Body: clip(r.Ad.Body, 240), Button: clip(r.Ad.ButtonText, 30), Badge: clip(r.Ad.Badge, 20)}
	// Only plain web links open from the panel.
	if u, err := url.Parse(r.Ad.LinkURL); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
		a.Link = u.String()
	}
	return a, nil
}

func (c *adsCache) get(ctx context.Context, placement string) *Ad {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t, ok := c.at[placement]; ok && time.Since(t) < adsTTL {
		return c.ad[placement]
	}
	a, err := fetchAd(ctx, placement)
	c.at[placement] = time.Now()
	if err != nil {
		c.at[placement] = time.Now().Add(adsRetry - adsTTL) // look again sooner
		a = c.ad[placement]                                 // keep the last good one
	}
	c.ad[placement] = a
	return a
}

func (s *Server) adRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/ad", s.authed(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("placement")
		if !adPlacements[p] {
			fail(w, http.StatusBadRequest, "unknown placement")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		a := ads.get(ctx, p)
		if a == nil && p != "home" {
			a = ads.get(ctx, "home")
		}
		writeJSON(w, http.StatusOK, map[string]any{"ad": a})
	}))
	m.HandleFunc("POST /api/ad/click", s.authed(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID        string `json:"id"`
			Placement string `json:"placement"`
		}
		if readJSON(r, &in) != nil || in.ID == "" || !adPlacements[in.Placement] {
			fail(w, http.StatusBadRequest, "bad request")
			return
		}
		body, _ := json.Marshal(map[string]string{"adId": clip(in.ID, 64), "productSlug": adsProduct, "placement": in.Placement})
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "POST", adsBase+"/click", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if res, err := adsClient.Do(req); err == nil {
				res.Body.Close()
			}
		}()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))
}
