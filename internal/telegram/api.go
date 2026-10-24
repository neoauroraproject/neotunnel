package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// apiBase is Telegram's Bot API; tests point it at a fake server.
var apiBase = "https://api.telegram.org"

// client talks to the Bot API with one token, through a proxy when set.
type client struct {
	token string
	http  *http.Client
}

func newClient(token, proxy string) (*client, error) {
	tr := &http.Transport{TLSHandshakeTimeout: 15 * time.Second, IdleConnTimeout: 90 * time.Second}
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil || (u.Scheme != "socks5" && u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("proxy must look like socks5://127.0.0.1:1080")
		}
		tr.Proxy = http.ProxyURL(u)
	}
	return &client{token: token, http: &http.Client{Transport: tr, Timeout: 75 * time.Second}}, nil
}

type apiReply struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
}

func (c *client) call(ctx context.Context, method string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", apiBase+"/bot"+c.token+"/"+method, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		// The URL holds the token; never let it into a message.
		return fmt.Errorf("telegram is not reachable: %s", strings.ReplaceAll(err.Error(), c.token, "…"))
	}
	defer res.Body.Close()
	var r apiReply
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&r); err != nil {
		return fmt.Errorf("telegram answered %s", res.Status)
	}
	if !r.OK {
		return fmt.Errorf("telegram: %s", r.Description)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

type user struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

type message struct {
	Chat struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	From user   `json:"from"`
	Text string `json:"text"`
}

type update struct {
	ID      int64    `json:"update_id"`
	Message *message `json:"message"`
}

func (c *client) me(ctx context.Context) (user, error) {
	var u user
	err := c.call(ctx, "getMe", map[string]any{}, &u)
	return u, err
}

func (c *client) updates(ctx context.Context, offset int64, wait time.Duration) ([]update, error) {
	var us []update
	err := c.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": int(wait.Seconds()), "allowed_updates": []string{"message"}}, &us)
	return us, err
}

// keyboard is a reply keyboard, rows of button labels.
type keyboard [][]string

func (c *client) send(ctx context.Context, chat int64, text string, kb keyboard) error {
	msg := map[string]any{"chat_id": chat, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true}
	if kb != nil {
		rows := make([][]map[string]string, len(kb))
		for i, r := range kb {
			for _, l := range r {
				rows[i] = append(rows[i], map[string]string{"text": l})
			}
		}
		msg["reply_markup"] = map[string]any{"keyboard": rows, "resize_keyboard": true}
	}
	return c.call(ctx, "sendMessage", msg, nil)
}
